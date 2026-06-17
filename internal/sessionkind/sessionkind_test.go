package sessionkind

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// TestParseStringRoundTrip pins that every enum constant's slug parses back to
// the same constant, and that String() emits the canonical slug.
func TestParseStringRoundTrip(t *testing.T) {
	for _, k := range All() {
		got, err := Parse(k.String())
		if err != nil {
			t.Errorf("Parse(%q): unexpected error %v", k.String(), err)
			continue
		}
		if got != k {
			t.Errorf("Parse(%q) = %v, want %v", k.String(), got, k)
		}
	}
}

// TestParseInvalid rejects an unknown slug.
func TestParseInvalid(t *testing.T) {
	if _, err := Parse("nope"); err == nil {
		t.Fatal("Parse(\"nope\") = nil error, want rejection")
	}
	if err := Validate("nope"); err == nil {
		t.Fatal("Validate(\"nope\") = nil error, want rejection")
	}
	if err := Validate("background"); err != nil {
		t.Fatalf("Validate(\"background\") = %v, want nil", err)
	}
}

// TestVerifyIntegrity_MatchesSchema confirms the Go enum agrees with the
// session_kinds rows seeded by schema.sql, and that drift fails closed.
func TestVerifyIntegrity_MatchesSchema(t *testing.T) {
	db := freshSchemaDB(t)

	if err := VerifyIntegrity(db); err != nil {
		t.Fatalf("VerifyIntegrity on seeded schema = %v, want nil", err)
	}

	// Missing row → fail closed.
	if _, err := db.Exec("DELETE FROM session_kinds WHERE id = 2"); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if err := VerifyIntegrity(db); err == nil {
		t.Fatal("VerifyIntegrity with missing row = nil, want error")
	}

	// Extra row with no matching constant → fail closed.
	if _, err := db.Exec(
		"INSERT INTO session_kinds (id, slug, label) VALUES (2, 'background', 'Background'), (99, 'bogus', 'Bogus')",
	); err != nil {
		t.Fatalf("reinsert + extra row: %v", err)
	}
	if err := VerifyIntegrity(db); err == nil {
		t.Fatal("VerifyIntegrity with extra row = nil, want error")
	}
}

func freshSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/k.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}
