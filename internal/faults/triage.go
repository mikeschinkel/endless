package faults

// Triage state (E-2272): where the fault-triage job routed an incident, and
// what came of it — one error_triage row per incident.
//
// It lives here, beside the incident it hangs off, for the reason the rest of
// this package does: the errors tables are this package's, and a second writer
// to them elsewhere would be a second source of truth. Like everything else
// here it is machine-local and written directly, never through the ledger: an
// incident id means nothing on another machine or after a rebuild.
//
// The job decides; this file only stores. Accept and Decline are the two writes
// a session makes (`endless errors accept|decline`), Escalate the one a person
// makes; SaveTriage is the job's.

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/mikeschinkel/go-doterr"
)

// TriageState is the job's step for one incident. See the error_triage
// comment in internal/schema/schema.sql for what each means.
type TriageState string

const (
	TriageWaiting   TriageState = "waiting"
	TriageDelivered TriageState = "delivered"
	TriageResumed   TriageState = "resumed"
	TriageQueued    TriageState = "queued"
	TriageFiled     TriageState = "filed"
	TriageSpawned   TriageState = "spawned"
	TriageLinked    TriageState = "linked"
	TriageRecorded  TriageState = "recorded"
	TriageAccepted  TriageState = "accepted"
)

// Settled reports whether the job has nothing left to do for an incident in
// this state: a session took it, or it rides on a fix task that has (or had) a
// session of its own.
func (s TriageState) Settled() (settled bool) {
	switch s {
	case TriageAccepted, TriageSpawned, TriageLinked, TriageRecorded:
		settled = true
	}
	return settled
}

// Triage is one error_triage row. Zero ids and empty times mean "not set".
type Triage struct {
	ErrorID           int64
	State             TriageState
	SessionID         int64 // the session asked: messaged or resumed
	DeliveredAt       string
	FixTaskID         int64
	AcceptedSessionID int64
	AcceptedAt        string
	DeclinedSessionID int64
	DeclinedAt        string
	DeclineReason     string
	EscalatedAt       string
	Note              string
	UpdatedAt         string
}

// ErrNoIncident is returned by the answer writes for an id no incident has.
var ErrNoIncident = errors.New("no such error")

// ErrNotOpen is returned by Escalate for an incident already cleared.
var ErrNotOpen = errors.New("error is cleared")

const triageColumns = `t.error_id, t.state, t.session_id, t.delivered_at, t.fix_task_id,
	t.accepted_session_id, t.accepted_at, t.declined_session_id, t.declined_at,
	t.decline_reason, t.escalated_at, t.note, t.updated_at`

// rowScanner is the one method *sql.Row and *sql.Rows share.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTriage(r rowScanner, extra ...any) (t Triage, err error) {
	var state string
	var session, fix, accepted, declined sql.NullInt64
	var delivered, acceptedAt, declinedAt, reason, escalated, note sql.NullString

	dest := []any{&t.ErrorID, &state, &session, &delivered, &fix,
		&accepted, &acceptedAt, &declined, &declinedAt,
		&reason, &escalated, &note, &t.UpdatedAt}
	err = r.Scan(append(dest, extra...)...)
	if err != nil {
		return t, err
	}
	t.State = TriageState(state)
	t.SessionID = session.Int64
	t.DeliveredAt = delivered.String
	t.FixTaskID = fix.Int64
	t.AcceptedSessionID = accepted.Int64
	t.AcceptedAt = acceptedAt.String
	t.DeclinedSessionID = declined.Int64
	t.DeclinedAt = declinedAt.String
	t.DeclineReason = reason.String
	t.EscalatedAt = escalated.String
	t.Note = note.String
	return t, nil
}

// GetTriage returns an incident's triage row; ok is false when it has none.
func GetTriage(errorID int64) (t Triage, ok bool, err error) {
	var db *sql.DB

	db, err = database()
	if err != nil {
		goto end
	}
	t, err = scanTriage(db.QueryRow(`SELECT `+triageColumns+` FROM error_triage t WHERE t.error_id = ?`, errorID))
	if err == sql.ErrNoRows {
		err = nil
		goto end
	}
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	ok = true

end:
	return t, ok, err
}

// SaveTriage writes the job's view of one incident: every column but the
// answers, which belong to the sessions (Accept, Decline) and to a person
// (Escalate) and are never overwritten here.
func SaveTriage(t Triage) (err error) {
	var db *sql.DB

	db, err = database()
	if err != nil {
		goto end
	}
	_, err = db.Exec(`
		INSERT INTO error_triage (error_id, state, session_id, delivered_at, fix_task_id, note, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		ON CONFLICT (error_id) DO UPDATE SET
		    state        = excluded.state,
		    session_id   = excluded.session_id,
		    delivered_at = excluded.delivered_at,
		    fix_task_id  = excluded.fix_task_id,
		    note         = excluded.note,
		    updated_at   = excluded.updated_at`,
		t.ErrorID, string(t.State), nullableID(t.SessionID), nullableText(t.DeliveredAt),
		nullableID(t.FixTaskID), nullableText(t.Note))
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
	}

