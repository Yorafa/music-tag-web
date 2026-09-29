package handler

import "encoding/base64"

// base64Decode is the one place the handler decodes a data URI payload.
//
// It exists as a named function rather than an inline
// base64.StdEncoding.DecodeString so the two cover paths — the inline
// `artwork` on /api/music_id3/ and the binary body of /api/album_cover/ —
// cannot drift into using different alphabets. StdEncoding is correct for
// what taglib emits; RawStdEncoding (which ignores padding) is not, and a
// silently different choice here would corrupt every cover instead of
// failing one.
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
