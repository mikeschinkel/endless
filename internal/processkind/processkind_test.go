package processkind_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/processkind"
	"github.com/mikeschinkel/endless/internal/schema"
)

func freshSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// TestParseStringRoundTrip pins that every constant's slug parses back to the
// same constant, and String() emits the canonical slug.
func TestParseStringRoundTrip(t *testing.T) {
	for _, k := range processkind.All() {
		got, err := processkind.Parse(k.String())
		if err != nil {
			t.Errorf("Parse(%q): unexpected error %v", k.String(), err)
			continue
		}
		if got != k {
			t.Errorf("Parse(%q) = %v, want %v", k.String(), got, k)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := processkind.Parse("nope"); err == nil {
		t.Fatal(`Parse("nope") = nil error, want rejection`)
	}
	if err := processkind.Validate("tmux"); err != nil {
		t.Fatalf(`Validate("tmux") = %v, want nil`, err)
	}
}

// TestUsesServer pins which kinds carry a server half in their identity. The
// rule lives on the enum rather than at each call site so a future multiplexer
// kind cannot be misclassified by a caller that was written before it existed.
func TestUsesServer(t *testing.T) {
	if !processkind.ProcessKindTmux.UsesServer() {
		t.Error("tmux must require a server_uuid; a bare pane id is not an identity")
	}
	if processkind.ProcessKindPID.UsesServer() {
		t.Error("pid must NOT require a server_uuid; a bare OS process has no multiplexer")
	}
}

// TestVerifyIntegrity_MatchesSchema confirms the Go enum agrees with the
// process_kinds rows seeded by schema.sql, and that drift fails closed.
func TestVerifyIntegrity_MatchesSchema(t *testing.T) {
	db := freshSchemaDB(t)

	if err := processkind.VerifyIntegrity(db); err != nil {
		t.Fatalf("VerifyIntegrity on seeded schema = %v, want nil", err)
	}

	if _, err := db.Exec("DELETE FROM process_kinds WHERE id = 2"); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if err := processkind.VerifyIntegrity(db); err == nil {
		t.Fatal("VerifyIntegrity with a missing row = nil, want error")
	}

	if _, err := db.Exec(
		"INSERT INTO process_kinds (id, slug, label) VALUES (2, 'pid', 'OS process'), (99, 'bogus', 'Bogus')",
	); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
	if err := processkind.VerifyIntegrity(db); err == nil {
		t.Fatal("VerifyIntegrity with an unknown row = nil, want error")
	}
}

// TestKindForLegacyProcess covers the migration's classifier, including the
// cases it must REFUSE. A wrong guess mints an identity that can never match an
// observation — permanently `unknown` — which is worse than leaving the binding
// absent and letting the next hook rebuild it.
func TestKindForLegacyProcess(t *testing.T) {
	tests := []struct {
		in      string
		kind    processkind.ProcessKind
		address string
		ok      bool
	}{
		{"%414", processkind.ProcessKindTmux, "%414", true},
		{"%0", processkind.ProcessKindTmux, "%0", true},
		{"pid:1234", processkind.ProcessKindPID, "1234", true},
		{"", 0, "", false},
		{"%", 0, "", false},    // a bare sigil is not a pane id
		{"pid:", 0, "", false}, // ...nor an empty pid
		{"garbage", 0, "", false},
	}
	for _, tc := range tests {
		kind, address, ok := processkind.KindForLegacyProcess(tc.in)
		if ok != tc.ok || kind != tc.kind || address != tc.address {
			t.Errorf("KindForLegacyProcess(%q) = (%v, %q, %v), want (%v, %q, %v)",
				tc.in, kind, address, ok, tc.kind, tc.address, tc.ok)
		}
	}
}
