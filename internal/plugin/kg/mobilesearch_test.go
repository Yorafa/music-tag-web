package kg

import (
	"context"
	"net/http"
	"strings"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// kgMobileBody is the real shape of mobiles.kugou.com/api/v3/search/song
// (captured 2026-09). It is a different API from the signed
// complexsearch endpoint the plugin used to call, and the difference is
// not cosmetic: it renames every field the mapping reads.
//
//	hash          ← was FileHash
//	songname      ← was SongName
//	singername    ← was SingerName
//	album_name    ← was AlbumName
//	album_id      ← was AlbumID
//	duration      ← was Duration (seconds in both)
//	trans_param.union_cover ← was Image
//
// Note the two fields the old shape carried that this one does not:
// SingerId and PublishTime. Artist id and release year are simply not in
// the mobile response, so those rows come back with them empty — which is
// why this endpoint is a fix for "returns nothing at all", not a
// like-for-like swap.
const kgMobileBody = `{
  "status": 1,
  "errcode": 0,
  "data": {
    "total": 480,
    "info": [
      {
        "hash": "b3a52a7a958bf0aed0ebfba2e9a818b7",
        "songname": "晴天",
        "singername": "周杰伦",
        "album_name": "叶惠美",
        "album_id": "966846",
        "duration": 269,
        "filename": "周杰伦 - 晴天",
        "extname": "mp3",
        "trans_param": {
          "union_cover": "http://imge.kugou.com/stdmusic/{size}/20230920/cover.jpg"
        }
      }
    ]
  }
}`

func TestServer_Search_MobileShape(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, kgMobileBody), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "晴天", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Id != "b3a52a7a958bf0aed0ebfba2e9a818b7" {
		t.Errorf("Id = %q, want the `hash` field", s.Id)
	}
	if s.Name != "晴天" {
		t.Errorf("Name = %q, want the `songname` field", s.Name)
	}
	if s.Artist != "周杰伦" {
		t.Errorf("Artist = %q, want the `singername` field", s.Artist)
	}
	if s.Album != "叶惠美" {
		t.Errorf("Album = %q, want the `album_name` field", s.Album)
	}
	if s.AlbumId != "966846" {
		t.Errorf("AlbumId = %q, want the `album_id` field", s.AlbumId)
	}
	// 269s = 4:29, this track's real length.
	if s.Duration < 268 || s.Duration > 270 {
		t.Errorf("Duration = %v, want ~269 (4:29)", s.Duration)
	}
	// The cover hides under trans_param and carries a {size} placeholder.
	if s.AlbumImg != "http://imge.kugou.com/stdmusic/150/20230920/cover.jpg" {
		t.Errorf("AlbumImg = %q, want union_cover with {size}→150", s.AlbumImg)
	}
}

// The mobile endpoint signals failure in the BODY, not the status line: a
// rejected request comes back HTTP 200 with a non-zero errcode and no
// data. Treating that as success yields zero songs and no diagnostic, so
// a rate-limit or geo-block looks identical to "no matches".
func TestServer_Search_MobileErrcodeIsSurfaced(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, `{"status":0,"errcode":-1,"error_msg":"rate limited","data":{}}`), nil
	}}
	_, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Fatal("a non-zero errcode must not be reported as a successful empty search")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %v, want it to carry the upstream message", err)
	}
}

// The keyword is interpolated into the query string. A Chinese or any
// non-ASCII term has to be percent-encoded, and a raw one makes the
// upstream answer HTTP 400 — which is what silently disabled the whole
// source for most real queries.
func TestServer_Search_EncodesTheKeyword(t *testing.T) {
	srv := NewServer()
	var gotURL string
	srv.client.Transport = &kgRT{fn: func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResp(200, kgMobileBody), nil
	}}
	srv.Search(context.Background(), &pb.SearchRequest{Query: "周杰伦 晴天", Page: 1, Limit: 10})
	if !strings.Contains(gotURL, "keyword=%E5%91%A8%E6%9D%B0%E4%BC%A6") {
		t.Errorf("URL = %q, want the keyword percent-encoded", gotURL)
	}
	if strings.Contains(gotURL, "周杰伦") {
		t.Errorf("URL = %q, want no raw non-ASCII in the query", gotURL)
	}
}

// A result with no cover must not panic on the missing trans_param, and
// must still return the rest of the row.
func TestServer_Search_MissingTransParamIsSafe(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, `{"status":1,"errcode":0,"data":{"info":[
			{"hash":"h1","songname":"s","singername":"a","album_name":"","album_id":"","duration":0}
		]}}`), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	if resp.Songs[0].AlbumImg != "" {
		t.Errorf("AlbumImg = %q, want empty", resp.Songs[0].AlbumImg)
	}
	if resp.Songs[0].Id != "h1" {
		t.Errorf("Id = %q, want h1 — the row should survive a missing cover", resp.Songs[0].Id)
	}
}