end:
	return err
}

// Accept records that sessionID took the incident: it is that session's to fix.
// A later decline by the same or another session supersedes it.
func Accept(errorID, sessionID int64) (err error) {
	return answer(errorID, `
		INSERT INTO error_triage (error_id, state, accepted_session_id, accepted_at)
		VALUES (?, 'accepted', ?, strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		ON CONFLICT (error_id) DO UPDATE SET
		    state               = 'accepted',
		    accepted_session_id = excluded.accepted_session_id,
		    accepted_at         = excluded.accepted_at,
		    updated_at          = strftime('%Y-%m-%dT%H:%M:%S', 'now')`,
		errorID, sessionID)
}

// Decline records that sessionID refused the incident, and why. The incident
// goes to queued, so the job files it as a bugfix task on its next run; an
// earlier acceptance is withdrawn.
func Decline(errorID, sessionID int64, reason string) (err error) {
	return answer(errorID, `
		INSERT INTO error_triage (error_id, state, declined_session_id, declined_at, decline_reason)
		VALUES (?, 'queued', ?, strftime('%Y-%m-%dT%H:%M:%S', 'now'), ?)
		ON CONFLICT (error_id) DO UPDATE SET
		    state               = 'queued',
		    declined_session_id = excluded.declined_session_id,
		    declined_at         = excluded.declined_at,
		    decline_reason      = excluded.decline_reason,
		    accepted_session_id = NULL,
		    accepted_at         = NULL,
		    updated_at          = strftime('%Y-%m-%dT%H:%M:%S', 'now')`,
		errorID, sessionID, strings.TrimSpace(reason))
}

// Escalate stamps the incident for immediate delivery: the job skips waiting
// for its session to go idle. Refused on a cleared incident, which nothing
// routes.
func Escalate(errorID int64) (err error) {
	var incident Incident
	var ok bool

	incident, ok, err = Get(errorID)
	if err != nil {
		goto end
	}
	if !ok {
		err = doterr.NewErr(ErrFaults, ErrNoIncident)
		goto end
	}
	if incident.ClearedAt != "" {
		err = doterr.NewErr(ErrFaults, ErrNotOpen)
		goto end
	}
	err = answer(errorID, `
		INSERT INTO error_triage (error_id, state, escalated_at)
		VALUES (?, 'waiting', strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		ON CONFLICT (error_id) DO UPDATE SET
		    escalated_at = excluded.escalated_at,
		    updated_at   = strftime('%Y-%m-%dT%H:%M:%S', 'now')`,
		errorID)

end:
	return err
}

// answer runs one answer upsert, refusing an id no incident has: the
// foreign key would reject it anyway, but as a constraint error nobody can act
// on.
func answer(errorID int64, stmt string, args ...any) (err error) {
	var db *sql.DB
	var exists int

	db, err = database()
	if err != nil {
		goto end
	}
	err = db.QueryRow(`SELECT count(*) FROM errors WHERE id = ?`, errorID).Scan(&exists)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	if exists == 0 {
		err = doterr.NewErr(ErrFaults, ErrNoIncident)
		goto end
	}
	_, err = db.Exec(stmt, args...)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
	}

end:
	return err
}

// Routable is one open incident the job may act on, with its triage row when
// it has one.
type Routable struct {
	Incident Incident
	Triage   Triage
	HasRow   bool
}

// Routables returns the open incidents of the given projects first seen at or
// after each project's opt-in watermark, whose triage is not settled — oldest
// first, so a burst is worked in the order it happened.
func Routables(projectIDs []int64) (out []Routable, err error) {
	var db *sql.DB
	var incidents []Incident
	var byID = map[int64]int{}
	var rows *sql.Rows

	if len(projectIDs) == 0 {
		goto end
	}
	db, err = database()
	if err != nil {
		goto end
	}

	incidents, err = query(db, `
		 JOIN fault_triage_projects w ON w.project_id = e.project_id
		WHERE e.cleared_at IS NULL
		  AND e.first_seen_at >= w.enabled_at
		  AND e.project_id IN (`+placeholders(len(projectIDs))+`)
		  AND NOT EXISTS (SELECT 1 FROM error_triage t
		                   WHERE t.error_id = e.id
		                     AND t.state IN ('accepted', 'spawned', 'linked', 'recorded'))`,
		0, idArgs(projectIDs)...)
	if err != nil {
		goto end
	}
	// query orders newest first; routing works oldest first.
	for i := len(incidents) - 1; i >= 0; i-- {
		byID[incidents[i].ID] = len(out)
		out = append(out, Routable{Incident: incidents[i]})
	}
	if len(out) == 0 {
		goto end
	}

	rows, err = db.Query(`SELECT `+triageColumns+` FROM error_triage t WHERE t.error_id IN (`+
		placeholders(len(out))+`)`, routableArgs(out)...)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	defer rows.Close()
	for rows.Next() {
		var t Triage
		t, err = scanTriage(rows)
		if err != nil {
			err = doterr.NewErr(ErrFaults, ErrScanning, err)
			goto end
		}
		i := byID[t.ErrorID]
		out[i].Triage, out[i].HasRow = t, true
	}
	err = rows.Err()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
	}

