package events

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// Session-notice fan-out (E-2422). A change to a task tells every session
// holding it, through a session_notices row the session drains on its next
// turn (monitor.PendingNotices). Five SQLite triggers did this until E-2422:
// tasks_notify_sessions (E-1917), task_content_notify_insert/update/delete
// (E-1531) and task_landings_notify_sessions (E-2005). They fired on project
// tables and wrote a machine table, and a trigger cannot reach a table in
// another attached database — so they could not survive the storage split
// (E-2215). The rules they carried live here now, unchanged:
//
//   - WHAT is watched: status, phase, complexity and risk on the task row;
//     plan, analysis and notes as content. Description is not watched (E-1994):
//     the plan is the spec, and a description edit changes nothing a session
//     works from. outcome and reason are not watched either.
//   - Content is never reproduced. Each side of a content change is the single
//     character '…' (U+2026) or null, so a notice still tells added, edited and
//     cleared apart (monitor.elisionSentinel is the reader's copy).
//   - Ratings render as their slugs, so a notice stays readable when a level is
//     relabelled.
//   - ONE EDIT, ONE LINE: everything one event changes on one task is one
//     notice. The content triggers got there by merging into a row the
//     task-row trigger had written in the same second; comparing a snapshot
//     taken before the event with one taken after gets there by construction.
//   - CHANGED → NOTIFY, UNCHANGED → DON'T. The triggers existed so no mutation
//     site could forget to compare old against new. The snapshot is taken
//     around dispatch, the one choke point every task mutation passes through —
//     the event's own write, a cascade, and the epic re-derivation it sets off
//     alike — so no mutation site can skip it either.
//   - Self-suppression and the landing rule: see noticeActor.
//   - Fan-out skips sessions in state 'ended': see deliverSessionNotices.
//
// The notices are written AFTER the project write commits, never inside its
// transaction, because after the split they live in a different database file.
// A replay (ProjectToTempDB) never reaches dispatch, so it fans nothing out.

// noticeContentNames are the content kinds a session holding a task is told
// about, in the order a notice lists them.
var noticeContentNames = []taskcontent.Name{
	taskcontent.Plan, taskcontent.Analysis, taskcontent.Notes,
}

// noticeElision stands in for content a notice says changed without carrying.
const noticeElision = "…"

// noticeRow is the watched slice of one task row. Ratings are slugs, not ids.
type noticeRow struct {
	status, phase, complexity, risk sql.NullString
}

// noticeSnapshot is the watched state of every task a live session holds, plus
// the watched content of the event's own task.
//
// Content is read for the event's task alone: task_content is written only for
// the task an event names (writeTaskContent's callers all pass the entity id),
// and reading every held task's plans twice per event would cost megabytes.
type noticeSnapshot struct {
	rows    map[int64]noticeRow
	taskID  int64
	content map[taskcontent.Name]string
}

// pendingNotice is one notice waiting for its transaction to commit.
type pendingNotice struct {
	taskID    int64
	changes   string
	changedAt string
}

// noticeBatch is what one event has to tell the sessions holding its tasks.
type noticeBatch struct {
	actor   any // the acting session id, or nil — see noticeActor
	notices []pendingNotice
}

// dispatchWithNotices runs dispatch between two snapshots and returns, beside
// its result, the notices the event owes. Callers deliver the batch only after
// their own commit succeeds.
func dispatchWithNotices(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, *noticeBatch, error) {
	before, err := takeNoticeSnapshot(db, evt)
	if err != nil {
		return nil, nil, err
	}
	result, err := dispatch(db, evt, emit)
	if err != nil {
		return nil, nil, err
	}
	after, err := takeNoticeSnapshot(db, evt)
	if err != nil {
		return nil, nil, err
	}
	batch := &noticeBatch{actor: noticeActor(evt)}
	if before != nil {
		batch.notices = diffNoticeSnapshots(before, after, now())
	}
	if n, ok := landedNotice(evt); ok {
		batch.notices = append(batch.notices, n)
	}
	return result, batch, nil
}

// noticeActor is the session a notice is not sent to, and what its
// changed_by_session records: the emitting session, only when an agent harness
// emitted the event. A session is not told about its own change — that is
// noise, and noise trains an agent to skim the line that matters.
//
// Why the harness and not the session id alone (E-1917, E-2005, E-2006): the
// session id answers "who is this command credited to", and a person's command
// is credited to the agent beside them — through `esu`'s ENDLESS_SESSION_ID in
// their shell, a TMUX_PANE match, or the sibling-pane inference (E-1294). So a
// person editing a task, or landing it, from the pane next to the agent holding
// it would silence exactly the session that needed to hear. Actor.Harness is
// non-empty only when an agent harness ran the emitting process, and it is read
// off the envelope rather than the executing process's environment, so a
// replayed or hand-rolled event carries its own answer.
//
// A nil actor suppresses nobody. That is the direction a missed detection must
// fail in: one redundant line, never silence.
func noticeActor(evt *Event) any {
	if evt.Actor.Harness == "" || evt.Actor.SessionID == "" {
		return nil
	}
	sid, err := strconv.ParseInt(evt.Actor.SessionID, 10, 64)
	if err != nil {
		return nil
	}
	return sid
}

