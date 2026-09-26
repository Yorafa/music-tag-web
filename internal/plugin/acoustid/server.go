// Package acoustid AcoustID gRPC 插件。Ported from applications/task/services/acoust.py + component/mz/run.py。
//
// 关键点：
//   - 不是搜索源：title 字段实际是 **音频文件完整路径**，调 fpcalc 生成 fingerprint 后 POST api.acoustid.org。
//   - SupportsSearch=false（handler 走 fetch_id3_by_title）。
//   - fpcalc 二进制缺失时静默降级返回空，警示日志，让聚合源 (smart_tag) 可继续工作。
//   - fpcalc 的调用与输出解析在 internal/fingerprint，与 dedup 声纹层共用一份。
//     这里用压缩指纹（-json），因为那是 web API 接受的形式；dedup 用的是
//     子指纹列表（-raw），两者不能互相比较，也不能互换。
package acoustid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/fingerprint"
)

const apiURL = "https://api.acoustid.org/v2/lookup"

// defaultAPIKey 是 AcoustID 官方文档里给的公共测试 key，服务端明确写着它
// 「会在几天后过期，不要用在真实应用里」。所以它只是默认值：真实部署应该
// 设 ACOUSTID_API_KEY（注册后在 https://acoustid.org/login 取）。
// 之前这里硬编码的 8o9ZcDHDxb 根本不是有效 key，服务端一律回
// {"error":{"code":4,"message":"invalid API key"}}。
const defaultAPIKey = "fMcSGVkZWAI"

const envAPIKey = "ACOUSTID_API_KEY"

type Server struct {
	pb.UnimplementedTagSourceServer
	client   *http.Client
	fpcalc   string
	apiKey   string
	disabled bool
}

func NewServer() *Server {
	s := &Server{client: &http.Client{Timeout: 15 * time.Second}}
	s.apiKey = os.Getenv(envAPIKey)
	if s.apiKey == "" {
		s.apiKey = defaultAPIKey
	}
	if s.apiKey == defaultAPIKey {
		log.Printf("[acoustid] %s not set; using the shared public test key, which AcoustID may expire. Register at https://acoustid.org/login", envAPIKey)
	}
	if path, err := fingerprint.LookPath(); err == nil {
		s.fpcalc = path
	} else {
		s.disabled = true
		log.Printf("[acoustid] %v; every lookup will return empty", err)
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
//
// 每一个失败分支都返回空而不上报错误，因为聚合源 (smart_tag) 会把本插件
// 和五个真正按曲名搜索的源放在同一个 fan-out 里，一个源的故障不应该让
// 整个抓取失败。代价是「没匹配上」和「根本没跑成」在响应里长得一样，
// 所以下面每条分支都留日志 —— 插件的包注释一直声称有，实际并没有。
func (s *Server) FetchId3ByTitle(ctx context.Context, req *pb.FetchId3Request) (*pb.FetchId3Response, error) {
	if s.disabled {
		return &pb.FetchId3Response{Songs: []*pb.Song{}}, nil
	}
	if req.Title == "" {
		return &pb.FetchId3Response{}, nil
	}
	// The packed form, because that is what the AcoustID web API accepts.
	// The subfingerprint list the dedup stage compares is a different thing
	// and lives in internal/fingerprint alongside this call.
	fp, err := fingerprint.Compress(ctx, s.fpcalc, req.Title)
	if err != nil {
		// 最常见的原因是容器没挂音乐库，fpcalc 打不开文件。
		log.Printf("[acoustid] fpcalc failed for %s: %v", req.Title, err)
		return &pb.FetchId3Response{}, nil
	}
	matches, err := s.match(ctx, fp.Data, fp.Duration)
	if err != nil {
		log.Printf("[acoustid] match request failed for %s (fp len=%d dur=%d): %v", req.Title, len(fp.Data), fp.Duration, err)
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

// ─── match ────────────────────────────────────────────────────────────────

type match struct {
	RecordingID string
	Title       string
	Artist      string
	Album       string
}

// apiRecording 镜像一条 MusicBrainz recording 在 /v2/lookup 响应里的形状。
type apiRecording struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Artists       []apiNamed  `json:"artists"`
	ReleaseGroups []apiTitled `json:"releasegroups"`
}

type apiNamed struct {
	Name string `json:"name"`
}

type apiTitled struct {
	Title string `json:"title"`
}

func matchFor(rec apiRecording) match {
	artist, album := "", ""
	if len(rec.Artists) > 0 {
		artist = rec.Artists[0].Name
	}
	if len(rec.ReleaseGroups) > 0 {
		album = rec.ReleaseGroups[0].Title
	}
	return match{RecordingID: rec.ID, Title: rec.Title, Artist: artist, Album: album}
}

func (s *Server) match(ctx context.Context, fingerprint string, duration int) ([]match, error) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("client", s.apiKey)
	_ = w.WriteField("duration", fmt.Sprint(duration))
	_ = w.WriteField("fingerprint", fingerprint)
	_ = w.WriteField("meta", "recordings releasegroups compress")
	w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var data struct {
		// Errors come back as HTTP 200 with a populated error object, so
		// checking the status code alone is not enough — and returning an
		// empty result set for them is what made this plugin look like it
		// simply never matched anything.
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Results []struct {
			Recordings []apiRecording `json:"recordings"`
			Score      float64        `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("acoustid: decode %s (status %d, body=%.200q): %w", apiURL, resp.StatusCode, raw, err)
	}
	if data.Error != nil {
		return nil, fmt.Errorf("acoustid: api error %d: %s", data.Error.Code, data.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("acoustid: %s returned %s (body=%.200q)", apiURL, resp.Status, raw)
	}
	// 不同的 AcoustID 指纹（不同压制、不同音轨）会指向同一条 MusicBrainz
	// recording，所以直接展开 results 会把同一首歌返回多次。去重，同一条
	// recording 保留 score 最高的那次。
	out := make([]match, 0)
	idx := make(map[string]int, len(data.Results))
	score := make(map[string]float64, len(data.Results))
	for _, r := range data.Results {
		for _, rec := range r.Recordings {
			if i, seen := idx[rec.ID]; seen {
				if r.Score > score[rec.ID] {
					out[i] = matchFor(rec)
					score[rec.ID] = r.Score
				}
				continue
			}
			idx[rec.ID] = len(out)
			score[rec.ID] = r.Score
			out = append(out, matchFor(rec))
		}
	}
	return out, nil
}
