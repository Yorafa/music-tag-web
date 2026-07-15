// Package netease implements the NetEase Cloud Music tag source as a gRPC server.
// Ported from applications/task/services/music_resource.py NetEaseMusicClient.
package netease

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const (
	baseURL = "https://music.163.com"
	// AES-ECB key for linux/forward endpoint (from music-dl)
	aesKeyHex = "7246674226682325323F5E6544673A51"
)

var defaultHeaders = map[string]string{
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	"Referer":    "https://music.163.com/",
}

// Server implements the TagSource gRPC service.
type Server struct {
	pb.UnimplementedTagSourceServer
	aesKey   []byte
	client   *http.Client
}

// NewServer creates a new NetEase plugin server.
func NewServer() (*Server, error) {
	key, err := hex.DecodeString(aesKeyHex)
	if err != nil {
		return nil, fmt.Errorf("aes key decode: %w", err)
	}
	return &Server{
		aesKey: key,
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (s *Server) GetPluginInfo(ctx context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name:           "netease",
		DisplayName:    "网易云音乐",
		SupportsSearch: true,
		SupportsLyric:  true,
		SupportsId3:    true,
	}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	offset := (req.Page - 1) * req.Limit
	songs, err := s.doSearch(ctx, req.Query, int(offset), int(req.Limit))
	if err != nil {
		return &pb.SearchResponse{}, nil
	}
	normalized := s.normalize(songs)
	hasMore := len(songs) >= int(req.Limit)

	pbSongs := make([]*pb.Song, len(normalized))
	for i, song := range normalized {
		pbSongs[i] = &pb.Song{
			Id:       song["id"].(string),
			Name:     song["name"].(string),
			Artist:   song["artist"].(string),
			ArtistId: song["artist_id"].(string),
			Album:    song["album"].(string),
			AlbumId:  song["album_id"].(string),
			AlbumImg: song["album_img"].(string),
			Year:     song["year"].(string),
		}
	}
	return &pb.SearchResponse{Songs: pbSongs, HasMore: hasMore}, nil
}

func (s *Server) FetchId3ByTitle(ctx context.Context, req *pb.FetchId3Request) (*pb.FetchId3Response, error) {
	songs, err := s.doSearch(ctx, req.Title, 0, 10)
	if err != nil {
		return &pb.FetchId3Response{}, nil
	}
	normalized := s.normalize(songs)
	pbSongs := make([]*pb.Song, len(normalized))
	for i, song := range normalized {
		pbSongs[i] = &pb.Song{
			Id:       song["id"].(string),
			Name:     song["name"].(string),
			Artist:   song["artist"].(string),
			ArtistId: song["artist_id"].(string),
			Album:    song["album"].(string),
			AlbumId:  song["album_id"].(string),
			AlbumImg: song["album_img"].(string),
			Year:     song["year"].(string),
		}
	}
	return &pb.FetchId3Response{Songs: pbSongs}, nil
}

func (s *Server) FetchLyric(ctx context.Context, req *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	url := fmt.Sprintf("%s/api/song/lyric?id=%s&lv=-1&kv=-1&tv=-1", baseURL, req.SongId)
	resp, err := s.doGetWithContext(ctx, url)
	if err != nil {
		return &pb.FetchLyricResponse{}, nil
	}
	var data map[string]interface{}
	json.Unmarshal(resp, &data)
	lrc, _ := data["lrc"].(map[string]interface{})
	lyric, _ := lrc["lyric"].(string)
	return &pb.FetchLyricResponse{Lyric: lyric}, nil
}

// --- Internal ---

func (s *Server) doSearch(ctx context.Context, title string, offset, limit int) ([]map[string]interface{}, error) {
	eparamsData := map[string]interface{}{
		"method": "POST",
		"url":    "http://music.163.com/api/cloudsearch/pc",
		"params": map[string]interface{}{
			"s":      title,
			"type":   1,
			"offset": offset,
			"limit":  limit,
		},
	}
	raw, _ := json.Marshal(eparamsData)
	encrypted := s.encodeAES(string(raw))

	body := fmt.Sprintf("eparams=%s", encrypted)
	resp, err := s.doPostWithContext(ctx, baseURL+"/api/linux/forward", "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	json.Unmarshal(resp, &result)
	songs, _ := result["result"].(map[string]interface{})
	list, _ := songs["songs"].([]interface{})

	out := make([]map[string]interface{}, 0, len(list))
	for _, s := range list {
		if m, ok := s.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Server) normalize(songs []map[string]interface{}) []map[string]interface{} {
	for _, song := range songs {
		artists, _ := song["ar"].([]interface{})
		album, _ := song["al"].(map[string]interface{})

		var artist, artistID string
		if len(artists) > 0 {
			names := make([]string, len(artists))
			for i, a := range artists {
				am, _ := a.(map[string]interface{})
				names[i], _ = am["name"].(string)
			}
			artist = strings.Join(names, ",")
			if am, ok := artists[0].(map[string]interface{}); ok {
				if id, ok := am["id"]; ok {
					artistID = fmt.Sprintf("%v", id)
				}
			}
		}

		year := ""
		if yt, ok := song["publishTime"]; ok {
			if yf, ok := yt.(float64); ok && yf > 0 {
				t := time.UnixMilli(int64(yf))
				year = strconv.Itoa(t.Year())
			}
		}
		if year == "" && album != nil {
			if yt, ok := album["publishTime"]; ok {
				if yf, ok := yt.(float64); ok && yf > 0 {
					t := time.UnixMilli(int64(yf))
					year = strconv.Itoa(t.Year())
				}
			}
		}

		cover := ""
		if album != nil {
			if pic, ok := album["picUrl"].(string); ok {
				cover = pic
			}
		}
		albumName := ""
		albumID := ""
		if album != nil {
			if n, ok := album["name"].(string); ok {
				albumName = n
			}
			if id, ok := album["id"]; ok {
				albumID = fmt.Sprintf("%v", id)
			}
		}

		song["artist"] = artist
		song["artist_id"] = artistID
		song["album"] = albumName
		song["album_id"] = albumID
		song["album_img"] = cover
		song["year"] = year
	}
	return songs
}

func (s *Server) encodeAES(raw string) string {
	// PKCS7 padding
	blockSize := 16
	padding := blockSize - len(raw)%blockSize
	padText := make([]byte, padding)
	for i := range padText {
		padText[i] = byte(padding)
	}
	paddedRaw := []byte(raw)
	paddedRaw = append(paddedRaw, padText...)

	block, err := aes.NewCipher(s.aesKey)
	if err != nil {
		return ""
	}
	encrypted := make([]byte, len(paddedRaw))
	for i := 0; i < len(paddedRaw); i += blockSize {
		block.Encrypt(encrypted[i:i+blockSize], paddedRaw[i:i+blockSize])
	}
	return strings.ToUpper(hex.EncodeToString(encrypted))
}

func (s *Server) doGetWithContext(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range defaultHeaders {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *Server) doPostWithContext(ctx context.Context, url, contentType string, body io.Reader) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", url, body)
	req.Header.Set("Content-Type", contentType)
	for k, v := range defaultHeaders {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// --- Kugou helper for reuse across plugins ---

// KugouSignature computes the MD5 hash for KuGou API auth.
func KugouSignature(text string) string {
	h := md5.Sum([]byte(text))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

// SimpleGet sends a GET request.
func SimpleGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// SimplePost sends a POST request.
func SimplePost(url, contentType string, body io.Reader) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, contentType, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
