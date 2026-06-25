// Tests for the reopen-context resolver (E-1645). The inherited-session pick
// must prefer a prior ended session that left evidence of real work (a populated
// transcript_path or a span of >=10s) over a sub-10s evidence-free ghost
// (E-1640) — even when the ghost started more recently. It must NOT order by
// duration (Pattern B would let a stale long-span row win).
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
// started_at / last_activity (so the test controls the computed duration) and
// an optional transcript_path (evidence of real work).
func seedEndedSession(t *testing.T, db *sql.DB, id, taskID int64,
	startedAt, lastActivity string, transcript *string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions
		 (id, session_id, project_id, state, active_task_id, started_at,
		  last_activity, transcript_path)
		 VALUES (?, ?, 1, 'ended', ?, ?, ?, ?)`,
		id, "sess-"+strconv.FormatInt(id, 10), taskID,
		startedAt, lastActivity, transcript,
	); err != nil {
		t.Fatalf("seed ended session %d: %v", id, err)
	}
}

// TestReopenContext_PrefersRealOverGhost is the linchpin: a >=10s real session
// that started EARLIER must beat a sub-10s evidence-free ghost that started
// later. Picking by recency alone (or by max duration) would get this wrong.
func TestReopenContext_PrefersRealOverGhost(t *testing.T) {
	db := newReopenTestDB(t)
	seedReopenTask(t, db, 1645, "did the thing")

	const realID, ghostID = 101, 102
	// real: 30s span, started earlier, no transcript — evidence is the span.
	seedEndedSession(t, db, realID, 1645,
		"2026-06-23T00:00:00", "2026-06-23T00:00:30", nil)
	// ghost: 5s span, started later, no transcript — an E-1640 ghost.
	seedEndedSession(t, db, ghostID, 1645,
		"2026-06-23T00:01:00", "2026-06-23T00:01:05", nil)

	ctx, err := reopenContext(db, 1645)
	if err != nil {
		t.Fatalf("reopenContext: %v", err)
	}
	if ctx.InheritedSessionID != realID {
		t.Errorf("InheritedSessionID = %d, want real session %d (ghost is %d)",
			ctx.InheritedSessionID, realID, ghostID)
	}
}

// TestReopenContext_TranscriptIsEvidence: a sub-10s session still counts as real
// when it left a transcript_path, beating a longer-span row with neither when it
// started later. (Confirms transcript_path is honored as evidence.)
func TestReopenContext_TranscriptIsEvidence(t *testing.T) {
	db := newReopenTestDB(t)
	seedReopenTask(t, db, 1645, "")

	const withTranscript, plainGhost = 201, 202
	tp := "/tmp/transcript.jsonl"
	// 3s span but has a transcript → evidence; started later.
	seedEndedSession(t, db, withTranscript, 1645,
		"2026-06-23T00:02:00", "2026-06-23T00:02:03", &tp)
	// 4s span, no transcript, started earlier → not evidence.
	seedEndedSession(t, db, plainGhost, 1645,
		"2026-06-23T00:00:00", "2026-06-23T00:00:04", nil)

	ctx, err := reopenContext(db, 1645)
	if err != nil {
		t.Fatalf("reopenContext: %v", err)
	}
	if ctx.InheritedSessionID != withTranscript {
		t.Errorf("InheritedSessionID = %d, want transcript-bearing %d",
			ctx.InheritedSessionID, withTranscript)
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
		"2026-06-23T00:00:00", "2026-06-23T00:00:30", nil)
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
