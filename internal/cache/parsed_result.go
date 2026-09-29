// Package cache — parsed_result.go holds the per-row shape the 解析文件名
// write carries from the gateway to the worker.
//
// It used to live beside the preview token cache, back when the flow was
// preview → token → apply and this struct was what the token held. The
// token is gone (the dialog renders its plan locally and the apply derives
// it from paths + rule), but the row is not: it is what travels in the
// asynq payload, and it is the trust boundary for the write — a tag the
// struct does not carry cannot be written, so a missing field fails
// closed.
package cache

// ParsedResult is one row's parse outcome.
//
// Status mirrors utils.Status* ("ok" / "ambiguous" / "unparsable") so the
// JSON wire shape round-trips byte-identically with the parser's own
// vocabulary.
type ParsedResult struct {
	Path        string `json:"path"`
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumartist,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Year        string `json:"year,omitempty"`
	TrackNumber string `json:"tracknumber,omitempty"`
	DiscNumber  string `json:"discnumber,omitempty"`
	Status      string `json:"status"`
}

// Empty reports whether the row carries nothing to write. The worker skips
// these rather than calling tag.Write with an empty update.
func (r ParsedResult) Empty() bool {
	return r.Title == "" && r.Artist == "" && r.Album == "" &&
		r.AlbumArtist == "" && r.Genre == "" && r.Year == "" &&
		r.TrackNumber == "" && r.DiscNumber == ""
}
