// file_test.go — FileList + MusicID3 端到端测试。
//
// 关键回归点：Round-7 时 P1.5 hardening 把 SafeJoin(root, "") 拒绝为空串，
// 与 handler/file.go::FileList 文档里承诺的 "empty FilePath = 列出 MUSIC_DIR"
// 不一致。本次把 SafeJoin 改回 honour 空路径（返回 cleanedRoot），本文件用
// 真实 httptest 路径钉住两端契约：
//
//  1. 前端 POST /api/file_list/ with {"file_path": ""} 必须成功
//  2. 返回 data[0].Children 必须等于 MUSIC_DIR 根目录的条目
//  3. 把每次与 utils.SafeJoin 的契约改动耦合起来，未来谁再收紧
//     SafeJoin 的空字符串分支，CI 就会立刻在这里红掉。
//  4. POST /api/music_id3/ 恢复后端读 tag（前端 hydrate / openEditor 依赖）。
package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/config"
)

// TestMain 给他 handler 包下所有测试一个统一的"干净环境"，避免被现有
// 测试套件 (e.g. auth_test.go) 在 init 阶段触发 config.Load() 自动
// 写 bootstrap 凭据到 /app/data（容器外没有 /app 写权限）。这里只设置
// ADMIN_USERS 让 config.Load() 直接走 "real env" 路径不写文件。
func TestMain(m *testing.M) {
	// 自测试的环境 override：admin seed 让 config.Load() 不去写
	// /app/data/.bootstrap-creds（该路径在容器外不可写）。
	_ = os.Setenv("ADMIN_USERS", "admin:$2b$10$abcdefghijklmnopqrstuv")
	_ = os.Setenv("JWT_SECRET", "test-jwt-secret-file-list-pin-test-do-not-use-in-prod")
	// 关闭 webhook auto-bootstrap 噪声
	_ = os.Setenv("WEBHOOK_INTERNAL_TOKEN", "test-webhook-token-not-real")
	// ALLOW_INSECURE_DEFAULTS 不开——保持 production-grade 拒绝行为
	// (即禁止 default admin:admin fallback)。

	// Redirect the bootstrap creds file to a writable test-only path so
	// ./internal/gateway/handler 测试在容器外（无 /app/data 写权限）
	// 也能跑完 auth_test.go 的 Load()。 The path lives under a tmp dir
	// private to this handler-package test bin; persisted across runs in
	// the same dir so subsequent invocations skip the first-boot-generate
	// path, matching the prod idempotency contract.
	if dir, err := os.MkdirTemp("", "mtw-testbootstrap-*"); err == nil {
		config.OverrideBootstrapPathForTest(filepath.Join(dir, ".bootstrap-creds"))
	}
	os.Exit(m.Run())
}

