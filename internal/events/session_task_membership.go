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

// Session-task membership verbs (E-1696, E-2173). `session task add`, the
// top-level `touch`, and `session task remove` — the explicit path into and out
// of a session's scope, alongside the otherwise-automatic session_tasks capture.
//
// `add` and `touch` differ only in the relation they offer the ladder, and that
// difference is the whole of each verb's meaning: `queued` is decided work that
// belongs on the session's agenda, `revisited` is a task the session merely has
// in scope. Both share enrollSessionTasks.
//
// The two are NOT symmetric with `session hide --task` / `session unhide --task`
// (E-1914), and the asymmetry is the point:
//
//   - hide   — display suppression. The session_tasks row survives, so
//     `task show`'s "Touched by:" still reports the touch that really happened.
//     For a capture that is real but noisy.
//   - remove — the association itself. DELETEs the session_tasks row (and its
//     relation with it), and clears any hide alongside. For a capture that was
//     simply wrong.
//
// Every executor here resolves the session the same way: the payload's
// `process` field carries either the "__session_id=N" sentinel or a raw tmux
// pane id, and membershipRequest turns it into a sessions.id.

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
// `claimed` is the only relation that outranks `queued`, so enrollSessionTasks'
// generic "left alone" set is exactly the already-claimed set here, and
// renderQueued can name it as such.
func execSessionTasksQueued(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	queued, alreadyClaimed, _, err := enrollSessionTasks(
		db, evt, "session_tasks.queued", sessiontaskrelation.RelationQueued,
	)
	if err != nil {
		return nil, err
	}
	return &ExecuteResult{Markdown: renderQueued(queued, alreadyClaimed)}, nil
}

// execSessionTasksTouched handles KindSessionTasksTouched: put each named task
// in the emitting session's scope at relation `revisited` (E-2173), changing
// nothing about the task itself.
//
// This is the verb whose WHOLE job is scope entry. Before it existed the only
// way onto `session status` was to edit the task — in practice rewriting
// `phase` purely for the capture side effect — which wrote a field change into
// the ledger to buy a display effect and notified every other session holding
// the task. Touching writes one session_tasks row and nothing else.
//
// `revisited` is the weakest relation any emitter produces, so the ladder
// leaves a stronger stored relation alone: touching a task this session
// claimed, queued or filed reports the relation it already has rather than
// demoting it. Touching a task at `referenced` (weaker) or `revisited` (equal)
// upserts, which refreshes updated_at — the fact the verb is asked to record.
func execSessionTasksTouched(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	touched, unchanged, prior, err := enrollSessionTasks(
		db, evt, "session_tasks.touched", sessiontaskrelation.RelationRevisited,
	)
	if err != nil {
		return nil, err
	}
	return &ExecuteResult{Markdown: renderTouched(touched, unchanged, prior)}, nil
}

// enrollSessionTasks is the shared body of the two scope-entry verbs: resolve
// the request, refuse unknown ids, then offer `rel` to the ladder once per task.
//
// It returns the tasks the upsert ran for, the tasks whose stored relation
// already outranked `rel` (so the ladder would have refused the downgrade and
// the upsert is skipped rather than issued and ignored), and every task's prior
// relation — which a renderer needs to say WHAT a left-alone task already is.
//
// The stored relation is read BEFORE the upsert because that is the only moment
// the two outcomes are distinguishable; the ladder decides them either way,
// this only observes it.
//
// An id naming no live task is a hard error for the whole call (the open
// transaction rolls back): it is a typo, and silently enrolling nothing would
// hide it. kind attributes any failure to the verb the user ran.
func enrollSessionTasks(
	db dbQuerier, evt *Event, kind string, rel sessiontaskrelation.Relation,
) (applied, unchanged []int64, prior map[int64]sessiontaskrelation.Relation, err error) {
	sessionID, taskIDs, err := membershipRequest(db, evt, kind)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := requireLiveTasks(db, taskIDs, kind); err != nil {
		return nil, nil, nil, err
	}

	prior = make(map[int64]sessiontaskrelation.Relation, len(taskIDs))
	for _, taskID := range taskIDs {
		stored, err := sessionTaskRelationOf(db, sessionID, taskID)
		if err != nil {
			return nil, nil, nil, err
		}
		prior[taskID] = stored
		if stored.Outranks(rel) {
			unchanged = append(unchanged, taskID)
			continue
		}
		if err := upsertSessionTask(
			db, strconv.FormatInt(sessionID, 10), taskID, rel,
		); err != nil {
			return nil, nil, nil, err
		}
		applied = append(applied, taskID)
	}
	return applied, unchanged, prior, nil
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

// renderTouched formats the `touch` result for chat.
//
// Unlike queued, THREE relations outrank `revisited`, so "left as is" has to
// name which one a task already holds — "already in this session" alone would
// leave the reader unable to tell a claim from an incidental capture. One line
// per left-alone task rather than a grouped list: the set is one or two ids in
// practice, and grouping would cost more to read than it saves.
func renderTouched(
	touched, unchanged []int64, prior map[int64]sessiontaskrelation.Relation,
) string {
	var b strings.Builder
	if len(touched) > 0 {
		fmt.Fprintf(&b, "Touched %s — in this session's scope, unchanged.\n", taskList(touched))
	}
	for _, taskID := range unchanged {
		fmt.Fprintf(&b, "E-%d already in this session as %s — left as is.\n",
			taskID, prior[taskID].Label())
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
