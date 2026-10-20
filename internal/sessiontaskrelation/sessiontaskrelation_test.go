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
		"claimed":    sessiontaskrelation.RelationClaimed,
		"filed":      sessiontaskrelation.RelationFiled,
		"revisited":  sessiontaskrelation.RelationRevisited,
		"referenced": sessiontaskrelation.RelationReferenced,
		"touched":    sessiontaskrelation.RelationTouched,
		"bound":      sessiontaskrelation.RelationBound,
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
	// surfaced and queued are the pre-E-2291 slugs of ids 2 and 5: a rename
	// retires the old slug rather than aliasing it.
	for _, slug := range []string{"", "goal", "Claimed", "CLAIMED", "Referenced", "QUEUED", "read", "surfaced", "queued"} {
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
	if all := sessiontaskrelation.All(); len(all) != 6 {
		t.Errorf("All() returned %d, want 6", len(all))
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
		(1, 'claimed', 'Claimed'), (2, 'filed', 'Filed'), (3, 'revisited', 'Revisited'),
		(4, 'referenced', 'Referenced'), (5, 'touched', 'Touched'), (6, 'bound', 'Bound')`); err != nil {
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
	if _, err := db.Exec(`INSERT INTO session_task_relations VALUES (1, 'wrong', 'Claimed'), (2, 'filed', 'Filed'), (3, 'revisited', 'Revisited'), (4, 'referenced', 'Referenced'), (5, 'touched', 'Touched'), (6, 'bound', 'Bound')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := sessiontaskrelation.VerifyIntegrity(db)
	if err == nil || !strings.Contains(err.Error(), "slug mismatch") {
		t.Errorf("expected slug-mismatch error, got %v", err)
	}
}

func TestVerifyIntegrity_LabelMismatch(t *testing.T) {
	db := newSeededDB(t)
	if _, err := db.Exec(`INSERT INTO session_task_relations VALUES (1, 'claimed', 'Wrong'), (2, 'filed', 'Filed'), (3, 'revisited', 'Revisited'), (4, 'referenced', 'Referenced'), (5, 'touched', 'Touched'), (6, 'bound', 'Bound')`); err != nil {
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

// TestRank_IsStrictTotalOrder pins the ladder E-1696 introduced and E-2291
// extended: claimed < bound < touched < filed < revisited < referenced, with no
// two relations sharing a rank. bound directly below claimed is what lets a
// claim after a bind upgrade the row, and a bind after a touch upgrade it.
//
// Ties are the failure this guards. Rank drives BOTH the upsert's upgrade test
// and the display tier, so two relations at the same rank would make captures
// non-deterministic (neither outranks the other, so whichever arrived first
// sticks) while looking perfectly correct in every single-relation test.
func TestRank_IsStrictTotalOrder(t *testing.T) {
	want := []sessiontaskrelation.Relation{
		sessiontaskrelation.RelationClaimed,
		sessiontaskrelation.RelationBound,
		sessiontaskrelation.RelationTouched,
		sessiontaskrelation.RelationFiled,
		sessiontaskrelation.RelationRevisited,
		sessiontaskrelation.RelationReferenced,
	}
	for i := 1; i < len(want); i++ {
		if !want[i-1].Outranks(want[i]) {
			t.Errorf("%s should outrank %s (ranks %d, %d)",
				want[i-1], want[i], want[i-1].Rank(), want[i].Rank())
		}
	}
	seen := make(map[int]sessiontaskrelation.Relation, len(want))
	for _, rel := range sessiontaskrelation.All() {
		if prev, dup := seen[rel.Rank()]; dup {
			t.Errorf("rank %d shared by %s and %s", rel.Rank(), prev, rel)
		}
		seen[rel.Rank()] = rel
	}
	if len(seen) != len(sessiontaskrelation.All()) {
		t.Errorf("ranks cover %d relations, want %d", len(seen), len(sessiontaskrelation.All()))
	}
}

// TestOutranks_IsStrict pins that an equal relation does NOT outrank itself. The
// upsert's upgrade test is `incoming.Outranks(stored)`, so a non-strict
// comparison would rewrite relation_id on every repeat touch for no reason.
func TestOutranks_IsStrict(t *testing.T) {
	for _, rel := range sessiontaskrelation.All() {
		if rel.Outranks(rel) {
			t.Errorf("%s outranks itself", rel)
		}
	}
}

// TestRank_UnknownRanksLast covers the ELSE arm of the SQL CASE built from this
// enum (sessiontaskrelation.RankCaseSQL): a NULL relation_id and an id from a newer
// binary both land on Relation(0). It must rank BELOW every real relation, so a
// NULL historical row gets filled in by the next capture and an unknown value
// never outranks real work.
func TestRank_UnknownRanksLast(t *testing.T) {
	unknown := sessiontaskrelation.Relation(0)
	for _, rel := range sessiontaskrelation.All() {
		if !rel.Outranks(unknown) {
			t.Errorf("%s (rank %d) should outrank unknown (rank %d)",
				rel, rel.Rank(), unknown.Rank())
		}
	}
}

// TestRename_KeepsIDs pins E-2291's renames: ids 2 and 5 keep their persisted
// ids, so every existing session_tasks row reads under the new name with no
// data migration, and bound takes the next free id rather than any earlier one.
func TestRename_KeepsIDs(t *testing.T) {
	for _, tc := range []struct {
		rel         sessiontaskrelation.Relation
		id          int
		slug, label string
	}{
		{sessiontaskrelation.RelationFiled, 2, "filed", "Filed"},
		{sessiontaskrelation.RelationTouched, 5, "touched", "Touched"},
		{sessiontaskrelation.RelationBound, 6, "bound", "Bound"},
	} {
		if int(tc.rel) != tc.id || tc.rel.String() != tc.slug || tc.rel.Label() != tc.label {
			t.Errorf("relation %d = (%d, %q, %q), want (%d, %q, %q)",
				tc.id, int(tc.rel), tc.rel.String(), tc.rel.Label(), tc.id, tc.slug, tc.label)
		}
	}
}

// TestAnchors pins the anchor set every reader that separates "the session's
// own task" from "its other tasks" uses: claimed and bound, nothing else.
func TestAnchors(t *testing.T) {
	want := map[sessiontaskrelation.Relation]bool{
		sessiontaskrelation.RelationClaimed: true,
		sessiontaskrelation.RelationBound:   true,
	}
	for _, rel := range sessiontaskrelation.All() {
		if got := rel.IsAnchor(); got != want[rel] {
			t.Errorf("%s.IsAnchor() = %v, want %v", rel, got, want[rel])
		}
	}
	if len(sessiontaskrelation.Anchors()) != len(want) {
		t.Errorf("Anchors() has %d entries, want %d", len(sessiontaskrelation.Anchors()), len(want))
	}
}