// TestFileList_EmptyFilePathListsRoot pins:
//
//	POST /api/file_list/  body={"file_path":""}  → 200 OK + listing of
//	utils.MusicRoot() (= the MUSIC_DIR env var).
//
// 是 SafeJoin("operate-on-root sentinel") 契约在端到端的 regression 钉子。
func TestFileList_EmptyFilePathListsRoot(t *testing.T) {
	// ─── Setup: tmpdir as MUSIC_DIR, seed two entries ───
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song1.mp3"), []byte("stub"), 0o644); err != nil {
		t.Fatalf("seed mp3: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "album"), 0o755); err != nil {
		t.Fatalf("seed album dir: %v", err)
	}
	t.Setenv("MUSIC_DIR", dir)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/file_list/", FileList)

	body := strings.NewReader(`{"file_path": ""}`)
	req, _ := http.NewRequest("POST", "/api/file_list/", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d (want 200); body=%s", w.Code, w.Body.String())
	}

	// ─── Decode response ───
	var resp struct {
		Result  bool       `json:"result"`
		Code    string     `json:"code"`
		Data    []FileItem `json:"data"`
		Message string     `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, "<truncated>")
	}
	if !resp.Result {
		t.Fatalf("result=false (want true); message=%q", resp.Message)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("data length=%d (want 1 — the root wrapper)", len(resp.Data))
	}
	root := resp.Data[0]

	// ─── Cross-check children against real os.ReadDir(root) ───
	wantNames := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(dir): %v", err)
	}
	for _, e := range entries {
		wantNames[e.Name()] = "" // we just care about presence + icon
		if e.IsDir() {
			wantNames[e.Name()] = "icon-folder"
		} else {
			wantNames[e.Name()] = "icon-script-file"
		}
	}

	gotNames := map[string]string{}
	for _, c := range root.Children {
		gotNames[c.Name] = c.Icon
	}
	for name, wantIcon := range wantNames {
		gotIcon, ok := gotNames[name]
		if !ok {
			t.Errorf("child %q present on disk but missing from payload", name)
			continue
		}
		if gotIcon != wantIcon {
			t.Errorf("child %q: icon=%q (want %q)", name, gotIcon, wantIcon)
		}
	}
	for name := range gotNames {
		if _, ok := wantNames[name]; !ok {
			t.Errorf("child %q in payload but not on disk", name)
		}
	}

	// ─── Must NOT be the literal "path is empty" error from Round-7 ───
	if strings.Contains(resp.Message, "path is empty") {
		t.Fatalf("regressed to Round-7 error: %q", resp.Message)
	}
	if strings.Contains(resp.Message, "SafeJoin") {
		t.Fatalf("regressed to SafeJoin-leak error: %q", resp.Message)
	}

	// ─── Sanity: nested-file (non-empty file_path) still works ───
	body2 := strings.NewReader(`{"file_path": "album"}`)
	req2, _ := http.NewRequest("POST", "/api/file_list/", body2)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("nested album/ listing: status=%d (want 200); body=%s", w2.Code, w2.Body.String())
	}
}

// TestMusicID3_ReadsTagsFromMusicDir pins the restored backend-owned
// tag-read path used by frontend hydrateTags / openEditor:
//
//	POST /api/music_id3/ {file_path, file_name}
//	  → SafeJoin(MUSIC_DIR, file_path, file_name)
//	  → tag.Read → SuccessData envelope with title/filename/…
//
// Without this route the frontend falls back to browser Range-parse
// (/media + music-metadata), which is less efficient for batch hydrate.
func TestMusicID3_ReadsTagsFromMusicDir(t *testing.T) {
	dir := t.TempDir()
	// Minimal parseable ID3v2 header — dhowden/tag sniffs "ID3" and
	// returns empty tags rather than error; handler still fills
	// filename/size and SuccessData's envelope.
	stub := []byte("ID3\x03\x00\x00\x00\x00\x00\x00")
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), stub, 0o644); err != nil {
		t.Fatalf("seed mp3: %v", err)
	}
	t.Setenv("MUSIC_DIR", dir)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/music_id3/", MusicID3)

	body := strings.NewReader(`{"file_path":"","file_name":"song.mp3"}`)
	req, _ := http.NewRequest("POST", "/api/music_id3/", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d (want 200); body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Result  bool                   `json:"result"`
		Code    string                 `json:"code"`
		Data    map[string]interface{} `json:"data"`
		Message string                 `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if !resp.Result {
		t.Fatalf("result=false message=%q", resp.Message)
	}
	if resp.Data == nil {
		t.Fatalf("data is nil")
	}
	if got, _ := resp.Data["filename"].(string); got != "song.mp3" {
		t.Fatalf("filename=%v (want song.mp3); data=%v", resp.Data["filename"], resp.Data)
	}
}

// TestMusicID3_RejectsPathEscape keeps the SafeJoin defence for
// file_name / file_path traversal attempts.
func TestMusicID3_RejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUSIC_DIR", dir)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/music_id3/", MusicID3)

	body := strings.NewReader(`{"file_path":"..","file_name":"etc/passwd"}`)
	req, _ := http.NewRequest("POST", "/api/music_id3/", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d (want 200 envelope); body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Result  bool   `json:"result"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Result {
		t.Fatalf("expected result=false on path escape; message=%q", resp.Message)
	}
	if !strings.Contains(resp.Message, "路径不安全") {
		t.Fatalf("message=%q (want 路径不安全)", resp.Message)
	}
}
