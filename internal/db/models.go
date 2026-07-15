// Package db 包含 GORM 模型与连接初始化。
//
// 表名与字段名严格对齐 Django applications/*/models.py，方便两个后端
// 同表同步 (Django 已迁过的 sqlite3 / mysql 数据库可被 Go 直接消费)。
package db

import "time"

// Folder 镜像 music.models.Folder。
type Folder struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Name         string    `gorm:"column:name"`
	Path         string    `gorm:"column:path;index"`
	Size         int64     `gorm:"column:size"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
	LastScanTime time.Time `gorm:"column:last_scan_time;autoUpdateTime"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
	FileType     string    `gorm:"column:file_type"` // folder|music|image|youtube
	UID          string    `gorm:"column:uid;type:char(32);uniqueIndex"`
	ParentID     string    `gorm:"column:parent_id;type:char(32);index"`
	State        string    `gorm:"column:state"` // none|scanning|scanned|updated
}

// TableName 明确指定表名（与 Django `music_folder` 表对齐）。
func (Folder) TableName() string { return "music_folder" }

// Task 镜像 task.models.Task。
type Task struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	SongName   string    `gorm:"column:song_name"`
	ArtistName string    `gorm:"column:artist_name"`
	FullPath   string    `gorm:"column:full_path;uniqueIndex"`
	State      string    `gorm:"column:state"`
	ParentPath string    `gorm:"column:parent_path"`
	Filename   string    `gorm:"column:filename"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (Task) TableName() string { return "task_task" }

// TaskRecord 镜像 task.models.TaskRecord。
type TaskRecord struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	SongName   string    `gorm:"column:song_name"`
	ArtistName string    `gorm:"column:artist_name"`
	FullPath   string    `gorm:"column:full_path;index"`
	TagSource  string    `gorm:"column:tag_source"`
	Icon       string    `gorm:"column:icon"`
	State      string    `gorm:"column:state;index"`
	Extra      string    `gorm:"column:extra"`
	Batch      string    `gorm:"column:batch;index"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
	// 以下 5 字段 P1 yt_dl handler 需要，Django models.TaskRecord 实际表里没有，
	// GORM AutoMigrate 会自动加列；旧库可让 GORM 加默认值填充。
	TaskID     string    `gorm:"column:task_id"`
	FileName   string    `gorm:"column:file_name"`
	Source     string    `gorm:"column:source"`
	UID        string    `gorm:"column:uid"`
	FileType   string    `gorm:"column:file_type"`
	Status     string    `gorm:"column:status"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (TaskRecord) TableName() string { return "task_taskrecord" }

// Track / Album / Artist / Genre / Attachment 对齐 music/models。
type Track struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string    `gorm:"column:name"`
	Path      string    `gorm:"column:path;uniqueIndex"`
	AlbumID   *int64    `gorm:"column:album_id"`
	ArtistID  *int64    `gorm:"column:artist_id"`
	HasCover  bool      `gorm:"column:has_cover_art"`
	TrackNum  int       `gorm:"column:track_number"`
	DiscNum   int       `gorm:"column:disc_number"`
	Plays     int       `gorm:"column:plays_count"`
	Year      int       `gorm:"column:year"`
	Size      int64     `gorm:"column:size"`
	Suffix    string    `gorm:"column:suffix"`
	Mime      string    `gorm:"column:mimetype"`
	Duration  float64   `gorm:"column:duration"`
	BitRate   int       `gorm:"column:bit_rate"`
	GenreID   *int64    `gorm:"column:genre_id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
	FullText  string    `gorm:"column:full_text"`
	Comment   string    `gorm:"column:comment"`
	Lyrics    string    `gorm:"column:lyrics"`
	AccessedDate time.Time `gorm:"column:accessed_date"` // last-played timestamp; reserved for future playback tracking
}

func (Track) TableName() string { return "music_track" }

type Album struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string    `gorm:"column:name"`
	ArtistID   *int64    `gorm:"column:artist_id"`
	AllArtists string    `gorm:"column:all_artist_ids"`
	MaxYear    int       `gorm:"column:max_year"`
	SongCount  int       `gorm:"column:song_count"`
	PlaysCount int       `gorm:"column:plays_count"`
	Duration   float64   `gorm:"column:duration"`
	GenreID    *int64    `gorm:"column:genre_id"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`
	AccessedAt time.Time `gorm:"column:accessed_date"`
	FullText   string    `gorm:"column:full_text;uniqueIndex"`
	Size       int64     `gorm:"column:size"`
	Comment    string    `gorm:"column:comment"`
	Paths      string    `gorm:"column:paths"`
	Desc       string    `gorm:"column:description"`
	CoverID    *int64    `gorm:"column:attachment_cover_id"`
}

