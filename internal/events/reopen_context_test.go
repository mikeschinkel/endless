// Tests for the reopen-context resolver (E-1645). The inherited-session pick
// must prefer a prior ended session that left evidence of real work over a
// sub-10s evidence-free ghost (E-1640) — even when the ghost started more
// recently. It must NOT order by duration (Pattern B would let a stale
// long-span row win).
//
// The evidence test was three disjuncts and is now two. transcript_path was
// dropped by E-1905. `process IS NOT NULL` used to be unreachable — the query
// filters to state='ended', and E-1530's triggers NULLed the binding on exactly
// those rows — but E-1898 removed both the triggers and the code that cleared
// it, so an ended session keeps the binding it ran on. That disjunct is live
// again, and TestEndedSessionKeepsItsBinding pins it.
package events

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// newReopenTestDB stands up a schema-applied SQLite DB with a seeded project.
func newReopenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-06-23T00:00:00', '2026-06-23T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

// seedReopenTask inserts a task with an outcome.
func seedReopenTask(t *testing.T, db *sql.DB, id int64, outcome string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, outcome)
		 VALUES (?, 1, 'reopen target', 'assumed', ?)`,
		id, outcome,
	); err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

// seedEndedSession inserts an ended session bound to taskID with explicit
// started_at / last_activity, so the test controls the computed duration — the
// only evidence signal left after E-1905 dropped transcript_path (the query's
// other disjunct, `process IS NOT NULL`, cannot fire on a state='ended' row:
// E-1530's triggers NULL `process` the moment a row reaches that state).
func seedEndedSession(t *testing.T, db *sql.DB, id, taskID int64,
	startedAt, lastActivity string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions
		 (id, session_id, project_id, state, active_task_id, started_at,
		  last_activity)
		 VALUES (?, ?, 1, 'ended', ?, ?, ?)`,
		id, "sess-"+strconv.FormatInt(id, 10), taskID,
		startedAt, lastActivity,
	); err != nil {
		t.Fatalf("seed ended session %d: %v", id, err)
	}
}

