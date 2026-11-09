package dbmaint

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LinksFile is the file, in the backups directory, that links each schema
// migration to the backup taken just before it applied. One JSON object per
// line, appended, never rewritten.
//
// Recovery needs the link because "the newest backup" is the wrong answer: the
// hourly backup job writes one after the migration as readily as before it,
// and a backup taken after an unwanted migration carries the migration.
const LinksFile = "pre-migration.jsonl"

// LinkProtection is how long retention keeps a linked backup whatever its
// tier would say. Within it, a later scheduled backup in the same hour cannot
// displace the copy a recovery would restore.
const LinkProtection = 30 * 24 * time.Hour

// Link is one migration's record: the versions either side, the backup taken
// before it, and who ran it.
type Link struct {
	At time.Time `json:"at"`
	// Backup is the backup's file name, inside the backups directory.
	Backup string `json:"backup"`
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	// Binary is the executable that migrated, and Cwd where it was standing:
	// between them they usually name the worktree whose code took the step.
	Binary string `json:"binary,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

// AppendLink records link in backupsDir's links file.
func AppendLink(backupsDir string, link Link) (err error) {
	var f *os.File
	var data []byte

	data, err = json.Marshal(link)
	if err != nil {
		goto end
	}
	f, err = os.OpenFile(filepath.Join(backupsDir, LinksFile),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		goto end
	}
	_, err = f.Write(append(data, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

end:
	if err != nil {
		err = fmt.Errorf("recording the pre-migration backup link in %s: %w", backupsDir, err)
	}
	return err
}

// ReadLinks returns backupsDir's links, oldest first. A missing file is no
// links; a line that does not parse is skipped, because one damaged line must
// not hide every other migration's backup.
func ReadLinks(backupsDir string) (links []Link, err error) {
	f, err := os.Open(filepath.Join(backupsDir, LinksFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var link Link
		if json.Unmarshal(scanner.Bytes(), &link) != nil || link.Backup == "" {
			continue
		}
		links = append(links, link)
	}
	return links, scanner.Err()
}

// ProtectedBackups is the set of backup names that retention must keep at now:
// every backup a link younger than LinkProtection names.
func ProtectedBackups(links []Link, now time.Time) map[string]bool {
	protected := make(map[string]bool)
	for _, link := range links {
		if now.Sub(link.At) <= LinkProtection {
			protected[link.Backup] = true
		}
	}
	return protected
}

// LinkFor picks the link whose backup undoes a migration that left the database
// at dbVersion when the binary carries binaryVersion: the newest link that
// started at or below the binary's version and reached at least the database's.
// ok is false when no link fits.
//
// It never falls back to "the newest backup". A migration nobody linked has no
// backup recovery can vouch for, and saying so is the honest answer.
func LinkFor(links []Link, dbVersion, binaryVersion int64) (link Link, ok bool) {
	for i := len(links) - 1; i >= 0; i-- {
		l := links[i]
		if l.From <= binaryVersion && l.To >= dbVersion && l.From < dbVersion {
			return l, true
		}
	}
	return Link{}, false
}
