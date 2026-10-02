package autospawnjob

// The prime job (E-1994): start a task's session ahead of need, so it reads in,
// asks what it cannot answer, and holds — `primed` — until its user resumes it.
//
// A sibling of the auto-spawn job in this package rather than a package of its
// own, because everything but the selection is the same act: start a Claude
// session nobody asked for, one per interval, in the tmux session the user is
// looking at, from the project's own checkout, through the Python CLI. The
// cadence and target are auto_spawn's (config.Prime says why); the opt-in and
// the cap are prime's own.
//
// # What is eligible
//
// A task is a candidate when ALL of these hold — see primeEligibleQuery:
//
//  1. a plan was attached to it (tasks.prime_requested, set by the executor's
//     unplanned→submitted inference) and it still has one;
//  2. status `submitted` or `ready` — nobody has started it;
//  3. phase `now` or `urgent`;
//  4. its project opted in (`prime.enabled`) and is under `prime.cap`;
//  5. nothing non-terminal blocks it;
//  6. no open question — a task waiting on a person is waiting already;
//  7. no session has ever bound to it. This is also what retires the request:
//     the primed session binds from its worktree, so the task drops out here
//     without anyone clearing the flag.
//
// # The cap
//
// Outstanding primed sessions per project: live sessions bound to a task still
// `unplanned`, `submitted` or `ready`. A session counts from the moment it binds
// — reading in, then primed — until the user resumes it and `task claim` moves
// the task to `underway`. Each is a live process and a tmux window.

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// PrimeJobName keys the prime job's scheduling row. It must stay stable.
const PrimeJobName = "prime"

// primeJob implements jobs.Job. Stateless, like job.
type primeJob struct{}

func init() {
	jobs.Register(primeJob{})
}

// Name returns the stable scheduling key.
func (primeJob) Name() (name string) {
	return PrimeJobName
}

// Schedule shares auto-spawn's cadence: one user-level rate for every session
// started unasked.
func (primeJob) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval:   loadSettings().interval,
		MaxBackoff: maxBackoff,
	}
}

// Run primes at most one eligible task, or records why it did not.
func (primeJob) Run(ctx context.Context) (err error) {
	var db *sql.DB
	var s settings
	var pick candidate
	var skip string
	var target string

	// A sandbox database's project rows point at real checkouts; see job.Run.
	if monitor.IsSandboxActive() {
		jobs.Note(ctx, "skipped: the runner is on a worktree sandbox database")
		goto end
	}

	db, err = monitor.DB()
	if err != nil {
		goto end
	}
	s = loadSettings()

	pick, skip, err = selectPrimeCandidate(db, loadPrimePolicy)
	if err != nil || skip != "" {
		jobs.Note(ctx, skip)
		goto end
	}

	target, skip, err = resolveTarget(s.target)
	if err != nil || skip != "" {
		jobs.Note(ctx, skip)
		goto end
	}

	// A typo is a configuration error, failed rather than defaulted — job.Run's
	// reason. The placement is auto_spawn's: one setting for every window
	// opened unasked.
	_, err = spawnlaunchcmd.ParsePlacement(s.placement)
	if err != nil {
		err = fmt.Errorf("auto_spawn.%w", err)
		goto end
	}

	err = runEndless(ctx, pick, primeArgs(pick.taskID, target, s.placement))
	if err != nil {
		goto end
	}
	jobs.Note(ctx, "primed E-"+strconv.FormatInt(pick.taskID, 10)+describeTarget(s.target, target))

end:
	return err
}

// loadPrimePolicy reads prime.enabled and prime.cap from the project's own
// config file, never inheriting either from the user's (see config.Prime).
func loadPrimePolicy(projectPath string) (p projectPolicy, err error) {
	var cfg *config.EndlessConfig

	cfg, err = config.LoadProject(dt.DirPath(projectPath))
	if err != nil {
		goto end
	}
	p.enabled = cfg.Prime.Enabled
	p.cap = cfg.Prime.Cap
	if p.cap <= 0 {
		p.cap = config.DefaultPrimeCap
	}

end:
	return p, err
}

