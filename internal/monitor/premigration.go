package monitor

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mikeschinkel/endless/internal/dbmaint"
)

// BackupBeforeMigrate takes the backup a migration from version `from` to
// version `to` stands on, and links the two (E-2397).
//
// Two things make it different from an ordinary BackupDB:
//
//   - It is always FRESH. The throttle would name a backup up to a minute old,
//     and the writes in that minute are exactly what a recovery from this
//     migration would lose.
//   - It is LINKED. `endless db recover` restores the backup the unwanted
//     migration took just before it applied, found by this link, never by
//     "newest": the hourly job writes a backup after the migration as readily
//     as before it, and that one carries the migration. The link also keeps
//     the backup through retention for dbmaint.LinkProtection, so the newer
//     backup that wins its hour cannot prune it.
//
// A link that cannot be written fails the call, as a failed backup does: a
// migration whose way back cannot be found is the condition this exists to
// prevent.
func BackupBeforeMigrate(ctx context.Context, from, to int64) (BackupResult, error) {
	res, err := backupDB(ctx, true)
	if err != nil && res.Path == "" {
		return res, err
	}
	cwd, _ := os.Getwd()
	linkErr := dbmaint.AppendLink(backupsDir(), dbmaint.Link{
		At:     time.Now(),
		Backup: filepath.Base(res.Path),
		From:   from,
		To:     to,
		Binary: executablePath(),
		Cwd:    cwd,
	})
	if linkErr != nil {
		return res, linkErr
	}
	// A retention failure (err with a Path) rides out unchanged, exactly as
	// BackupDB reports it.
	return res, err
}

// BackupsDir is where backups of the current database live, and where their
// pre-migration links are recorded.
func BackupsDir() string { return backupsDir() }

// PreRestoreDir is where a restore parks the database it replaces: beside the
// database, deliberately NOT in the backups directory, whose retention sweep
// would age a parked copy out on its schedule rather than the user's.
func PreRestoreDir() string { return filepath.Join(filepath.Dir(DBPath()), "pre-restore") }
