package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"go-music-tag/internal/plugin"
	"go-music-tag/internal/taskclient"
	"go-music-tag/internal/tasks"
)

// YoutubeSearch handles POST /api/youtube_search/ — synchronous search via
// the youtube gRPC plugin. Mirrors Django's in-process search.
func YoutubeSearch(c *gin.Context) {
	var req struct {
		Query      string `json:"query" binding:"required"`
		MaxResults int    `json:"max_results"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	if req.MaxResults <= 0 {
		req.MaxResults = 10
	}
	if req.MaxResults > 20 {
		req.MaxResults = 20
	}

	ds, err := plugin.GetDownloadSource("youtube")
	if err != nil {
		Failure(c, "youtube plugin not available: "+err.Error())
		return
	}
	items, err := ds.Search(c.Request.Context(), req.Query, req.MaxResults)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	SuccessData(c, items)
}

// YoutubeDownload handles POST /api/youtube_download/ — enqueue a YouTube
// download task to asynq. Mirrors Django's `download_youtube_task.delay(video_id)`.
//
// Body: { video_id: "..." }
// Optional: extra_audio_format (mp3/m4a/ogg/wav)
//
// Security (P1.5 issue F): extra_audio_format is sanitised by
// tasks.SanitizeYTDLPOutputFormat before being folded into the task
// payload. Anything outside {mp3,m4a,ogg,vorbis,wav} is rejected so the
// worker can't be tricked into passing a yt-dlp flag via this field.
func YoutubeDownload(c *gin.Context) {
	var req struct {
		VideoID       string `json:"video_id" binding:"required"`
		ExtraAudioFmt string `json:"extra_audio_format"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	extraJSON := ""
	if req.ExtraAudioFmt != "" {
		fmt2, err := tasks.SanitizeYTDLPOutputFormat(req.ExtraAudioFmt)
		if err != nil {
			Failure(c, err.Error())
			return
		}
		extraJSON = `{"output_format":"` + fmt2 + `"}`
	}
	taskclient.Init()
	t, err := tasks.NewTypedTask(tasks.TypeYouTubeDownload,
		&tasks.YouTubeDownloadPayload{
			VideoID:   req.VideoID,
			ExtraJSON: extraJSON,
			// RequestedBy is left empty: middleware doesn't propagate user_id
			// yet. Worker logs the owner in db.TaskRecord when this is wired.
		},
		asynq.Queue("default"), asynq.MaxRetry(1), asynq.Timeout(30*60*1e9),
	)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	info, err := taskclient.Enqueue(t)
	if err != nil {
		Failure(c, "enqueue: "+err.Error())
		return
	}
	Success(c, "下载任务已提交，请稍后刷新文件列表查看", gin.H{
		"task_id": info.ID,
		"type":    info.Type,
	})
}
