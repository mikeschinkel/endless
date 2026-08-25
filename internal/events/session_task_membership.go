package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// Session-task membership verbs (E-1696). `session task add` and
// `session task remove` — the correction path for the otherwise-automatic
// session_tasks capture.
//
// The two are NOT symmetric with `session hide --task` / `session unhide --task`
// (E-1914), and the asymmetry is the point:
//
//   - hide   — display suppression. The session_tasks row survives, so
//     `task show`'s "Touched by:" still reports the touch that really happened.
//     For a capture that is real but noisy.
//   - remove — the association itself. DELETEs the session_tasks row (its
//     relation and its do_order with it), and clears any hide alongside. For a
//     capture that was simply wrong.
//
// Both executors resolve the session the same way execSessionTasksOrdered does:
// the payload's `process` field carries either the "__session_id=N" sentinel or
// a raw tmux pane id.

// execSessionTasksQueued handles KindSessionTasksQueued: promote each named task
// to relation `queued` for the emitting session, creating the session_tasks row
// if the session has not touched the task before.
//
// The write goes through upsertSessionTask rather than a direct UPDATE so the
// upgrade-only ladder governs it exactly as it governs an automatic capture. Two
// behaviors fall out of that rather than being special-cased here:
//
//   - Queuing a task already captured as `referenced`, `revisited` or `surfaced`
//     upgrades it — `queued` outranks all three.
//   - Queuing the session's OWN claimed task leaves it `claimed`; the ladder
//     refuses the downgrade. That is reported as a no-op rather than an error,
//     because asking to work on what you already claimed is redundant, not wrong.
//
// An id naming no live task is a hard error for the whole call (the open
// transaction rolls back): it is a typo, and silently queuing nothing would hide
// it. This matches execSessionTasksOrdered's treatment of unknown ids.
func execSessionTasksQueued(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	sessionID, taskIDs, err := membershipRequest(db, evt, "session_tasks.queued")
	if err != nil {
		return nil, err
	}
	if err := requireLiveTasks(db, taskIDs, "session_tasks.queued"); err != nil {
		return nil, err
	}

	var queued, alreadyClaimed []int64
	for _, taskID := range taskIDs {
		// Read the stored relation BEFORE the upsert so the report can tell
		// "promoted" from "left alone". The ladder decides the outcome either
		// way; this only observes it.
		prior, err := sessionTaskRelationOf(db, sessionID, taskID)
		if err != nil {
			return nil, err
		}
		if prior == sessiontaskrelation.RelationClaimed {
			alreadyClaimed = append(alreadyClaimed, taskID)
			continue
		}
		if err := upsertSessionTask(
			db, strconv.FormatInt(sessionID, 10), taskID,
			sessiontaskrelation.RelationQueued,
		); err != nil {
			return nil, err
		}
		queued = append(queued, taskID)
	}

	return &ExecuteResult{Markdown: renderQueued(queued, alreadyClaimed)}, nil
}

// execSessionTasksRemoved handles KindSessionTasksRemoved: drop each named
// task's session_tasks row for the emitting session.
//
// Removing the session's own CLAIMED task is refused outright. That row is not
// a false positive by construction — the session claimed that task — and the
// next task event would recreate it anyway, so accepting the request would be a
// lie about what happened. Refusing the whole call (rather than skipping the id)
// keeps `session task remove E-1 E-2` from half-succeeding. There is no escape
// hatch and none to offer: ED-1560 makes sessions.task_id write-once and E-1968
// disabled `task release`, so a claim is not something a session can undo.
//
// Removing a task the session never touched is a NO-OP, not an error, matching
// `session unhide --task`. It is reported so a typo still surfaces, but it does
// not fail the call: "make sure this is not on my list" is a reasonable thing to
// ask about a task that already is not.
//
// The hide is cleared alongside the row. session_hidden_tasks and session_tasks
// are independent tables keyed on the same (session, task) pair with no
// relationship between them, so dropping membership leaves the hide behind on
// its own. If the task is later re-captured — an edit, or `session task add` —
// it returns ALREADY hidden, with nothing on screen to explain why. Clearing it
// here is what makes remove mean "off the record" rather than "off the record
// but still carrying a suppression you cannot see".
func execSessionTasksRemoved(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	sessionID, taskIDs, err := membershipRequest(db, evt, "session_tasks.removed")
	if err != nil {
		return nil, err
	}

	// Reject the whole call before mutating anything if any id is claimed.
	var claimed []string
	for _, taskID := range taskIDs {
		rel, err := sessionTaskRelationOf(db, sessionID, taskID)
		if err != nil {
			return nil, err
		}
		if rel == sessiontaskrelation.RelationClaimed {
			claimed = append(claimed, fmt.Sprintf("E-%d", taskID))
		}
	}
	if len(claimed) > 0 {
		sort.Strings(claimed)
		return nil, fmt.Errorf(
			"events: session_tasks.removed refuses this session's claimed task(s): %s "+
				"(a claim cannot be dropped; `session hide --task` suppresses it "+
				"from the listing)",
			strings.Join(claimed, ", "),
		)
	}

	var removed, absent []int64
	for _, taskID := range taskIDs {
		res, err := db.Exec(
			`DELETE FROM session_tasks WHERE session_id = ? AND task_id = ?`,
			sessionID, taskID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"events: delete session_tasks (%d, %d): %w", sessionID, taskID, err,
			)
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			absent = append(absent, taskID)
			continue
		}
		if _, err := db.Exec(
			`DELETE FROM session_hidden_tasks WHERE session_id = ? AND task_id = ?`,
			sessionID, taskID,
		); err != nil {
			return nil, fmt.Errorf(
				"events: clear hide for (%d, %d): %w", sessionID, taskID, err,
			)
		}
		removed = append(removed, taskID)
	}

	return &ExecuteResult{Markdown: renderRemoved(removed, absent)}, nil
}

