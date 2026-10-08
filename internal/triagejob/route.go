package triagejob

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
)

// sessionInfo is what routing needs to know about the session an incident was
// raised by, or was sent to.
type sessionInfo struct {
	found        bool
	live         bool   // not ended, and not observably gone
	unobservable bool   // its tmux server could not be reached: no opinion either way
	idle         bool   // the hook-recorded state is idle
	lastActivity string // '%Y-%m-%dT%H:%M:%S' UTC, as sessions.last_activity holds it
	pane         string // tmux pane id on THIS server; "" when it has none here
	uuid         string // Claude's session id
	taskID       int64
}

// delivery is what the sender reported.
type delivery string

const (
	delivered delivery = "DELIVERED"
	held      delivery = "HELD"
	notFound  delivery = "NOT_FOUND"
	failed    delivery = "FAILED"
)

// effects is everything routing does to the world or reads from outside the
// fault store. The real one is in effects.go; tests substitute their own.
type effects interface {
	session(id int64) (sessionInfo, error)
	transcriptExists(uuid string) bool
	deliver(ctx context.Context, pane, message string) (delivery, error)
	// startAllowed reports whether a session may be started now — the
	// throttle and the tmux target — or why not.
	startAllowed() (ok bool, why string, err error)
	resume(ctx context.Context, sessionID int64, projectPath, message string) error
	fileTask(ctx context.Context, projectPath string, spec taskSpec) (taskID int64, err error)
	updateContext(ctx context.Context, projectPath string, taskID int64, context string) error
	spawn(ctx context.Context, projectPath string, taskID int64) (where string, err error)
	// fixTask reports a fix task's state: whether it has settled, and whether
	// any session ever bound to it.
	fixTask(taskID int64) (settled, claimed bool, err error)
	now() time.Time
}

// router moves incidents along their routes.
type router struct {
	fx    effects
	paths map[int64]string // project id → resolved directory
}

func newRouter(db *sql.DB, projects []project) *router {
	r := &router{fx: realEffects{db: db}, paths: map[int64]string{}}
	for _, p := range projects {
		r.paths[p.id] = p.path
	}
	return r
}

// stamp renders t the way the fault store and sessions.last_activity do, so
// the two compare as strings.
func stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05")
}

// route moves one incident as far along its route as it can go this run, and
// saves where it got to. escalated is `errors escalate`: deliver without
// waiting for the session to go idle. Returns a note for the run, "" when
// nothing changed and nothing is worth saying.
func (r *router) route(ctx context.Context, in faults.Routable, escalated bool) (note string, err error) {
	t := in.Triage
	t.ErrorID = in.Incident.ID
	before := t
	escalated = escalated || (t.EscalatedAt != "" && t.EscalatedAt > t.DeliveredAt)

	switch t.State {
	case "", faults.TriageWaiting:
		err = r.firstContact(ctx, in.Incident, &t, escalated)
	case faults.TriageDelivered, faults.TriageResumed:
		err = r.awaitAnswer(ctx, in.Incident, &t, escalated)
	}
	if err == nil && t.State == faults.TriageQueued {
		err = r.file(ctx, in.Incident, &t)
	}
	if err == nil && t.State == faults.TriageFiled {
		err = r.spawnFix(ctx, in.Incident, &t)
	}

	if t != before || !in.HasRow {
		if serr := faults.SaveTriage(t); serr != nil && err == nil {
			err = serr
		}
	}
	if t.Note != before.Note || t.State != before.State {
		note = fmt.Sprintf("error %d: %s", t.ErrorID, t.Note)
	}
	return note, err
}

// firstContact routes an incident nobody has been asked about yet, or whose
// session was busy (or whose resume was throttled) last time.
func (r *router) firstContact(ctx context.Context, in faults.Incident, t *faults.Triage, escalated bool) (err error) {
	var s sessionInfo
	var fix bool

	if in.SessionID == 0 {
		// No session to ask. A task with no session — a job's fault about one
		// task, or a person's command in a worktree — may still be a fix task.
		fix, err = faults.IsFixTask(in.TaskID)
		if err != nil {
			return err
		}
		if fix {
			return r.record(ctx, in, t, in.TaskID)
		}
		queue(t, "raised by no session")
		return nil
	}

	s, err = r.fx.session(in.SessionID)
	if err != nil {
		return err
	}
	if !s.found {
		queue(t, fmt.Sprintf("ES-%d no longer exists", in.SessionID))
		return nil
	}
	fix, err = faults.IsFixTask(s.taskID)
	if err != nil {
		return err
	}
	if fix {
		return r.record(ctx, in, t, s.taskID)
	}

	switch {
	case s.live && s.pane != "":
		if !s.idle && !escalated {
			wait(t, fmt.Sprintf("waiting for ES-%d to go idle", in.SessionID))
			return nil
		}
		return r.message(ctx, in, t, in.SessionID, s.pane)
	case s.unobservable:
		wait(t, fmt.Sprintf("ES-%d's tmux server cannot be reached; waiting to see it", in.SessionID))
		return nil
	case s.live:
		queue(t, fmt.Sprintf("ES-%d is live in no tmux pane here, so it cannot be messaged", in.SessionID))
		return nil
	case s.uuid != "" && r.fx.transcriptExists(s.uuid):
		return r.resumeEnded(ctx, in, t, in.SessionID)
	default:
		queue(t, fmt.Sprintf("ES-%d has ended and its transcript is gone", in.SessionID))
		return nil
	}
}