// TestEndedSessionKeepsItsBinding INVERTS the former
// TestEndedSessionProcessIsAlwaysNull, which pinned E-1530's rule that an ended
// session has no binding and warned that "if that invariant is ever relaxed,
// this fails and the evidence clause becomes live again". E-1898 relaxed it,
// deliberately, and this is that clause becoming live.
//
// A binding is history: "session 901 ran on pane %42 of server test-server"
// stays true after the session ends, and erasing it at end-of-life destroyed
// the evidence that diagnosed the 2026-08-05 incident. E-1530 only cleared it
// so a reused pane id could not resolve to a dead row; identity now makes that
// collision impossible, so the erasure bought nothing and cost the record.
func TestEndedSessionKeepsItsBinding(t *testing.T) {
	db := newReopenTestDB(t)
	seedReopenTask(t, db, 1905, "")

	res, err := db.Exec(
		`INSERT INTO processes (kind_id, server_uuid, address) VALUES (1, 'test-server', '%42')`,
	)
	if err != nil {
		t.Fatalf("seed process identity: %v", err)
	}
	processID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO sessions
		 (id, session_id, project_id, state, active_task_id, started_at,
		  last_activity, process_id)
		 VALUES (901, 'sess-901', 1, 'ended', 1905,
		         '2026-08-06T00:00:00', '2026-08-06T00:00:03', ?)`,
		processID,
	); err != nil {
		t.Fatalf("seed ended session with a binding: %v", err)
	}

	var got sql.NullInt64
	if err := db.QueryRow(
		"SELECT process_id FROM sessions WHERE id = 901",
	).Scan(&got); err != nil {
		t.Fatalf("read back process_id: %v", err)
	}
	if !got.Valid {
		t.Fatal("process_id on an ended row was cleared; the binding is history and must survive")
	}
	if got.Int64 != processID {
		t.Errorf("process_id = %d, want %d (unchanged)", got.Int64, processID)
	}
}

// TestReopenContext_PrefersRealOverGhost is the linchpin: a >=10s real session
// that started EARLIER must beat a sub-10s evidence-free ghost that started
// later. Picking by recency alone (or by max duration) would get this wrong.
func TestReopenContext_PrefersRealOverGhost(t *testing.T) {
	db := newReopenTestDB(t)
	seedReopenTask(t, db, 1645, "did the thing")

	const realID, ghostID = 101, 102
	// real: 30s span, started earlier — the span is the evidence.
	seedEndedSession(t, db, realID, 1645,
		"2026-06-23T00:00:00", "2026-06-23T00:00:30")
	// ghost: 5s span, started later — an E-1640 ghost.
	seedEndedSession(t, db, ghostID, 1645,
		"2026-06-23T00:01:00", "2026-06-23T00:01:05")

	ctx, err := reopenContext(db, 1645)
	if err != nil {
		t.Fatalf("reopenContext: %v", err)
	}
	if ctx.InheritedSessionID != realID {
		t.Errorf("InheritedSessionID = %d, want real session %d (ghost is %d)",
			ctx.InheritedSessionID, realID, ghostID)
	}
}

// TestReopenContext_GhostsTieBreakOnRecency pins E-1905's one intended behavior
// change. This case used to be decided by transcript_path: the 3s row carried
// one, so it beat the 4s row on evidence. With the column gone both rows are
// evidence-free, the ORDER BY's first key ties, and the pick falls through to
// `started_at DESC` — which lands on the same row here, for a different reason.
// Recency is the right fallback among ghosts: neither did provable work, so the
// most recent attempt is the most applicable context to reopen into.
func TestReopenContext_GhostsTieBreakOnRecency(t *testing.T) {
	db := newReopenTestDB(t)
	seedReopenTask(t, db, 1645, "")

	const laterGhost, earlierGhost = 201, 202
	// 3s span, started later.
	seedEndedSession(t, db, laterGhost, 1645,
		"2026-06-23T00:02:00", "2026-06-23T00:02:03")
	// 4s span, started earlier — longer, but the ORDER BY must not rank by
	// duration (Pattern B), so this must NOT win.
	seedEndedSession(t, db, earlierGhost, 1645,
		"2026-06-23T00:00:00", "2026-06-23T00:00:04")

	ctx, err := reopenContext(db, 1645)
	if err != nil {
		t.Fatalf("reopenContext: %v", err)
	}
	if ctx.InheritedSessionID != laterGhost {
		t.Errorf("InheritedSessionID = %d, want most-recent ghost %d",
			ctx.InheritedSessionID, laterGhost)
	}
}

// TestReopenContext_NoEndedSessions returns 0 (and empty snapshot) when the task
// has no ended sessions to inherit.
func TestReopenContext_NoEndedSessions(t *testing.T) {
	db := newReopenTestDB(t)
	seedReopenTask(t, db, 1645, "outcome text")

	ctx, err := reopenContext(db, 1645)
	if err != nil {
		t.Fatalf("reopenContext: %v", err)
	}
	if ctx.InheritedSessionID != 0 {
		t.Errorf("InheritedSessionID = %d, want 0", ctx.InheritedSessionID)
	}
	if ctx.LastStatusSnapshot != "" {
		t.Errorf("LastStatusSnapshot = %q, want empty", ctx.LastStatusSnapshot)
	}
	if ctx.PriorOutcome != "outcome text" {
		t.Errorf("PriorOutcome = %q, want %q", ctx.PriorOutcome, "outcome text")
	}
}

// TestReopenContext_RendersSnapshot: the inherited session's latest
// session_statuses row is rendered to markdown as last_status_snapshot.
func TestReopenContext_RendersSnapshot(t *testing.T) {
	db := newReopenTestDB(t)
	seedReopenTask(t, db, 1645, "")

	const sid = 301
	seedEndedSession(t, db, sid, 1645,
		"2026-06-23T00:00:00", "2026-06-23T00:00:30")
	if _, err := db.Exec(
		`INSERT INTO session_statuses (session_id, active_task_id, headline, created_at)
		 VALUES (?, 1645, 'RESUME HEADLINE', '2026-06-23T00:00:20')`,
		sid,
	); err != nil {
		t.Fatalf("seed session_status: %v", err)
	}

	ctx, err := reopenContext(db, 1645)
	if err != nil {
		t.Fatalf("reopenContext: %v", err)
	}
	if ctx.InheritedSessionID != sid {
		t.Fatalf("InheritedSessionID = %d, want %d", ctx.InheritedSessionID, sid)
	}
	if ctx.LastStatusSnapshot == "" {
		t.Fatalf("LastStatusSnapshot is empty, want rendered markdown")
	}
	if want := "RESUME HEADLINE"; !strings.Contains(ctx.LastStatusSnapshot, want) {
		t.Errorf("LastStatusSnapshot = %q, want it to contain %q",
			ctx.LastStatusSnapshot, want)
	}
}
