package handler

import (
	"context"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/plugin"
	"go-music-tag/internal/tag"
)

// FetchID3ByTitle handles POST /api/fetch_id3_by_title/
//
// 关键差异：
//   - resource == "acoustid"：title 替换为 full_path（Django 一致行为）
//   - resource == "smart_tag"：并发多源 fan-out + 打分 + 去重（对齐 SmartTagClient.fetch_id3_by_title）
func FetchID3ByTitle(c *gin.Context) {
	var req struct {
		Title    string `json:"title" binding:"required"`
		Resource string `json:"resource" binding:"required"`
		FullPath string `json:"full_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}

	title := req.Title

	if req.Resource == "acoustid" {
		title = req.FullPath
	}

	if req.Resource == "smart_tag" {
		songs, err := SmartTagSearch(c.Request.Context(), title, req.FullPath)
		if err != nil {
			Failure(c, err.Error())
			return
		}
		SuccessData(c, songs)
		return
	}

	src, err := plugin.GetTagSource(req.Resource)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	out, err := src.FetchID3ByTitle(c.Request.Context(), title)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	SuccessData(c, out)
}

// FetchLyric handles POST /api/fetch_lyric/
func FetchLyric(c *gin.Context) {
	var req struct {
		SongID   string `json:"song_id" binding:"required"`
		Resource string `json:"resource" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	src, err := plugin.GetTagSource(req.Resource)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	lyric, lerr := src.FetchLyric(c.Request.Context(), req.SongID)
	if lerr != nil {
		SuccessData(c, "未找到歌词 "+lerr.Error())
		return
	}
	SuccessData(c, lyric)
}

// SearchMusic handles POST /api/search_music/ — 多源轮询搜索歌曲。
//
// Stage A of docs/plugable-plugins.md: the frontend's localStorage
// enabled-set IS the single source of truth for which sources to query.
// The handler no longer falls back to "fan-out to everything" when the
// client omits `sources` — an empty or missing `sources` is rejected so
// the two sides stay in agreement. Stage A's smartTagSources() helper
// handles the smart-tag auto-scrape fan-out path explicitly; this
// handler covers only the user-initiated search path.
func SearchMusic(c *gin.Context) {
	var req struct {
		Query   string         `json:"query" binding:"required"`
		Sources []string       `json:"sources" binding:"required,min=1"` // must include ≥1 source; empty ⇒ 400
		Pages   map[string]int `json:"pages"`
		Limit   int            `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	if req.Pages == nil {
		req.Pages = map[string]int{}
	}

	// Binding guarantees at least one source. No silent default-fanout:
	// the frontend's `useSourceStore.enabled` IS the source of truth.
	sources := req.Sources

	allSongs := []plugin.Song{}
	newPages := map[string]int{}
	hasMore := map[string]bool{}

	for _, source := range sources {
		curPage := req.Pages[source] + 1
		if source == "youtube" {
			ds, err := plugin.GetDownloadSource("youtube")
			if err != nil {
				newPages[source] = req.Pages[source]
				hasMore[source] = false
				continue
			}
			maxResults := curPage * req.Limit
			items, err := ds.Search(c.Request.Context(), req.Query, maxResults)
			if err != nil {
				newPages[source] = req.Pages[source]
				hasMore[source] = false
				continue
			}
			start := (curPage - 1) * req.Limit
			end := start + req.Limit
			if start >= len(items) {
				start, end = 0, 0
			}
			if end > len(items) {
				end = len(items)
			}
			for _, it := range items[start:end] {
				allSongs = append(allSongs, plugin.Song{
					Name: it.Title, Artist: it.Channel, Cover: it.Thumbnail, Source: "youtube",
				})
			}
			newPages[source] = curPage
			hasMore[source] = len(items) >= maxResults
			continue
		}

		src, err := plugin.GetTagSource(source)
		if err != nil {
			newPages[source] = req.Pages[source]
			hasMore[source] = false
			continue
		}
		result, err := src.Search(c.Request.Context(), req.Query, curPage, req.Limit)
		if err != nil {
			newPages[source] = req.Pages[source]
			hasMore[source] = false
			continue
		}
		for i := range result.Songs {
			result.Songs[i].Source = source
		}
		allSongs = append(allSongs, result.Songs...)
		newPages[source] = curPage
		hasMore[source] = result.HasMore
	}

	SuccessData(c, gin.H{
		"songs":    allSongs,
		"pages":    newPages,
		"has_more": hasMore,
	})
}

// ─── SmartTagSearch ────────────────────────────────────────────────────────

// smartTagSources returns the registered tag sources that support title
// search. AcoustID is excluded because it fingerprints audio, not titles.
// The slice is rebuilt per call so a plugin registered after startup is
// picked up automatically (no re-init needed).
func smartTagSources() []string {
	out := make([]string, 0, len(plugin.ListTagSources()))
	for _, name := range plugin.ListTagSources() {
		if name == "acoustid" {
			continue
		}
		ts, err := plugin.GetTagSource(name)
		if err != nil || !ts.SupportsSearch() {
			continue
		}
		out = append(out, name)
	}
	return out
}

// SmartTagSearch 并发 fan-out 到所有支持 title-search 的 tag 源，再打分 + 去重 + 取 top-15。
//
// 对齐 Django SmartTagClient.fetch_id3_by_title：
//   - 全路径存在时先 tag.Read(fullPath) 取 audio file 自身的 (title, artist, album)
//     作为打分的 source-of-truth；与 file 元数据完全无关的纯 title 搜索退化为空 artist。
//   - 已加入 ctx 取消：每个 goroutine 入口先查 ctx.Err()，plugin.FetchID3ByTitle(ctx)
//     也是 ctx-aware；channel cap=len(sources) 防止 send 阻塞。
//   - 替代 Stage A 之前的硬编码 `sourcesDefault`：源集现读 plugin registry，
//     新增内置 plugin 自动加入 fan-out（除非它不支持 title search）。
func SmartTagSearch(ctx context.Context, title, fullPath string) ([]plugin.Song, error) {
	if title == "" {
		return nil, nil
	}

	// 从 audio file 自身读取 (title, artist, album) —— 与 Django match_song 一致。
	fileArtist := ""
	fileAlbum := ""
	if fullPath != "" {
		if info, err := tag.Read(fullPath); err == nil {
			if fileArtist == "" {
				fileArtist = info.Artist
			}
			if fileAlbum == "" {
				fileAlbum = info.Album
			}
		}
	}
	if title == "" {
		title = strings.TrimSpace(strings.TrimSuffix(fullPath, "")) // fallback
	}

	sources := smartTagSources()
	if len(sources) == 0 {
		return nil, nil
	}

	type sourceResult struct {
		source string
		songs  []plugin.Song
		err    error
	}
	ch := make(chan sourceResult, len(sources))
	for _, srcName := range sources {
		srcName := srcName
		go func() {
			if ctx.Err() != nil {
				ch <- sourceResult{source: srcName, err: ctx.Err()}
				return
			}
			src, err := plugin.GetTagSource(srcName)
			if err != nil {
				ch <- sourceResult{source: srcName, err: err}
				return
			}
			out, err := src.FetchID3ByTitle(ctx, title)
			ch <- sourceResult{source: srcName, songs: out, err: err}
		}()
	}

	all := []plugin.Song{}
	seenIDs := map[string]bool{}
	for i := 0; i < len(sources); i++ {
		r := <-ch
		if r.err != nil || len(r.songs) == 0 {
			continue
		}
		for _, sg := range r.songs {
			sg.Source = r.source
			id := sg.ID
			if id != "" && seenIDs[id] {
				continue
			}
			if id != "" {
				seenIDs[id] = true
			}
			sg.Score = scoreMatch(title, fileArtist, fileAlbum, sg)
			if sg.Name != "" && matchScoreSimple(title, sg.Name) == 0 {
				continue // 标题不沾边 → 丢弃
			}
			all = append(all, sg)
		}
	}

	sortSongsByScore(all)

	// 去重：同 (name, artist) 取首条
	dedup := []plugin.Song{}
	keySet := map[string]bool{}
	for _, sg := range all {
		k := strings.ToLower(sg.Name) + "\x00" + strings.ToLower(sg.Artist)
		if keySet[k] {
			continue
		}
		keySet[k] = true
		dedup = append(dedup, sg)
	}
	if len(dedup) > 15 {
		dedup = dedup[:15]
	}
	return dedup, nil
}

func sortSongsByScore(s []plugin.Song) {
	// 简单插入排序，分数高的在前
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Score > s[j-1].Score; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// scoreMatch 三维 (title, artist, album) 打分。空字符串字段返回 0。
func scoreMatch(title, artist, album string, sg plugin.Song) float64 {
	t := matchScoreSimple(title, sg.Name)
	a := matchScoreSimple(artist, sg.Artist)
	if artist != "" && a == 0 {
		a = -2
	}
	if artist == "" && a >= 1 && t >= 1 {
		t = 2
	}
	alb := matchScoreSimple(album, sg.Album)
	return t + a + alb
}

var (
	reParen    = regexp.MustCompile(`[\(\[][^)\]]*[\)\]]`)
	reFeat     = regexp.MustCompile(`(?i)\b(feat|ft)\.?\s*\S*`)
	reNonAlnum = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// matchScoreSimple 与 Python match_score 核心等价：
//   完全相同 → 2；子串包含 → 1；token 重叠 ≥ 一半 → 1；不沾 → 0。
func matchScoreSimple(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	ca := cleanForMatch(a)
	cb := cleanForMatch(b)
	if ca == cb {
		return 2
	}
	if ca == "" || cb == "" {
		return 0
	}
	if strings.Contains(cb, ca) || strings.Contains(ca, cb) {
		return 1
	}
	tokensA := strings.Fields(ca)
	tokensB := strings.Fields(cb)
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0
	}
	hits := 0
	setB := map[string]bool{}
	for _, t := range tokensB {
		setB[t] = true
	}
	for _, t := range tokensA {
		if setB[t] {
			hits++
		}
	}
	if hits > 0 && float64(hits) >= float64(len(tokensA))/2 {
		return 1
	}
	return 0
}

func cleanForMatch(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reParen.ReplaceAllString(s, "")
	s = reFeat.ReplaceAllString(s, "")
	s = reNonAlnum.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
