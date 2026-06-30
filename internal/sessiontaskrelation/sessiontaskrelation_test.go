package sessiontaskrelation_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

func TestParse_AcceptsKnownSlugs(t *testing.T) {
	cases := map[string]sessiontaskrelation.Relation{
		"goal":      sessiontaskrelation.RelationGoal,
		"surfaced":  sessiontaskrelation.RelationSurfaced,
		"revisited": sessiontaskrelation.RelationRevisited,
	}
	for slug, want := range cases {
		got, err := sessiontaskrelation.Parse(slug)
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
	for _, slug := range []string{"", "Goal", "GOAL", "referenced", "queued"} {
		_, err := sessiontaskrelation.Parse(slug)
		if err == nil {
			t.Errorf("Parse(%q) accepted invalid value", slug)
			continue
		}
		if !strings.Contains(err.Error(), "invalid relation") {
			t.Errorf("Parse(%q) error did not mention 'invalid relation': %v", slug, err)
		}
	}
}

func TestRelation_StringRoundTrip(t *testing.T) {
	for _, rel := range sessiontaskrelation.All() {
		slug := rel.String()
		parsed, err := sessiontaskrelation.Parse(slug)
		if err != nil {
			t.Errorf("round-trip failed for %v: Parse(%q) → %v", rel, slug, err)
			continue
		}
		if parsed != rel {
			t.Errorf("round-trip mismatch: %v.String()=%q, Parse(%q)=%v", rel, slug, slug, parsed)
		}
	}
}

func TestAll_HasExpectedCount(t *testing.T) {
	if all := sessiontaskrelation.All(); len(all) != 3 {
		t.Errorf("All() returned %d, want 3", len(all))
	}
}

func newSeededDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE session_task_relations (id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, label TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func seedAll(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO session_task_relations (id, slug, label) VALUES
		(1, 'goal', 'Goal'), (2, 'surfaced', 'Surfaced'), (3, 'revisited', 'Revisited')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestVerifyIntegrity_OK(t *testing.T) {
	db := newSeededDB(t)
	seedAll(t, db)
	if err := sessiontaskrelation.VerifyIntegrity(db); err != nil {
		t.Errorf("VerifyIntegrity returned error on aligned table: %v", err)
	}
}

func TestVerifyIntegrity_MissingEnumRow(t *testing.T) {
	db := newSeededDB(t) // empty table: every constant is missing a row
	err := sessiontaskrelation.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "missing from session_task_relations") {
		t.Errorf("expected missing-row error, got %v", err)
	}
}

func TestVerifyIntegrity_SlugMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO session_task_relations VALUES (1, 'wrong', 'Goal'), (2, 'surfaced', 'Surfaced'), (3, 'revisited', 'Revisited')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := sessiontaskrelation.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "slug mismatch") {
		t.Errorf("expected slug-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_LabelMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO session_task_relations VALUES (1, 'goal', 'Wrong'), (2, 'surfaced', 'Surfaced'), (3, 'revisited', 'Revisited')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := sessiontaskrelation.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "label mismatch") {
		t.Errorf("expected label-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_UnknownTableRow(t *testing.T) {
	db := newSeededDB(t)
	seedAll(t, db)
	if _, err := db.Exec(`INSERT INTO session_task_relations VALUES (99, 'rogue', 'Rogue')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := sessiontaskrelation.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "no matching enum constant") {
		t.Errorf("expected unknown-row error, got %v", err)
	}
}
