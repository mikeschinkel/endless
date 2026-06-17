package tmuxcmd

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// withMonitorDB stands up a fresh schema-applied SQLite DB and rebinds
// monitor.DB() to it for the lifetime of t via monitor.SetTestDB, so
// the renderer's GetActiveBlockers call resolves against a seeded DB
// instead of the real one. Pattern lifted from internal/web tests.
//
// Concurrency: SetTestDB mutates package-level state in monitor; tests
// using this helper must NOT call t.Parallel().
func withMonitorDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "endless.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)
	return db
}

// seedTaskWithStatus inserts a tasks row with the given id and status
// under project 1.
func seedTaskWithStatus(t *testing.T, db *sql.DB, id int64, status string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, 1, 'task', ?)",
		id, status,
	); err != nil {
		t.Fatalf("seed task id=%d: %v", id, err)
	}
}

// seedBlocks inserts a task_deps "blocks" row meaning blockerID blocks
// taskID. Mirrors the helper of the same name in monitor/_test.
func seedBlocks(t *testing.T, db *sql.DB, blockerID, taskID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, 'blocks')`,
		blockerID, taskID,
	); err != nil {
		t.Fatalf("seed blocks %d→%d: %v", blockerID, taskID, err)
	}
}

// TestFormat_OmitsEmptyFields pins the compact-row contract: format
// builds "[E-N] · A · B · …" but drops any segment whose source field
// is empty, so a barely-populated task doesn't render gratuitous bullet
// separators.
func TestFormat_OmitsEmptyFields(t *testing.T) {
	tier := int64(3)
	tests := []struct {
		name           string
		info           *monitor.ActiveTaskInfo
		wantParts      []string
		notWant        []string
		wantSeparators int // -1 to skip the check
	}{
		{
			name: "all fields present",
			info: &monitor.ActiveTaskInfo{
				TaskID: 42, ProjectName: "proj", Type: "task",
				Phase: "now", Tier: &tier, Status: "in_progress",
			},
			wantParts:      []string{"[E-42]", "proj", "task", "now", "t3", "in_progress"},
			wantSeparators: 5, // all five non-ID fields contribute
		},
		{
			name: "tier nil drops the t-segment",
			info: &monitor.ActiveTaskInfo{
				TaskID: 7, ProjectName: "proj", Type: "task",
				Phase: "now", Tier: nil, Status: "ready",
			},
			wantParts:    []string{"[E-7]", "proj", "task", "now", "ready"},
			wantSeparators: 4, // proj, task, now, ready → 4 " · " separators (no tier)
		},
		{
			name: "blank project + type + phase omitted",
			info:           &monitor.ActiveTaskInfo{TaskID: 9, Status: "ready"},
			wantParts:      []string{"[E-9]", "ready"},
			wantSeparators: 1, // only status
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := format(tc.info)
			for _, want := range tc.wantParts {
				if !strings.Contains(got, want) {
					t.Errorf("format() = %q, missing %q", got, want)
				}
			}
			if tc.wantSeparators >= 0 && strings.Count(got, " · ") != tc.wantSeparators {
				t.Errorf("format() = %q, separator count = %d, want %d",
					got, strings.Count(got, " · "), tc.wantSeparators)
			}
			for _, bad := range tc.notWant {
				if strings.Contains(got, bad) {
					t.Errorf("format() = %q, should not contain %q", got, bad)
				}
			}
		})
	}
}

// TestTierString_HandlesNilAndZero pins the documented rule: nil or 0
// tier prints "" so callers can drop the segment entirely; positive
// values render as "tN".
func TestTierString_HandlesNilAndZero(t *testing.T) {
	zero := int64(0)
	five := int64(5)
	tests := []struct {
		name string
		in   *int64
		want string
	}{
		{"nil", nil, ""},
		{"zero", &zero, ""},
		{"positive", &five, "t5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tierString(tc.in); got != tc.want {
				t.Errorf("tierString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTaskIDPrefix pins the three epic-context prefix shapes (E-1571):
// no epic → "E-<task>"; epic == task (viewing the epic) → "E-<epic>";
// epic != task (viewing a child) → "E-<epic>:E-<child>". Pure function,
// no DB needed.
func TestTaskIDPrefix(t *testing.T) {
	epic := int64(100)
	tests := []struct {
		name string
		info *monitor.ActiveTaskInfo
		want string
	}{
		{
			name: "no epic context",
			info: &monitor.ActiveTaskInfo{TaskID: 42, ActiveEpicID: nil},
			want: "E-42",
		},
		{
			name: "viewing the epic itself",
			info: &monitor.ActiveTaskInfo{TaskID: 100, ActiveEpicID: &epic},
			want: "E-100",
		},
		{
			name: "viewing a child of the epic",
			info: &monitor.ActiveTaskInfo{TaskID: 137, ActiveEpicID: &epic},
			want: "E-100:E-137",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskIDPrefix(tc.info); got != tc.want {
				t.Errorf("taskIDPrefix() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBlockersSegment_EmptyWhenNoBlockers pins case (1) from the plan:
// a task with no rows in task_deps yields an empty segment so the
// status line shows no trailing chip and no leading ` · ` separator.
func TestBlockersSegment_EmptyWhenNoBlockers(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")

	if got := blockersSegment(100); got != "" {
		t.Errorf("blockersSegment(100) = %q, want empty", got)
	}
}

// TestBlockersSegment_OneBlocker pins case (2): a single active blocker
// renders as `{E-N}` wrapped in the orange color group.
func TestBlockersSegment_OneBlocker(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")
	seedTaskWithStatus(t, db, 7, "ready")
	seedBlocks(t, db, 7, 100)

	got := blockersSegment(100)
	if !strings.Contains(got, "{E-7}") {
		t.Errorf("got %q, want it to contain {E-7}", got)
	}
	if !strings.Contains(got, "#[fg=colour208") {
		t.Errorf("got %q, want orange color escape", got)
	}
	if !strings.HasSuffix(got, "#[default]") {
		t.Errorf("got %q, want trailing #[default] reset", got)
	}
}

// TestBlockersSegment_TwoBlockers pins case (3): exactly two blockers
// render as `{E-A E-B}` (id-sort ASC, single space between IDs, no
// trailing "+" overflow marker).
func TestBlockersSegment_TwoBlockers(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")
	seedTaskWithStatus(t, db, 42, "in_progress")
	seedTaskWithStatus(t, db, 9, "ready")
	seedBlocks(t, db, 42, 100)
	seedBlocks(t, db, 9, 100)

	got := blockersSegment(100)
	if !strings.Contains(got, "{E-9 E-42}") {
		t.Errorf("got %q, want to contain {E-9 E-42}", got)
	}
	if strings.Contains(got, "+") {
		t.Errorf("got %q, should not contain + overflow marker for exactly 2", got)
	}
}

// TestBlockersSegment_ThreePlusBlockers pins case (4): three or more
// active blockers render as `{E-A E-B +}` — only the first two IDs
// (id-sort ASC) appear inline, with "+" as the overflow marker.
func TestBlockersSegment_ThreePlusBlockers(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")
	for _, id := range []int64{50, 60, 70, 80} {
		seedTaskWithStatus(t, db, id, "ready")
		seedBlocks(t, db, id, 100)
	}

	got := blockersSegment(100)
	if !strings.Contains(got, "{E-50 E-60 +}") {
		t.Errorf("got %q, want to contain {E-50 E-60 +}", got)
	}
	if strings.Contains(got, "E-70") || strings.Contains(got, "E-80") {
		t.Errorf("got %q, should not list third+ blocker IDs inline", got)
	}
}

// TestBlockersSegment_AllTerminalIsEmpty pins case (5): blockers in any
// terminal status ({confirmed,assumed,declined,obsolete}) drop off the
// segment; if every blocker is terminal the segment vanishes entirely.
func TestBlockersSegment_AllTerminalIsEmpty(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")
	for _, st := range []string{"confirmed", "assumed", "declined", "obsolete"} {
		var id int64
		switch st {
		case "confirmed":
			id = 11
		case "assumed":
			id = 12
		case "declined":
			id = 13
		case "obsolete":
			id = 14
		}
		seedTaskWithStatus(t, db, id, st)
		seedBlocks(t, db, id, 100)
	}

	if got := blockersSegment(100); got != "" {
		t.Errorf("blockersSegment = %q, want empty (all blockers terminal)", got)
	}
}

// TestBlockersSegment_MixedTerminalAndActive pins case (6): only active
// blockers appear, and they count toward the 2-then-"+" cap; the
// terminal entries are invisible even though they exist in task_deps.
func TestBlockersSegment_MixedTerminalAndActive(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")
	// id ASC: 20 (active), 21 (confirmed), 22 (active). Active-only → {E-20 E-22}.
	seedTaskWithStatus(t, db, 20, "ready")
	seedTaskWithStatus(t, db, 21, "confirmed")
	seedTaskWithStatus(t, db, 22, "in_progress")
	seedBlocks(t, db, 20, 100)
	seedBlocks(t, db, 21, 100)
	seedBlocks(t, db, 22, 100)

	got := blockersSegment(100)
	if !strings.Contains(got, "{E-20 E-22}") {
		t.Errorf("got %q, want to contain {E-20 E-22}", got)
	}
	if strings.Contains(got, "E-21") {
		t.Errorf("got %q, terminal blocker E-21 should not appear", got)
	}
}

// TestBlockersSegment_NoColorEscapeWhenEmpty pins case (7)'s negative
// half: an empty segment must NOT include color escapes, so the status
// row stays free of stray `#[fg=...]` codes that would leak coloring
// into downstream segments.
func TestBlockersSegment_NoColorEscapeWhenEmpty(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")

	got := blockersSegment(100)
	if got != "" {
		t.Errorf("got %q, want empty string (no escape codes either)", got)
	}
}

