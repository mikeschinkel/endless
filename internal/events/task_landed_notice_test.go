// E-2005: a session is told when a task it holds is landed.
//
// Landing changes no field tasks_notify_sessions watches, so before this the
// notice a user most wants after landing from another terminal simply did not
// exist. These tests drive the real executor against the real schema, so the
// insert, the trigger, and the suppression rule are exercised together — the
// trigger is where the rule actually lives, and a test that seeded
// session_notices directly would prove nothing about it.
//
// The rule, stated once: an AGENT is not told about a land it performed; a
// PERSON's land is announced to every holder, including the session the land
// was attributed to. That second half is the case this exists for, and it is
// inexpressible from session_id alone.
package events

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// seedLandingSession creates a session in the given state and makes it a holder
// of task 1337, so a landing has somebody to notify.
func seedLandingSession(t *testing.T, db *sql.DB, sessionID int64, state string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state,
		                       active_task_id, kind_id, started_at, last_activity)
		 VALUES (?, ?, 1, 'claude', ?, 1337, 1,
		         '2026-08-20T00:00:00', '2026-08-20T00:00:00')`,
		sessionID, "sess-landing-"+strconv.FormatInt(sessionID, 10), state,
	); err != nil {
		t.Fatalf("seed session %d: %v", sessionID, err)
	}
	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (?, 1337, '2026-08-20T00:00:00', '2026-08-20T00:00:00')`,
		sessionID,
	); err != nil {
		t.Fatalf("seed session_task %d: %v", sessionID, err)
	}
}

// landingNoticesFor returns the `changes` JSON of every notice queued for a
// session, so a test can assert both how many arrived and what they say.
func landingNoticesFor(t *testing.T, db *sql.DB, sessionID int64) []string {
	t.Helper()
	rows, err := db.Query(
		"SELECT changes FROM session_notices WHERE session_id = ? ORDER BY id",
		sessionID,
	)
	if err != nil {
		t.Fatalf("query notices: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan notice: %v", err)
		}
		out = append(out, c)
	}
	return out
}

