// Package dbrecover puts a backup back in place of the database: the atomic
// install `endless db restore` and `endless db recover` share, the checks that
// prove the result matches its backup, and the question of who still has the
// database open (E-2397).
//
// The install is the fix for the 2026-10-18 empty database. `db restore` used
// to MOVE the live database aside and then copy the backup in, and in the gap
// the path was empty: the tmux status line opened it, and SQLite created a
// fresh database where the restore was about to put the backup. Here the path
// is never empty. The live file is COPIED aside, the backup is copied to a
// temporary name beside the database, and a rename puts it in place — the one
// step POSIX makes atomic. The maintenance lock (internal/dbmaint) covers the
// rest: nobody opens the file while the swap runs.
package dbrecover

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SidecarSuffixes are the files SQLite may keep beside a database: the WAL pair
// and the rollback journal. A stale one beside a fresh file is how the
// 2026-08-10 restore left every reader failing with "database is locked".
var SidecarSuffixes = []string{"-wal", "-shm", "-journal"}

// tmpSuffix names the backup's copy beside the database before the rename.
const tmpSuffix = ".restore-tmp"

// InstallResult names what the install moved.
type InstallResult struct {
	// Parked is the pre-restore copy of the database and its sidecars, in the
	// order written. Empty when there was no database to park.
	Parked []string `json:"parked"`
}

// ParkedDB is the parked database file itself, not a sidecar, or "".
func (r InstallResult) ParkedDB() string {
	if len(r.Parked) == 0 {
		return ""
	}
	return r.Parked[0]
}

// beforeRename runs between the park and the rename. A seam, so a test can
// fail an install partway and prove what a partial failure leaves behind.
var beforeRename = func() error { return nil }

// Install replaces the database at dbPath with backup, atomically.
//
//  1. Copy the database and its sidecars into parkDir as
//     endless-<stamp>.db{,-wal,-shm,-journal}, so the parked set opens as one.
//  2. Copy the backup to a temporary name beside the database.
//  3. Remove the live sidecars: they belong to the file being replaced, and the
//     parked copy already holds their data.
//  4. Rename the temporary copy over the database.
//
// The caller holds the maintenance lock. On an error before step 4 the live
// database is unchanged apart from step 3, whose data is in the parked copy;
// the error says where.
func Install(backup, dbPath, parkDir, stamp string) (res InstallResult, err error) {
	var mode os.FileMode = 0o644
	var info os.FileInfo
	tmp := dbPath + tmpSuffix

	info, err = os.Stat(dbPath)
	switch {
	case err == nil:
		mode = info.Mode().Perm()
	case errors.Is(err, os.ErrNotExist):
		err = nil
	default:
		goto end
	}

	res.Parked, err = park(dbPath, parkDir, stamp)
	if err != nil {
		goto end
	}

	err = copyFile(backup, tmp, mode)
	if err != nil {
		err = fmt.Errorf("copying %s beside the database: %w", backup, err)
		goto end
	}
	defer func() { _ = os.Remove(tmp) }()

	for _, suffix := range SidecarSuffixes {
		rmErr := os.Remove(dbPath + suffix)
		if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = fmt.Errorf("removing the replaced database's %s: %w", suffix, rmErr)
			goto end
		}
	}

	err = beforeRename()
	if err != nil {
		goto end
	}

	err = os.Rename(tmp, dbPath)
	if err != nil {
		err = fmt.Errorf("renaming the restored copy into place: %w", err)
		goto end
	}

end:
	if err != nil && res.ParkedDB() != "" {
		err = fmt.Errorf("%w (the pre-restore database is intact at %s)", err, res.ParkedDB())
	}
	return res, err
}

// park copies the database and its sidecars into dir. The database comes first
// in the result.
func park(dbPath, dir, stamp string) (parked []string, err error) {
	base := filepath.Join(dir, "endless-"+stamp+".db")
	for _, suffix := range append([]string{""}, SidecarSuffixes...) {
		src := dbPath + suffix
		info, statErr := os.Stat(src)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return parked, statErr
		}
		err = os.MkdirAll(dir, 0o755)
		if err != nil {
			return parked, err
		}
		dst := base + suffix
		err = copyFile(src, dst, info.Mode().Perm())
		if err != nil {
			return parked, fmt.Errorf("parking %s: %w", src, err)
		}
		parked = append(parked, dst)
	}
	return parked, nil
}

// copyFile copies src to dst with mode, synced to disk before it returns, so a
// rename that follows publishes a complete file.
func copyFile(src, dst string, mode os.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(dst, mode)
	}
	return err
}
