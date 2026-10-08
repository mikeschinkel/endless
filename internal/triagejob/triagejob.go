// Package triagejob routes each new fault incident to a session that will fix
// it (E-2272, designed in E-2269).
//
// # What it is
//
// A registered job on the E-698 fire-once runner. On each due run it takes every
// open incident of every opted-in project (`fault_triage.enabled` in the
// project's own config) first seen after the job first saw the opt-in, and
// moves each one step along its route. Where an incident is on that route is
// its error_triage row (internal/faults/triage.go).
//
// # The routes
//
// The incident's attribution (E-2268) decides:
//
//   - Raised by a live session: wait until Endless's hook-recorded state says it
//     is idle, then message it — "we think error N is yours" — asking it to run
//     `endless errors accept N` or `endless errors decline N --reason ...`. The
//     message goes through a throwaway `claude -p` sender calling SendMessage,
//     which finds the target by its tmux pane. A message the sender reports
//     held, or cannot deliver, counts as undelivered.
//   - Raised by a session that has ended, whose transcript still exists: resume
//     it, in a new tmux window that does not take focus, with the message as its
//     first prompt. Its task is not reopened: the session sets revisit itself if
//     it accepts.
//   - Otherwise — nobody to ask, a decline, a session that went idle again
//     without answering, a message that could not be delivered — file one
//     bugfix task per fingerprint and spawn it. A recurrence of the fingerprint
//     joins the open fix task; one whose fix task has settled files a new task
//     that cleans up the old one.
//   - Raised by a fix session (a session on a bugfix task this job filed):
//     recorded on that fix task, never spawned — the loop guard.
//
// # The throttle
//
// Starting sessions — a resume or a spawn — is throttled to ONE outstanding at
// a time across the machine: none is started while a resumed session has not
// answered, or a spawned fix task has not reached unverified. The rest wait.
// Messages to live sessions are not throttled.
//
// # Skips are not failures
//
// Nothing opted in, nothing to route, and every wait end the run successfully
// with a note (`endless jobs list`). A run that hit a failure records it and
// moves on to the next incident; the run then fails, and backs off.
package triagejob

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// JobName keys this job's scheduling row. It must stay stable.
const JobName = "fault-triage"

const (
	// maxBackoff caps backoff after consecutive failing runs.
	maxBackoff = 30 * time.Minute

	// leaseTTL bounds one run. A run may deliver several messages, each a
	// `claude -p` of up to senderTimeout, so the default (2×interval, 5m
	// floor) is too short to be safe.
	leaseTTL = 30 * time.Minute
)

type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() (name string) {
	return JobName
}

// Schedule reads fault_triage.interval from the user's config on every call.
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval:   loadInterval(),
		MaxBackoff: maxBackoff,
		LeaseTTL:   leaseTTL,
	}
}

// Run moves every routable incident one step.
func (job) Run(ctx context.Context) (err error) {
	var db *sql.DB
	var projects []project
	var enabled []int64
	var routables []faults.Routable
	var r *router
	var notes []string
	var failures []error

	// A sandbox's projects rows point at real checkouts; routing from it would
	// message real sessions and spawn real ones about incidents that exist only
	// in the copy.
	if monitor.IsSandboxActive() {
		jobs.Note(ctx, "skipped: the runner is on a worktree sandbox database")
		goto end
	}
	db, err = monitor.DB()
	if err != nil {
		goto end
	}
	projects, err = optedIn(db, loadProjectEnabled)
	if err != nil {
		goto end
	}
	for _, p := range projects {
		enabled = append(enabled, p.id)
	}
	// Synced before the empty check: a project that opted out loses its
	// watermark now, so opting back in later starts afresh.
	err = faults.SyncTriageProjects(enabled)
	if err != nil {
		goto end
	}
	if len(projects) == 0 {
		jobs.Note(ctx, "no project has opted in (fault_triage.enabled in its .endless/config.json)")
		goto end
	}

	routables, err = faults.Routables(enabled)
	if err != nil {
		goto end
	}
	if len(routables) == 0 {
		jobs.Note(ctx, "nothing to route")
		goto end
	}

	r = newRouter(db, projects)
	for _, routable := range routables {
		note, rerr := r.route(ctx, routable, false)
		if note != "" {
			notes = append(notes, note)
		}
		if rerr != nil {
			failures = append(failures, fmt.Errorf("error %d: %w", routable.Incident.ID, rerr))
		}
	}
	if len(notes) == 0 {
		// Every incident is where it was: waiting on a session, an answer or
		// the throttle. Said, so `jobs list` never shows a blank run.
		notes = append(notes, fmt.Sprintf("%d error(s) in flight; none moved", len(routables)))
	}
	jobs.Note(ctx, strings.Join(notes, "; "))
	err = errors.Join(failures...)

end:
	return err
}

