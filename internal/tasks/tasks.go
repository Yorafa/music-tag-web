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
)

// Task type constants. Names mirror Python Celery task names where possible.
const (
	TypeFullScanFolder   = "scan:full"
	TypeUpdateScanFolder = "scan:update"
	TypeBatchAutoTag     = "tag:batch_auto"
	TypeTidyFolder       = "folder:tidy"
	TypeYouTubeDownload  = "download:youtube"
	TypeClearMusic       = "db:clear"
)

// --- Payloads ---

type FullScanPayload struct {
	SubPaths [][2]string `json:"sub_paths"` // [(parentUID, path), ...]
}

type BatchAutoTagPayload struct {
	Batch      string   `json:"batch"`
	SourceList []string `json:"source_list"`
	SelectMode string   `json:"select_mode"`
}

type TidyFolderPayload struct {
	MusicPaths []string `json:"music_paths"`
	RootPath   string   `json:"root_path"`
	FirstDir   string   `json:"first_dir"`
	SecondDir  string   `json:"second_dir"`
}

type YouTubeDownloadPayload struct {
	VideoID      string `json:"video_id"`
	DownloadDir  string `json:"download_dir"`
	ExtraJSON    string `json:"extra,omitempty"` // format/output_format/quality
	Batch        string `json:"batch,omitempty"`
	RequestedBy  string `json:"requested_by,omitempty"`
}

type ClearMusicPayload struct{}

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

func NewBatchAutoTagMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeBatchAutoTag, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p BatchAutoTagPayload
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

func NewYouTubeDownloadMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypeYouTubeDownload, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p YouTubeDownloadPayload
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
