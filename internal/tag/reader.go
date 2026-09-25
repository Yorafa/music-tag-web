// Package tag contains audio-tag readers and writers for the
// gateway's multi-format workflow.
//
// # Why taglib is the primary reader
//
// tag.Read is used both over HTTP (POST /api/music_id3/ for frontend
// hydrate / openEditor) and internally by:
//
//  1. internal/tasks/batchtag.go::batchAutoTag — worker batch scrape
//  2. internal/tasks/tidy.go — folder tidy reads existing tags
//  3. internal/tag/writer.go::HandleSidecars — sidecar cover extraction
//     when /api/update_id3/ arrives without AlbumImg
//
// It used to be dhowden/tag only. That was wrong twice over, both
// measured with real ffmpeg-generated samples (internal/tag/probe_matrix_test.go
// and the sample generator next to it):
//
//  1. COVERAGE. dhowden understands 5 containers (mp3/flac/ogg/opus/mp4).
//     Every other format in audioext.libraryExts — wav, aiff, wma, wv, tta,
//     ape, mpc, dsf, dff — failed with "no tags found" or "EOF". The file
//     browser still listed them, the editor still accepted a save, and the
//     save reported success, so the user saw their tags vanish with no error
//     anywhere. taglib parses all of them.
//
//  2. CORRECTNESS, which is worse than the coverage gap because it was
//     silent. On a two-artist file, dhowden returns:
//
//     mp3   "A甲B乙"                          (glued, no separator)
//     flac  "B乙"                             (first value dropped)
//     ogg   "B乙"                             (first value dropped)
//     m4a   "A甲\x00\x00\x00\x14data\x00..."  (the MP4 atom header size
//     parsed as if it were text)
//
//     and it drops TRACKNUMBER entirely on flac/ogg (0/0 where taglib
//     reports 3/12). So the old reader was not "less complete", it was
//     returning wrong answers for the formats it did claim to handle.
//
// taglib is a wazero/WASM build of upstream TagLib, so it costs ~0.35 ms
// per call versus dhowden's ~0.03 ms. Read is on the batch-scan path, not
// a per-keystroke path, so that trade buys correct data plus real
// duration/bitrate/sample-rate at no meaningful cost.
//
// dhowden is kept as a last-resort fallback rather than deleted: it is pure
// Go, so if the WASM module ever fails to instantiate we degrade to a
// partial read instead of losing the format outright.
package tag

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	dhtag "github.com/dhowden/tag"
	"go.senan.xyz/taglib"
)

// ErrUnsupportedFormat is returned when neither taglib nor dhowden can parse
// the file as audio. Callers must surface this rather than substituting a
// made-up title — see Read for why.
var ErrUnsupportedFormat = errors.New("tag: unsupported audio format")

// multiValueSep joins multi-valued taglib frames into the single string the
// TagInfo/JSON contract exposes. Vorbis comments and MP4 atoms are genuinely
// multi-valued ("ARTIST" repeated), so this is lossy by design; the frontend
// renders one line, and the writer re-splits on this separator.
const multiValueSep = "; "

