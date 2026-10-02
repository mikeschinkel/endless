package monitor

import (
	"errors"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// ---------------------------------------------------------------------------
// PrimeSession / ResumeFromPrimed / IdleSession (E-1994) — a read-in that holds
// for its user.
// ---------------------------------------------------------------------------

// TestPrimeSession_OnlyFromWorking pins the narrow write. `session primed` is
// run from inside a turn, so the session is working; anything else is refused
// with ErrNotPrimable rather than written.
func TestPrimeSession_OnlyFromWorking(t *testing.T) {
	for _, state := range sessionstate.Get(sessionstate.All) {
		t.Run(state, func(t *testing.T) {
			db := seedPromptSession(t, state)
			err := PrimeSession("sess-A")
			got, _, _ := sessionLifecycleRow(t, db, "sess-A")
			if state == sessionstate.Working {
				if err != nil {
					t.Fatalf("PrimeSession: %v", err)
				}
				if got != sessionstate.Primed {
					t.Errorf("state = %q, want primed", got)
				}
				return
			}
			if !errors.Is(err, ErrNotPrimable) {
				t.Errorf("PrimeSession from %q: err = %v, want ErrNotPrimable", state, err)
			}
			if got != state {
				t.Errorf("state = %q, want unchanged %q", got, state)
			}
		})
	}
}

// TestPrimeSession_RequiresATask: a read-in is always of a task, so a session
// holding none has nothing to be primed on.
func TestPrimeSession_RequiresATask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, started_at, last_activity)
		 VALUES ('sess-B', 1, 'claude', ?, '2026-01-01T00:00:00', '2026-01-01T00:00:00')`,
		sessionstate.Working,
	); err != nil {
		t.Fatal(err)
	}
	if err := PrimeSession("sess-B"); !errors.Is(err, ErrNotPrimable) {
		t.Errorf("err = %v, want ErrNotPrimable", err)
	}
}

// TestIdleSession_KeepsPrimed is the Stop half. The read-in primes itself inside
// its last turn, and the Stop that ends the turn arrives afterwards; idling the
// row there would erase the only thing telling a primed session from a hung one.
func TestIdleSession_KeepsPrimed(t *testing.T) {
	db := seedPromptSession(t, sessionstate.Working)
	if err := PrimeSession("sess-A"); err != nil {
		t.Fatalf("PrimeSession: %v", err)
	}
	if err := IdleSession("sess-A"); err != nil {
		t.Fatalf("IdleSession: %v", err)
	}
	if got, _, _ := sessionLifecycleRow(t, db, "sess-A"); got != sessionstate.Primed {
		t.Errorf("state after Stop = %q, want primed", got)
	}
}

// TestIdleSession_StillIdlesEverythingElse: the exclusion is one state wide.
func TestIdleSession_StillIdlesEverythingElse(t *testing.T) {
	for _, state := range sessionstate.Get(sessionstate.All) {
		if state == sessionstate.Primed {
			continue
		}
		t.Run(state, func(t *testing.T) {
			db := seedPromptSession(t, state)
			if err := IdleSession("sess-A"); err != nil {
				t.Fatalf("IdleSession: %v", err)
			}
			if got, _, _ := sessionLifecycleRow(t, db, "sess-A"); got != sessionstate.Idle {
				t.Errorf("state = %q, want idle", got)
			}
		})
	}
}

// TestResumeFromPrimed_ClearsOnlyPrimedOnce: it fires on every
// UserPromptSubmit, so it must leave every other state alone and report a
// resume exactly once — the drift check runs on that report.
func TestResumeFromPrimed_ClearsOnlyPrimedOnce(t *testing.T) {
	for _, state := range sessionstate.Get(sessionstate.All) {
		t.Run(state, func(t *testing.T) {
			db := seedPromptSession(t, state)
			resumed, err := ResumeFromPrimed("sess-A")
			if err != nil {
				t.Fatalf("ResumeFromPrimed: %v", err)
			}
			want := state
			if state == sessionstate.Primed {
				want = sessionstate.Working
			}
			if resumed != (state == sessionstate.Primed) {
				t.Errorf("resumed = %v from %q", resumed, state)
			}
			if got, _, _ := sessionLifecycleRow(t, db, "sess-A"); got != want {
				t.Errorf("state = %q, want %q", got, want)
			}
			again, _ := ResumeFromPrimed("sess-A")
			if again {
				t.Error("second ResumeFromPrimed reported a resume")
			}
		})
	}
}