// message delivers the triage message to a live session's pane.
func (r *router) message(ctx context.Context, in faults.Incident, t *faults.Triage, sessionID int64, pane string) (err error) {
	var d delivery

	// Stamped BEFORE sending: a session that takes its turn on the message
	// and stops within the same second must still read as having moved on
	// after it.
	sentAt := stamp(r.fx.now())
	d, err = r.fx.deliver(ctx, pane, triageMessage(in))
	if err != nil {
		// The sender itself failed to run — a broken tool, not an answer
		// about this session. Fall back rather than retry forever: the error
		// still fails the run, so it is seen.
		queue(t, fmt.Sprintf("could not message ES-%d", sessionID))
		return err
	}
	if d != delivered {
		queue(t, fmt.Sprintf("message to ES-%d was not delivered (%s)", sessionID, d))
		return nil
	}
	t.State = faults.TriageDelivered
	t.SessionID = sessionID
	t.DeliveredAt = sentAt
	t.Note = fmt.Sprintf("asked ES-%d", sessionID)
	return nil
}

// resumeEnded resumes an ended session with the triage message — a session
// start, so throttled.
func (r *router) resumeEnded(ctx context.Context, in faults.Incident, t *faults.Triage, sessionID int64) (err error) {
	ok, why, err := r.fx.startAllowed()
	if err != nil {
		return err
	}
	if !ok {
		wait(t, fmt.Sprintf("resuming ended ES-%d waits: %s", sessionID, why))
		return nil
	}
	path := r.paths[in.ProjectID]
	err = r.fx.resume(ctx, sessionID, path, triageMessage(in))
	if err != nil {
		queue(t, fmt.Sprintf("could not resume ES-%d", sessionID))
		return err
	}
	t.State = faults.TriageResumed
	t.SessionID = sessionID
	t.DeliveredAt = stamp(r.fx.now())
	t.Note = fmt.Sprintf("resumed ended ES-%d to ask it", sessionID)
	return nil
}

// awaitAnswer watches a session that was asked. An answer arrives as a state
// change written by `errors accept|decline`, so an incident still here has had
// none: the job falls back once the session has visibly moved on without one.
func (r *router) awaitAnswer(ctx context.Context, in faults.Incident, t *faults.Triage, escalated bool) (err error) {
	var s sessionInfo

	s, err = r.fx.session(t.SessionID)
	if err != nil {
		return err
	}
	settling := r.fx.now().Sub(parseStamp(t.DeliveredAt)) < answerGrace
	switch {
	case settling && t.State == faults.TriageResumed && !s.live:
		// A resumed session reads as ended until its first hook revives the
		// row; that is startup, not an exit.
	case !s.found || (!s.live && !s.unobservable):
		queue(t, fmt.Sprintf("ES-%d ended without answering", t.SessionID))
	case escalated && s.live && s.pane != "":
		// Escalated after the first message: say it again, now.
		return r.message(ctx, in, t, t.SessionID, s.pane)
	case s.idle && s.lastActivity > t.DeliveredAt && !settling:
		// Idle, with hook activity after the message went out: it took its
		// turn on the message and stopped without accepting or declining.
		queue(t, fmt.Sprintf("ES-%d went idle without answering", t.SessionID))
	}
	return nil
}

// record attaches an incident raised by a fix session to that fix task: never
// spawned, so a fix that raises a fault cannot spawn a fix for itself.
func (r *router) record(ctx context.Context, in faults.Incident, t *faults.Triage, fixTaskID int64) (err error) {
	t.State = faults.TriageRecorded
	t.FixTaskID = fixTaskID
	t.Note = fmt.Sprintf("raised by fix task E-%d's own work; recorded on it", fixTaskID)
	// Saved before the context is rebuilt, so the rebuild lists this incident.
	if err = faults.SaveTriage(*t); err != nil {
		return err
	}
	return r.refreshContext(ctx, in.ProjectID, fixTaskID)
}

