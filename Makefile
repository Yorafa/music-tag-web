.PHONY: all build gateway worker plugins clean proto

# Binary output directory
OUT_DIR = bin

# Auto-discover all plugin binaries by listing cmd/plugins/<name> dirs
PLUGIN_DIRS := $(wildcard ./cmd/plugins/*)
PLUGINS := $(patsubst ./cmd/plugins/%,%,$(PLUGIN_DIRS))

all: proto build

proto:
	@echo "=== Generating protobuf code ==="
	protoc --go_out=. --go-grpc_out=. api/proto/*.proto

build: gateway worker plugins

gateway:
	@echo "=== Building gateway ==="
	go build -o $(OUT_DIR)/gateway ./cmd/gateway

worker:
	@echo "=== Building worker ==="
	go build -o $(OUT_DIR)/worker ./cmd/worker

plugins:
	@echo "=== Building plugins ==="
	@for plugin in $(PLUGINS); do \
		echo "  -> $$plugin"; \
		go build -o $(OUT_DIR)/$$plugin-plugin ./cmd/plugins/$$plugin; \
	done

clean:
	rm -rf $(OUT_DIR)

run-gateway:
	go run ./cmd/gateway

run-netease:
	go run ./cmd/plugins/netease

run-kugou:
	go run ./cmd/plugins/kugou

run-kuwo:
	go run ./cmd/plugins/kuwo

run-migu:
	go run ./cmd/plugins/migu

run-qmusic:
	go run ./cmd/plugins/qmusic

run-musicbrainz:
	go run ./cmd/plugins/musicbrainz

run-acoustid:
	go run ./cmd/plugins/acoustid

run-worker:
	go run ./cmd/worker

deps:
	go mod tidy

# proto-regen: emit canonical .pb.go files to api/proto/tagplugin/
# (the location the Go module actually imports from).
#
# Why this command looks the way it does:
#   - `-I=api/proto` strips the api/proto/ prefix from the protoc input
#     path, so a file at api/proto/tag_source.proto is treated as
#     `tag_source.proto` for purposes of the path-relative output rule.
#   - `--go_out=api/proto/tagplugin` chooses the output directory.
#   - `--go_opt=paths=source_relative` then maps `tag_source.proto`
#     → `tag_source.pb.go` (just the basename) inside that out dir.
#   - Combined: input `api/proto/tag_source.proto` + the proto's own
#     `option go_package = "go-music-tag/api/proto/tagplugin";` → output
#     `api/proto/tagplugin/tag_source.pb.go` with `package tagplugin`.
#
# Earlier versions of this target used `--go_opt=paths=source_relative`
# WITHOUT `-I=api/proto`, which made protoc emit to BOTH `./api/proto/`
# AND the go_package-mapped `./go-music-tag/api/proto/...` (a dual-write
# bug): the operator then had to manually `cp` the second copy into
# `./api/proto/tagplugin/`. The combined `-I` + re-anchored --go_out is
# the one-pass fix.
#
# Requires: `protoc-gen-go` and `protoc-gen-go-grpc` on $PATH (install
# via `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`
# and `google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`).
proto-regen:
	@echo "=== Regenerating protobuf ==="
	protoc -I=api/proto \
		--go_out=api/proto/tagplugin --go_opt=paths=source_relative \
		--go-grpc_out=api/proto/tagplugin --go-grpc_opt=paths=source_relative \
		api/proto/*.proto