// landedEventWith builds a task.landed event with an explicit base branch and
// harness — the two facts E-2005 added.
func landedEventWith(t *testing.T, sessionID, harness, baseBranch, sha string) *Event {
	t.Helper()
	evt := landedEvent(t, 1337, sessionID)
	payload, err := json.Marshal(TaskLandedPayload{
		Branch:         "task/1337-stop-deleting-worktrees",
		BaseBranch:     baseBranch,
		MergeCommitSHA: sha,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	evt.Payload = payload
	evt.Actor.Harness = harness
	return evt
}

// TestExecTaskLanded_RecordsBaseBranchAndHarness pins the two new columns
// against the two new inputs: the payload's base_branch and the envelope's
// actor.harness. Without both reaching the row, the trigger below has nothing
// to decide on and nothing to render.
func TestExecTaskLanded_RecordsBaseBranchAndHarness(t *testing.T) {
	db := newLandingTestDB(t)
	evt := landedEventWith(t, "", "claude_cli", "main", "1dd00061234")

	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	var baseBranch, harness sql.NullString
	if err := db.QueryRow(
		"SELECT base_branch, landed_by_harness FROM task_landings WHERE task_id = 1337",
	).Scan(&baseBranch, &harness); err != nil {
		t.Fatalf("query: %v", err)
	}
	if baseBranch.String != "main" {
		t.Errorf("base_branch: got %q, want %q", baseBranch.String, "main")
	}
	if harness.String != "claude_cli" {
		t.Errorf("landed_by_harness: got %q, want %q", harness.String, "claude_cli")
	}
}

// TestExecTaskLanded_NoHarnessRecordsNull is the human's land, and NULL is not
// a fallback here: it is the value the suppression rule reads as "a person did
// this, tell everyone". An empty string would be a recorded value and would
// silence the session that needed to hear.
func TestExecTaskLanded_NoHarnessRecordsNull(t *testing.T) {
	db := newLandingTestDB(t)
	evt := landedEventWith(t, "", "", "", "1dd00061234")

	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	var baseBranch, harness sql.NullString
	if err := db.QueryRow(
		"SELECT base_branch, landed_by_harness FROM task_landings WHERE task_id = 1337",
	).Scan(&baseBranch, &harness); err != nil {
		t.Fatalf("query: %v", err)
	}
	if harness.Valid {
		t.Errorf("landed_by_harness: got %q, want NULL", harness.String)
	}
	if baseBranch.Valid {
		t.Errorf("base_branch: got %q, want NULL", baseBranch.String)
	}
}

// TestLandingNotice_AgentIsNotToldAboutItsOwnLand keeps the notice from being
// noise. An agent that just ran the land already knows.
func TestLandingNotice_AgentIsNotToldAboutItsOwnLand(t *testing.T) {
	db := newLandingTestDB(t)
	seedLandingSession(t, db, 9200, "working")
	seedLandingSession(t, db, 9201, "working")

	evt := landedEventWith(t, "9200", "claude_cli", "main", "1dd00061234")
	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	if got := len(landingNoticesFor(t, db, 9200)); got != 0 {
		t.Errorf("the landing agent must not be notified, got %d notices", got)
	}
	if got := len(landingNoticesFor(t, db, 9201)); got != 1 {
		t.Errorf("the other holder must still be notified, got %d notices", got)
	}
}

// TestLandingNotice_HumanLandNotifiesEveryHolder is the 99th-percentile case
// and THE reason Actor.Harness had to exist.
//
// The event carries session 9200 in Actor.SessionID — which is what the
// resolver produces for a person running `endless worktree land` with `esu`
// active or from a pane beside the agent — but no harness, so the process was a
// human's shell. Suppressing on session id alone would silence 9200, the one
// session whose work just landed.
func TestLandingNotice_HumanLandNotifiesEveryHolder(t *testing.T) {
	db := newLandingTestDB(t)
	seedLandingSession(t, db, 9200, "working")
	seedLandingSession(t, db, 9201, "idle")

	evt := landedEventWith(t, "9200", "", "main", "1dd00061234")
	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	if got := len(landingNoticesFor(t, db, 9200)); got != 1 {
		t.Errorf("the session the land was attributed to must hear about it, got %d notices", got)
	}
	// idle, not ended: an idle session can come back, and a notice surviving
	// until it does is the entire point of one-shot delivery.
	if got := len(landingNoticesFor(t, db, 9201)); got != 1 {
		t.Errorf("an idle holder must be notified, got %d notices", got)
	}
}

// TestLandingNotice_EndedSessionsAreNotNotified mirrors tasks_notify_sessions:
// an ended session never takes another turn, so its notice is undeliverable by
// construction and would only accumulate.
func TestLandingNotice_EndedSessionsAreNotNotified(t *testing.T) {
	db := newLandingTestDB(t)
	seedLandingSession(t, db, 9202, "ended")

	evt := landedEventWith(t, "", "", "main", "1dd00061234")
	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	if got := len(landingNoticesFor(t, db, 9202)); got != 0 {
		t.Errorf("an ended session must not be queued a notice, got %d", got)
	}
}

// TestLandingNotice_PayloadShape pins the JSON the trigger writes, because the
// renderer parses it and the two are only correct together. The `landed` key is
// synthetic — no task field is named that — and the sha is abbreviated to 7 in
// SQL rather than at render time so the notice log records what was shown.
func TestLandingNotice_PayloadShape(t *testing.T) {
	db := newLandingTestDB(t)
	seedLandingSession(t, db, 9203, "working")

	evt := landedEventWith(t, "", "", "main", "1dd00061234")
	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	notices := landingNoticesFor(t, db, 9203)
	if len(notices) != 1 {
		t.Fatalf("want exactly one notice, got %d", len(notices))
	}
	var decoded map[string]struct {
		Before any `json:"before"`
		After  any `json:"after"`
	}
	if err := json.Unmarshal([]byte(notices[0]), &decoded); err != nil {
		t.Fatalf("notice changes is not the object the renderer expects: %v (%s)",
			err, notices[0])
	}
	change, ok := decoded["landed"]
	if !ok {
		t.Fatalf("notice carries no `landed` key: %s", notices[0])
	}
	if change.Before != nil {
		t.Errorf("before: got %v, want null", change.Before)
	}
	if change.After != "main@1dd0006" {
		t.Errorf("after: got %v, want %q", change.After, "main@1dd0006")
	}
}

// TestLandingNotice_NoBaseBranchRecordsShaAlone covers the record-only backfill
// (E-1719), which knows no base branch. The notice degrades to naming the
// commit rather than inventing a branch.
func TestLandingNotice_NoBaseBranchRecordsShaAlone(t *testing.T) {
	db := newLandingTestDB(t)
	seedLandingSession(t, db, 9204, "working")

	evt := landedEventWith(t, "", "", "", "6671bca9abc")
	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	notices := landingNoticesFor(t, db, 9204)
	if len(notices) != 1 {
		t.Fatalf("want exactly one notice, got %d", len(notices))
	}
	if want := `"after":"6671bca"`; !strings.Contains(notices[0], want) {
		t.Errorf("notice %s does not carry %s", notices[0], want)
	}
}

// TestLandingNotice_ChangedBySessionOnlyWhenAnAgentLanded keeps the column
// meaning what it means for the sibling trigger: it names the session that MADE
// the change, and a human's land was not made by the session it is attributed
// to.
func TestLandingNotice_ChangedBySessionOnlyWhenAnAgentLanded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		harness   string
		wantValid bool
	}{
		{"human land stamps NULL", "", false},
		{"agent land stamps the session", "claude_cli", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newLandingTestDB(t)
			seedLandingSession(t, db, 9205, "working")
			// A DIFFERENT session lands, so the agent case still queues a
			// notice for 9205 to inspect.
			seedLandingSession(t, db, 9206, "working")

			evt := landedEventWith(t, "9206", tc.harness, "main", "1dd00061234")
			if _, err := execTaskLanded(db, evt); err != nil {
				t.Fatalf("execTaskLanded: %v", err)
			}
			var actor sql.NullInt64
			if err := db.QueryRow(
				"SELECT changed_by_session FROM session_notices WHERE session_id = 9205",
			).Scan(&actor); err != nil {
				t.Fatalf("query notice actor: %v", err)
			}
			if actor.Valid != tc.wantValid {
				t.Errorf("changed_by_session valid = %v, want %v", actor.Valid, tc.wantValid)
			}
			if tc.wantValid && actor.Int64 != 9206 {
				t.Errorf("changed_by_session = %d, want 9206", actor.Int64)
			}
		})
	}
}

