// Package events defines event payloads + topic constants used across the
// worker / gateway boundary.
//
// Events are JSON-encoded and broadcast over a Bus (Redis in production,
// NullBus in tests). The file-rename lifecycle currently emits one event:
//
//   • TopicFileMoved — emitted by tasks.TidyFolderHandler after a successful
//     os.Rename + db.Folder / db.Track path update. The webhook handler
//     subscribes and uses this to invalidate its in-memory PathCache.
package events

// TopicFileMoved is the topic for filesystem rename events.
const TopicFileMoved = "file:moved"

// FileMovedEvent describes a single rename / move operation. Both paths are
// absolute filesystem paths as recorded by the worker (mirrors db.Folder /
// db.Track.path). Action disambiguates future retries / soft-deletes without
// needing extra topics.
type FileMovedEvent struct {
	Action  string `json:"action"`   // "renamed" | "moved"
	OldPath string `json:"old_path"` // pre-rename path (no longer exists)
	NewPath string `json:"new_path"` // post-rename path (canonical)
}