// file puts a queued incident under a bugfix task: the open fix task of its
// fingerprint when there is one, else a new one.
func (r *router) file(ctx context.Context, in faults.Incident, t *faults.Triage) (err error) {
	var existing, taskID int64
	var settled, claimed bool
	var cleansUp []int64

	path := r.paths[in.ProjectID]
	if path == "" {
		// Unreachable for an opted-in project; reachable for an escalated
		// incident in a project with no resolvable directory, or none at all.
		t.Note = "no project directory to file a bugfix task in"
		return fmt.Errorf("error %d: %s", in.ID, t.Note)
	}

	existing, err = faults.FixTaskFor(in)
	if err != nil {
		return err
	}
	if existing != 0 {
		settled, claimed, err = r.fx.fixTask(existing)
		if err != nil {
			return err
		}
		if !settled {
			t.FixTaskID = existing
			t.State = faults.TriageLinked
			t.Note = fmt.Sprintf("joined open fix task E-%d", existing)
			if !claimed {
				// Filed but never started: this incident can start it.
				t.State = faults.TriageFiled
			}
			if err = faults.SaveTriage(*t); err != nil {
				return err
			}
			return r.refreshContext(ctx, in.ProjectID, existing)
		}
		cleansUp = append(cleansUp, existing)
	}
	if in.TaskID != 0 && in.TaskID != existing {
		cleansUp = append(cleansUp, in.TaskID)
	}

	taskID, err = r.fx.fileTask(ctx, path, taskSpec{
		title:       fixTitle(in),
		description: fixDescription(in),
		context:     fixContext([]faults.Routable{{Incident: in, Triage: *t, HasRow: true}}, r.details),
		plan:        fixPlan(),
		cleansUp:    cleansUp,
	})
	if err != nil {
		t.Note = "could not file a bugfix task"
		return err
	}
	t.FixTaskID = taskID
	t.State = faults.TriageFiled
	t.Note = fmt.Sprintf("filed E-%d", taskID)
	if existing != 0 {
		t.Note += fmt.Sprintf(" (E-%d, its earlier fix, has settled)", existing)
	}
	// Saved at once: a run that dies between filing and saving would file the
	// same incident again next time.
	return faults.SaveTriage(*t)
}

// spawnFix starts a session on a filed fix task — throttled.
func (r *router) spawnFix(ctx context.Context, in faults.Incident, t *faults.Triage) (err error) {
	// Several incidents can be waiting on one fix task; the first to start
	// it leaves the rest riding along.
	_, claimed, err := r.fx.fixTask(t.FixTaskID)
	if err != nil {
		return err
	}
	if claimed {
		t.State = faults.TriageLinked
		t.Note = fmt.Sprintf("joined fix task E-%d, already started", t.FixTaskID)
		return nil
	}
	ok, why, err := r.fx.startAllowed()
	if err != nil {
		return err
	}
	if !ok {
		t.Note = fmt.Sprintf("E-%d waits to be spawned: %s", t.FixTaskID, why)
		return nil
	}
	where, err := r.fx.spawn(ctx, r.paths[in.ProjectID], t.FixTaskID)
	if err != nil {
		t.Note = fmt.Sprintf("could not spawn E-%d", t.FixTaskID)
		return err
	}
	t.State = faults.TriageSpawned
	t.Note = fmt.Sprintf("spawned E-%d%s", t.FixTaskID, where)
	return nil
}

// refreshContext rewrites a fix task's context to list every incident on it.
func (r *router) refreshContext(ctx context.Context, projectID, taskID int64) (err error) {
	incidents, err := faults.FixTaskIncidents(taskID)
	if err != nil {
		return err
	}
	path := r.paths[projectID]
	if path == "" {
		return fmt.Errorf("E-%d: no project directory to update it from", taskID)
	}
	return r.fx.updateContext(ctx, path, taskID, fixContext(incidents, r.details))
}

// details reads an incident's logged occurrences for a fix task's context. A
// read failure leaves them out: the context still names the incident, and
// `errors show --detail` is one command away.
func (r *router) details(errorID int64) []faults.Detail {
	d, err := faults.Details(errorID)
	if err != nil {
		return nil
	}
	return d
}

// answerGrace is how long after asking a session the job reads nothing into
// its state. A resumed session starts as an ended row until its first hook; a
// session on no task stays `idle` while it works (only a session bound to a
// task is woken to working), so an idle reading this soon says nothing about
// whether it is answering.
const answerGrace = 5 * time.Minute

// parseStamp reads a stamp written by stamp; the zero time on anything else,
// which reads as long ago.
func parseStamp(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

func queue(t *faults.Triage, why string) {
	t.State = faults.TriageQueued
	t.Note = why
}

func wait(t *faults.Triage, why string) {
	t.State = faults.TriageWaiting
	t.Note = why
}
