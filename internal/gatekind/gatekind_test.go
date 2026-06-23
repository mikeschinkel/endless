package gatekind_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/gatekind"
)

func TestParse_AcceptsKnownSlugs(t *testing.T) {
	cases := map[string]gatekind.GateKind{
		"revisit": gatekind.GateKindRevisit,
	}
	for slug, want := range cases {
		got, err := gatekind.Parse(slug)
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
	for _, slug := range []string{"", "Revisit", "REVISIT", "pivot", "pause"} {
		_, err := gatekind.Parse(slug)
		if err == nil {
			t.Errorf("Parse(%q) accepted invalid value", slug)
			continue
		}
		if !strings.Contains(err.Error(), "invalid gate kind") {
			t.Errorf("Parse(%q) error did not mention 'invalid gate kind': %v", slug, err)
		}
	}
}

func TestGateKind_StringRoundTrip(t *testing.T) {
	for _, gk := range gatekind.All() {
		slug := gk.String()
		parsed, err := gatekind.Parse(slug)
		if err != nil {
			t.Errorf("round-trip failed for %v: Parse(%q) → %v", gk, slug, err)
			continue
		}
		if parsed != gk {
			t.Errorf("round-trip mismatch: %v.String()=%q, Parse(%q)=%v", gk, slug, slug, parsed)
		}
	}
}

func TestAll_HasExpectedCount(t *testing.T) {
	if all := gatekind.All(); len(all) != 1 {
		t.Errorf("All() returned %d, want 1", len(all))
	}
}

func newSeededDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE gate_kinds (id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, label TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func seedAll(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO gate_kinds (id, slug, label) VALUES (1, 'revisit', 'Revisit')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestVerifyIntegrity_OK(t *testing.T) {
	db := newSeededDB(t)
	seedAll(t, db)
	if err := gatekind.VerifyIntegrity(db); err != nil {
		t.Errorf("VerifyIntegrity returned error on aligned table: %v", err)
	}
}

func TestVerifyIntegrity_MissingEnumRow(t *testing.T) {
	db := newSeededDB(t) // empty table: the 'revisit' constant has no row
	err := gatekind.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "missing from gate_kinds") {
		t.Errorf("expected missing-row error, got %v", err)
	}
}

func TestVerifyIntegrity_SlugMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO gate_kinds VALUES (1, 'revisited', 'Revisit')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := gatekind.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "slug mismatch") {
		t.Errorf("expected slug-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_LabelMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO gate_kinds VALUES (1, 'revisit', 'Wrong')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := gatekind.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "label mismatch") {
		t.Errorf("expected label-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_UnknownTableRow(t *testing.T) {
	db := newSeededDB(t)
	seedAll(t, db)
	if _, err := db.Exec(`INSERT INTO gate_kinds VALUES (99, 'rogue', 'Rogue')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := gatekind.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "no matching enum constant") {
		t.Errorf("expected unknown-row error, got %v", err)
	}
}