// TestEmittingActor_StampsTheObservedHarness pins that the harness is READ from
// the environment of the emitting process rather than accepted from a caller,
// and that it survives into the event JSON — the ledger line is what a rebuild
// replays, so a field that marshals away would be lost on projection.
func TestEmittingActor_StampsTheObservedHarness(t *testing.T) {
	t.Run("an agent's Bash tool", func(t *testing.T) {
		// The variable Claude Code exports into every subprocess it spawns.
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
		actor := EmittingActor(ActorCLI, "user@host", "9200")
		if actor.Harness != "claude_cli" {
			t.Errorf("Harness = %q, want %q", actor.Harness, "claude_cli")
		}
		line, err := json.Marshal(Event{Actor: actor})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(line), `"harness":"claude_cli"`) {
			t.Errorf("harness did not survive into the ledger line: %s", line)
		}
	})

	t.Run("a person at a shell", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
		t.Setenv("__CFBundleIdentifier", "")
		actor := EmittingActor(ActorCLI, "user@host", "9200")
		if actor.Harness != "" {
			t.Errorf("Harness = %q, want empty", actor.Harness)
		}
		line, err := json.Marshal(Event{Actor: actor})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// omitempty: a human's event carries no harness key at all, which is
		// also what every event predating the field looks like.
		if strings.Contains(string(line), "harness") {
			t.Errorf("a human's event must carry no harness key: %s", line)
		}
	})
}