// selectPrimeCandidate returns the one task this run should prime, or a reason
// it should prime nothing — selectCandidate's shape over prime's policy.
func selectPrimeCandidate(db *sql.DB, policy policyFunc) (pick candidate, skip string, err error) {
	var projects []projectRow
	var open []int64
	var paths = map[int64]string{}
	var optedIn, atCap int
	var p projectPolicy
	var outstanding int
	var row *sql.Row

	projects, err = registeredProjects(db)
	if err != nil {
		goto end
	}

	for _, pr := range projects {
		p, err = policy(pr.path)
		if err != nil {
			err = fmt.Errorf("reading prime config for project %q: %w", pr.name, err)
			goto end
		}
		if !p.enabled {
			continue
		}
		optedIn++
		outstanding, err = outstandingPrimed(db, pr.id)
		if err != nil {
			goto end
		}
		if outstanding >= p.cap {
			atCap++
			continue
		}
		open = append(open, pr.id)
		paths[pr.id] = pr.path
	}

	switch {
	case optedIn == 0:
		skip = "no project has opted in (prime.enabled in its .endless/config.json)"
		goto end
	case len(open) == 0:
		skip = "every opted-in project is at its prime cap (" + strconv.Itoa(atCap) + " of " + strconv.Itoa(optedIn) + ")"
		goto end
	}

	row = db.QueryRow(primeEligibleQuery(len(open)), int64sToArgs(open)...)
	err = row.Scan(&pick.taskID, new(int64))
	if err == sql.ErrNoRows {
		err = nil
		skip = "nothing to prime"
		goto end
	}
	if err != nil {
		err = fmt.Errorf("selecting a prime candidate: %w", err)
		goto end
	}
	pick.projectPath, err = projectPathOf(db, pick.taskID, paths)

end:
	return pick, skip, err
}

// primeEligibleQuery is conditions 1–3 and 5–7 of the doc above, over the
// projects that passed 4. n is how many project ids it filters on.
func primeEligibleQuery(n int) string {
	return `
		SELECT t.id, t.project_id
		  FROM live_tasks t
		 WHERE t.project_id IN (` + placeholders(n) + `)
		   AND t.prime_requested = 1
		   AND t.status IN ('` + string(taskstatus.Submitted) + `', '` + string(taskstatus.Ready) + `')
		   AND t.phase IN ('now', 'urgent')
		   AND NOT EXISTS (
		       SELECT 1
		         FROM task_deps d
		         JOIN live_tasks b ON b.id = d.source_id
		        WHERE d.source_type = 'task'
		          AND d.target_type = 'task'
		          AND d.target_id = t.id
		          AND d.dep_type = 'blocks'
		          AND b.status NOT IN (` + taskstatus.SQLList(taskstatus.Terminal) + `))
		   AND EXISTS (
		       SELECT 1
		         FROM task_content c
		        WHERE c.task_id = t.id
		          AND c.name = 'plan'
		          AND trim(c.content, ' ' || char(9) || char(10) || char(13)) != '')
		   AND NOT EXISTS (
		       SELECT 1 FROM task_questions q
		        WHERE q.task_id = t.id AND q.status = 'open')
		   AND NOT EXISTS (
		       SELECT 1 FROM sessions s WHERE s.task_id = t.id)
		 ORDER BY CASE t.phase WHEN 'urgent' THEN 0 ELSE 1 END,
		          t.updated_at, t.id
		 LIMIT 1`
}

// outstandingPrimed counts the project's live sessions bound to a task nobody
// has started — reading in or primed. The resume's `task claim` moves the task
// to `underway`, which is where a session stops counting.
func outstandingPrimed(db *sql.DB, projectID int64) (n int, err error) {
	err = db.QueryRow(`
		SELECT count(DISTINCT t.id)
		  FROM live_tasks t
		  JOIN sessions s ON s.task_id = t.id
		 WHERE t.project_id = ?
		   AND t.status IN ('`+string(taskstatus.Unplanned)+`', '`+string(taskstatus.Submitted)+`', '`+string(taskstatus.Ready)+`')
		   AND s.state IN (`+sessionstate.SQLList(sessionstate.Live)+`)`, projectID,
	).Scan(&n)
	if err != nil {
		err = fmt.Errorf("counting outstanding primed sessions: %w", err)
	}
	return n, err
}

// primeArgs is the `endless` argv for one prime, spawnArgs' counterpart.
func primeArgs(taskID int64, target, placement string) []string {
	args := []string{"--no-session", "task", "prime", "E-" + strconv.FormatInt(taskID, 10), "--auto",
		"--placement", placement}
	if target != "" {
		args = append(args, "--target-session", target)
	}
	return args
}

func int64sToArgs(ids []int64) []any {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return args
}
