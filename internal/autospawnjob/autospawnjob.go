// Package autospawnjob starts a Claude session, unasked, on a task that is safe
// to work without an explicit spawn (E-1814, under E-1812; ED-1538 as amended).
//
// # What it is
//
// A registered job on the E-698 fire-once runner, not a process of its own.
// Whatever fires the runner — a session monitor, a project monitor, `endless
// jobs run` — fires this when it is due. Each due run spawns AT MOST ONE task,
// so the job's cadence is the rate throttle E-1815 asked for: it keeps a batch
// of Claude sessions from starting at once (CPU, tmux stability) and leaves a
// window to react between spawns. The cadence is the user's
// `auto_spawn.interval`, read by Schedule, so it is visible and adjustable in
// `endless jobs list` with no machinery of its own.
//
// # What is eligible
//
// A task is a candidate when ALL of these hold — see selectCandidate:
//
//  1. status `ready` (human-approved, ED-1538);
//  2. rated complexity `low` and risk `low`;
//  3. phase `now` or `urgent`;
//  4. its type is auto-spawnable (task_types.auto_spawnable);
//  5. its project opted in (`auto_spawn.enabled` in the project's own config);
//  6. nothing non-terminal blocks it or precedes it (E-2270: the job never
//     passes --out-of-order, so an unfinished predecessor parks it too);
//  7. it has a plan and no open question;
//  8. no session ever claimed it.
//
// 6–8 mirror `task spawn`'s own refusals, so the job never picks a task spawn
// would refuse — which it would otherwise pick again on every run.
//
// # The cap
//
// A project with `auto_spawn.cap` auto-spawned tasks outstanding (underway or
// unverified, claimed by a session flagged auto_spawned) gets nothing more until
// one settles. Only auto-spawned work counts: the rest of the unverified pile
// is sediment, and a cap measured against it would never clear (E-1815).
//
// # Skips are not failures
//
// "Nothing opted in", "every project at its cap", "nothing eligible" and "no
// tmux client attached" each end the run successfully, with the reason recorded
// as the run's note (jobs.Note), which `endless jobs list` shows. Only a spawn
// that was attempted and failed is an error, and backs off.
package autospawnjob

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/rating"
	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// JobName keys this job's scheduling row. It must stay stable: changing it
// orphans the row and restarts the job's history.
const JobName = "auto-spawn"

const (
	// maxBackoff caps backoff after consecutive spawn failures. A spawn that
	// keeps failing is one a person has to look at; retrying it every interval
	// would only repeat the same fault.
	maxBackoff = time.Hour

	// spawnTimeout bounds one `task spawn` subprocess. It creates a worktree
	// and a tmux window and returns; it does not wait for Claude.
	spawnTimeout = 2 * time.Minute
)

// job implements jobs.Job. It carries no state: every decision is re-derived
// from the database and the config on each run.
type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() (name string) {
	return JobName
}

// Schedule reads the cadence from the user's config on every call, so a
// changed `auto_spawn.interval` takes effect at the next reschedule.
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval:   loadSettings().interval,
		MaxBackoff: maxBackoff,
	}
}

// Run spawns at most one eligible task, or records why it did not.
func (job) Run(ctx context.Context) (err error) {
	var db *sql.DB
	var s settings
	var pick candidate
	var skip string
	var target string

	// A sandbox database is a copy of main whose projects rows point at REAL
	// checkouts. Spawning from it would create real worktrees and real Claude
	// sessions for tasks that exist only in the copy.
	if monitor.IsSandboxActive() {
		jobs.Note(ctx, "skipped: the runner is on a worktree sandbox database")
		goto end
	}

	db, err = monitor.DB()
	if err != nil {
		goto end
	}
	s = loadSettings()

	pick, skip, err = selectCandidate(db, loadProjectPolicy)
	if err != nil || skip != "" {
		jobs.Note(ctx, skip)
		goto end
	}

	target, skip, err = resolveTarget(s.target)
	if err != nil || skip != "" {
		jobs.Note(ctx, skip)
		goto end
	}

	// A typo here is a configuration error, failed rather than quietly
	// defaulted, for the same reason an unknown target is: so it is seen.
	_, err = spawnlaunchcmd.ParsePlacement(s.placement)
	if err != nil {
		err = fmt.Errorf("auto_spawn.%w", err)
		goto end
	}

	err = runEndless(ctx, pick, spawnArgs(pick.taskID, target, s.placement))
	if err != nil {
		goto end
	}
	jobs.Note(ctx, "spawned E-"+strconv.FormatInt(pick.taskID, 10)+describeTarget(s.target, target))

end:
	return err
}