// takeNoticeSnapshot reads the watched state a task event can change. It
// returns nil for any other entity, which can change none of it.
func takeNoticeSnapshot(db dbQuerier, evt *Event) (*noticeSnapshot, error) {
	if evt.Entity.Type != EntityTask {
		return nil, nil
	}
	rows, err := db.Query(
		`SELECT t.id, t.status, t.phase, cl.slug, rl.slug
		   FROM tasks t
		   LEFT JOIN complexity_levels cl ON cl.id = t.complexity_id
		   LEFT JOIN risk_levels rl ON rl.id = t.risk_id
		  WHERE t.id IN (SELECT st.task_id
		                   FROM session_tasks st
		                   JOIN sessions s ON s.id = st.session_id
		                  WHERE s.state != ?)`,
		sessionstate.Ended,
	)
	if err != nil {
		return nil, fmt.Errorf("events: snapshot held tasks: %w", err)
	}
	defer rows.Close()
	snap := &noticeSnapshot{rows: map[int64]noticeRow{}}
	for rows.Next() {
		var id int64
		var r noticeRow
		if err := rows.Scan(&id, &r.status, &r.phase, &r.complexity, &r.risk); err != nil {
			return nil, fmt.Errorf("events: scan held task: %w", err)
		}
		snap.rows[id] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("events: read held tasks: %w", err)
	}

	taskID, err := strconv.ParseInt(evt.Entity.ID, 10, 64)
	if err != nil || taskID <= 0 {
		return snap, nil
	}
	if _, held := snap.rows[taskID]; !held {
		return snap, nil
	}
	snap.taskID = taskID
	snap.content, err = readNoticeContent(db, taskID)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// readNoticeContent reads one task's watched content rows. An absent name means
// the task has no such content: task_content holds no empty rows.
func readNoticeContent(db dbQuerier, taskID int64) (map[taskcontent.Name]string, error) {
	slugs := make([]any, 0, len(noticeContentNames)+1)
	slugs = append(slugs, taskID)
	for _, n := range noticeContentNames {
		slugs = append(slugs, n.Slug())
	}
	rows, err := db.Query(
		`SELECT name, content FROM task_content
		  WHERE task_id = ? AND name IN (?`+strings.Repeat(", ?", len(noticeContentNames)-1)+`)`,
		slugs...,
	)
	if err != nil {
		return nil, fmt.Errorf("events: snapshot task %d content: %w", taskID, err)
	}
	defer rows.Close()
	out := map[taskcontent.Name]string{}
	for rows.Next() {
		var name, content string
		if err := rows.Scan(&name, &content); err != nil {
			return nil, fmt.Errorf("events: scan task %d content: %w", taskID, err)
		}
		for _, n := range noticeContentNames {
			if n.Slug() == name {
				out[n] = content
			}
		}
	}
	return out, rows.Err()
}

// noticeField is one key of a notice's `changes` object.
type noticeField struct {
	name          string
	before, after any
}

// diffNoticeSnapshots compares two snapshots and returns one notice per task
// whose watched state moved, in task id order. A task present on only one side
// is skipped: it was not held when the event began, or stopped being held.
func diffNoticeSnapshots(before, after *noticeSnapshot, changedAt string) []pendingNotice {
	ids := make([]int64, 0, len(before.rows))
	for id := range before.rows {
		if _, ok := after.rows[id]; ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	var out []pendingNotice
	for _, id := range ids {
		fields := diffNoticeRow(before.rows[id], after.rows[id])
		if id == before.taskID && id == after.taskID {
			fields = append(fields, diffNoticeContent(before.content, after.content)...)
		}
		if len(fields) == 0 {
			continue
		}
		out = append(out, pendingNotice{
			taskID: id, changes: encodeNoticeChanges(fields), changedAt: changedAt,
		})
	}
	return out
}

func diffNoticeRow(b, a noticeRow) []noticeField {
	var out []noticeField
	for _, f := range []struct {
		name          string
		before, after sql.NullString
	}{
		{"status", b.status, a.status},
		{"phase", b.phase, a.phase},
		{"complexity", b.complexity, a.complexity},
		{"risk", b.risk, a.risk},
	} {
		if f.before == f.after {
			continue
		}
		out = append(out, noticeField{f.name, nullableString(f.before), nullableString(f.after)})
	}
	return out
}

// diffNoticeContent elides each side: '…' when the content is there, null when
// it is not. An edit is '…' → '…', which is why the comparison is on the text.
func diffNoticeContent(b, a map[taskcontent.Name]string) []noticeField {
	var out []noticeField
	for _, n := range noticeContentNames {
		bv, bok := b[n]
		av, aok := a[n]
		if bok == aok && bv == av {
			continue
		}
		out = append(out, noticeField{n.Slug(), elided(bok), elided(aok)})
	}
	return out
}

func elided(present bool) any {
	if present {
		return noticeElision
	}
	return nil
}

func nullableString(s sql.NullString) any {
	if s.Valid {
		return s.String
	}
	return nil
}

// encodeNoticeChanges writes the `changes` object with its keys in the order
// given, which is the order the triggers wrote them in, and each value as
// before then after. encoding/json would sort a map's keys; the renderer orders
// fields itself, but a stored notice is also read by eye in the delivery log.
func encodeNoticeChanges(fields []noticeField) string {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		// Values are strings or nil, which cannot fail to marshal.
		k, _ := json.Marshal(f.name)
		bv, _ := json.Marshal(f.before)
		av, _ := json.Marshal(f.after)
		buf.Write(k)
		buf.WriteString(`:{"before":`)
		buf.Write(bv)
		buf.WriteString(`,"after":`)
		buf.Write(av)
		buf.WriteByte('}')
	}
	buf.WriteByte('}')
	return buf.String()
}

// landedNotice is the notice a task.landed event owes (E-2005): landing changes
// no watched field, so without it a session was never told its own task had
// landed — the notice a user most wants after landing from another terminal.
// The synthetic `landed` key is not a task field; monitor.RenderNotice handles
// it as its own sentence. It is dated by the event, as task_landings.landed_at
// is, so a historical record-only landing (E-1719) is dated when it happened.
func landedNotice(evt *Event) (pendingNotice, bool) {
	if evt.Kind != KindTaskLanded {
		return pendingNotice{}, false
	}
	var p TaskLandedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return pendingNotice{}, false
	}
	taskID, err := strconv.ParseInt(evt.Entity.ID, 10, 64)
	if err != nil {
		return pendingNotice{}, false
	}
	sha := p.MergeCommitSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	where := sha
	if p.BaseBranch != "" {
		where = p.BaseBranch + "@" + sha
	}
	return pendingNotice{
		taskID:    taskID,
		changes:   encodeNoticeChanges([]noticeField{{"landed", nil, where}}),
		changedAt: kairosToISO(evt.TS),
	}, true
}