// TestFormat_AppendsBlockersSegment is the integration check: format()
// composes the row from [E-NNN] · project · ... · status · {blockers}
// when blockers exist, and omits both the segment and its leading
// separator when none exist.
func TestFormat_AppendsBlockersSegment(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")
	seedTaskWithStatus(t, db, 7, "ready")
	seedBlocks(t, db, 7, 100)

	info := &monitor.ActiveTaskInfo{
		TaskID: 100, ProjectName: "p", Type: "task",
		Phase: "now", Status: "in_progress",
	}
	got := format(info)
	if !strings.Contains(got, "{E-7}") {
		t.Errorf("format() = %q, want it to contain the {E-7} chip", got)
	}
	// The leading separator must precede the color escape, not the brace.
	if !strings.Contains(got, " · #[fg=colour208") {
		t.Errorf("format() = %q, want ` · ` separator before the colored chip", got)
	}
}

// TestFormat_OmitsBlockersSegmentWhenEmpty is the negative companion:
// no blockers → no trailing chip and no extra separator.
func TestFormat_OmitsBlockersSegmentWhenEmpty(t *testing.T) {
	db := withMonitorDB(t)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedTaskWithStatus(t, db, 100, "in_progress")

	info := &monitor.ActiveTaskInfo{
		TaskID: 100, ProjectName: "p", Type: "task",
		Phase: "now", Status: "in_progress",
	}
	got := format(info)
	if strings.Contains(got, "{E-") {
		t.Errorf("format() = %q, should not contain `{E-` chip when unblocked", got)
	}
	// The row should end with the status text (no trailing color escape
	// would mean a phantom segment slipped in).
	if !strings.HasSuffix(got, "in_progress") {
		t.Errorf("format() = %q, want trailing status with no extra segment", got)
	}
}

// TestHintAndPlaceholder_NonEmpty pins that each hint helper returns a
// non-empty string. The exact wording is product-content and will drift;
// these tests guard against accidentally returning "" (which would
// reflow tmux's status row).
func TestHintAndPlaceholder_NonEmpty(t *testing.T) {
	if hintNoTask() == "" {
		t.Error("hintNoTask returned empty")
	}
	if hintNoSession() == "" {
		t.Error("hintNoSession returned empty")
	}
	if placeholder() == "" {
		t.Error("placeholder returned empty")
	}
}