// settings is the user-level half of the config: one job, one cadence.
type settings struct {
	interval  time.Duration
	target    string
	placement string
}

// loadSettings reads auto_spawn.interval, auto_spawn.target and
// auto_spawn.placement from the user's config (the CLI layer). A missing, unreadable or unparsable interval falls
// back to the default rather than failing: Schedule has no error return, and a
// typo in a preference must not stop the runner from scheduling anything.
func loadSettings() (s settings) {
	var cfg *config.EndlessConfig
	var err error

	s.interval, _ = time.ParseDuration(config.DefaultAutoSpawnInterval)
	s.target = config.AutoSpawnTargetActive
	s.placement = config.DefaultAutoSpawnPlacement

	cfg, err = config.Load("")
	if err != nil || cfg == nil {
		goto end
	}
	if d, perr := time.ParseDuration(cfg.AutoSpawn.Interval); perr == nil && d > 0 {
		s.interval = d
	}
	if cfg.AutoSpawn.Target != "" {
		s.target = cfg.AutoSpawn.Target
	}
	if cfg.AutoSpawn.Placement != "" {
		s.placement = cfg.AutoSpawn.Placement
	}

end:
	return s
}

// projectPolicy is the project-level half of the config.
type projectPolicy struct {
	enabled bool
	cap     int
}

// policyFunc reads one project's policy from its directory. A seam so the
// selector can be tested without writing config files.
type policyFunc func(projectPath string) (projectPolicy, error)

// loadProjectPolicy reads auto_spawn.enabled and auto_spawn.cap from the
// project's own config file, never inheriting either from the user's (see
// config.AutoSpawn).
func loadProjectPolicy(projectPath string) (p projectPolicy, err error) {
	var cfg *config.EndlessConfig

	cfg, err = config.LoadProject(dt.DirPath(projectPath))
	if err != nil {
		goto end
	}
	p.enabled = cfg.AutoSpawn.Enabled
	p.cap = cfg.AutoSpawn.Cap
	if p.cap <= 0 {
		p.cap = config.DefaultAutoSpawnCap
	}

end:
	return p, err
}

// candidate is the task the selector picked, with the project directory its
// spawn must run from (`task spawn` resolves the project and creates the
// worktree from its working directory).
type candidate struct {
	taskID      int64
	projectPath string
}

// selectCandidate returns the one task this run should spawn, or a reason it
// should spawn nothing. Exactly one of pick and skip is set on success.
//
// Ordering is across every opted-in project under its cap: `urgent` before
// `now`, then oldest first, so a long-waiting task is not starved by newer
// ones and a project with more eligible work does not crowd out the rest any
// more than its age says it should.
func selectCandidate(db *sql.DB, policy policyFunc) (pick candidate, skip string, err error) {
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
			err = fmt.Errorf("reading auto_spawn config for project %q: %w", pr.name, err)
			goto end
		}
		if !p.enabled {
			continue
		}
		optedIn++
		outstanding, err = outstandingAutoSpawned(db, pr.id)
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
		skip = "no project has opted in (auto_spawn.enabled in its .endless/config.json)"
		goto end
	case len(open) == 0:
		skip = "every opted-in project is at its cap (" + strconv.Itoa(atCap) + " of " + strconv.Itoa(optedIn) + ")"
		goto end
	}

	row = db.QueryRow(eligibleQuery(len(open)), eligibleArgs(open)...)
	err = row.Scan(&pick.taskID, new(int64))
	if err == sql.ErrNoRows {
		err = nil
		skip = "nothing eligible"
		goto end
	}
	if err != nil {
		err = fmt.Errorf("selecting an auto-spawn candidate: %w", err)
		goto end
	}
	pick.projectPath, err = projectPathOf(db, pick.taskID, paths)

end:
	return pick, skip, err
}

