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
		"task":       tasktype.TaskTypeTask,
		"bug":        tasktype.TaskTypeBug,
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

func TestParse_RejectsUnknown(t *testing.T) {
	for _, slug := range []string{"", "plan", "chore", "spike", "decision", "TASK", "Task"} {
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
	if _, err := db.Exec(`CREATE TABLE task_types (id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, label TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func seedAll(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO task_types (id, slug, label) VALUES
		(1, 'task', 'Task'), (2, 'bug', 'Bug'),
		(3, 'research', 'Research'), (4, 'epic', 'Epic'),
		(5, 'brainstorm', 'Brainstorm')`)
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
	if _, err := db.Exec(`INSERT INTO task_types VALUES (1, 'task', 'Task'), (2, 'bug', 'Bug'), (3, 'research', 'Research')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "missing from task_types") {
		t.Errorf("expected missing-row error, got %v", err)
	}
}

func TestVerifyIntegrity_SlugMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO task_types VALUES (1, 'tsk', 'Task'), (2, 'bug', 'Bug'), (3, 'research', 'Research'), (4, 'epic', 'Epic')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "slug mismatch") {
		t.Errorf("expected slug-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_LabelMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO task_types VALUES (1, 'task', 'Wrong'), (2, 'bug', 'Bug'), (3, 'research', 'Research'), (4, 'epic', 'Epic')`); err != nil {
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
	if _, err := db.Exec(`INSERT INTO task_types VALUES (99, 'rogue', 'Rogue')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := tasktype.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "no matching enum constant") {
		t.Errorf("expected unknown-row error, got %v", err)
	}
}
