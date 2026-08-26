package events

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// The status-lifecycle guard for `task update --status` (E-2018).
//
// Nothing enforced the documented lifecycle: `task update --status` accepted any
// status, from any status, from any actor, whether or not a session had ever
// claimed the task. Observed 2026-08-20 — a session holding E-1817 set E-2015 to
// `unverified` on a task that was `unplanned`, had never been claimed, and had
// no worktree, so `session goto E-2015` found nothing to go to. Both missing
// checks were computable from data Endless already had.
//
// Two plain exported validators called from execTaskFieldsUpdated, following
// ValidateNoParentCycle exactly (E-2067): that task chose a shared function over
// a registry for the same hook, and a second arrangement for the second rule
// would be one arrangement too many. Note the asymmetry that makes this more
// than a copy — the parent guard needs only the tasks table, while transition
// legality needs internal/taskstatus' edge table.
//
// # No --force
//
// Following E-1577/E-1579's precedent for the type×status gate: a correctness
// invariant with no bypass, on the grounds that the fix is to correct the call,
// not to override the gate. A --force here would be reached for on the first
// missing edge and would become habit, and an override that becomes habit is a
// guard that has stopped meaning anything.

// ValidateStatusTransition refuses a status change that is not an edge of the
// lifecycle for this task's type.
//
// The message names the illegal edge and then lists the statuses that ARE
// reachable from the current one, so the caller's next move is in the refusal
// rather than in the docs. That matters more here than usual: the caller is
// usually an agent, and an agent told only "no" will guess again.
func ValidateStatusTransition(from, to taskstatus.Status, tt tasktype.TaskType) error {
	if taskstatus.TransitionAllowed(from, to, tt) {
		return nil
	}

	reachable := taskstatus.ReachableFrom(from, tt)
	if len(reachable) == 0 {
		// A terminal status with no outbound edge for this type. Naming an
		// empty list would read as a rendering bug, so say what is true.
		return fmt.Errorf(
			"events: %q is not a legal status change from %q — %q is a terminal status for a %s task, with no transition out of it",
			to, from, from, tt)
	}
	return fmt.Errorf(
		"events: %q is not a legal status change from %q for a %s task (see docs/status-lifecycle.mmd); reachable from %q: %s",
		to, from, tt, from, strings.Join(reachable, ", "))
}

// ValidateStatusActor refuses a work-progress status the acting agent has no
// standing to set.
//
// Two rules, both computed from data Endless already had:
//
//   - A session may not move a task it does not hold into `underway` or
//     `unverified`. sessions.task_id is write-once (ED-1560), so "does this
//     session hold this task" is a lookup, not a heuristic.
//   - `unverified` additionally requires that SOME session claimed the task at
//     some point. "Implementation done" about work no session ever picked up
//     is the reported defect stated exactly.
//
// # Who is exempt
//
// A caller with no agent harness — a person at a shell, cron, a migration — is
// exempt entirely. This guards against an agent's mistake, not against a person.
//
// The exemption is keyed on Actor.Harness, NOT on Actor.Kind and NOT on the
// presence of a session id, both of which would get it wrong in opposite
// directions. `task update` always emits as kind `cli`, so keying on Kind would
// exempt everybody; and E-1294's resolver deliberately credits a bare shell in a
// sibling tmux pane to the Claude session next to it, so a person's command
// routinely arrives carrying an agent's session id — keying on session-presence
// would exempt nobody. Harness is the field that answers "WHO did this", which
// is why E-2005 added it.
func ValidateStatusActor(db dbQuerier, taskID int64, to taskstatus.Status, actor Actor) error {
	if actor.Harness == "" {
		return nil
	}
	if to != taskstatus.Underway && to != taskstatus.Unverified {
		return nil
	}

	if actor.SessionID != "" {
		held, err := sessionHeldTask(db, actor.SessionID)
		if err != nil {
			// An unreadable sessions row is not evidence of a mistake, and
			// referential integrity is not this validator's job — the same
			// stance ValidateNoParentCycle takes on an unreadable ancestor.
			return nil
		}
		if held == nil {
			return fmt.Errorf(
				"events: session %s has not claimed any task, so it may not set task %d to %q; claim it first (endless task claim E-%d)",
				actor.SessionID, taskID, to, taskID)
		}
		if *held != taskID {
			return fmt.Errorf(
				"events: session %s holds task %d, so it may not set task %d to %q; a session owns one task for its lifetime — spawn a session on E-%d instead (endless task spawn E-%d)",
				actor.SessionID, *held, taskID, to, taskID, taskID)
		}
	}

	if to == taskstatus.Unverified {
		claimed, err := taskEverClaimed(db, taskID)
		if err != nil {
			return nil
		}
		if !claimed {
			return fmt.Errorf(
				"events: task %d has never been claimed by any session, so %q would report implementation nobody did; claim it first (endless task claim E-%d)",
				taskID, to, taskID)
		}
	}
	return nil
}

// sessionHeldTask returns the task a session holds, or nil when it holds none.
// The error is reserved for a row that cannot be read at all.
func sessionHeldTask(db dbQuerier, sessionIDStr string) (*int64, error) {
	sessionID, err := strconv.ParseInt(sessionIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("events: unparseable session id %q: %w", sessionIDStr, err)
	}
	var held sql.NullInt64
	if err := db.QueryRow("SELECT task_id FROM sessions WHERE id = ?", sessionID).
		Scan(&held); err != nil {
		return nil, fmt.Errorf("events: read session %d: %w", sessionID, err)
	}
	if !held.Valid {
		return nil, nil
	}
	return &held.Int64, nil
}

// taskEverClaimed reports whether any session's write-once task_id points at
// this task. Write-once is what makes this a fact rather than a snapshot: a
// session that ever held the task still says so.
func taskEverClaimed(db dbQuerier, taskID int64) (bool, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions WHERE task_id = ?", taskID).
		Scan(&n); err != nil {
		return false, fmt.Errorf("events: count sessions holding task %d: %w", taskID, err)
	}
	return n > 0, nil
}

// taskStatusAndType reads the row's current status and effective task type.
//
// A NULL type_id resolves to the default type rather than an error: legacy rows
// predate the enum (E-1548 reclassifies them), and the only thing the type
// decides here is which lane a task is on. Refusing to move a NULL-typed row
// would strand exactly the rows least able to fix themselves.
func taskStatusAndType(db dbQuerier, taskID any) (taskstatus.Status, tasktype.TaskType, error) {
	var status string
	var typeID sql.NullInt64
	if err := db.QueryRow("SELECT status, type_id FROM tasks WHERE id = ?", taskID).
		Scan(&status, &typeID); err != nil {
		return "", 0, fmt.Errorf("events: load task for the status guard: %w", err)
	}
	tt := tasktype.TaskTypeTask
	if typeID.Valid {
		tt = tasktype.TaskType(typeID.Int64)
	}
	return status, tt, nil
}
