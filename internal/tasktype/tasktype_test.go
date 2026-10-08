package tasktype_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/tasktype"
)

func TestParse_AcceptsKnownSlugs(t *testing.T) {
	cases := map[string]tasktype.TaskType{
		"todo":       tasktype.TaskTypeTask,
		"bugfix":     tasktype.TaskTypeBug,
		"research":   tasktype.TaskTypeResearch,
		"epic":       tasktype.TaskTypeEpic,
		"brainstorm": tasktype.TaskTypeBrainstorm,
	}
	for slug, want := range cases {
		got, err := tasktype.Parse(slug)
		if err != nil {
			t.Errorf("Parse(%q) returned error: %v", slug, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", slug, got, want)
		}
	}
}

// E-1659: 'task'->'todo' and 'bug'->'bugfix' renamed the slugs, but the legacy
// slugs must still Parse to the same ids so historical task.created /
// task.fields_updated events replay correctly (the type_id is stable; only the
// label moved). String() emits only the current slugs — see StringRoundTrip.
func TestParse_AcceptsLegacyAliases(t *testing.T) {
	cases := map[string]tasktype.TaskType{
		"task": tasktype.TaskTypeTask,
		"bug":  tasktype.TaskTypeBug,
	}
	for slug, want := range cases {
		got, err := tasktype.Parse(slug)
		if err != nil {
			t.Errorf("Parse(%q) legacy alias returned error: %v", slug, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", slug, got, want)
		}
	}
}

func TestParse_RejectsUnknown(t *testing.T) {
	for _, slug := range []string{"", "plan", "chore", "spike", "decision", "TODO", "Todo"} {
		_, err := tasktype.Parse(slug)
		if err == nil {
			t.Errorf("Parse(%q) accepted invalid value", slug)
			continue
		}
		if !strings.Contains(err.Error(), "invalid task type") {
			t.Errorf("Parse(%q) error did not mention 'invalid task type': %v", slug, err)
		}
	}
}

func TestTaskType_StringRoundTrip(t *testing.T) {
	for _, tt := range tasktype.All() {
		slug := tt.String()
		parsed, err := tasktype.Parse(slug)
		if err != nil {
			t.Errorf("round-trip failed for %v: Parse(%q) → %v", tt, slug, err)
			continue
		}
		if parsed != tt {
			t.Errorf("round-trip mismatch: %v.String()=%q, Parse(%q)=%v", tt, slug, slug, parsed)
		}
	}
}

func TestAll_HasExpectedCount(t *testing.T) {
	all := tasktype.All()
	if len(all) != 5 {
		t.Errorf("All() returned %d, want 5", len(all))
	}
}

func newSeededDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE task_types (id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, label TEXT NOT NULL, auto_spawnable INTEGER NOT NULL DEFAULT 0, lands INTEGER NOT NULL DEFAULT 1, requires_verify_suite INTEGER NOT NULL DEFAULT 0, settles_on_land INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func seedAll(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO task_types (id, slug, label, auto_spawnable, lands, requires_verify_suite, settles_on_land) VALUES
		(1, 'todo', 'Todo', 1, 1, 1, 1), (2, 'bugfix', 'Bugfix', 1, 1, 1, 1),
		(3, 'research', 'Research', 0, 0, 0, 0), (4, 'epic', 'Epic', 0, 1, 0, 0),
		(5, 'brainstorm', 'Brainstorm', 0, 0, 0, 0)`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestVerifyIntegrity_OK(t *testing.T) {
	db := newSeededDB(t)
	seedAll(t, db)
	if err := tasktype.VerifyIntegrity(db); err != nil {
		t.Errorf("VerifyIntegrity returned error on aligned table: %v", err)
	}
}

func TestVerifyIntegrity_MissingEnumRow(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO task_types (id, slug, label, auto_spawnable, lands, requires_verify_suite, settles_on_land) VALUES (1, 'todo', 'Todo', 1, 1, 1, 1), (2, 'bugfix', 'Bugfix', 1, 1, 1, 1), (3, 'research', 'Research', 0, 0, 0, 0)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "missing from task_types") {
		t.Errorf("expected missing-row error, got %v", err)
	}
}

func TestVerifyIntegrity_SlugMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO task_types (id, slug, label) VALUES (1, 'tsk', 'Todo'), (2, 'bugfix', 'Bugfix'), (3, 'research', 'Research'), (4, 'epic', 'Epic')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "slug mismatch") {
		t.Errorf("expected slug-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_LabelMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO task_types (id, slug, label) VALUES (1, 'todo', 'Wrong'), (2, 'bugfix', 'Bugfix'), (3, 'research', 'Research'), (4, 'epic', 'Epic')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "label mismatch") {
		t.Errorf("expected label-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_UnknownTableRow(t *testing.T) {
	db := newSeededDB(t)
	seedAll(t, db)
	if _, err := db.Exec(`INSERT INTO task_types (id, slug, label) VALUES (99, 'rogue', 'Rogue')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "no matching enum constant") {
		t.Errorf("expected unknown-row error, got %v", err)
	}
}

// TestAutoSpawnable pins which types the auto-spawn job may start (E-1814):
// implementation work only.
func TestAutoSpawnable(t *testing.T) {
	want := map[tasktype.TaskType]bool{
		tasktype.TaskTypeTask:       true,
		tasktype.TaskTypeBug:        true,
		tasktype.TaskTypeResearch:   false,
		tasktype.TaskTypeEpic:       false,
		tasktype.TaskTypeBrainstorm: false,
	}
	for _, tt := range tasktype.All() {
		if got := tt.AutoSpawnable(); got != want[tt] {
			t.Errorf("%s.AutoSpawnable() = %t, want %t", tt, got, want[tt])
		}
	}
}

// TestVerifyIntegrity_AutoSpawnableMismatch is the drift gate: a table that
// says research is auto-spawnable disagrees with the enum and fails closed, so
// the selector's column can never quietly widen the candidate pool.
func TestVerifyIntegrity_AutoSpawnableMismatch(t *testing.T) {
	db := newSeededDB(t)
	seedAll(t, db)
	if _, err := db.Exec(`UPDATE task_types SET auto_spawnable = 1 WHERE slug = 'research'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "auto_spawnable mismatch") {
		t.Errorf("expected auto_spawnable-mismatch error, got %v", err)
	}
}

// TestLandProperties pins how `worktree land` treats each type (E-2262): the
// implementation types need a user verify and settle on land; the findings
// types never land; an epic lands ungated and unsettled.
func TestLandProperties(t *testing.T) {
	type props struct{ lands, requires, settles bool }
	want := map[tasktype.TaskType]props{
		tasktype.TaskTypeTask:       {true, true, true},
		tasktype.TaskTypeBug:        {true, true, true},
		tasktype.TaskTypeResearch:   {false, false, false},
		tasktype.TaskTypeEpic:       {true, false, false},
		tasktype.TaskTypeBrainstorm: {false, false, false},
	}
	for _, tt := range tasktype.All() {
		got := props{tt.Lands(), tt.RequiresVerifySuite(), tt.SettlesOnLand()}
		if got != want[tt] {
			t.Errorf("%s land properties = %+v, want %+v", tt, got, want[tt])
		}
	}
}

// TestVerifyIntegrity_LandPropertyMismatch is the drift gate for the three
// land columns, as AutoSpawnableMismatch is for theirs.
func TestVerifyIntegrity_LandPropertyMismatch(t *testing.T) {
	for _, column := range []string{"lands", "requires_verify_suite", "settles_on_land"} {
		t.Run(column, func(t *testing.T) {
			db := newSeededDB(t)
			seedAll(t, db)
			if _, err := db.Exec(`UPDATE task_types SET ` + column + ` = 1 - ` + column + ` WHERE slug = 'todo'`); err != nil {
				t.Fatalf("update: %v", err)
			}
			err := tasktype.VerifyIntegrity(db)
			if err == nil || !strings.Contains(err.Error(), column+" mismatch") {
				t.Errorf("expected %s-mismatch error, got %v", column, err)
			}
		})
	}
}
