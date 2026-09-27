package handler

import (
	"context"
	"log"
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
//   - limit：仅对 smart_tag 生效，缺省 DefaultSmartCandidateLimit，上限
//     MaxSmartCandidateLimit。分页是客户端行为，但服务端先把结果截断到 15
//     的话，"查看更多" 翻到第 4 页也只能看到同一批 15 条。
func FetchID3ByTitle(c *gin.Context) {
	var req struct {
		Title    string `json:"title" binding:"required"`
		Resource string `json:"resource" binding:"required"`
		FullPath string `json:"full_path"`
		Limit    int    `json:"limit"`
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
		songs, err := SmartTagSearchLimit(c.Request.Context(), title, req.FullPath, candidateLimit(req.Limit))
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
			// ID is mandatory: PlayButton / resolveStreamUrl build
			// `/api/stream/?src=youtube&id=<video_id>` from song.id.
			// Omitting it produced id= empty → StreamAudio 400
			// "missing src or id" on every YouTube preview click.
			for _, it := range items[start:end] {
				allSongs = append(allSongs, plugin.Song{
					ID:     it.ID,
					Name:   it.Title,
					Artist: it.Channel,
					Cover:  it.Thumbnail,
					Source: source,
					// yt-dlp reports the length in seconds, already
					// normalised by the DownloadItem contract. This was
					// the one source that HAD a duration and dropped it
					// on the floor here, so YouTube rows showed no time
					// while every other source would have.
					Duration: it.Duration,
				})
			}
			newPages[source] = curPage
			hasMore[source] = len(items) >= maxResults
			continue
		}

		src, err := plugin.GetTagSource(source)
		if err != nil {
			// Surface the unreachable-dial failure on the gateway log so
			// operators can tell network-vs-code without packet-tracing.
			// Without this line, a silent continue masks the root cause of
			// "search returned no songs for source X".
			log.Printf("[SearchMusic] tag plugin %q unreachable at registry: %v", source, err)
			newPages[source] = req.Pages[source]
			hasMore[source] = false
			continue
		}
		result, err := src.Search(c.Request.Context(), req.Query, curPage, req.Limit)
		if err != nil {
			// Surface upstream search failures (timeout, HTTP 403, etc.).
			// acoustid intentionally returns no Songs for Search() so we
			// don't expect errors from it; every err here is a real fault.
			log.Printf("[SearchMusic] plugin %q Search() failed: %v", source, err)
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

// DefaultSmartCandidateLimit / MaxSmartCandidateLimit bound how much of a
// fan-out the gateway will return. The default is the historical top-15, so
// every existing caller sees exactly what it saw before; the max is the
// ceiling a client can ask for, because the fan-out cost grows with the
// number of sources, not with the limit.
const (
	DefaultSmartCandidateLimit = 15
	MaxSmartCandidateLimit     = 100
)

// candidateLimit clamps a client-supplied limit. Zero or negative means
// "unspecified", which is the default rather than zero candidates — an
// omitted field is a client that does not know the field exists.
func candidateLimit(requested int) int {
	if requested <= 0 {
		return DefaultSmartCandidateLimit
	}
	if requested > MaxSmartCandidateLimit {
		return MaxSmartCandidateLimit
	}
	return requested
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
	return SmartTagSearchLimit(ctx, title, fullPath, DefaultSmartCandidateLimit)
}

// SmartTagSearchLimit is SmartTagSearch with a caller-chosen cut-off. The
// wrapper exists so the three-argument form every existing caller uses keeps
// its meaning ("the default 15") instead of silently becoming "no limit".
func SmartTagSearchLimit(ctx context.Context, title, fullPath string, limit int) ([]plugin.Song, error) {
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
	// Ordering only. Kept out of plugin.Song.Score so it cannot be mistaken
	// for a confidence — see the comment at the assignment below.
	rank := map[string]float64{}
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
			if sg.Name != "" && matchScoreSimple(title, sg.Name) == 0 {
				continue // 标题不沾边 → 丢弃
			}
			sg = annotateCandidate(title, sg)
			rank[sgKey(sg)] = scoreMatch(title, fileArtist, fileAlbum, sg)
			all = append(all, sg)
		}
	}

	sortSongsByRank(all, rank)

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
	if len(dedup) > limit {
		dedup = dedup[:limit]
	}
	return dedup, nil
}

// sortSongsByRank orders candidates by how well their text matched the
// query. This is a ranking aid, not a measurement: the values it sorts on
// never leave the gateway, because presenting them as a match percentage is
// what made every candidate look identical.
// annotateCandidate records what the gateway learned about a candidate
// while fanning out, and returns it.
//
// It deliberately does NOT touch Score. Score used to be assigned here as
// scoreMatch(title, artist, album, sg) — a 0..6 title-similarity sum that
// the UI then multiplied by 20 to render as a percentage. Since a scrape
// usually has no artist or album to compare (a filename is all the user
// has), that sum was 2 for every candidate and every row read a flat
// "40%", live versions and remixes included. Worse, the assignment
// overwrote the one number in the system that is actually measured:
// AcoustID's acoustic confidence, 0..1, which the plugin container had
// computed and put on the wire.
func annotateCandidate(title string, sg plugin.Song) plugin.Song {
	switch matchScoreSimple(title, sg.Name) {
	case 2:
		sg.TitleMatch = "exact"
	case 1:
		sg.TitleMatch = "partial"
	}
	return sg
}

func sortSongsByRank(s []plugin.Song, rank map[string]float64) {
	// 简单插入排序，分数高的在前
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && rank[sgKey(s[j])] > rank[sgKey(s[j-1])]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// sgKey identifies a candidate for the rank map. ID alone is not enough:
// several sources return an empty ID, and those would otherwise collapse
// into one bucket and be sorted by whatever the first of them scored.
func sgKey(sg plugin.Song) string {
	if sg.ID != "" {
		return sg.Source + "\x00" + sg.ID
	}
	return sg.Source + "\x00" + strings.ToLower(sg.Name) + "\x00" + strings.ToLower(sg.Artist)
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
//
//	完全相同 → 2；子串包含 → 1；token 重叠 ≥ 一半 → 1；不沾 → 0。
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