// Read reads every tag field plus metadata (duration/bitrate/codec) from an
// audio file.
//
// Failure modes, all of which return a non-nil error:
//
//   - file missing / stat fails
//   - neither taglib nor dhowden recognises the container
//
// Read does NOT invent a title from the filename when parsing fails. It
// used to, and that behaviour is the root cause of the worst bug in this
// package: a .wav whose tags could not be parsed came back as
// {Title: "sample"}, the editor displayed "sample" as if it were the real
// title, and a subsequent save wrote that fabrication over whatever the
// file actually contained. A file we cannot read is a file we must not
// pretend to know. Callers that need a display name already fall back to
// the filename themselves (see internal/tasks/batchtag.go).
func Read(path string) (*TagInfo, error) {
	cfg, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	info := &TagInfo{
		Filename: filepath.Base(path),
		Size:     int(cfg.Size()),
	}

	// taglib first: it is the only reader with both coverage and correct
	// multi-value semantics.
	//
	// Deciding "is this actually audio?" needs care, because neither
	// ReadTags nor ReadProperties is sufficient on its own. Measured on
	// deliberately broken input:
	//
	//	notaudio.wav (39 bytes of text)  ReadTags: nil err, 0 tags
	//	                                  ReadProperties: nil err, Format="wav"
	//	empty.mp3                         ReadTags: nil err, 0 tags
	//	                                  ReadProperties: nil err, Format="mpeg"
	//	garbage.flac                      ReadTags: "invalid file"
	//	                                  ReadProperties: nil err, Format=""
	//
	// ReadProperties reports Format derived from the file EXTENSION, so its
	// nil error and its Format field both claim a plain text file named
	// .wav is a 706 kbps wav. The one field that cannot be faked that way is
	// the duration: TagLib has to walk the actual packet/frame chain to
	// produce it, and there is no stream there to walk.
	//
	// So: a non-zero duration is the readability signal. Note this still
	// accepts a genuinely untagged wav (properties come from the header,
	// not the tags), which is the case the old code got wrong.
	tags, tagsErr := taglib.ReadTags(path)
	props, propsErr := taglib.ReadProperties(path)
	if propsErr != nil || props.Length <= 0 {
		info.Codec = ""
		return readWithDhowden(path, cfg, info)
	}

	if propsErr == nil {
		info.Duration = int(props.Length.Round(1e9).Seconds())
		info.BitRate = int(props.BitRate)
		info.Codec = codecFromProperties(props)
	}

	if tagsErr == nil {
		fillFromTaglib(info, tags, props)
	}

	// Embedded cover. Only attempt the read when the properties tell us
	// there is one — ReadImage spins up another WASM instance, and a
	// cover-less file is the common case.
	if propsErr == nil && len(props.Images) > 0 {
		if raw, err := taglib.ReadImage(path); err == nil && len(raw) > 0 {
			// Prefer the container's declared MIME type, but fall back to
			// sniffing the bytes. WavPack's APEv2 image block reports an
			// empty MIMEType even after taglib writes a PNG into it, and
			// defaulting that to image/jpeg made the frontend serve PNG
			// bytes under a JPEG content type.
			mimeType := props.Images[0].MIMEType
			if mimeType == "" {
				mimeType = normalizeMime(http.DetectContentType(raw))
			}
			info.Artwork = fmt.Sprintf("data:%s;base64,%s",
				normalizeMime(mimeType), base64.StdEncoding.EncodeToString(raw))
			// taglib describes images but does not report pixel
			// dimensions, so sniff them from the bytes.
			w, h := sniffImageWH(raw)
			if w > 0 {
				info.ArtworkW = w
			}
			if h > 0 {
				info.ArtworkH = h
			}
			info.ArtworkSize = int(SizeMB(int64(len(raw))))
		}
	}

	// Codec is metadata, not identity: a wav that parsed but somehow came
	// back with no format should still report something better than "".
	if info.Codec == "" {
		info.Codec = string(ProbeFile(path))
	}

	return info, nil
}

// fillFromTaglib copies the fields we care about out of taglib's
// uppercase, multi-valued tag map.
func fillFromTaglib(info *TagInfo, tags map[string][]string, props taglib.Properties) {
	info.Title = firstJoined(tags[taglib.Title])
	info.Artist = firstJoined(tags[taglib.Artist])
	info.Album = firstJoined(tags[taglib.Album])
	info.AlbumArtist = firstJoined(tags[taglib.AlbumArtist])
	info.Genre = strings.TrimSpace(firstJoined(tags[taglib.Genre]))
	info.Comment = firstJoined(tags[taglib.Comment])
	info.Lyrics = firstJoined(tags[taglib.Lyrics])
	info.Language = strings.TrimSpace(firstJoined(tags[taglib.Language]))

	// taglib already renders these as "3/12", the same shape formatNT
	// produces, so they pass through — but normalise the whitespace and
	// drop an empty/zero pair so the frontend does not show "0/0".
	info.TrackNumber = normalizeNT(firstJoined(tags[taglib.TrackNumber]))
	info.DiscNumber = normalizeNT(firstJoined(tags[taglib.DiscNumber]))

	// DATE is whatever the tagger wrote: "2021", "2021-05", "2021-05-04",
	// and sometimes garbage. Take the leading 4-digit run if it is a
	// plausible year and ignore the rest.
	if y := parseYear(firstJoined(tags[taglib.Date])); y > 0 {
		info.Year = y
	}

	// AlbumType is intentionally left empty. Nothing in the read path ever
	// populated it, and mapping some container-specific frame onto it would
	// be inventing semantics rather than reading a real field.
	_ = props
}

