// The pin that holds undeclaredRefs against the schema (E-2579).
//
// undeclaredRefs is a hand-maintained map of the references the schema holds
// as a bare INTEGER column with no FOREIGN KEY, between the tables `endless
// correct` can touch. Nothing in the schema names them, so nothing told anyone
// when a session reference drifted off the list. This test derives the
// expectation from the live schema and the target list, the way
// TestProjectReferences_CoversEverySchemaReference in internal/monitor does
// for the projects guard list, and fails in both directions: a reference the
// list lacks, and a list row the schema or the target list no longer holds.
//
// The scope is the ledger's, not the schema's. A machine-local table written
// outside the ledger (errors, error_triage, rater_claims and the like, per
// E-2257) is never a correction target, so correct never writes or deletes
// its rows and none of its task or session ids is a reference this list has
// any reason to hold. The forward direction therefore looks only at tables a
// CorrectionTarget names, and the reverse direction fails on a listed row
// whose table is outside that set.
//
// White-box, like epic_derivation_test.go beside it: undeclaredRefs and
// correctionTargets are unexported, and exporting them for one test would
// widen the package's surface for nothing.
package events

import (
	"database/sql"
	"fmt"
	"testing"
)

// referenceRules names the columns the test recognizes as a reference: an
// INTEGER column, in a target table other than the referenced one, whose name
// contains the word.
//
// THIS IS THE TEST'S LIMIT. It is only as good as the naming convention: a
// column that holds a sessions id without "session" in its name, or a tasks
// id without "task" in its name, escapes both this test and the list, and
// neither will say so. Every current reference follows the convention
// (session_id, changed_by_session, asked_by_session, created_by_session,
// task_id), so the rule is sound today, and a column named outside it is a
// review question, not something this test can catch. Rows the list holds
// under a condition — task_deps.source_id when source_type = 'task',
// decision_relations.target_id when target_kind = 'decision' — are outside
// the naming rule too, and are checked only in the reverse direction.
//
// The rule reads PRAGMA table_info, never the SQL text: a table altered by
// ADD COLUMN ends with `, col TYPE);` on its closing line, which defeats a
// regex anchored on a newline before the paren, and a manual pass over
// schema.sql got the list wrong in both directions for that reason.
var referenceRules = []struct {
	Word, RefTable, RefColumn string
}{
	{"session", "sessions", "id"},
	{"task", "tasks", "id"},
	{"decision", "decisions", "id"},
	{"project", "projects", "id"},
}

// TestUndeclaredRefs_CoverEveryTargetReference fails when undeclaredRefs, the
// live schema and the target list disagree: a column of a target table the
// rule recognizes as a reference that declares no FOREIGN KEY and is missing
// from the list, or a list row whose table or column no longer exists, now
// declares a FOREIGN KEY, or is not a table any correction target names.
func TestUndeclaredRefs_CoverEveryTargetReference(t *testing.T) {
	db := newDerivationDB(t)
	targets := targetTables()

	listed := map[string]undeclaredRef{}
	for _, r := range undeclaredRefs {
		listed[r.Table+"."+r.Column] = r
	}

	found := 0
	for _, rule := range referenceRules {
		for _, ref := range schemaReferences(t, db, rule.Word, rule.RefTable) {
			if !targets[ref.Table] {
				continue // outside correct's reach: not a reference this list holds
			}
			found++
			key := ref.Table + "." + ref.Column
			r, ok := listed[key]
			if !ok {
				t.Errorf("%s holds a %s id with no FOREIGN KEY and is missing from "+
					"undeclaredRefs — add {%q, %q, %q, %q, \"\"} there", key, rule.RefTable,
					ref.Table, ref.Column, rule.RefTable, rule.RefColumn)
				continue
			}
			if r.RefTable != rule.RefTable || r.RefColumn != rule.RefColumn {
				t.Errorf("undeclaredRefs says %s refers to %s.%s; the naming rule says %s.%s",
					key, r.RefTable, r.RefColumn, rule.RefTable, rule.RefColumn)
			}
		}
	}
	if found == 0 {
		t.Fatal("no undeclared references found — the query is wrong, not the schema")
	}

	// The other direction: every row of the list names a column of a target
	// table that exists and still declares no FOREIGN KEY. A row that names
	// nothing is a stale entry; one whose column now declares a key is
	// redundant with PRAGMA foreign_key_list, which the same checks already
	// read; one in a table no target names is outside correct's reach.
	for _, r := range undeclaredRefs {
		if !targets[r.Table] {
			t.Errorf("undeclaredRefs lists %s.%s, but no correction target names %s: correct "+
				"never writes or deletes its rows, so the reference is outside its scope — remove it",
				r.Table, r.Column, r.Table)
			continue
		}
		if !columnExists(t, db, r.Table, r.Column) {
			t.Errorf("undeclaredRefs lists %s.%s, which the schema no longer holds — remove it",
				r.Table, r.Column)
			continue
		}
		if !columnExists(t, db, r.RefTable, r.RefColumn) {
			t.Errorf("undeclaredRefs says %s.%s refers to %s.%s, which the schema no longer holds",
				r.Table, r.Column, r.RefTable, r.RefColumn)
		}
		if declaresForeignKey(t, db, r.Table, r.Column) {
			t.Errorf("undeclaredRefs lists %s.%s, which now declares a FOREIGN KEY — remove it",
				r.Table, r.Column)
		}
	}
}

// targetTables is the set of tables a CorrectionTarget names: the tables
// `endless correct` can write or delete, and so the only tables whose
// references concern it.
func targetTables() map[string]bool {
	out := map[string]bool{}
	for _, t := range correctionTargets {
		if t.Table != "" {
			out[t.Table] = true
		}
	}
	return out
}

// schemaRef is one column the naming rule recognizes as an undeclared
// reference.
type schemaRef struct{ Table, Column string }

// schemaReferences lists every INTEGER column outside refTable whose name
// contains word and that declares no FOREIGN KEY, read from PRAGMA table_info
// and PRAGMA foreign_key_list. Collected before returning: the test DB holds
// one connection, so no other query may run while the result set is open.
func schemaReferences(t *testing.T, db *sql.DB, word, refTable string) []schemaRef {
	t.Helper()
	rows, err := db.Query(`
		SELECT m.name, c.name
		  FROM sqlite_master AS m
		  JOIN pragma_table_info(m.name) AS c
		 WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name != ?
		   AND c.type LIKE '%INT%' AND c.name LIKE '%' || ? || '%'
		   AND NOT EXISTS (SELECT 1 FROM pragma_foreign_key_list(m.name) AS fk WHERE fk."from" = c.name)
		 ORDER BY m.name, c.cid`, refTable, word)
	if err != nil {
		t.Fatalf("reading %s references: %v", refTable, err)
	}
	defer rows.Close()
	var out []schemaRef
	for rows.Next() {
		var r schemaRef
		if err := rows.Scan(&r.Table, &r.Column); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info(%q) WHERE name = ?`, table), column).Scan(&n)
	if err != nil {
		t.Fatalf("reading columns of %s: %v", table, err)
	}
	return n > 0
}

func declaresForeignKey(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM pragma_foreign_key_list(%q) WHERE "from" = ?`, table), column).Scan(&n)
	if err != nil {
		t.Fatalf("reading foreign keys of %s: %v", table, err)
	}
	return n > 0
}
