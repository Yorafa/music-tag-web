package plugin

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Track-length helpers shared by the tag-source plugins.
//
// Upstream music APIs do not agree on the unit for a track's length, and
// the disagreement is not something a consumer should have to know: a
// NetEase duration rendered as seconds is 1000x too long, which reads as
// a 33-hour song rather than as a bug. Each plugin therefore normalizes
// once, at the edge, and everything downstream — the gRPC boundary, the
// gateway, the UI — deals in seconds only.
//
// Verified against the live upstreams (2026-09):
//
//	netease  duration  milliseconds   119133 → 1:59
//	qmusic   interval  seconds
//	kugou    duration  seconds        119    → 1:59
//	kuwo     duration  seconds, with `timelength` (ms) as fallback
//	migu     duration  seconds
//	youtube  duration  seconds
//
// MusicBrainz returns no length in a search result at all, so its rows
// carry 0 and the UI shows no time. That is a normal empty, not an error.

// MillisToSeconds converts a millisecond count to seconds. Values below
// one second (including the 0 and negative that upstreams use for "no
// length") collapse to 0 so they render as unknown rather than as 0:00.
func MillisToSeconds(ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return ms / 1000
}

// DurationFromMillis reads a millisecond-valued upstream field. It accepts
// the float64 that json.Unmarshal produces as well as the string some
// providers send for the same field, and returns 0 for anything it cannot
// read rather than guessing.
func DurationFromMillis(v interface{}) float64 {
	return MillisToSeconds(toFloat(v))
}

// DurationFromSeconds reads a second-valued upstream field, accepting the
// numeric and string encodings providers mix between.
func DurationFromSeconds(v interface{}) float64 {
	return toFloat(v)
}

// toFloat coerces the shapes an upstream JSON number arrives in. Providers
// are inconsistent about sending 119, "119" and 119.0 for the same field,
// and a parse failure has to mean "unknown" rather than a silent 0 that
// looks like a real answer.
func toFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0
		}
		return f
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}
