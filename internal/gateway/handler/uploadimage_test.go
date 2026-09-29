package handler

import (
	"bytes"
	"encoding/base64"
	"hash/crc32"
	"image"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/audit"
	"go-music-tag/internal/plugin"
)

// Fixtures are pre-encoded and embedded as base64 on purpose: importing
// image/png here to *build* them would register the decoder and mask the
// registration bug this file is here to catch.
const (
	fixturePNG1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	fixtureGIF1x1 = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
)

func newUploadRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/upload_image/", UploadImage)
	return r
}

// postUpload builds a multipart request with the given body as upload_file.
func postUpload(body []byte, filename string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("upload_file", filename)
	if err != nil {
		panic(err)
	}
	_, _ = fw.Write(body)
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload_image/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	newUploadRouter().ServeHTTP(w, req)
	return w
}

// TestImageCodecsAreRegistered is the regression pin for the bug where
// image.DecodeConfig had no codec linked in, so EVERY cover was rejected
// with "image: unknown format". If someone drops the blank imports in
// update.go, this fails before any other spec in the file gets a chance to
// blame its own fixture.
func TestImageCodecsAreRegistered(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(fixturePNG1x1)
	if err != nil {
		t.Fatal(err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
		t.Fatalf("PNG codec not registered (format=%q): %v", format, err)
	}
}

// TestUploadImage_AcceptsRealImages pins that genuine covers now pass —
// i.e. the fix did not turn the validation into a blanket rejection.
func TestUploadImage_AcceptsRealImages(t *testing.T) {
	for _, tc := range []struct{ name, b64 string }{
		{"png", fixturePNG1x1},
		{"gif", fixtureGIF1x1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := base64.StdEncoding.DecodeString(tc.b64)
			if err != nil {
				t.Fatal(err)
			}
			w := postUpload(raw, "cover."+tc.name)
			if !bytes.Contains(w.Body.Bytes(), []byte(`"result":true`)) {
				t.Fatalf("rejected a valid %s (body=%s)", tc.name, w.Body.String())
			}
		})
	}
}

// assertRejected checks the API envelope rather than the HTTP status.
//
// This codebase's Failure() deliberately answers HTTP 200 with
// {"result":false,"code":"400"} — every handler does it, and axios callers
// branch on `result`. Asserting on w.Code would have passed for every
// rejection and failed for every acceptance, i.e. exactly backwards.
func assertRejected(t *testing.T, w *httptest.ResponseRecorder, wantMsg string) {
	t.Helper()
	body := w.Body.Bytes()
	if bytes.Contains(body, []byte(`"result":true`)) {
		t.Fatalf("accepted — bytes would be written to the tag (body=%s)", body)
	}
	if wantMsg != "" && !bytes.Contains(body, []byte(wantMsg)) {
		t.Errorf("body=%s (want a %q failure)", body, wantMsg)
	}
}

// TestUploadImage_RejectsNonImage: the bytes go straight into an APIC
// frame, so a non-image body must be refused rather than poisoning the tag.
func TestUploadImage_RejectsNonImage(t *testing.T) {
	cases := map[string][]byte{
		"plain text":  []byte("this is definitely not an image"),
		"html":        []byte("<!DOCTYPE html><html><body>hi</body></html>"),
		"json":        []byte(`{"not":"an image"}`),
		"empty":       nil,
		"binary junk": {0x00, 0x01, 0x02, 0xff, 0xfe, 0x7f},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			assertRejected(t, postUpload(body, "cover.jpg"), "")
		})
	}
}

// TestUploadImage_RejectsOversize pins the 20 MiB cap. Sends just over the
// limit; the handler must refuse before base64-encoding the whole body.
func TestUploadImage_RejectsOversize(t *testing.T) {
	oversize := bytes.Repeat([]byte{0x41}, maxUploadImageBytes+1)
	assertRejected(t, postUpload(oversize, "cover.jpg"), "too large")
}

// TestUploadImage_RejectsDecompressionBomb pins the pixel-count ceiling.
//
// A tiny file can declare enormous dimensions in its header, and anything
// that later materialises the pixels (a player's cover renderer included)
// then allocates that much memory. We only read the header here, so the
// declared size is the whole risk.
func TestUploadImage_RejectsDecompressionBomb(t *testing.T) {
	// A PNG IHDR declaring 30000x30000 = 900M pixels, hand-built so this
	// spec still does not import image/png.
	bomb := buildFakeBigPNG(t, 30000, 30000)
	assertRejected(t, postUpload(bomb, "cover.png"), "too large")
}

// buildFakeBigPNG emits a PNG header (signature + IHDR carrying the given
// dimensions) followed by filler. The IHDR CRC must be correct or
// image.DecodeConfig rejects the chunk before it ever reads the dimensions
// — which would make the bomb test pass for the wrong reason ("not
// decodable" rather than "too large").
func buildFakeBigPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) // signature

	ihdr := make([]byte, 13)
	be32 := func(dst []byte, v uint32) {
		dst[0], dst[1], dst[2], dst[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	be32(ihdr[0:4], w)
	be32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // colour type: truecolour

	// PNG chunk layout: length(4) + type(4) + data + crc(4). The CRC covers
	// type + data only — NOT the length prefix. (Verified against
	// image/png: including the length yields "invalid checksum".)
	lenBuf := make([]byte, 4)
	be32(lenBuf, uint32(len(ihdr)))
	buf.Write(lenBuf)
	buf.WriteString("IHDR")
	buf.Write(ihdr)

	crc := crc32.ChecksumIEEE(append(append([]byte{}, "IHDR"...), ihdr...))
	crcOut := make([]byte, 4)
	be32(crcOut, crc)
	buf.Write(crcOut)

	buf.Write(bytes.Repeat([]byte{0}, 64)) // filler
	return buf.Bytes()
}

// keep imports honest: these are used by the shared stream test helpers
// that live in the same package.
var (
	_ = plugin.ResetForTesting
	_ = audit.ActionUploadCover
	_ = pb.PluginInfoResponse{}
)