end:
	if err != nil {
		out = nil
	}
	return out, err
}

// SyncTriageProjects makes the opt-in watermarks match enabled: a project newly
// opted in gets a watermark of now, and a project no longer opted in loses its
// own, so opting back in later starts afresh rather than triaging everything
// raised while it was off.
func SyncTriageProjects(enabled []int64) (err error) {
	var db *sql.DB

	db, err = database()
	if err != nil {
		goto end
	}
	for _, id := range enabled {
		_, err = db.Exec(`INSERT INTO fault_triage_projects (project_id) VALUES (?)
		                  ON CONFLICT (project_id) DO NOTHING`, id)
		if err != nil {
			err = doterr.NewErr(ErrFaults, ErrQuery, err)
			goto end
		}
	}
	if len(enabled) == 0 {
		_, err = db.Exec(`DELETE FROM fault_triage_projects`)
	} else {
		_, err = db.Exec(`DELETE FROM fault_triage_projects WHERE project_id NOT IN (`+
			placeholders(len(enabled))+`)`, idArgs(enabled)...)
	}
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
	}

end:
	return err
}

// FixTaskFor returns the most recent bugfix task any incident of this
// fingerprint was filed under — the "one task per fingerprint" lookup — or 0.
// A cleared incident's recurrence is a new row with the same key, which is
// exactly the case this exists for.
func FixTaskFor(incident Incident) (taskID int64, err error) {
	var db *sql.DB
	var id sql.NullInt64

	db, err = database()
	if err != nil {
		goto end
	}
	err = db.QueryRow(`
		SELECT t.fix_task_id
		  FROM error_triage t
		  JOIN errors e ON e.id = t.error_id
		 WHERE COALESCE(e.project_id, 0) = ?
		   AND e.source = ? AND e.code = ? AND e.fingerprint = ?
		   AND t.fix_task_id IS NOT NULL
		   AND t.state IN ('filed', 'spawned', 'linked', 'accepted')
		 ORDER BY t.error_id DESC
		 LIMIT 1`,
		incident.ProjectID, incident.Source, incident.Code, incident.Fingerprint,
	).Scan(&id)
	if err == sql.ErrNoRows {
		err = nil
		goto end
	}
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	taskID = id.Int64

end:
	return taskID, err
}

// IsFixTask reports whether taskID is a bugfix task the job filed — which is
// what makes a session on it a fix session, whose own faults are recorded on it
// rather than spawned (the loop guard).
func IsFixTask(taskID int64) (fix bool, err error) {
	var db *sql.DB
	var n int

	if taskID == 0 {
		goto end
	}
	db, err = database()
	if err != nil {
		goto end
	}
	err = db.QueryRow(`SELECT count(*) FROM error_triage
	                    WHERE fix_task_id = ? AND state IN ('filed', 'spawned', 'linked', 'accepted')`,
		taskID).Scan(&n)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	fix = n > 0

end:
	return fix, err
}

// FixTaskIncidents returns every incident filed under, linked to or recorded on
// taskID, oldest first, with how each got there — what the fix task's context
// lists.
func FixTaskIncidents(taskID int64) (out []Routable, err error) {
	var db *sql.DB
	var rows *sql.Rows

	db, err = database()
	if err != nil {
		goto end
	}
	rows, err = db.Query(`SELECT `+triageColumns+` FROM error_triage t WHERE t.fix_task_id = ? ORDER BY t.error_id`, taskID)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	defer rows.Close()
	for rows.Next() {
		var t Triage
		t, err = scanTriage(rows)
		if err != nil {
			err = doterr.NewErr(ErrFaults, ErrScanning, err)
			goto end
		}
		out = append(out, Routable{Triage: t, HasRow: true})
	}
	err = rows.Err()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	for i := range out {
		var ok bool
		out[i].Incident, ok, err = Get(out[i].Triage.ErrorID)
		if err != nil {
			goto end
		}
		if !ok {
			out[i].Incident.ID = out[i].Triage.ErrorID
		}
	}

end:
	return out, err
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func idArgs(ids []int64) (args []any) {
	args = make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

func routableArgs(rs []Routable) (args []any) {
	args = make([]any, len(rs))
	for i, r := range rs {
		args[i] = r.Incident.ID
	}
	return args
}

func nullableText(s string) (value any) {
	if s != "" {
		value = s
	}
	return value
}

// ResolveRaiser is the bound RaiserResolver applied to explicit, exported for
// the commands that must know which session is running them — `errors accept`
// and `errors decline` record that session — without re-deriving the policy.
func ResolveRaiser(explicit Raiser) (raiser Raiser) {
	return resolveRaiser(explicit)
}