func (Album) TableName() string { return "music_album" }

type Artist struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string    `gorm:"column:name;uniqueIndex"`
	AlbumCount int       `gorm:"column:album_count"`
	FullText   string    `gorm:"column:full_text"`
	SongCount  int       `gorm:"column:song_count"`
	Size       int64     `gorm:"column:size"`
	MBzID      string    `gorm:"column:mbz_artist_id"`
	CoverID    *int64    `gorm:"column:attachment_cover_id"`
}

func (Artist) TableName() string { return "music_artist" }

type Genre struct {
	ID   int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name;uniqueIndex"`
}

func (Genre) TableName() string { return "music_genre" }

type Attachment struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	URL       string    `gorm:"column:url"`
	Creation  time.Time `gorm:"column:creation_date"`
	LastFetch time.Time `gorm:"column:last_fetch_date"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mimetype"`
	File      string    `gorm:"column:file"`
}

func (Attachment) TableName() string { return "music_attachment" }

// TrackAttachment 保留别名以兼容外部可能引用 db.TrackAttachment。
type TrackAttachment = Attachment

// ─── Auth + music app parallel-mirror models ──────────────────────────────
//
// Mirror Django:
//   * django.contrib.auth.models.User
//   * applications.user.models.UserProfile
//   * applications.music.models.Playlist
//   * applications.music.models.TrackFavorite
//
// P1: passwords are plain text (ADMIN_USERS env-driven like handler/auth.go).
// Production should swap to a hashed-password column + bcrypt — out of scope.

type User struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Username     string    `gorm:"column:username;size:150;uniqueIndex"`
	Password     string    `gorm:"column:password;size:128"` // P1: plain (matches handler loadUsers)
	IsSuperuser  bool      `gorm:"column:is_superuser"`
	IsStaff      bool      `gorm:"column:is_staff"`
	IsActive     bool      `gorm:"column:is_active"`
	DateJoined   time.Time `gorm:"column:date_joined;autoCreateTime"`
	LastLogin    time.Time `gorm:"column:last_login"`
}

func (User) TableName() string { return "auth_user" }

type UserProfile struct {
	ID     int64 `gorm:"column:id;primaryKey;autoIncrement"`
	UserID int64 `gorm:"column:user_id;uniqueIndex"` // FK to auth_user.id
}

func (UserProfile) TableName() string { return "user_userprofile" }

type Playlist struct {
	ID              int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Name            string    `gorm:"column:name;size:50"`
	UserID          int64     `gorm:"column:user_id;index"`
	CreationDate    time.Time `gorm:"column:creation_date;autoCreateTime"`
	ModificationDate time.Time `gorm:"column:modification_date;autoUpdateTime"`
	PrivacyLevel    string    `gorm:"column:privacy_level;size:30;default:'instance'"`
}

func (Playlist) TableName() string { return "music_playlist" }

type TrackFavorite struct {
	ID            int64     `gorm:"column:id;primaryKey;autoIncrement"`
	CreationDate  time.Time `gorm:"column:creation_date;autoCreateTime"`
	UserID        int64     `gorm:"column:user_id;index;uniqueIndex:idx_user_track"`
	TrackID       int64     `gorm:"column:track_id;index;uniqueIndex:idx_user_track"`
}

func (TrackFavorite) TableName() string { return "music_trackfavorite" }

// PlaylistTrack 是 Playlist 与 Track 的多对多中间表。
//
// 镜像 Django 的 applications.music.models.PlaylistTrack （Django 侧的
// `playlist.playlist_tracks` related manager，Django 通过 through-model
// 暴露该中间表）。Go 这里建一个显式的 join table，以使 SORT BY 顺序、
// JOIN 查询更輕便。P1.5 issue D 引入，填与 getPlaylists 的 songCount/
// duration 计算。
//
// 索引:
//   * unique (playlist_id, track_id)        防同一 playlist 重复加同一 track
//   * (playlist_id, position)               ORDER BY position
//   * (track_id)                            反向查询某 track 所在 playlist
type PlaylistTrack struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	PlaylistID int64     `gorm:"column:playlist_id;index;uniqueIndex:idx_pl_playlist_track"`
	TrackID    int64     `gorm:"column:track_id;index;uniqueIndex:idx_pl_playlist_track"`
	Position   int       `gorm:"column:position;index:idx_pl_playlist_position"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (PlaylistTrack) TableName() string { return "music_playlisttrack" }
