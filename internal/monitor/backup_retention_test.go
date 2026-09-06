package monitor

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// now is the reference clock every case below prunes against. A fixed instant,
// because a policy whose result depends on when the test ran is not a policy.
var retentionNow = time.Date(2026, 9, 6, 12, 0, 0, 0, time.Local)

func stampName(t time.Time) string {
	return backupPrefix + t.Format(backupStampLayout) + backupSuffix
}

func TestParseBackupName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Time
		ok    bool
	}{
		{"a backup", "endless-20260906-115959.db", time.Date(2026, 9, 6, 11, 59, 59, 0, time.Local), true},
		{"wrong prefix", "backup-20260906-115959.db", time.Time{}, false},
		{"wrong suffix", "endless-20260906-115959.sqlite", time.Time{}, false},
		{"a sidecar", "endless-20260906-115959.db-wal", time.Time{}, false},
		{"unparsable stamp", "endless-not-a-time.db", time.Time{}, false},
		{"no stamp at all", "endless-.db", time.Time{}, false},
		{"trailing junk", "endless-20260906-115959-copy.db", time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBackupName(tt.input)
			if ok != tt.ok {
				t.Fatalf("parseBackupName(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if ok && !got.Equal(tt.want) {
				t.Errorf("parseBackupName(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

// TestRetentionTiers pins the boundaries themselves: which tier an age lands in
// is the whole policy, and an off-by-one here is a year of history quietly
// pruned to a month.
func TestRetentionTiers(t *testing.T) {
	tests := []struct {
		name   string
		age    time.Duration
		bucket string
		within bool
	}{
		{"just written", 0, "h:2026-09-06T12", true},
		{"an hour old", time.Hour, "h:2026-09-06T11", true},
		{"at the hourly edge", 24 * time.Hour, "h:2026-09-05T12", true},
		{"just past it", 24*time.Hour + time.Second, "d:2026-09-05", true},
		{"a week old", 7 * 24 * time.Hour, "d:2026-08-30", true},
		{"at the daily edge", 30 * 24 * time.Hour, "d:2026-08-07", true},
		{"just past it", 30*24*time.Hour + time.Second, "w:2026-32", true},
		{"at the weekly edge", 365 * 24 * time.Hour, "w:2025-36", true},
		{"past every tier", 365*24*time.Hour + time.Second, "", false},
		// A future stamp is clock skew, not an expired backup. Keeping it is the
		// conservative answer; dropping it would delete the newest file on disk.
		{"stamped in the future", -time.Hour, "h:2026-09-06T13", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, within := retentionBucket(retentionNow.Add(-tt.age), retentionNow)
			if within != tt.within {
				t.Fatalf("within = %v, want %v (bucket %q)", within, tt.within, bucket)
			}
			if bucket != tt.bucket {
				t.Errorf("bucket = %q, want %q", bucket, tt.bucket)
			}
		})
	}
}

// TestBackupsToPruneKeepsNewestPerBucket is the behaviour the count rotation
// could not express: three backups inside one hour collapse to one, and the
// survivor is the newest.
func TestBackupsToPruneKeepsNewestPerBucket(t *testing.T) {
	files := []backupFile{
		{Name: "a", Stamp: retentionNow.Add(-90 * time.Minute)},
		{Name: "b", Stamp: retentionNow.Add(-80 * time.Minute)},
		{Name: "c", Stamp: retentionNow.Add(-70 * time.Minute)},
	}
	// All three fall in the 10:xx hour relative to a 12:00 now, so two go.
	drop := backupsToPrune(files, retentionNow)
	sort.Strings(drop)
	if len(drop) != 2 || drop[0] != "a" || drop[1] != "b" {
		t.Fatalf("drop = %v, want [a b] — the newest of a bucket survives", drop)
	}
}

// TestBackupsToPruneNeverEmptiesTheShelf covers the case the old rotation could
// not reach and the new one must not either: every backup has aged past every
// tier. Pruning to zero would be the policy deleting the only thing standing
// between the operator and data loss.
func TestBackupsToPruneNeverEmptiesTheShelf(t *testing.T) {
	files := []backupFile{
		{Name: "ancient", Stamp: retentionNow.AddDate(-3, 0, 0)},
		{Name: "older", Stamp: retentionNow.AddDate(-4, 0, 0)},
	}
	drop := backupsToPrune(files, retentionNow)
	if len(drop) != 1 || drop[0] != "older" {
		t.Fatalf("drop = %v, want [older] — the newest backup is never dropped", drop)
	}
}

func TestBackupsToPruneEmptyAndSingle(t *testing.T) {
	if drop := backupsToPrune(nil, retentionNow); len(drop) != 0 {
		t.Errorf("empty directory: drop = %v, want none", drop)
	}
	one := []backupFile{{Name: "only", Stamp: retentionNow}}
	if drop := backupsToPrune(one, retentionNow); len(drop) != 0 {
		t.Errorf("single backup: drop = %v, want none", drop)
	}
}

// TestRetentionSteadyState is the policy's headline claim, exercised end to end:
// feed it a year of backups taken every hour and the survivors must be 24
// hourly, then daily out to 30 days, then weekly out to a year — not a count.
//
// The old rotation, given the same input, kept the newest 60 files: two and a
// half days of history and nothing older.
func TestRetentionSteadyState(t *testing.T) {
	var files []backupFile
	for age := time.Duration(0); age <= 366*24*time.Hour; age += time.Hour {
		stamp := retentionNow.Add(-age)
		files = append(files, backupFile{Name: stampName(stamp), Stamp: stamp})
	}

	dropped := make(map[string]bool)
	for _, name := range backupsToPrune(files, retentionNow) {
		dropped[name] = true
	}

	var hourly, daily, weekly, expired int
	for _, file := range files {
		if dropped[file.Name] {
			continue
		}
		age := retentionNow.Sub(file.Stamp)
		switch {
		case age <= hourlyWindow:
			hourly++
		case age <= dailyWindow:
			daily++
		case age <= weeklyWindow:
			weekly++
		default:
			expired++
		}
	}

	// 25, not 24: both the 12:00 backup taken now and the one taken exactly 24h
	// ago sit at an hour boundary, so the window spans 25 distinct hours.
	if hourly != 25 {
		t.Errorf("hourly tier kept %d, want 25 (one per hour across the window)", hourly)
	}
	// 2026-08-07 through 2026-09-05 inclusive — every calendar day the window
	// touches past the hourly tier.
	if daily != 30 {
		t.Errorf("daily tier kept %d, want 30 (one per day past the first)", daily)
	}
	// 52 ISO weeks in the year, less the ~4.3 weeks the finer tiers already hold.
	if weekly < 46 || weekly > 49 {
		t.Errorf("weekly tier kept %d, want 46..49 (one per ISO week)", weekly)
	}
	// The one exception to "older than every tier is dropped" is the newest
	// backup, and nothing here is that old.
	if expired != 0 {
		t.Errorf("kept %d backups older than every tier, want 0", expired)
	}

	total := hourly + daily + weekly
	if total > 110 {
		t.Errorf("steady state kept %d files; the policy is about a hundred", total)
	}
}

// TestPruneBackupsLeavesForeignFilesAlone is the safety property the old
// rotation lacked. It deleted by LIST POSITION, so anything that sorted early —
// a parked file, an operator's copy, a README — went first.
func TestPruneBackupsLeavesForeignFilesAlone(t *testing.T) {
	dir := t.TempDir()

	aged := retentionNow.AddDate(-2, 0, 0)
	mustWrite(t, dir, stampName(aged))
	mustWrite(t, dir, stampName(retentionNow.Add(-30*time.Minute)))
	mustWrite(t, dir, stampName(retentionNow.Add(-45*time.Minute)))
	foreign := []string{
		"README.md",
		"endless.db",
		"endless-20260906-115959.db-wal",
		"pre-restore-20260906-115959.db",
	}
	for _, name := range foreign {
		mustWrite(t, dir, name)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	removed, err := pruneBackups(dir, retentionNow)
	if err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}
	// Two of the three backups share the 11:xx hour bucket; the two-year-old one
	// has aged out of every tier and is not the newest.
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	for _, name := range foreign {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("pruneBackups removed %s, which is not a backup: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "nested")); err != nil {
		t.Errorf("pruneBackups removed the nested directory: %v", err)
	}
}

func TestPruneBackupsMissingDirIsNotAnError(t *testing.T) {
	removed, err := pruneBackups(filepath.Join(t.TempDir(), "never-created"), retentionNow)
	if err != nil {
		t.Errorf("pruneBackups on a missing dir: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestNewestBackup(t *testing.T) {
	if _, ok := newestBackup(nil); ok {
		t.Error("newestBackup(nil) reported a backup")
	}
	files := []backupFile{
		{Name: "old", Stamp: retentionNow.Add(-2 * time.Hour)},
		{Name: "new", Stamp: retentionNow},
		{Name: "middle", Stamp: retentionNow.Add(-time.Hour)},
	}
	got, ok := newestBackup(files)
	if !ok || got.Name != "new" {
		t.Errorf("newestBackup = %q (ok=%v), want \"new\"", got.Name, ok)
	}
}

// TestListBackupsSkipsNonBackups pins what feeds both the throttle and the
// sweep: only BackupDB's own filenames.
func TestListBackupsSkipsNonBackups(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, stampName(retentionNow))
	mustWrite(t, dir, "endless.db")
	mustWrite(t, dir, "notes.txt")

	files, err := listBackups(dir)
	if err != nil {
		t.Fatalf("listBackups: %v", err)
	}
	if len(files) != 1 || files[0].Name != stampName(retentionNow) {
		t.Errorf("listBackups = %v, want just the one backup", files)
	}
}

func mustWrite(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