// eligibleQuery is conditions 1–4 and 6–8 of the package doc, over the
// projects that passed 5 and the cap. n is how many project ids it filters on.
func eligibleQuery(n int) string {
	return `
		SELECT t.id, t.project_id
		  FROM live_tasks t
		  JOIN task_types ty ON ty.id = t.type_id
		 WHERE t.project_id IN (` + placeholders(n) + `)
		   AND t.status = '` + string(taskstatus.Ready) + `'
		   AND t.complexity_id = ?
		   AND t.risk_id = ?
		   AND t.phase IN ('now', 'urgent')
		   AND ty.auto_spawnable = 1
		   AND NOT EXISTS (
		       SELECT 1
		         FROM task_deps d
		         JOIN live_tasks b ON b.id = d.source_id
		        WHERE d.source_type = 'task'
		          AND d.target_type = 'task'
		          AND d.target_id = t.id
		          AND d.dep_type IN ('blocks', 'precedes')
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
		          t.created_at, t.id
		 LIMIT 1`
}

func eligibleArgs(projectIDs []int64) []any {
	args := make([]any, 0, len(projectIDs)+2)
	for _, id := range projectIDs {
		args = append(args, id)
	}
	return append(args, int(rating.Low), int(rating.Low))
}

// outstandingAutoSpawned counts the project's auto-spawned tasks that have not
// settled: underway or unverified, claimed by a session flagged auto_spawned.
// A session stalled on a permission prompt still counts (E-1815) — the cap
// measures claims on the user's attention, and a stalled session is one.
func outstandingAutoSpawned(db *sql.DB, projectID int64) (n int, err error) {
	err = db.QueryRow(`
		SELECT count(DISTINCT t.id)
		  FROM live_tasks t
		  JOIN sessions s ON s.task_id = t.id
		 WHERE t.project_id = ?
		   AND t.status IN ('`+string(taskstatus.Underway)+`', '`+string(taskstatus.Unverified)+`')
		   AND s.auto_spawned = 1`, projectID,
	).Scan(&n)
	if err != nil {
		err = fmt.Errorf("counting outstanding auto-spawned tasks: %w", err)
	}
	return n, err
}

type projectRow struct {
	id   int64
	name string
	path string // resolved, never the stored `~/...` form
}

// registeredProjects lists every project with its RESOLVED path. The column
// holds a home-relative form (E-2011), and config is read from a real
// directory.
func registeredProjects(db *sql.DB) (out []projectRow, err error) {
	var rows *sql.Rows
	var pr projectRow
	var stored string

	rows, err = db.Query(`SELECT id, name, path FROM live_projects ORDER BY id`)
	if err != nil {
		err = fmt.Errorf("listing projects: %w", err)
		goto end
	}
	defer rows.Close()
	for rows.Next() {
		if err = rows.Scan(&pr.id, &pr.name, &stored); err != nil {
			err = fmt.Errorf("listing projects: %w", err)
			goto end
		}
		pr.path, err = monitor.ResolvedProjectPath(stored)
		if err != nil {
			// A project whose directory cannot be resolved cannot have opted
			// in: its config file is unreachable. Skip it rather than fail the
			// run for every other project.
			err = nil
			continue
		}
		out = append(out, pr)
	}
	err = rows.Err()

end:
	return out, err
}

