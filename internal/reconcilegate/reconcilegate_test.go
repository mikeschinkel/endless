package reconcilegate

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

const task int64 = 7

func newDB(t *testing.T, typeID int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := schema.Migrate(db); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, name, path, status, created_at, updated_at)
	                 VALUES (1, 'p', '/tmp/p', 'active', '', '')`)
	mustExec(t, db, `INSERT INTO tasks (id, project_id, title, phase, status, type_id)
	                 VALUES (?, 1, 't', 'now', 'underway', ?)`, task, typeID)
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// item inserts a plan item; approved copies the statement into the baseline.
func item(t *testing.T, db *sql.DB, seq int64, kind, origin, state string, approved bool) {
	t.Helper()
	var aStmt, aKind, aAt any
	if approved {
		aStmt, aKind, aAt = "s", kind, "2026-10-01T00:00:00"
	}
	mustExec(t, db, `INSERT INTO task_plan_items
	    (task_id, seq, sort_order, kind, statement, origin, state,
	     approved_statement, approved_kind, approved_at, created_at, updated_at)
	    VALUES (?, ?, ?, ?, 's', ?, ?, ?, ?, ?, '', '')`,
		task, seq, seq, kind, origin, state, aStmt, aKind, aAt)
}

// reconcile records a reconciliation over the current items with the given
// verdicts and question statuses ("" = no question).
func reconcile(t *testing.T, db *sql.DB, fp string, verdicts map[int64]string, qstatus map[int64]string) {
	t.Helper()
	digest := digestOf(t, db)
	res, err := db.Exec(`INSERT INTO task_reconciliations (task_id, seq, fingerprint, plan_digest, created_at)
	    VALUES (?, (SELECT COALESCE(MAX(seq),0)+1 FROM task_reconciliations WHERE task_id = ?), ?, ?, '')`,
		task, task, fp, digest)
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := res.LastInsertId()
	for seq, v := range verdicts {
		var qid any
		if st := qstatus[seq]; st != "" {
			r, err := db.Exec(`INSERT INTO task_questions (task_id, series, question, status, kind, created_at)
			    VALUES (?, 1, 'q', ?, 'different', '')`, task, st)
			if err != nil {
				t.Fatal(err)
			}
			qid, _ = r.LastInsertId()
		}
		mustExec(t, db, `INSERT INTO task_reconciliation_entries (reconciliation_id, item_seq, verdict, question_id)
		    VALUES (?, ?, ?, ?)`, rid, seq, v, qid)
	}
}

func digestOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	d, err := monitor.PlanDigest(db, task)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustCheck(t *testing.T, db *sql.DB, fp string, mode Mode) Result {
	t.Helper()
	r, err := check(db, task, fp, mode, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func wantProblem(t *testing.T, r Result, substr string) {
	t.Helper()
	if r.Pass {
		t.Fatalf("gate passed, want a problem containing %q", substr)
	}
	for _, p := range r.Problems {
		if strings.Contains(p, substr) {
			return
		}
	}
	t.Fatalf("problems %q, want one containing %q", r.Problems, substr)
}

func TestGate_PassesWhenCurrentAndSettled(t *testing.T) {
	db := newDB(t, 1)
	item(t, db, 1, "deliverable", "planned", "active", true)
	item(t, db, 2, "exclusion", "planned", "active", true)
	reconcile(t, db, "fp", map[int64]string{1: "differently", 2: "kept"}, map[int64]string{1: "answered"})
	if r := mustCheck(t, db, "fp", ModeHandover); !r.Pass {
		t.Fatalf("gate refused: %v", r.Problems)
	}
}

func TestGate_EachConditionOnItsOwn(t *testing.T) {
	t.Run("research with no items", func(t *testing.T) {
		db := newDB(t, 3) // research
		if r := mustCheck(t, db, "fp", ModeHandover); !r.Pass || r.Skipped == "" {
			t.Fatalf("research gated: %+v", r)
		}
	})
	t.Run("research with items", func(t *testing.T) {
		db := newDB(t, 3) // research
		item(t, db, 1, "deliverable", "planned", "active", true)
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "has not been reconciled")
	})
	t.Run("no deliverable", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "exclusion", "planned", "active", false)
		reconcile(t, db, "fp", map[int64]string{1: "kept"}, nil)
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "no active deliverable")
	})
	t.Run("no items, handover", func(t *testing.T) {
		db := newDB(t, 1)
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "no active deliverable")
	})
	t.Run("no items, backstop skips", func(t *testing.T) {
		db := newDB(t, 1)
		if r := mustCheck(t, db, "fp", ModeBackstop); !r.Pass || r.Skipped == "" {
			t.Fatalf("backstop did not skip: %+v", r)
		}
	})
	t.Run("not reconciled", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "not been reconciled")
	})
	t.Run("code changed", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		reconcile(t, db, "fp", map[int64]string{1: "as_planned"}, nil)
		wantProblem(t, mustCheck(t, db, "other", ModeHandover), "code changed")
	})
	t.Run("plan changed after reconciling", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		reconcile(t, db, "fp", map[int64]string{1: "as_planned"}, nil)
		mustExec(t, db, `UPDATE task_plan_items SET statement = 'new', approved_statement = 'new'`)
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "changed or were re-approved")
	})
	t.Run("missing entry", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		reconcile(t, db, "fp", map[int64]string{}, nil)
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "no entry for P1")
	})
	t.Run("open discrepancy", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		reconcile(t, db, "fp", map[int64]string{1: "not_done"}, map[int64]string{1: "open"})
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "waiting for the user")
	})
	t.Run("any settled status counts", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		reconcile(t, db, "fp", map[int64]string{1: "blocked"}, map[int64]string{1: "withdrawn"})
		if r := mustCheck(t, db, "fp", ModeHandover); !r.Pass {
			t.Fatalf("withdrawn question did not settle: %v", r.Problems)
		}
	})
	t.Run("unapproved grown item is a discrepancy", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		item(t, db, 2, "deliverable", "grown", "active", false)
		reconcile(t, db, "fp", map[int64]string{1: "as_planned", 2: "as_planned"}, nil)
		wantProblem(t, mustCheck(t, db, "fp", ModeHandover), "P2")
	})
	t.Run("baseline differs", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		item(t, db, 2, "deliverable", "planned", "withdrawn", true)
		mustExec(t, db, `UPDATE task_plan_items SET statement = 'edited' WHERE seq = 1`)
		reconcile(t, db, "fp", map[int64]string{1: "as_planned"}, nil)
		r := mustCheck(t, db, "fp", ModeHandover)
		wantProblem(t, r, "P1 changed")
		wantProblem(t, r, "P2 withdrawn")
	})
}

// --- fingerprint ---------------------------------------------------------

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", msg)
}

func TestFingerprint_OwnChangesOnly(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "z.go", "package z\n")
	commit(t, dir, "base")
	gitRun(t, dir, "checkout", "-q", "-b", "task/1")

	ctx := context.Background()
	fp0, _, err := Fingerprint(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp0 != EmptyFingerprint {
		t.Errorf("no own changes: %q, want %q", fp0, EmptyFingerprint)
	}

	write(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	commit(t, dir, "work")
	fp1, _, _ := Fingerprint(ctx, dir)
	if fp1 == EmptyFingerprint {
		t.Fatal("an edit left the fingerprint empty")
	}

	write(t, dir, ".endless/db-ledger/x.jsonl", "{}\n")
	commit(t, dir, "Endless: ledger")
	if fp, _, _ := Fingerprint(ctx, dir); fp != fp1 {
		t.Errorf("an .endless/-only commit changed the fingerprint")
	}

	// main moves on in a file the branch does not touch; a clean rebase keeps it.
	gitRun(t, dir, "checkout", "-q", "main")
	write(t, dir, "z.go", "package z\n\nvar Z = 1\n")
	commit(t, dir, "main moves")
	gitRun(t, dir, "checkout", "-q", "task/1")
	gitRun(t, dir, "rebase", "-q", "main")
	if fp, _, _ := Fingerprint(ctx, dir); fp != fp1 {
		t.Errorf("a clean rebase changed the fingerprint")
	}

	write(t, dir, "a.go", "package a\n\nfunc A() { _ = 1 }\n")
	commit(t, dir, "more work")
	if fp, _, _ := Fingerprint(ctx, dir); fp == fp1 {
		t.Errorf("an edit left the fingerprint unchanged")
	}
}

// The gate stands aside for a move the lifecycle refuses outright, so the
// lifecycle's refusal — which names the reachable statuses — is what is shown.
func TestGate_StandsAsideForAnIllegalMove(t *testing.T) {
	db := newDB(t, 1)
	mustExec(t, db, `UPDATE tasks SET status = 'unplanned' WHERE id = ?`, task)
	r, err := check(db, task, "fp", ModeHandover, "unverified")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Pass || r.Skipped == "" {
		t.Fatalf("gate did not stand aside for unplanned → unverified: %+v", r)
	}
	mustExec(t, db, `UPDATE tasks SET status = 'underway' WHERE id = ?`, task)
	if r, err = check(db, task, "fp", ModeHandover, "unverified"); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r, "no active deliverable")
}

// E-2485: a hand-in is refused while any active planned item is not approved,
// even on a task none of whose items was ever approved — approval skipped here
// cannot be given later, since items freeze from `unverified` on.
func TestGate_HandoverRequiresPlannedItemsApproved(t *testing.T) {
	unapproved := func(t *testing.T) *sql.DB {
		db := newDB(t, 1)
		// Both items were added while the task was underway; neither approved.
		item(t, db, 1, "deliverable", "planned", "active", false)
		item(t, db, 2, "exclusion", "planned", "active", false)
		reconcile(t, db, "fp", map[int64]string{1: "as_planned", 2: "kept"}, nil)
		return db
	}
	for _, to := range []string{"unverified", "assumed"} {
		t.Run("refused to "+to, func(t *testing.T) {
			db := unapproved(t)
			r, err := check(db, task, "fp", ModeHandover, to)
			if err != nil {
				t.Fatal(err)
			}
			wantProblem(t, r, "P1, P2 are not approved")
			wantProblem(t, r, "endless task plan approve E-7 P1 P2")
		})
	}
	t.Run("approving them lets it pass", func(t *testing.T) {
		db := unapproved(t)
		mustExec(t, db, `UPDATE task_plan_items SET approved_statement = statement,
		    approved_kind = kind, approved_at = '2026-10-02T00:00:00'`)
		reconcile(t, db, "fp", map[int64]string{1: "as_planned", 2: "kept"}, nil)
		if r := mustCheck(t, db, "fp", ModeHandover); !r.Pass {
			t.Fatalf("gate refused approved items: %v", r.Problems)
		}
	})
	t.Run("grown items kept through an answered question pass", func(t *testing.T) {
		db := newDB(t, 1)
		item(t, db, 1, "deliverable", "planned", "active", true)
		item(t, db, 2, "deliverable", "grown", "active", false)
		reconcile(t, db, "fp", map[int64]string{1: "as_planned", 2: "as_planned"},
			map[int64]string{2: "answered"})
		if r := mustCheck(t, db, "fp", ModeHandover); !r.Pass {
			t.Fatalf("gate refused a kept grown item: %v", r.Problems)
		}
	})
	t.Run("backstop does not refuse", func(t *testing.T) {
		db := unapproved(t)
		if r := mustCheck(t, db, "fp", ModeBackstop); !r.Pass {
			t.Fatalf("backstop refused: %v", r.Problems)
		}
	})
	t.Run("a frozen task moving on is not refused", func(t *testing.T) {
		// Already unverified with unapproved items, as tasks were before this
		// shipped: its items cannot be approved now, so refusing would strand it.
		db := unapproved(t)
		mustExec(t, db, `UPDATE tasks SET status = 'unverified' WHERE id = ?`, task)
		r, err := check(db, task, "fp", ModeHandover, "assumed")
		if err != nil {
			t.Fatal(err)
		}
		if !r.Pass {
			t.Fatalf("gate refused unverified → assumed: %v", r.Problems)
		}
	})
}
