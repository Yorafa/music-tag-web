// Package config holds cross-cutting configuration loaders for the
// gateway. Loader.go specifically serves the C.4 / Stage B plugin
// YAML override flow: a directory (default `data/sources`) containing
// one YAML per plugin; each YAML can override that plugin's runtime
// `api_base` URL or rotate one or more header-level secrets.
//
// `LoadSourceOverrides` runs at gateway startup AND on every
// `POST /api/sources/refresh/`. It is intentionally tolerant of missing
// / malformed files so a single bad config cannot break startup or
// runtime overrides — everything that's wrong just logs and continues.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"go-music-tag/internal/plugin"
)

// SourceOverride mirrors the YAML schema read from each
// `data/sources/<name>.yaml`. We use the plugin package's alias here so
// the loader's result type is the same one (*plugin.Registry).RefreshOverrides
// consumes — handlers stay thin (load + dispatch), and there's only one
// canonical shape across config + plugin layers.
//
// YAML schema (one file per plugin) — example for kuwo:
//
//	name: kuwo
//	api_base: "https://cn-ali-mirror.example.com"   # optional
//	secrets:
//	  kuwoSecret: "new-key-by-2026"                  # optional
//	enabled: true                                    # reserved (see Open Details G)
//
// The plugin name in `name` is matched against the registry key; any
// file whose plugin name is not in the registry is silently dropped at
// dispatch time (forward-compatible: a future plugin can ship with its
// YAML ahead of being registered).
type SourceOverride = plugin.SourceOverride

// LoadSourceOverrides scans `dir` for *.yaml files and returns a map
// keyed by SourceOverride.Name. Missing dir → empty map (no error).
// Per-file failures do not abort the scan — a parse error is logged,
// the file is quarantined as `<name>.yaml.bak`, and the loop continues.
// Quarantining (vs deleting / leaving in place) means the operator
// can recover the failed file from the .bak copy without re-typing.
func LoadSourceOverrides(dir string) (map[string]SourceOverride, error) {
	out := make(map[string]SourceOverride)
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing dir is benign: operator hasn't created any
			// overrides yet. Empty map returned without error.
			return out, nil
		}
		return nil, fmt.Errorf("config: read dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"[config] %s: read error: %v (skipping)\n", path, err)
			continue
		}
		var ov SourceOverride
		if err := yaml.Unmarshal(data, &ov); err != nil {
			fmt.Fprintf(os.Stderr,
				"[config] %s: parse error: %v (quarantining to %s.bak)\n",
				path, err, path)
			// Rename the bad file aside so the next refresh doesn't
			// repeat the loop. Operator can read .bak to see what failed.
			if rerr := os.Rename(path, path+".bak"); rerr != nil {
				fmt.Fprintf(os.Stderr,
					"[config] %s: quarantine rename failed: %v\n", path, rerr)
			}
			continue
		}
		if ov.Name == "" {
			// YAML.name missing: fall back to filename so operators can
			// ship an override file without specifying name: explicitly.
			base := filepath.Base(e.Name())
			ov.Name = base[:len(base)-len(".yaml")]
		}
		out[ov.Name] = ov
	}
	return out, nil
}