// projectPathOf returns the resolved directory of the picked task's project.
func projectPathOf(db *sql.DB, taskID int64, paths map[int64]string) (path string, err error) {
	var projectID int64
	err = db.QueryRow(`SELECT project_id FROM tasks WHERE id = ?`, taskID).Scan(&projectID)
	if err != nil {
		err = fmt.Errorf("resolving the project of E-%d: %w", taskID, err)
		return "", err
	}
	return paths[projectID], nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// tmuxOutput runs one tmux command and returns its trimmed stdout. A package
// var so target resolution can be tested without a tmux server.
var tmuxOutput = func(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// resolveTarget turns the auto_spawn.target setting into the session id the
// window opens in, or a reason to skip this run.
//
//   - active: the session of the most recently active ATTACHED client — where
//     the user is. No attached client means nobody is looking, and a spawn now
//     would land in a session no one will see, so the run skips and says so.
//     Auto-spawn therefore pauses by itself while the user is fully detached.
//   - monitor: the session the runner itself is in, which spawn-window already
//     resolves from $TMUX_PANE; target is left empty. Without a pane there is
//     no such session, and the run skips.
//
// Any other value is a configuration error, reported as a failure so it is
// seen rather than silently ignored.
func resolveTarget(setting string) (target, skip string, err error) {
	switch setting {
	case "", config.AutoSpawnTargetActive:
		target = mostRecentlyActiveClientSession()
		if target == "" {
			skip = "no tmux client is attached; auto-spawn waits until one is"
		}
	case config.AutoSpawnTargetMonitor:
		if os.Getenv("TMUX_PANE") == "" {
			skip = "auto_spawn.target is monitor, but the runner is not in a tmux pane"
		}
	default:
		err = fmt.Errorf("auto_spawn.target %q is not one of %q or %q",
			setting, config.AutoSpawnTargetActive, config.AutoSpawnTargetMonitor)
	}
	return target, skip, err
}

// mostRecentlyActiveClientSession asks tmux for every attached client and
// returns the session id of the one with the latest activity, or "" when there
// is no server or no client. list-clients lists attached clients only, so a
// detached session never qualifies.
func mostRecentlyActiveClientSession() (session string) {
	var latest int64 = -1

	out, err := tmuxOutput("list-clients", "-F", "#{client_activity} #{session_id}")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		activity, id, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || id == "" {
			continue
		}
		n, perr := strconv.ParseInt(activity, 10, 64)
		if perr != nil {
			continue
		}
		if n > latest {
			latest, session = n, id
		}
	}
	return session
}

// describeTarget renders where the window went, for the run's note.
func describeTarget(setting, target string) string {
	if target != "" {
		return " into tmux session " + target
	}
	if setting == config.AutoSpawnTargetMonitor {
		return " into the monitor's tmux session"
	}
	return ""
}

// runEndless shells one `endless` command for pick — the auto-spawn job's
// `--no-session task spawn E-N --auto`, or the prime job's `task prime` — from
// the project's directory.
//
// The Python CLI owns the spawn — the gates, the pre-claim, the worktree, the
// handoff — so the job runs exactly what a person would, plus the two hidden
// flags. It runs from the project's main checkout because `task spawn`
// resolves the project and creates the worktree from its working directory.
//
// The child opens this runner's database by monitor.ChildDBRoute, never by
// environment (E-2186). Run has already skipped a sandbox runner, so the only
// route left is main — no flag, since the child's default IS main — and any
// other answer is refused rather than spawned against the wrong database.
//
// Output is captured, never inherited: jobs.Job forbids writing to the
// trigger's terminal. It rides along in the error on failure.
func runEndless(ctx context.Context, pick candidate, args []string) (err error) {
	var bin string
	var cmd *exec.Cmd
	var out []byte
	var cancel context.CancelFunc
	var dbArgs []string

	if pick.projectPath == "" {
		err = fmt.Errorf("E-%d: its project directory could not be resolved", pick.taskID)
		goto end
	}
	bin, err = exec.LookPath("endless")
	if err != nil {
		err = fmt.Errorf("%w: the Python CLI is not on PATH", err)
		goto end
	}

	dbArgs, _, err = monitor.ChildDBRoute()
	if err == nil && len(dbArgs) != 0 {
		err = fmt.Errorf("the runner's database is not main (%v)", dbArgs)
	}
	if err != nil {
		err = fmt.Errorf("%s E-%d: %w", args[2], pick.taskID, err)
		goto end
	}

	ctx, cancel = context.WithTimeout(ctx, spawnTimeout)
	defer cancel()
	cmd = exec.CommandContext(ctx, bin, args...)
	cmd.Dir = pick.projectPath

	out, err = cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s E-%d: %w: %s", args[2], pick.taskID, err, tail(string(out)))
	}

end:
	return err
}

// spawnArgs is the `endless` argv for one auto-spawn.
//
// --no-session because nothing that asked for this spawn is a Claude session.
// Without it the pre-claim's status event is attributed through session
// resolution, which from a monitor pane either refuses outright or — worse —
// finds the Claude session sharing the monitor's tmux window and credits the
// claim to it. With it the event is recorded as the system's, which is what
// the flag exists for (cron, scripts, anything with no session behind it).
//
// placement is always passed (E-2234): `task spawn` defaults to first, which is
// right for a person who asked for the window and wrong for one nobody did.
func spawnArgs(taskID int64, target, placement string) []string {
	args := []string{"--no-session", "task", "spawn", "E-" + strconv.FormatInt(taskID, 10), "--auto",
		"--to-" + placement}
	if target != "" {
		args = append(args, "--target-session", target)
	}
	return args
}

// tailLimit bounds how much subprocess output rides along in a fault.
const tailLimit = 2000

func tail(s string) (out string) {
	out = strings.TrimSpace(s)
	if len(out) > tailLimit {
		out = "…" + out[len(out)-tailLimit:]
	}
	return out
}
