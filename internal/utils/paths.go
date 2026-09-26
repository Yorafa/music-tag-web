// Package utils — programmatic accessors for filesystem roots used by the
// path-traversal defence. Tests pin the env so SafeJoin consumers can
// build a single contract.
package utils

import "os"

// MusicRoot returns the trusted music-library root. Both FileList /
// MusicID3 / UpdateID3 / BatchUpdateID3 root user-supplied paths under
// this directory via utils.SafeJoin. Centralising the env read here
// keeps every handler agreeing on the same root.
func MusicRoot() string {
	if v := os.Getenv("MUSIC_DIR"); v != "" {
		return v
	}
	return "/app/media"
}

// DataDir returns the writable state directory (SQLite db, downloads, and
// now the delete trash). Same env-first contract as MusicRoot so handlers
// and config.Config agree without threading a config value through every
// call site — config.DataDir is the same read, just resolved at boot.
func DataDir() string {
	if v := os.Getenv("DATA_DIR"); v != "" {
		return v
	}
	return "/app/data"
}
