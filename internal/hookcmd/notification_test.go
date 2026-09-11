package hookcmd

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// E-2091. The producer for the attention board's first rank.
//
// These drive the handler with SYNTHETIC payloads rather than a live harness,
// which is the only honest way to test it: the fact under test is what Endless
// does with a `notification_type`, and Claude Code's willingness to emit one is
// not something a test here can establish either way.

// notifyTestDB seeds a project and one session in `state`, and points the
// monitor singleton at a throwaway database for the duration of the test.
func notifyTestDB(t *testing.T, state string) *sql.DB {
	t.Helper()
	db := newSchemaDB(t)
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, 'proj', '/tmp/proj')",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, task_id,
		                       started_at, last_activity)
		 VALUES (1, 'sess-A', 1, 'claude', ?, NULL,
		         '2026-09-01T00:00:00', '2026-09-01T00:00:00')`, state,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return db
}

func sessionState(t *testing.T, db *sql.DB) string {
	t.Helper()
	var state string
	if err := db.QueryRow(
		"SELECT state FROM sessions WHERE session_id='sess-A'",
	).Scan(&state); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return state
}

func notification(notificationType string) claudePayload {
	return claudePayload{
		SessionID:        "sess-A",
		EventName:        "Notification",
		NotificationType: notificationType,
	}
}

// TestNotification_PermissionPromptBlocksTheSession is the whole point of the
// task: Claude Code asks the user to approve a tool call, and the session stops
// reading `working`.
func TestNotification_PermissionPromptBlocksTheSession(t *testing.T) {
	db := notifyTestDB(t, sessionstate.Working)

	if err := handleNotification(notification("permission_prompt")); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	if got := sessionState(t, db); got != sessionstate.Prompted {
		t.Errorf("state = %q, want %q", got, sessionstate.Prompted)
	}
}

// TestNotification_IdlePromptIdlesTheSession: the input prompt sat untouched for
// 60 seconds, which is what `idle` already says. It gets no state of its own.
func TestNotification_IdlePromptIdlesTheSession(t *testing.T) {
	db := notifyTestDB(t, sessionstate.Working)

	if err := handleNotification(notification("idle_prompt")); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	if got := sessionState(t, db); got != sessionstate.Idle {
		t.Errorf("state = %q, want %q", got, sessionstate.Idle)
	}
}

// TestNotification_AnUnmodelledTypeWritesNothing is the rule, not a gap.
// `notification_type` carries at least a dozen documented values — auth,
// elicitation, quota, agent-team — and an unrecognised one is also the shape a
// harness change arrives in. Moving the session on either would be a guess.
func TestNotification_AnUnmodelledTypeWritesNothing(t *testing.T) {
	for _, notificationType := range []string{
		"auth_required", "elicitation", "agent_needs_input", "", "permission",
	} {
		t.Run(notificationType, func(t *testing.T) {
			db := notifyTestDB(t, sessionstate.Working)

			if err := handleNotification(notification(notificationType)); err != nil {
				t.Fatalf("handleNotification: %v", err)
			}
			if got := sessionState(t, db); got != sessionstate.Working {
				t.Errorf("state = %q, want it left at %q", got, sessionstate.Working)
			}
		})
	}
}

// TestClearPromptState_ReturnsAPromptedSessionToWorking is the clearing half.
// PostToolUse means the approved tool completed; UserPromptSubmit means the user
// typed instead. Both mean the answer arrived.
func TestClearPromptState_ReturnsAPromptedSessionToWorking(t *testing.T) {
	db := notifyTestDB(t, sessionstate.Prompted)

	clearPromptState(claudePayload{SessionID: "sess-A", EventName: "PostToolUse"})

	if got := sessionState(t, db); got != sessionstate.Working {
		t.Errorf("state = %q, want %q", got, sessionstate.Working)
	}
}

// TestClearPromptState_LeavesEveryOtherStateAlone pins the narrowness that
// makes the clear safe to call on every PostToolUse and UserPromptSubmit. It
// only ever undoes a state PromptSession wrote; it can never demote a session
// that moved on some other way — an idle one between turns least of all.
func TestClearPromptState_LeavesEveryOtherStateAlone(t *testing.T) {
	for _, state := range sessionstate.Get(sessionstate.All) {
		if state == sessionstate.Prompted {
			continue
		}
		t.Run(state, func(t *testing.T) {
			db := notifyTestDB(t, state)

			clearPromptState(claudePayload{SessionID: "sess-A", EventName: "PostToolUse"})

			if got := sessionState(t, db); got != state {
				t.Errorf("state = %q, want it left at %q", got, state)
			}
		})
	}
}

// TestNotification_RoundTrip is the shape a real turn takes: the prompt blocks
// the session, the user approves, the tool completes, and the next event puts
// it back to work.
func TestNotification_RoundTrip(t *testing.T) {
	db := notifyTestDB(t, sessionstate.Working)

	if err := handleNotification(notification("permission_prompt")); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	if got := sessionState(t, db); got != sessionstate.Prompted {
		t.Fatalf("state after the prompt = %q, want %q", got, sessionstate.Prompted)
	}

	clearPromptState(claudePayload{SessionID: "sess-A", EventName: "PostToolUse"})
	if got := sessionState(t, db); got != sessionstate.Working {
		t.Fatalf("state after the approval = %q, want %q", got, sessionstate.Working)
	}

	// A second clear is a no-op rather than an error: both clearing events can
	// fire in one turn.
	clearPromptState(claudePayload{SessionID: "sess-A", EventName: "UserPromptSubmit"})
	if got := sessionState(t, db); got != sessionstate.Working {
		t.Errorf("state after a redundant clear = %q, want %q", got, sessionstate.Working)
	}
}

// TestNotification_StopFromPromptedEndsIdle pins the case the design leans on
// for its safety argument: a turn that ends with a prompt still outstanding.
// `Stop` writes `idle` unconditionally, so the prompt state cannot survive a
// turn boundary even if no clearing event ever fires — which is why a missed
// clear costs a stale glyph and nothing more.
func TestNotification_StopFromPromptedEndsIdle(t *testing.T) {
	db := notifyTestDB(t, sessionstate.Prompted)

	if err := monitor.IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}
	if got := sessionState(t, db); got != sessionstate.Idle {
		t.Errorf("state = %q, want %q", got, sessionstate.Idle)
	}
}
