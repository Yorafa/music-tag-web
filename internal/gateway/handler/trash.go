package handler

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/audit"
	"go-music-tag/internal/trash"
	"go-music-tag/internal/utils"
)

// The delete trash, made visible.
//
// DeleteFiles moves a file to DATA_DIR/.trash/<timestamp>/<path under
// MUSIC_DIR> instead of unlinking it, on the stated grounds that "a hard
// delete would be irreversible from inside the app". That promise was only
// half-kept: the file survived, but nothing in the product could show it to
// anyone, so in practice the operator had to `docker exec` into the box and
// read a dot-directory by hand. A recovery path nobody can find is not a
// recovery path.
//
// Two endpoints, deliberately no third:
//
//	GET  /api/trash/          list batches and their files
//	POST /api/trash/restore/   put files back
//	POST /api/trash/purge/     destroy them for good
//
// There is no purge. Purging is irreversible and irreversible-by-API is the
// thing this feature exists to undo; whoever wants the disk space can remove
// the directory on the host, where the action is visible to them.
//
// (Superseded: PurgeTrash was added on request. It exists, and it is the one
// call in this product that cannot be taken back — so it requires an explicit
// `confirm: true` the restore path does not, and it writes an audit row marked
// `irreversible`.)

// trashBatch is one deletion run. The directory name is the timestamp the
// delete handler stamped it with, so it doubles as the id.
type trashBatch struct {
	ID        string      `json:"id"`
	DeletedAt time.Time   `json:"deleted_at"`
	Files     []trashFile `json:"files"`
	TotalSize int64       `json:"total_size"`
}

type trashFile struct {
	// Path relative to MUSIC_DIR — the same shape delete_files accepted, so
	// a user can see where a file used to live and where it will go back to.
	RelPath     string    `json:"rel_path"`
	Size        int64     `json:"size"`
	ModifiedAt  time.Time `json:"modified_at"`
	IsAudio     bool      `json:"is_audio"`
	ContentType string    `json:"content_type"`
}

type trashListing struct {
	Batches []trashBatch `json:"batches"`
	// Counts describe the WHOLE trash, not just what is listed, so the UI can
	// say "showing 2000 of 4321" rather than implying everything is on screen.
	TotalFiles int    `json:"total_files"`
	Truncated  bool   `json:"truncated"`
	TotalSize  int64  `json:"total_size"`
	Root       string `json:"root"`
}

// maxTrashFiles bounds one listing. A user who has deleted a few thousand
// tracks should see the most recent ones, not a response that walks the
// entire trash on every dialog open.
const maxTrashFiles = 2000

// ListTrash handles GET /api/trash/ — what has been deleted, newest first.
//
// A batch with no files still shows up with an empty file list, because an
// empty batch is itself information: it is a deletion whose files have all
// been restored, and hiding it would make the history disagree with the
// directory listing.
func ListTrash(c *gin.Context) {
	root := filepath.Join(utils.DataDir(), ".trash")
	listing := trashListing{Batches: []trashBatch{}, Root: root}

	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			Failure(c, "读取回收站失败: "+err.Error())
			return
		}
		// No trash at all is an empty trash, not an error — every fresh
		// install is in this state.
		SuccessData(c, listing)
		return
	}

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		batch, files, size, err := readTrashBatch(filepath.Join(root, e.Name()), e.Name())
		if err != nil {
			// One unreadable batch must not hide the rest of the history.
			continue
		}
		// A batch directory with nothing left in it is not a record of
		// anything: its files were restored or purged, and the directory
		// survives only because nothing removes it. Listing it puts a row
		// in the dialog that has no file, no checkbox and nothing to
		// recover — which reads as "there is something here" when there is
		// not. The directory is left on disk rather than deleted here,
		// because a batch that was emptied by a human dropping a file back
		// in is not ours to destroy; a whole-batch purge removes it when
		// the user actually asks.
		if files == 0 {
			continue
		}
		listing.Batches = append(listing.Batches, batch)
		listing.TotalFiles += files
		listing.TotalSize += size
		if files > maxTrashFiles {
			listing.Truncated = true
		}
	}

	// Newest first: the batch someone is looking for is almost always the one
	// they just created.
	sort.Slice(listing.Batches, func(i, j int) bool {
		return listing.Batches[i].ID > listing.Batches[j].ID
	})
	SuccessData(c, listing)
}