// deliver writes the batch's notices once the project write has committed.
//
// Fan-out goes to every session holding the task except the actor, and never
// to a session in state 'ended': it takes no more turns, so its notice is
// undeliverable by construction and only accumulates — 82% of the table was
// that before the filter (E-1917). `idle` and `needs_input` sessions are kept;
// they can come back, and a notice surviving until they do is the point of
// one-shot delivery. A session that ends after its notice is written is
// ReapNoticesForEndedSessions's to clean up. The JOIN is inner on purpose:
// session_tasks has no FK to sessions, and a session gone entirely has nobody
// to tell.
//
// A failure here is recorded as a fault, not returned: the task change it
// describes has already committed, and failing the command would report an
// edit that landed as one that did not. Nothing is blocked, so it waits on the
// errors channel for the user rather than spending the agent's attention.
func (b *noticeBatch) deliver(db *sql.DB) {
	if b == nil || len(b.notices) == 0 {
		return
	}
	if err := deliverSessionNotices(db, b); err != nil {
		taskIDs := make([]int64, 0, len(b.notices))
		for _, n := range b.notices {
			taskIDs = append(taskIDs, n.taskID)
		}
		faults.Record(faults.Fault{
			Code:    faults.ErrCodeSessionNoticesUnwritten,
			Source:  "events:notices",
			Summary: "a task change was recorded but its session notices were not written",
			Detail:  err.Error(),
			Fields:  map[string]any{"task_ids": taskIDs},
		})
	}
}

func deliverSessionNotices(db *sql.DB, b *noticeBatch) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("events: begin notice write: %w", err)
	}
	defer tx.Rollback()
	for _, n := range b.notices {
		if _, err := tx.Exec(
			`INSERT INTO session_notices
			     (session_id, task_id, changes, changed_at, changed_by_session)
			 SELECT st.session_id, ?, ?, ?, ?
			   FROM session_tasks st
			   JOIN sessions s ON s.id = st.session_id
			  WHERE st.task_id = ?
			    AND st.session_id IS NOT ?
			    AND s.state != ?`,
			n.taskID, n.changes, n.changedAt, b.actor,
			n.taskID, b.actor, sessionstate.Ended,
		); err != nil {
			return fmt.Errorf("events: write notices for task %d: %w", n.taskID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("events: commit notices: %w", err)
	}
	return nil
}
