package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup retention (E-2121).
//
// The rotation this replaces kept the last SIXTY backups by list position. That
// is a count where the thing being protected is an AGE: how far back can I
// reach? With backups written only before a schema migration the two looked
// alike, but they diverge the moment backups become frequent — sixty hourly
// backups is two and a half days of history, and a burst of migrations would
// evict the older ones outright. The count also had no floor and no ceiling in
// TIME, so "how old is my oldest backup?" had no answer that did not depend on
// how busy the week had been.
//
// So retention is now tiered by age, the shape every backup tool converges on:
//
//	hourly for 24 hours, then daily for 30 days, then weekly for a year
//
// which is about a hundred files at steady state — the same order of magnitude
// the count rotation held, spread over a year instead of over a fortnight.
//
// # Buckets are calendar periods, not rolling windows
//
// A backup's TIER comes from its age (relative to now); its BUCKET comes from
// the calendar (the hour, day, or ISO week its own timestamp falls in). Calendar
// buckets are stable — a file's bucket never changes as the clock advances,
// only its tier does — so the same file set prunes to the same survivors no
// matter when the sweep happens to run.
//
// Within a bucket the NEWEST backup wins, matching restic and borg: the state
// at the end of an hour/day/week is the state you actually want to roll back
// to.
//
// # What it will not touch
//
// Only files named exactly `endless-<YYYYmmdd-HHMMSS>.db` — what BackupDB
// writes — are considered at all. Anything else in the directory is left alone,
// including subdirectories. The old rotation deleted by list position and would
// happily have unlinked a stray file that sorted early.
//
// And the newest backup is never dropped, even when it is older than every
// tier. A prune that can empty the shelf is not a retention policy.

const (
	// backupPrefix and backupSuffix bracket the timestamp in a backup's name.
	backupPrefix = "endless-"
	backupSuffix = ".db"

	// backupStampLayout is the timestamp BackupDB formats into that name. It is
	// LOCAL time — BackupDB uses time.Now().Format — so parsing it back must
	// use time.Local, not UTC.
	backupStampLayout = "20060102-150405"
)

// The retention tiers, as ages measured back from now.
const (
	hourlyWindow = 24 * time.Hour
	dailyWindow  = 30 * 24 * time.Hour
	weeklyWindow = 365 * 24 * time.Hour
)

// backupFile is one file in the backups directory whose name parsed as a
// backup, paired with the timestamp that name encodes.
//
// The timestamp comes from the NAME rather than from the file's mtime: the name
// records when the backup was taken, and mtime records when the bytes were last
// written — which a copy, a restore, or an rsync changes. Retention is a
// statement about the first.
type backupFile struct {
	Name  string
	Stamp time.Time
}

// backupsDir is where backups live: beside the database they back up, so that
// ForceRealDB() redirecting DBPath() at the real database also moves its
// backups out of the sandbox (E-1450).
func backupsDir() string {
	return filepath.Join(filepath.Dir(DBPath()), "backups")
}

// parseBackupName reads the timestamp out of a backup's filename. ok is false
// for any name that is not exactly BackupDB's, which is how everything else in
// the directory stays untouched.
func parseBackupName(name string) (stamp time.Time, ok bool) {
	if !strings.HasPrefix(name, backupPrefix) || !strings.HasSuffix(name, backupSuffix) {
		return time.Time{}, false
	}
	body := name[len(backupPrefix) : len(name)-len(backupSuffix)]
	stamp, err := time.ParseInLocation(backupStampLayout, body, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return stamp, true
}

// retentionBucket returns the calendar period a backup competes within, given
// how old it is. within is false once the backup has aged out of every tier.
//
// A stamp in the FUTURE (clock skew, or a machine that moved timezone) lands in
// the hourly tier rather than in no tier at all: age <= hourlyWindow is true for
// a negative age, which is the conservative answer — keep it.
func retentionBucket(stamp, now time.Time) (bucket string, within bool) {
	age := now.Sub(stamp)
	switch {
	case age <= hourlyWindow:
		return "h:" + stamp.Format("2006-01-02T15"), true
	case age <= dailyWindow:
		return "d:" + stamp.Format("2006-01-02"), true
	case age <= weeklyWindow:
		year, week := stamp.ISOWeek()
		return fmt.Sprintf("w:%04d-%02d", year, week), true
	}
	return "", false
}

// backupsToPrune names the backups the policy drops, newest-first order applied
// so that the survivor of each bucket is its newest member.
func backupsToPrune(files []backupFile, now time.Time) (drop []string) {
	newestFirst := make([]backupFile, len(files))
	copy(newestFirst, files)
	sort.Slice(newestFirst, func(i, j int) bool {
		if newestFirst[i].Stamp.Equal(newestFirst[j].Stamp) {
			return newestFirst[i].Name > newestFirst[j].Name
		}
		return newestFirst[i].Stamp.After(newestFirst[j].Stamp)
	})

	kept := make(map[string]bool, len(newestFirst))
	for i, file := range newestFirst {
		bucket, within := retentionBucket(file.Stamp, now)
		// i == 0 is the newest backup on the shelf, kept whatever its age.
		if i > 0 && (!within || kept[bucket]) {
			drop = append(drop, file.Name)
			continue
		}
		if within {
			kept[bucket] = true
		}
	}
	return drop
}

// pruneBackups applies the retention policy to dir, deleting what has aged out
// and returning how many files it removed. The directory and the clock are
// arguments rather than ambient, which is what makes the policy testable without
// waiting a year.
//
// A missing directory is not an error: nothing has been backed up yet.
//
// Removal errors do not abort the sweep — one unlink failing says nothing about
// the next — but the first is returned, so a directory that has stopped being
// maintained cannot fail silently forever.
func pruneBackups(dir string, now time.Time) (removed int, err error) {
	files, err := listBackups(dir)
	if err != nil {
		return 0, err
	}

	for _, name := range backupsToPrune(files, now) {
		rmErr := os.Remove(filepath.Join(dir, name))
		if rmErr != nil && !os.IsNotExist(rmErr) {
			if err == nil {
				err = fmt.Errorf("prune backup %s: %w", filepath.Join(dir, name), rmErr)
			}
			continue
		}
		removed++
	}
	return removed, err
}

// newestBackup returns the most recent backup in files, by the timestamp in its
// name. ok is false when there are none.
func newestBackup(files []backupFile) (newest backupFile, ok bool) {
	for _, file := range files {
		if !ok || file.Stamp.After(newest.Stamp) {
			newest, ok = file, true
		}
	}
	return newest, ok
}

// listBackups reads dir and returns every file whose name is one of BackupDB's.
// A missing directory yields no files and no error.
func listBackups(dir string) (files []backupFile, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backups dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		stamp, ok := parseBackupName(entry.Name())
		if !ok {
			continue
		}
		files = append(files, backupFile{Name: entry.Name(), Stamp: stamp})
	}
	return files, nil
}
