// Package tasks defines asynq task types, payloads, and a ProcessFunc adapter
// that wraps each handler so it can be registered with asynq.NewServeMux.
//
// Replaces the Python Celery task system in applications/task/tasks.py.
package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"

	"go-music-tag/internal/cache"
)

// Task type constants. Names mirror Python Celery task names where possible.
const (
	TypeFullScanFolder       = "scan:full"
	TypeUpdateScanFolder     = "scan:update"
	TypeTidyFolder           = "folder:tidy"
	TypePruneEmptyFolders    = "folder:prune_empty"
	TypeDownloadGeneric      = "download:generic" // unified download type — payload.Source dispatches to the matching DownloadSource
	TypeClearMusic           = "db:clear"
	TypeApplyParsedFilenames = "tag:apply_parsed_filenames" // C.2 bulk-apply worker; payload = ApplyParsedFilenamesPayload
)

// --- Payloads ---

type FullScanPayload struct {
	SubPaths [][2]string `json:"sub_paths"` // [(parentUID, path), ...]
}

type TidyFolderPayload struct {
	MusicPaths []string `json:"music_paths"`
	RootPath   string   `json:"root_path"`
	FirstDir   string   `json:"first_dir"`
	SecondDir  string   `json:"second_dir"`
}

// DownloadPayload is the generic, source-routed download task body.
// The new generic download endpoint POST /api/download enqueues this so
// any registered DownloadSource (youtube today, soundcloud/etc tomorrow)
// can share one asynq task type and one worker handler. DestDir is the
// user's chosen server-side destination (`settings.downloadPath`
// relative to MUSIC_DIR) for "加入库"; empty means the global default
// (e.g. /app/media/<downloadPath>), resolved by the worker.
type DownloadPayload struct {
	Source      string `json:"source"` // registered DownloadSource name
	VideoID     string `json:"video_id"`
	DestDir     string `json:"dest_dir,omitempty"` // optional server-side destination (relative to MUSIC_DIR)
	ExtraJSON   string `json:"extra,omitempty"`    // format/output_format/quality
	Batch       string `json:"batch,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`
}

type ClearMusicPayload struct{}

// ApplyParsedFilenamesPayload is the carrier for TypeApplyParsedFilenames.
// Shape mirrors cache.ParsedResult 1:1 so JSON wire-format round-trips
// byte-identically from handler.Save → cache.Load → worker tag.Write.
// We import cache here because (a) the message bus needs a stable wire
// shape and (b) cache is a leaf package — no import cycle risk.
type ApplyParsedFilenamesPayload struct {
	Results []cache.ParsedResult `json:"results"`
}

// --- Encode/Decode helpers ---

func Encode(payload interface{}) ([]byte, error) {
	return json.Marshal(payload)
}

func Decode(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// NewTypedTask 把 payload 编码为 JSON + 用 opts 构造 asynq.Task。
// 供 gateway handler 使用 (e.g. tasks.NewTypedTask(tasks.TypeFullScanFolder, &payload, opts...)).
func NewTypedTask(typeName string, payload interface{}, opts ...asynq.Option) (*asynq.Task, error) {
	var data []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		data = b
	}
	return asynq.NewTask(typeName, data, opts...), nil
}

// --- Handler interface ---

// Handler is the per-type entry point. ctx is asynq's task context which is
// cancelled on retry timeout / worker shutdown.
type Handler interface {
	ProcessTask(ctx context.Context, t Task) error
}

// Task is a typed wrapper passed to handlers so we don't need to repeat the
// payload decode boilerplate in each handler.
type Task struct {
	Type    string
	Payload interface{}
}

// HandlerFunc lets plain functions satisfy Handler.
type HandlerFunc func(ctx context.Context, t Task) error

func (f HandlerFunc) ProcessTask(ctx context.Context, t Task) error {
	return f(ctx, t)
}

// asynqAdapter wraps a Handler and provides an asynq.HandlerFunc.
type asynqAdapter struct {
	decode func(data []byte) (interface{}, error)
	h      Handler
}

func (a *asynqAdapter) ProcessTask(ctx context.Context, t *asynq.Task) error {
	payload, err := a.decode(t.Payload())
	if err != nil {
		return fmt.Errorf("%s: decode payload: %w", t.Type(), err)
	}
	return a.h.ProcessTask(ctx, Task{Type: t.Type(), Payload: payload})
}

// --- Mux registration helpers ---

// NewFullScanMux registers FullScanFolder on the mux.
func NewFullScanMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeFullScanFolder, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p FullScanPayload
			err := Decode(data, &p)
			return &p, err
		},
		h: h,
	}).ProcessTask)
}

func NewUpdateScanMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeUpdateScanFolder, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p FullScanPayload
			err := Decode(data, &p)
			return &p, err
		},
		h: h,
	}).ProcessTask)
}

func NewTidyFolderMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeTidyFolder, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p TidyFolderPayload
			err := Decode(data, &p)
			return &p, err
		},
		h: h,
	}).ProcessTask)
}

// NewPruneEmptyFoldersMux registers the TypePruneEmptyFolders handler.
func NewPruneEmptyFoldersMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypePruneEmptyFolders, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p PruneEmptyFoldersPayload
			err := Decode(data, &p)
			return &p, err
		},
		h: h,
	}).ProcessTask)
}

// NewDownloadGenericMux registers the unified TypeDownloadGeneric handler.
// Handler h is expected to accept *DownloadPayload and dispatch by payload.Source.
func NewDownloadGenericMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeDownloadGeneric, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p DownloadPayload
			err := Decode(data, &p)
			return &p, err
		},
		h: h,
	}).ProcessTask)
}

func NewClearMusicMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeClearMusic, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			return &ClearMusicPayload{}, nil
		},
		h: h,
	}).ProcessTask)
}

// NewApplyParsedFilenamesMux registers the bulk-apply worker. The handler
// (in parsedfilenames.go) is a HandlerFunc over ApplyParsedFilenamesPayload.
func NewApplyParsedFilenamesMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeApplyParsedFilenames, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p ApplyParsedFilenamesPayload
			err := Decode(data, &p)
			return &p, err
		},
		h: h,
	}).ProcessTask)
}
