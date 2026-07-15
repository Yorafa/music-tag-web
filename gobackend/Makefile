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

proto-regen:
	@echo "=== Regenerating protobuf ==="
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/*.proto
