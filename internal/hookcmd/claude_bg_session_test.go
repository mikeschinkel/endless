package hookcmd

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestDecorateBgSession_HookBindsUUID is the integration test for the
// SessionStart bg-decoration branch (E-1568): a bg agent's hook fires with
// CLAUDE_JOB_DIR=<.../short_id> and the real UUID, and decorateBgSession
// attaches that UUID to the dispatch row keyed by short_id.
//
// It binds monitor.DB() to a temp database via the XDG_CONFIG_HOME +
// SetDBContextDir seam. monitor.DB() is a process-global singleton, so all
// DB-dependent assertions live in this single test; the guard-only tests below
// return before any DB call and are order-independent.
func TestDecorateBgSession_HookBindsUUID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	monitor.SetDBContextDir(dir)

	db, err := monitor.DB()
	if err != nil {
		t.Fatalf("monitor.DB(): %v", err)
	}
	if _, err = db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err = db.Exec("INSERT INTO tasks (id, project_id, title, status) VALUES (10, 1, 't', 'underway')"); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Dispatch row: session_id NULL + short_id "feed1234", kind background.
	if _, err = monitor.RecordBgAgentSession(10, "feed1234"); err != nil {
		t.Fatalf("RecordBgAgentSession: %v", err)
	}

	t.Setenv("CLAUDE_JOB_DIR", filepath.Join(dir, "jobs", "feed1234"))
	decorateBgSession(claudePayload{
		EventName: "SessionStart",
		Source:    "startup",
		SessionID: "uuid-real",
	})

	var sessionID sql.NullString
	if err = db.QueryRow(
		"SELECT session_id FROM sessions WHERE short_id='feed1234'",
	).Scan(&sessionID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if sessionID.String != "uuid-real" {
		t.Errorf("session_id = %q, want uuid-real", sessionID.String)
	}

	// A short_id with no matching dispatch row leaves everything unchanged
	// (the hook falls through to normal tracking).
	t.Setenv("CLAUDE_JOB_DIR", filepath.Join(dir, "jobs", "nomatch"))
	decorateBgSession(claudePayload{
		EventName: "SessionStart",
		Source:    "startup",
		SessionID: "uuid-other",
	})
	var n int
	if err = db.QueryRow(
		"SELECT count(*) FROM sessions WHERE session_id='uuid-other'",
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("a no-match decoration created/updated %d rows, want 0", n)
	}
}

// TestDecorateBgSession_SkipsNonSessionStart confirms the branch is inert on
// non-SessionStart events (CLAUDE_JOB_DIR is set on every event of a bg agent,
// so the EventName guard is what scopes the UPDATE to once-per-session). No DB
// is touched, so this is safe regardless of singleton state.
func TestDecorateBgSession_SkipsNonSessionStart(t *testing.T) {
	t.Setenv("CLAUDE_JOB_DIR", "/tmp/jobs/abc12345")
	// Would panic on a nil/closed DB if it tried to act; returning early is
	// the correctness condition.
	decorateBgSession(claudePayload{
		EventName: "PreToolUse",
		SessionID: "uuid-x",
	})
}

// TestDecorateBgSession_SkipsWhenNoJobDir confirms a normal (non-bg) session,
// which has no CLAUDE_JOB_DIR, never touches the bg path.
func TestDecorateBgSession_SkipsWhenNoJobDir(t *testing.T) {
	t.Setenv("CLAUDE_JOB_DIR", "")
	decorateBgSession(claudePayload{
		EventName: "SessionStart",
		Source:    "startup",
		SessionID: "uuid-y",
	})
}