// readWithDhowden is the fallback for when the taglib WASM module could not
// read the file at all (instantiation failure, or a container TagLib does
// not know). It is best-effort by construction: pure Go, five containers,
// and — as measured — lossy on multi-value frames. It is kept because a
// partial read beats none, not because it is trusted.
func readWithDhowden(path string, cfg os.FileInfo, info *TagInfo) (*TagInfo, error) {
	format := ProbeFile(path)

	// Check the container magic BEFORE parsing. dhowden's Identify() falls
	// back to the file EXTENSION, so it will happily hand back a Metadata
	// for a text file named .wav and its parser then returns an
	// empty-but-non-nil result. Refusing up front on an unrecognised
	// header is also cheaper: it avoids opening the file at all.
	if format == FormatUnknown {
		return nil, fmt.Errorf("%w: %s: unrecognised container header",
			ErrUnsupportedFormat, filepath.Base(path))
	}

	f, err := os.Open(path)
	if err != nil {
		// Stat succeeded a moment ago; failing to open is a real error and
		// must not be reported as a successful empty read.
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	md, err := dhtag.ReadFrom(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnsupportedFormat,
			filepath.Base(path), err)
	}
	info.Codec = CodecName(format)

	info.Title = md.Title()
	info.Artist = md.Artist()
	info.Album = md.Album()
	info.AlbumArtist = md.AlbumArtist()
	info.Genre = strings.TrimSpace(md.Genre())
	info.Comment = md.Comment()
	info.Lyrics = md.Lyrics()
	info.TrackNumber = formatNT(md.Track())
	info.DiscNumber = formatNT(md.Disc())
	if y := md.Year(); y > 0 {
		info.Year = y
	}

	if pic := md.Picture(); pic != nil {
		info.Artwork = fmt.Sprintf("data:%s;base64,%s",
			normalizeMime(pic.MIMEType),
			base64.StdEncoding.EncodeToString(pic.Data))
		// dhowden/tag.Picture has no Width/Height, so sniff the bytes.
		w, h := sniffImageWH(pic.Data)
		if w > 0 {
			info.ArtworkW = w
		}
		if h > 0 {
			info.ArtworkH = h
		}
		info.ArtworkSize = int(SizeMB(int64(len(pic.Data))))
	}

	// No properties available on this path, so fall back to the old
	// size/bitrate heuristic. It is a guess — see estimateBitrate — but it
	// is the only signal dhowden does not provide.
	info.Duration = estimateDuration(path, format, cfg.Size())
	info.BitRate = estimateBitrate(path, format, cfg.Size())

	return info, nil
}

// firstJoined returns the first non-empty value, or all values joined when a
// frame legitimately repeats. Vorbis comments and MP4 atoms store several
// artists as repeated ARTIST keys rather than one delimited string.
func firstJoined(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	kept := make([]string, 0, len(vals))
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			kept = append(kept, v)
		}
	}
	return strings.Join(kept, multiValueSep)
}

// normalizeNT cleans a "3/12" / "3" / " 3 / 12 " track or disc field and
// returns "" when it carries no usable number, matching formatNT's contract.
func normalizeNT(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	n, total := parseTrack(s)
	return formatNT(n, total)
}

// parseYear extracts a 4-digit year from a DATE field, tolerating the
// "2021-05-04" and "2021-05" forms taggers actually write, plus leading
// punctuation some ID3 encoders emit. Returns 0 for anything implausible.
func parseYear(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var digits strings.Builder
	for i, r := range s {
		if i >= 4 {
			break
		}
		if r < '0' || r > '9' {
			break
		}
		digits.WriteRune(r)
	}
	y, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0
	}
	// TagLib itself rejects years outside this window; matching it keeps a
	// garbage DATE frame from producing a nonsense "year" in the UI.
	if y < 1000 || y > 2999 {
		return 0
	}
	return y
}

// formatNT formats (num, total) as "3" or "3/12". Both zero/-1 → "".
func formatNT(num, total int) string {
	if num <= 0 && total <= 0 {
		return ""
	}
	if num <= 0 && total > 0 {
		return "0/" + strconv.Itoa(total)
	}
	if total > 0 {
		return strconv.Itoa(num) + "/" + strconv.Itoa(total)
	}
	return strconv.Itoa(num)
}