// RouteNow routes one incident immediately, skipping the wait for its session
// to go idle — `endless errors escalate` (E-2272). It ignores the opt-in and
// the watermark: naming the incident is the request. The throttle on starting
// sessions still applies. Returns what happened, for the command to print.
func RouteNow(ctx context.Context, errorID int64) (outcome string, err error) {
	var db *sql.DB
	var incident faults.Incident
	var t faults.Triage
	var ok, hasRow bool
	var projects []project

	if monitor.IsSandboxActive() {
		err = errSandbox
		goto end
	}
	db, err = monitor.DB()
	if err != nil {
		goto end
	}
	incident, ok, err = faults.Get(errorID)
	if err != nil {
		goto end
	}
	if !ok {
		err = faults.ErrNoIncident
		goto end
	}
	t, hasRow, err = faults.GetTriage(errorID)
	if err != nil {
		goto end
	}
	if hasRow && t.State.Settled() {
		outcome = fmt.Sprintf("error %d is already %s; nothing to escalate", errorID, describeSettled(t))
		goto end
	}
	projects, err = allProjects(db)
	if err != nil {
		goto end
	}
	outcome, err = newRouter(db, projects).route(ctx,
		faults.Routable{Incident: incident, Triage: t, HasRow: hasRow}, true)

end:
	return outcome, err
}

// errSandbox refuses an escalation on a sandbox database, for Run's reason.
var errSandbox = errors.New("routing starts and messages real sessions, so it runs only against the main database (--db main)")

// ErrSandbox reports whether err is RouteNow's sandbox refusal.
func ErrSandbox(err error) bool {
	return errors.Is(err, errSandbox)
}

func describeSettled(t faults.Triage) string {
	switch t.State {
	case faults.TriageAccepted:
		return fmt.Sprintf("accepted by ES-%d", t.AcceptedSessionID)
	case faults.TriageSpawned, faults.TriageLinked:
		return fmt.Sprintf("being fixed under E-%d", t.FixTaskID)
	case faults.TriageRecorded:
		return fmt.Sprintf("recorded on fix task E-%d", t.FixTaskID)
	}
	return string(t.State)
}

// loadInterval reads fault_triage.interval from the user's config, falling
// back to the default on anything unusable: Schedule has no error return.
func loadInterval() (d time.Duration) {
	d, _ = time.ParseDuration(config.DefaultFaultTriageInterval)
	cfg, err := config.Load("")
	if err != nil || cfg == nil {
		return d
	}
	if parsed, perr := time.ParseDuration(cfg.FaultTriage.Interval); perr == nil && parsed > 0 {
		d = parsed
	}
	return d
}

// enabledFunc reads one project's opt-in. A seam for tests.
type enabledFunc func(projectPath string) (bool, error)

// loadProjectEnabled reads fault_triage.enabled from the project's own config
// file, never inheriting it from the user's (see config.FaultTriage).
func loadProjectEnabled(projectPath string) (enabled bool, err error) {
	cfg, err := config.LoadProject(dt.DirPath(projectPath))
	if err != nil {
		return false, err
	}
	return cfg.FaultTriage.Enabled, nil
}

type project struct {
	id   int64
	name string
	path string // resolved
}

// allProjects lists every project with a resolvable directory.
func allProjects(db *sql.DB) (out []project, err error) {
	rows, err := db.Query(`SELECT id, name, path FROM live_projects ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p project
		var stored string
		if err = rows.Scan(&p.id, &p.name, &stored); err != nil {
			return nil, fmt.Errorf("listing projects: %w", err)
		}
		p.path, err = monitor.ResolvedProjectPath(stored)
		if err != nil {
			// Unresolvable: its config is unreachable, so it cannot have opted
			// in, and nothing can be filed or spawned in it.
			err = nil
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// optedIn lists the projects whose own config enables fault triage. A config
// that cannot be read fails the run, as it does auto-spawn's, so a typo that
// silently switched triage off is seen.
func optedIn(db *sql.DB, enabled enabledFunc) (out []project, err error) {
	all, err := allProjects(db)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		on, perr := enabled(p.path)
		if perr != nil {
			return nil, fmt.Errorf("reading fault_triage config for project %q: %w", p.name, perr)
		}
		if on {
			out = append(out, p)
		}
	}
	return out, nil
}