// membershipRequest unmarshals a SessionTaskMembershipPayload, resolves the
// emitting session, and parses the task ids. Shared by both membership
// executors, whose input handling is identical.
//
// kind is only used to attribute errors, so a failure names the verb the user
// ran rather than this helper.
func membershipRequest(db dbQuerier, evt *Event, kind string) (int64, []int64, error) {
	var p SessionTaskMembershipPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return 0, nil, fmt.Errorf("events: unmarshal %s payload: %w", kind, err)
	}

	sessionID, ok, err := sessionIDFromSentinel(db, p.Process)
	if err != nil {
		return 0, nil, err
	}
	if !ok {
		sessionID, err = liveSessionByProcessTx(db, p.Process)
		if err != nil {
			return 0, nil, fmt.Errorf(
				"events: no live session for process %q: %w", p.Process, err,
			)
		}
	}

	if len(p.TaskIDs) == 0 {
		return 0, nil, fmt.Errorf("events: %s names no tasks", kind)
	}
	seen := make(map[int64]bool, len(p.TaskIDs))
	taskIDs := make([]int64, 0, len(p.TaskIDs))
	for _, raw := range p.TaskIDs {
		taskID, perr := parseTaskDisplayID(raw)
		if perr != nil {
			return 0, nil, perr
		}
		if seen[taskID] {
			continue // a repeated id is the same request, not an error
		}
		seen[taskID] = true
		taskIDs = append(taskIDs, taskID)
	}
	return sessionID, taskIDs, nil
}

// requireLiveTasks fails unless every id names a live (non-removed) task. Reads
// live_tasks, not tasks, so a soft-removed task counts as absent (E-1929).
func requireLiveTasks(db dbQuerier, taskIDs []int64, kind string) error {
	var missing []string
	for _, taskID := range taskIDs {
		var got int64
		err := db.QueryRow(`SELECT id FROM live_tasks WHERE id = ?`, taskID).Scan(&got)
		if err != nil {
			missing = append(missing, fmt.Sprintf("E-%d", taskID))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf(
			"events: %s references unknown task(s): %s", kind, strings.Join(missing, ", "),
		)
	}
	return nil
}

// sessionTaskRelationOf returns the stored relation for one (session, task)
// pair. A pair with no row — or one whose relation_id is NULL (a pre-E-1462
// historical row) — yields Relation(0), which ranks below every real relation
// and matches no constant, so callers testing for a specific relation get the
// right answer without a separate "exists" flag.
func sessionTaskRelationOf(db dbQuerier, sessionID, taskID int64) (sessiontaskrelation.Relation, error) {
	var relID sql.NullInt64
	err := db.QueryRow(
		`SELECT relation_id FROM session_tasks WHERE session_id = ? AND task_id = ?`,
		sessionID, taskID,
	).Scan(&relID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf(
			"events: read session_tasks relation (%d, %d): %w", sessionID, taskID, err,
		)
	}
	if !relID.Valid {
		return 0, nil
	}
	return sessiontaskrelation.Relation(relID.Int64), nil
}

// renderQueued formats the `session task add` result for chat.
func renderQueued(queued, alreadyClaimed []int64) string {
	var b strings.Builder
	if len(queued) > 0 {
		fmt.Fprintf(&b, "Queued %s for this session.\n", taskList(queued))
	}
	if len(alreadyClaimed) > 0 {
		fmt.Fprintf(&b, "%s already claimed by this session — left as is.\n", taskList(alreadyClaimed))
	}
	return b.String()
}

// renderRemoved formats the `session task remove` result for chat. The absent
// line is not an error, but it is always reported: a silently ignored id is
// indistinguishable from a typo.
func renderRemoved(removed, absent []int64) string {
	var b strings.Builder
	if len(removed) > 0 {
		fmt.Fprintf(&b, "Removed %s from this session.\n", taskList(removed))
	}
	if len(absent) > 0 {
		fmt.Fprintf(&b, "%s not in this session — nothing to remove.\n", taskList(absent))
	}
	return b.String()
}

// taskList renders ids as a comma-joined display-form list ("E-1, E-2").
func taskList(ids []int64) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = fmt.Sprintf("E-%d", id)
	}
	return strings.Join(out, ", ")
}
