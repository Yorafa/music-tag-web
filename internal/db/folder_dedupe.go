package db

import (
	"fmt"
	"log"

	"gorm.io/gorm"
)

// folderRow is the projection the dedupe needs: which rows share a path,
// and which uid the losers fold into.
type folderRow struct {
	ID   int64
	Path string
	UID  string
}

// DedupeFolderPaths collapses duplicate music_folder rows down to one per
// path, so the unique index on `path` can be created.
//
// Legacy DBs are exactly the ones that need this: fullScan blind-INSERTed
// a fresh uid for every entry on every run, so a library scanned N times
// holds N copies of every path. AutoMigrate would fail to create the index
// against those rows, and the gateway/worker refuse to start when
// AutoMigrate errors — so this must run first.
//
// Rules:
//   - the lowest id wins (oldest row, i.e. the uid the rest of the system
//     has been referencing longest);
//   - children of a losing row are re-parented onto the winner, so the
//     folder tree does not develop orphans;
//   - one transaction, so a crash mid-way leaves the table as it was.
//
// Rows with an empty path are deduped too: they are indistinguishable to a
// unique index, and skipping them would only move the failure to index
// creation.
func DedupeFolderPaths(gdb *gorm.DB) error {
	if !gdb.Migrator().HasTable(&Folder{}) {
		return nil
	}
	table := (Folder{}).TableName()

	// Cheap probe first: a single grouped pass over an indexed column, and
	// the overwhelming majority of databases return nothing here and skip
	// the transaction entirely.
	type dupePath struct {
		Path string
		N    int64
	}
	var dupes []dupePath
	if err := gdb.Raw(
		fmt.Sprintf("SELECT path, COUNT(*) AS n FROM %s GROUP BY path HAVING COUNT(*) > 1", table),
	).Scan(&dupes).Error; err != nil {
		return fmt.Errorf("probe duplicate folder paths: %w", err)
	}
	if len(dupes) == 0 {
		return nil
	}
	log.Printf("[db] %s has %d duplicated path(s); folding them before adding the unique index", table, len(dupes))

	return gdb.Transaction(func(tx *gorm.DB) error {
		var rows []folderRow
		if err := tx.Model(&Folder{}).
			Select("id", "path", "uid").
			Order("id ASC").
			Find(&rows).Error; err != nil {
			return fmt.Errorf("load folder rows: %w", err)
		}
		// path -> surviving uid, and loser uid -> surviving uid.
		keeperByPath := make(map[string]string, len(rows))
		fold := make(map[string]string)
		loserIDs := make([]int64, 0, len(rows))
		for _, r := range rows {
			keep, seen := keeperByPath[r.Path]
			if !seen {
				keeperByPath[r.Path] = r.UID
				continue
			}
			if r.UID != "" {
				fold[r.UID] = keep
			}
			loserIDs = append(loserIDs, r.ID)
		}
		if len(loserIDs) == 0 {
			return nil
		}
		// Re-parent first: children pointing at a folded uid must land on
		// the survivor before the survivor's row disappears. The target uid
		// is never itself a loser, so this cannot cascade.
		for loser, keep := range fold {
			if err := tx.Model(&Folder{}).
				Where("parent_id = ?", loser).
				Update("parent_id", keep).Error; err != nil {
				return fmt.Errorf("reparent children of %s: %w", loser, err)
			}
		}
		if err := tx.Where("id IN ?", loserIDs).Delete(&Folder{}).Error; err != nil {
			return fmt.Errorf("delete duplicate folder rows: %w", err)
		}
		log.Printf("[db] folded %d duplicate folder row(s) into %d distinct path(s)", len(loserIDs), len(keeperByPath))
		return nil
	})
}
