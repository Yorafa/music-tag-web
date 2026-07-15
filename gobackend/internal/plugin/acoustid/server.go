// Package acoustid AcoustID gRPC 插件。Ported from applications/task/services/acoust.py + component/mz/run.py。
//
// 关键点：
//   - 不是搜索源：title 字段实际是 **音频文件完整路径**，调 fpcalc 生成 fingerprint 后 POST api.acoustid.org。
//   - SupportsSearch=false（handler 走 fetch_id3_by_title）。
//   - fpcalc 二进制缺失时静默降级返回空，警示日志，让聚合源 (smart_tag) 可继续工作。
//   - fpcalc v1.5+ 支持 -json 输出；老版本（v1.4 等）自动 fallback 到文本行解析。
package acoustid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const apiURL = "https://api.acoustid.org/v2/match"

type Server struct {
	pb.UnimplementedTagSourceServer
	client   *http.Client
	fpcalc   string
	disabled bool
}

func NewServer() *Server {
	s := &Server{client: &http.Client{Timeout: 15 * time.Second}}
	s.fpcalc = "fpcalc"
	if path, err := exec.LookPath("fpcalc"); err == nil {
		s.fpcalc = path
	} else {
		s.disabled = true
	}
	return s
}

func (s *Server) GetPluginInfo(_ context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name: "acoustid", DisplayName: "AcoustID 音频指纹",
		SupportsSearch: false, SupportsLyric: false, SupportsId3: true,
	}, nil
}

func (s *Server) Search(_ context.Context, _ *pb.SearchRequest) (*pb.SearchResponse, error) {
	return &pb.SearchResponse{Songs: []*pb.Song{}, HasMore: false}, nil
}

// FetchId3ByTitle: title 实际为文件路径。fpcalc 缺失则返回空。
func (s *Server) FetchId3ByTitle(ctx context.Context, req *pb.FetchId3Request) (*pb.FetchId3Response, error) {
	if s.disabled {
		return &pb.FetchId3Response{Songs: []*pb.Song{}}, nil
	}
	if req.Title == "" {
		return &pb.FetchId3Response{}, nil
	}
	fp, duration, err := runFPCalc(s.fpcalc, req.Title)
	if err != nil {
		return &pb.FetchId3Response{}, nil
	}
	matches, err := s.match(ctx, fp, duration)
	if err != nil {
		return &pb.FetchId3Response{}, nil
	}
	out := make([]*pb.Song, len(matches))
	for i, m := range matches {
		out[i] = &pb.Song{
			Id: m.RecordingID, Name: m.Title, Artist: m.Artist, Album: m.Album,
		}
	}
	return &pb.FetchId3Response{Songs: out}, nil
}

func (s *Server) FetchLyric(_ context.Context, _ *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	return &pb.FetchLyricResponse{}, nil
}

// ─── fpcalc ────────────────────────────────────────────────────────────────

// runFPCalc 优先 fpcalc v1.5+ 的 -json 模式；旧版走文本模式 fallback。
func runFPCalc(fpcalcPath, audioPath string) (string, int, error) {
	cmd := exec.Command(fpcalcPath, "-json", audioPath)
	out, err := cmd.Output()
	if err == nil {
		var parsed struct {
			Duration    float64 `json:"duration"`
			Fingerprint string  `json:"fingerprint"`
		}
		if jerr := json.Unmarshal(out, &parsed); jerr == nil && parsed.Fingerprint != "" {
			return parsed.Fingerprint, int(parsed.Duration), nil
		}
	}
	return runFPCalcText(fpcalcPath, audioPath)
}

// runFPCalcText 解析 fpcalc 默认文本输出：DURATION=N\nFINGERPRINT=...
func runFPCalcText(fpcalcPath, audioPath string) (string, int, error) {
	cmd := exec.Command(fpcalcPath, audioPath)
	out, err := cmd.Output()
	if err != nil {
		return "", 0, fmt.Errorf("fpcalc: %w", err)
	}
	var (
		fp  string
		dur int
	)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "FINGERPRINT="):
			fp = strings.TrimPrefix(line, "FINGERPRINT=")
		case strings.HasPrefix(line, "DURATION="):
			if s := strings.TrimPrefix(line, "DURATION="); s != "" {
				var d float64
				fmt.Sscanf(s, "%f", &d)
				dur = int(d)
			}
		}
	}
	if fp == "" {
		return "", 0, fmt.Errorf("fpcalc text: no fingerprint (output=%q)", string(out))
	}
	return fp, dur, nil
}

// ─── match ────────────────────────────────────────────────────────────────

type match struct {
	RecordingID string
	Title       string
	Artist      string
	Album       string
}

func (s *Server) match(ctx context.Context, fingerprint string, duration int) ([]match, error) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("client", "8o9ZcDHDxb")
	_ = w.WriteField("duration", fmt.Sprint(duration))
	_ = w.WriteField("fingerprint", fingerprint)
	_ = w.WriteField("meta", "recordings releasegroups compress")
	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", apiURL, body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var data struct {
		Results []struct {
			Recordings []struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				ReleaseGroups []struct {
					Title string `json:"title"`
				} `json:"releasegroups"`
			} `json:"recordings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	out := make([]match, 0)
	for _, r := range data.Results {
		for _, rec := range r.Recordings {
			artist := ""
			if len(rec.Artists) > 0 {
				artist = rec.Artists[0].Name
			}
			album := ""
			if len(rec.ReleaseGroups) > 0 {
				album = rec.ReleaseGroups[0].Title
			}
			out = append(out, match{
				RecordingID: rec.ID, Title: rec.Title, Artist: artist, Album: album,
			})
		}
	}
	return out, nil
}