func normalizeMime(t string) string {
	if t == "" {
		return "image/jpeg"
	}
	// http.DetectContentType returns "text/plain; charset=utf-8" for
	// unrecognised bytes; the artwork contract is an image data URI, so
	// anything non-image falls back to the historical default.
	if !strings.HasPrefix(t, "image/") {
		return "image/jpeg"
	}
	return t
}

// sniffImageWH pulls pixel dimensions straight out of the encoded bytes.
// It is a hand-rolled JPEG SOF / PNG IHDR walk rather than image.DecodeConfig
// because decoding registers format codecs process-wide, and this package
// must not be the thing that changes which formats the rest of the binary
// can decode. Unsupported or malformed data yields (0, 0), which callers
// treat as "dimensions unknown".
func sniffImageWH(data []byte) (int, int) {
	if w, h, ok := sniffPNGWH(data); ok {
		return w, h
	}
	return sniffJPEGWH(data)
}

// sniffPNGWH reads width/height out of the IHDR chunk, which is always the
// first chunk of a PNG and is therefore at a fixed offset — no walking.
func sniffPNGWH(data []byte) (int, int, bool) {
	// 8-byte signature, then 4-byte length, 4-byte "IHDR", then w/h.
	if len(data) < 24 {
		return 0, 0, false
	}
	if string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return 0, 0, false
	}
	w := int(data[16])<<24 | int(data[17])<<16 | int(data[18])<<8 | int(data[19])
	h := int(data[20])<<24 | int(data[21])<<16 | int(data[22])<<8 | int(data[23])
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// sniffJPEGWH walks JPEG segments to the SOF0/SOF2 frame header and reads
// the height/width pair from it. SOF2 is progressive, SOF0 baseline; the
// other SOFn variants (arithmetic-coded, lossless, hierarchical) are rare
// enough in cover art that skipping them just yields "unknown".
func sniffJPEGWH(data []byte) (int, int) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, 0
	}
	for i := 2; i+9 < len(data); {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if i+4 >= len(data) {
			return 0, 0
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if marker == 0xC0 || marker == 0xC2 {
			if i+9 >= len(data) {
				return 0, 0
			}
			h := int(data[i+5])<<8 | int(data[i+6])
			w := int(data[i+7])<<8 | int(data[i+8])
			return w, h
		}
		// Guard against a malformed length re-looping forever.
		if segLen < 2 {
			return 0, 0
		}
		i += 2 + segLen
	}
	return 0, 0
}

// codecFromProperties renders taglib's (container, inner codec) pair into
// the single Codec string TagInfo carries. Prefer the inner codec when there
// is one, since "ogg" alone cannot tell vorbis from opus — which is exactly
// the kind of guess the old CodecName made when it mapped every OggS file
// to "vorbis" regardless of what was inside.
func codecFromProperties(p taglib.Properties) string {
	if p.InnerCodec != "" {
		return p.InnerCodec
	}
	return p.Format
}

// estimateDuration derives a duration from file size and an assumed bitrate.
//
// This is a guess and is labelled as one: it exists only for the dhowden
// fallback path, where no real properties are available. On the taglib path
// Duration comes from the container's own header and is exact.
func estimateDuration(path string, format Format, size int64) int {
	if size <= 0 {
		return 0
	}
	br := estimateBitrate(path, format, size)
	if br <= 0 {
		return 0
	}
	return int(float64(size) * 8 / float64(br) / 1000)
}

// estimateBitrate is a per-format constant, i.e. a fabrication. It survives
// only on the dhowden fallback path. Measured against real files it was off
// by 6x or more: a 96 kbps FLAC was reported as 1000, a 30 kbps Ogg as 160.
func estimateBitrate(path string, format Format, _ int64) int {
	switch format {
	case FormatMP3:
		return 192
	case FormatFLAC:
		return 1000
	case FormatOGG:
		return 160
	case FormatMP4:
		return 256
	}
	return 0
}

// DecodePictureBase64 is the inverse of the data-URI encoding Read applies
// to Artwork, for the frontend's "data:" convention.
func DecodePictureBase64(s string) ([]byte, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, "", nil
	}
	mimeType := "image/jpeg"
	if strings.HasPrefix(s, "data:") {
		end := strings.Index(s, ";base64,")
		if end < 0 {
			return nil, "", errors.New("malformed data URI")
		}
		mimeType = s[5:end]
		s = s[end+8:]
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, "", fmt.Errorf("base64 decode: %w", err)
	}
	return data, mimeType, nil
}