// readTrashBatch walks one timestamp directory. It returns the batch, the
// number of files, and their total size.
func readTrashBatch(dir, id string) (trashBatch, int, int64, error) {
	batch := trashBatch{ID: id, Files: []trashFile{}}
	if ts, err := time.ParseInLocation("20060102-150405", id, time.Local); err == nil {
		batch.DeletedAt = ts
	} else {
		// A directory the delete handler did not name. List it anyway — the
		// files are still recoverable, and hiding them helps nobody.
		if info, err := os.Stat(dir); err == nil {
			batch.DeletedAt = info.ModTime()
		}
	}

	var size int64
	count := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable subtree: report the rest
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".") {
			return nil
		}
		count++
		size += info.Size()
		if count > maxTrashFiles {
			return filepath.SkipAll
		}
		batch.Files = append(batch.Files, trashFile{
			RelPath:     rel,
			Size:        info.Size(),
			ModifiedAt:  info.ModTime(),
			IsAudio:     audioext.IsLibraryExt(strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))),
			ContentType: contentTypeFor(rel),
		})
		return nil
	})
	if err != nil {
		return batch, count, size, err
	}
	batch.TotalSize = size
	return batch, count, size, nil
}

// contentTypeFor names a file by extension so the panel can render an image
// and so a caller can tell a track from a picture without a second lookup.
//
// Deliberately not mime.TypeByExtension: that answers from /etc/mime.types on
// unix, so the same file would be "audio/ogg" on one host and
// "application/octet-stream" on an alpine image with no mime.types at all. A
// field that changes meaning with the base image is worse than a short table.
func contentTypeFor(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if audioext.IsLibraryExt(ext) {
		if m, ok := audioMIMEs[ext]; ok {
			return m
		}
		return "audio/" + ext
	}
	switch ext {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "tif", "tiff":
		return "image/" + ext
	case "lrc", "nfo", "cue", "txt", "json":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// audioMIMEs names the audio types whose MIME name is not "audio/<ext>". Only
// the exceptions are listed — the same reasoning audioext's own tables use.
var audioMIMEs = map[string]string{
	"mp3":  "audio/mpeg",
	"m4a":  "audio/mp4",
	"m4b":  "audio/mp4",
	"opus": "audio/ogg",
	"wma":  "audio/x-ms-wma",
	"ape":  "audio/x-ape",
	"aiff": "audio/x-aiff",
	"aif":  "audio/x-aiff",
	"wv":   "audio/x-wavpack",
}

// validTrashBatchID reports whether id can name a batch directory.
//
// A batch id is a NAME, not a path, so the test is "one safe path segment" and
// not the glob-safety check unsafeCacheID does. That check answers a different
// question (`*`, `?`, `[` and `\`), and it waves `..` straight through — which
// resolved the batch to DATA_DIR itself and answered "file not found" for a
// batch the user could plainly see listed. Nothing escaped (SafeJoin still
// contained both ends), but the answer was nonsense.
func validTrashBatchID(id string) bool {
	if id == "" || len(id) > 64 || strings.HasPrefix(id, ".") {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// RestoreTrash handles POST /api/trash/restore/ — move files back.
//
// Body: {"batch_id": "20260927-115027", "rel_paths": ["a/b.ogg", ...]}
//
// Refuses rather than overwrites when the destination already exists: the
// whole point of restoring is to get a file back, and silently replacing a
// track the user has since re-downloaded with a two-day-old copy of the same
// name would be a worse outcome than an error message. The caller can delete
// the current file first and restore again.
func RestoreTrash(c *gin.Context) {
	var req struct {
		BatchID  string   `json:"batch_id" binding:"required"`
		RelPaths []string `json:"rel_paths" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	// A batch id is a directory name this app created, but it arrives from
	// the client, so it is validated as a name before it is ever joined.
	if !validTrashBatchID(req.BatchID) {
		Failure(c, "非法的批次 ID")
		return
	}

	musicRoot := utils.MusicRoot()
	batchDir := filepath.Join(utils.DataDir(), ".trash", req.BatchID)
	if info, err := os.Stat(batchDir); err != nil || !info.IsDir() {
		Failure(c, "回收站里没有这个批次")
		return
	}

	results := make([]restoreRow, 0, len(req.RelPaths))
	restored, failed := 0, 0
	for _, rel := range req.RelPaths {
		row := restoreRow{RelPath: rel}
		// Both ends are resolved under their own root and refused if they
		// escape: a `rel_path` is attacker-controlled and decides where a
		// file is written.
		//
		// The batchDir check looks redundant next to the musicRoot one below,
		// and provably is — but it is kept because it is the one that gives an
		// accurate reason. Same rel string, two roots: if it starts with
		// "..", SafeJoin(musicRoot, rel) necessarily escapes the library and
		// is refused; if it does not, it cannot escape batchDir either. So
		// the destination check alone is what makes the move safe, and
		// without the source check the user would be told "目标路径不在音乐库内"
		// for what is actually a bad path inside the batch.
		src, err := utils.SafeJoin(batchDir, rel)
		if err != nil {
			row.Status, row.Reason = "refused", "回收站内的路径不合法"
			failed++
			results = append(results, row)
			continue
		}
		dst, err := utils.SafeJoin(musicRoot, rel)
		if err != nil {
			row.Status, row.Reason = "refused", "目标路径不在音乐库内"
			failed++
			results = append(results, row)
			continue
		}
		if _, err := os.Lstat(src); err != nil {
			row.Status, row.Reason = "missing", "回收站里没有这个文件"
			failed++
			results = append(results, row)
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			row.Status, row.Reason = "exists", "目标位置已有同名文件，请先处理掉再恢复"
			failed++
			results = append(results, row)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			row.Status, row.Reason, failed = "failed", err.Error(), failed+1
			results = append(results, row)
			continue
		}
		// trash.MoveAside, not os.Rename: DATA_DIR and MUSIC_DIR are
		// separate mounts in the default compose layout, and a rename
		// cannot cross filesystems. This exact mistake made restore return
		// "invalid cross-device link" for every single file while the Go
		// test — one temp dir, one device — passed. MoveAside is the same
		// helper DeleteFiles uses in the other direction, so a restore is
		// the delete it undoes.
		if err := trash.MoveAside(src, dst); err != nil {
			row.Status, row.Reason, failed = "failed", err.Error(), failed+1
			results = append(results, row)
			continue
		}
		row.Status = "restored"
		row.RestoredTo = dst
		restored++
		results = append(results, row)
	}

	status := audit.StatusSuccess
	if failed > 0 && restored > 0 {
		status = audit.StatusPartial
	} else if restored == 0 {
		status = audit.StatusFailed
	}
	audit.Log(c.Request.Context(), audit.ActionDeleteFiles, req.BatchID, "admin", status,
		restored+failed, map[string]interface{}{
			"restored":     restored,
			"failed":       failed,
			"batch_id":     req.BatchID,
			"rel_paths":    req.RelPaths,
			"requested_by": "trash_restore",
		}, nil)

	SuccessData(c, restoreReport{Results: results, Restored: restored, Failed: failed})
}

type restoreRow struct {
	RelPath    string `json:"rel_path"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	RestoredTo string `json:"restored_to,omitempty"`
}

type restoreReport struct {
	Results  []restoreRow `json:"results"`
	Restored int          `json:"restored"`
	Failed   int          `json:"failed"`
}

// PurgeTrash handles POST /api/trash/purge/ — the one irreversible operation
// this product performs on the user's data, so it is the one that requires the
// client to say `confirm: true` rather than merely omitting a field.
//
// The flag is not ceremony. A restore only ever moves a file to a place the
// user can see and a purge only ever removes one from a listing they are
// looking at, but both are one HTTP call away from each other, and a client
// that wires the wrong button — or a retry that re-sends a body it already
// sent — must not be able to destroy data by accident. The UI asks for
// confirmation separately; this is the server refusing to be the only thing
// standing between a mistake and a deleted file.
//
// Body: {"batch_id": "...", "rel_paths": [...], "confirm": true}
// An empty rel_paths purges the whole batch, which is the only way to remove
// a batch directory itself.
func PurgeTrash(c *gin.Context) {
	var req struct {
		BatchID  string   `json:"batch_id" binding:"required"`
		RelPaths []string `json:"rel_paths"`
		Confirm  bool     `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	if !req.Confirm {
		Failure(c, "彻底删除不可撤销，需要显式确认")
		return
	}
	if !validTrashBatchID(req.BatchID) {
		Failure(c, "非法的批次 ID")
		return
	}

	batchDir := filepath.Join(utils.DataDir(), ".trash", req.BatchID)
	if info, err := os.Stat(batchDir); err != nil || !info.IsDir() {
		Failure(c, "回收站里没有这个批次")
		return
	}

	// Whole batch: Remove the directory itself so an emptied batch stops being
	// listed. Refused if anything unexpected is still in there, which also
	// means a stale listing cannot be used to sweep a batch that has since
	// gained files.
	if len(req.RelPaths) == 0 {
		entries, err := os.ReadDir(batchDir)
		if err != nil {
			Failure(c, "读取回收站批次失败: "+err.Error())
			return
		}
		// os.RemoveAll rather than os.Remove: this is the purge, and its whole
		// contract is "this is gone". os.Remove would refuse a batch holding a
		// file the listing did not show, which is safer but is a different
		// endpoint's behaviour.
		if err := os.RemoveAll(batchDir); err != nil {
			Failure(c, "彻底删除失败: "+err.Error())
			return
		}
		audit.Log(c.Request.Context(), audit.ActionTrashPurge, req.BatchID, "admin", audit.StatusSuccess,
			len(entries), map[string]interface{}{
				"purged":       len(entries),
				"batch_id":     req.BatchID,
				"whole_batch":  true,
				"requested_by": "trash_purge",
				"irreversible": true,
			}, nil)
		SuccessData(c, purgeReport{Purged: len(entries)})
		return
	}

	results := make([]purgeRow, 0, len(req.RelPaths))
	purged, failed := 0, 0
	for _, rel := range req.RelPaths {
		row := purgeRow{RelPath: rel}
		// The same containment rule as restore, and for the same reason: rel
		// is attacker-controlled and decides what gets destroyed. A purge that
		// can reach outside .trash is a remote delete for anyone who can POST.
		target, err := utils.SafeJoin(batchDir, rel)
		if err != nil {
			row.Status, row.Reason = "refused", "回收站内的路径不合法"
			failed++
			results = append(results, row)
			continue
		}
		info, err := os.Lstat(target)
		if err != nil {
			row.Status, row.Reason = "missing", "回收站里没有这个文件"
			failed++
			results = append(results, row)
			continue
		}
		// A directory in a batch means a rel_path was crafted to name one. The
		// whole-batch path above is the only way to remove directories, and it
		// is explicit about doing so.
		if info.IsDir() {
			row.Status, row.Reason = "refused", "只能逐个删除文件，删除整个批次请用「彻底删除此批次」"
			failed++
			results = append(results, row)
			continue
		}
		if err := os.Remove(target); err != nil {
			row.Status, row.Reason = "failed", err.Error()
			failed++
			results = append(results, row)
			continue
		}
		row.Status = "purged"
		purged++
		results = append(results, row)
	}

	status := audit.StatusSuccess
	if failed > 0 && purged > 0 {
		status = audit.StatusPartial
	} else if purged == 0 {
		status = audit.StatusFailed
	}
	audit.Log(c.Request.Context(), audit.ActionTrashPurge, req.BatchID, "admin", status,
		purged+failed, map[string]interface{}{
			"purged":       purged,
			"failed":       failed,
			"batch_id":     req.BatchID,
			"rel_paths":    req.RelPaths,
			"requested_by": "trash_purge",
			"irreversible": true,
		}, nil)

	SuccessData(c, purgeReport{Results: results, Purged: purged, Failed: failed})
}

type purgeRow struct {
	RelPath string `json:"rel_path"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type purgeReport struct {
	Results []purgeRow `json:"results,omitempty"`
	Purged  int        `json:"purged"`
	Failed  int        `json:"failed"`
}
