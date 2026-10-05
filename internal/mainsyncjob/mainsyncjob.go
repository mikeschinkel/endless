// Package mainsyncjob keeps each opted-in project's default branch in step with
// its upstream, in the background (E-2233).
//
// # Why
//
// `worktree land` never pushes, so local main runs hundreds of commits ahead of
// origin. Then origin gains one commit main lacks — a README edit made on the
// host — and integrating it is expensive: a `git pull` under pull.rebase=true
// replays every unpushed commit with a new SHA, and every open task branch's
// land is refused until it is rebased (E-2232). Keeping the two close keeps that
// integration a fast-forward.
//
// # What one run does, per project with main_sync.enabled
//
//  1. `git fetch <remote>` for main's upstream.
//  2. Only origin moved: `git merge --ff-only <upstream>`.
//  3. Only main moved: `git push <remote> main:<upstream branch>`. Never forced.
//  4. Both moved (diverged): change nothing, record ERR-0030 with each side's
//     count and both ways out, and let the user choose.
//  5. Main was rewritten under open task branches: record WARN-0031 naming
//     them, so each is rebased before its land is refused rather than after.
//
// A failed fetch, fast-forward or push is the run's error: the runner records
// it and backs off, so a missing credential is reported and decays toward the
// cap instead of retrying at full rate forever.
//
// # What it never does
//
// It never runs `git pull`. A bare pull does what each user's pull.rebase and
// pull.ff say, and Endless does not own anyone's git config; fetch, merge
// --ff-only and push behave the same everywhere. It never force-pushes, never
// merges or rebases on divergence, and never touches a task worktree.
//
// It works on the main checkout only, and only when that checkout has the
// default branch checked out: a fast-forward is a merge into HEAD, and merging
// into whatever else happens to be checked out there would be a different
// operation entirely.
package mainsyncjob

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/baserewrite"
	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// JobName keys this job's scheduling row. It must stay stable: changing it
// orphans the row and restarts the job's history.
const JobName = "main-sync"

const (
	// maxBackoff caps backoff after consecutive failures. A fetch or push that
	// keeps failing is almost always a credential or a network a person has
	// to fix; hammering the remote meanwhile helps nobody.
	maxBackoff = 2 * time.Hour

	// leaseTTL bounds a run. A warm run is one fetch per project; a run that
	// checks for a rewrite compares patch ids for every open task branch,
	// about a fifth of a second each on a repository with a long unpushed
	// history. Fifteen minutes clears both with room for a slow remote.
	leaseTTL = 15 * time.Minute
)

// job implements jobs.Job. Its only state is the in-process memory of main's
// tip per project (see rewriteWatch); everything else is re-derived each run.
type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() (name string) {
	return JobName
}

// Schedule reads the cadence from the user's config on every call, so a changed
// `main_sync.interval` takes effect at the next reschedule.
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval:   loadInterval(),
		MaxBackoff: maxBackoff,
		LeaseTTL:   leaseTTL,
	}
}

// Run syncs every opted-in project.
func (job) Run(ctx context.Context) (err error) {
	// A sandbox database is a copy of main whose projects rows point at REAL
	// checkouts. Syncing from it would push real branches on behalf of a
	// database that exists only for a test.
	if monitor.IsSandboxActive() {
		jobs.Note(ctx, "skipped: the runner is on a worktree sandbox database")
		return nil
	}

	projects, err := monitor.ActiveProjects()
	if err != nil {
		return fmt.Errorf("enumerate projects: %w", err)
	}
	note, err := runProjects(ctx, projects, enabled, systemGit)
	jobs.Note(ctx, note)
	return err
}

// runProjects syncs each project that opted in and returns the run's note. One
// project's failure does not abandon the others: they are independent
// repositories. Failures are joined into the one error the runner records.
//
// A project that has not opted in gets no git command at all, not even the
// fetch: off means off.
func runProjects(ctx context.Context, projects []monitor.ProjectRef,
	isEnabled func(root string) bool, git Git) (note string, err error) {
	var notes, failures []string
	for _, p := range projects {
		if !isEnabled(p.Root) {
			continue
		}
		res, serr := syncProject(ctx, p.ID, p.Root, git)
		if serr != nil {
			failures = append(failures, filepath.Base(p.Root)+": "+serr.Error())
		} else {
			notes = append(notes, filepath.Base(p.Root)+": "+res.String())
		}
		if ctx.Err() != nil {
			break
		}
	}

	if len(notes) == 0 && len(failures) == 0 {
		note = "no project has main_sync.enabled"
	}
	note = strings.Join(append([]string{note}, notes...), "; ")
	note = strings.TrimPrefix(note, "; ")
	if len(failures) > 0 {
		err = errors.New(strings.Join(failures, "; "))
	}
	return note, err
}

// Action is what a sync did to main.
type Action string

const (
	InSync        Action = "in sync"
	FastForwarded Action = "fast-forwarded"
	Pushed        Action = "pushed"
	Diverged      Action = "diverged"
)

// Result is one project's sync.
type Result struct {
	Branch   string
	Upstream string
	Action   Action
	// Ahead is how many commits main had that the upstream lacked, Behind the
	// reverse, both counted after the fetch and before anything changed.
	Ahead, Behind int
	// Stranded names the open task branches still carrying copies of commits
	// main holds under other SHAs. Empty unless a rewrite check ran and found
	// some.
	Stranded []StrandedBranch
}

// StrandedBranch is an open task branch that main was rewritten under.
type StrandedBranch struct {
	Branch   string
	Worktree string
	Copies   int
}

// String is the run note for this project.
func (r Result) String() string {
	s := string(r.Action)
	switch r.Action {
	case FastForwarded:
		s += fmt.Sprintf(" %s by %d from %s", r.Branch, r.Behind, r.Upstream)
	case Pushed:
		s += fmt.Sprintf(" %d to %s", r.Ahead, r.Upstream)
	case Diverged:
		s += fmt.Sprintf(" (%d ahead, %d behind %s) — nothing changed", r.Ahead, r.Behind, r.Upstream)
	}
	if len(r.Stranded) > 0 {
		s += fmt.Sprintf("; %d task branch(es) need `git rebase %s`", len(r.Stranded), r.Branch)
	}
	return s
}

// Git runs git in dir and returns stdout.
type Git func(ctx context.Context, dir string, args ...string) (string, error)

// recordFault is faults.Record, held as a var so tests can observe what a run
// reports without a bound fault store.
var recordFault = faults.Record

// syncProject is one project's run. It is the whole algorithm; Run only picks
// the projects.
func syncProject(ctx context.Context, projectID int64, root string, git Git) (res Result, err error) {
	run := func(args ...string) (string, error) {
		out, gerr := git(ctx, root, args...)
		return strings.TrimSpace(out), gerr
	}

	res.Branch, err = monitor.DefaultBranch(ctx, root)
	if err != nil {
		return res, fmt.Errorf("resolve the default branch: %w", err)
	}

	head, _ := run("symbolic-ref", "--short", "--quiet", "HEAD")
	if head != res.Branch {
		if head == "" {
			head = "a detached HEAD"
		}
		return res, fmt.Errorf("the main checkout is on %s, not %s; nothing was synced — "+
			"check out %s there", head, res.Branch, res.Branch)
	}

	remote, _ := run("config", "--get", "branch."+res.Branch+".remote")
	mergeRef, _ := run("config", "--get", "branch."+res.Branch+".merge")
	if remote == "" || remote == "." || mergeRef == "" {
		return res, fmt.Errorf("%s has no remote upstream to sync with — set one with "+
			"`git branch --set-upstream-to=<remote>/%s %s`, or turn main_sync off",
			res.Branch, res.Branch, res.Branch)
	}
	upstreamRef := res.Branch + "@{upstream}"

	// Fetch first: every decision below is about the remote as it is now.
	if _, err = run("fetch", "--quiet", remote); err != nil {
		return res, fmt.Errorf("fetch %s: %w", remote, err)
	}
	res.Upstream, err = run("rev-parse", "--abbrev-ref", upstreamRef)
	if err != nil {
		return res, fmt.Errorf("resolve %s's upstream after fetching: %w", res.Branch, err)
	}

	counts, err := run("rev-list", "--left-right", "--count", res.Branch+"..."+upstreamRef)
	if err != nil {
		return res, fmt.Errorf("compare %s with %s: %w", res.Branch, res.Upstream, err)
	}
	res.Ahead, res.Behind, err = parseCounts(counts)
	if err != nil {
		return res, err
	}

	switch {
	case res.Ahead == 0 && res.Behind == 0:
		res.Action = InSync
	case res.Ahead == 0:
		if _, err = run("merge", "--ff-only", "--quiet", upstreamRef); err != nil {
			return res, fmt.Errorf("fast-forward %s to %s: %w", res.Branch, res.Upstream, err)
		}
		res.Action = FastForwarded
	case res.Behind == 0:
		// An explicit refspec, so push.default and any per-remote push config
		// cannot send something else. No --force of any kind: a push the remote
		// rejects is a failure to report, never one to overrule.
		if _, err = run("push", "--quiet", remote, "refs/heads/"+res.Branch+":"+mergeRef); err != nil {
			return res, fmt.Errorf("push %s to %s: %w", res.Branch, res.Upstream, err)
		}
		res.Action = Pushed
	default:
		res.Action = Diverged
		recordDiverged(projectID, root, res)
	}

	res.Stranded, err = watchRewrite(ctx, root, res.Branch, git)
	if err != nil {
		return res, err
	}
	if len(res.Stranded) > 0 {
		recordRewritten(projectID, root, res)
	}
	return res, nil
}

var countsRe = regexp.MustCompile(`^(\d+)\s+(\d+)$`)

func parseCounts(out string) (ahead, behind int, err error) {
	m := countsRe.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, fmt.Errorf("unexpected rev-list --count output %q", out)
	}
	ahead, _ = strconv.Atoi(m[1])
	behind, _ = strconv.Atoi(m[2])
	return ahead, behind, nil
}

func recordDiverged(projectID int64, root string, res Result) {
	recordFault(faults.Fault{
		Code:        faults.ErrCodeMainDiverged,
		Source:      "job:" + JobName,
		Fingerprint: "main-diverged:" + root + ":" + res.Branch,
		ProjectID:   projectID,
		Summary: fmt.Sprintf("%s and %s have diverged: %s has %s %s lacks, and %s has %s "+
			"%s lacks. Sync changed nothing — merge or rebase is your call.",
			res.Branch, res.Upstream, res.Branch, plural(res.Ahead, "commit"), res.Upstream,
			res.Upstream, plural(res.Behind, "commit"), res.Branch),
		Detail: fmt.Sprintf("In %s:\n\n"+
			"  Merge:  git merge %s\n"+
			"          One merge commit; nothing else changes. Nothing in Endless\n"+
			"          assumes %s is linear.\n\n"+
			"  Rebase: git rebase %s\n"+
			"          Every unpushed commit on %s gets a new SHA, so every open\n"+
			"          task branch must run `git rebase %s` before it can land.\n\n"+
			"The next main-sync run pushes once %s contains %s.\n",
			root, res.Upstream, res.Branch, res.Upstream, res.Branch, res.Branch,
			res.Branch, res.Upstream),
		Fields: map[string]any{
			"root": root, "branch": res.Branch, "upstream": res.Upstream,
			"ahead": res.Ahead, "behind": res.Behind,
		},
	})
}

func recordRewritten(projectID int64, root string, res Result) {
	var names []string
	var detail strings.Builder
	fmt.Fprintf(&detail, "In %s, these open task branches still carry copies of "+
		"commits %s holds under other SHAs:\n\n", root, res.Branch)
	for _, s := range res.Stranded {
		names = append(names, s.Branch)
		fmt.Fprintf(&detail, "  %s  (%s)\n    %s\n", s.Branch, s.Worktree,
			monitor.BaseRewrittenDetail(res.Branch, s.Copies))
	}
	shown := names
	if len(shown) > 4 {
		shown = append(append([]string{}, shown[:4]...), "…")
	}
	recordFault(faults.Fault{
		Code:        faults.ErrCodeMainRewritten,
		Source:      "job:" + JobName,
		Fingerprint: "main-rewritten:" + root,
		ProjectID:   projectID,
		Summary: fmt.Sprintf("%s was rewritten under %s (%s): land refuses each until "+
			"`git rebase %s` runs in its worktree",
			res.Branch, plural(len(res.Stranded), "open task branch"),
			strings.Join(shown, ", "), res.Branch),
		Detail: detail.String(),
		Fields: map[string]any{"root": root, "branch": res.Branch, "stranded": names},
	})
}

// tipMemory is main's tip as each project's last run saw it, and whether that
// run left branches stranded. It is the gate that keeps the rewrite check off
// the common path.
//
// The check compares patch ids across every open task branch — seconds of git
// work on a repository with a long unpushed history, which a five-minute job
// must not spend on every run. A rewrite is visible far more cheaply: main's
// tip stops descending from the tip last seen. So the full check runs only on a
// project's first run in this process (nothing remembered yet), when main
// stopped descending, and while a previous check still found stranded branches
// (so the report stays current until they are rebased).
//
// In-process memory and not a file: losing it costs one full check in the next
// process, which is the same thing the first run costs anyway.
var (
	tipMu     sync.Mutex
	tipMemory = map[string]tipState{}
)

type tipState struct {
	tip      string
	stranded bool
}

// watchRewrite returns the open task branches main was rewritten under, running
// the full check only when tipMemory says it can have changed.
func watchRewrite(ctx context.Context, root, branch string, git Git) ([]StrandedBranch, error) {
	tip, err := git(ctx, root, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", branch, err)
	}
	tip = strings.TrimSpace(tip)

	tipMu.Lock()
	prev, seen := tipMemory[root]
	tipMu.Unlock()

	if seen && !prev.stranded {
		if prev.tip == tip {
			return nil, nil
		}
		// Exit 0 means the old tip is an ancestor: main only moved forward.
		if _, aerr := git(ctx, root, "merge-base", "--is-ancestor", prev.tip, tip); aerr == nil {
			remember(root, tip, false)
			return nil, nil
		}
	}

	stranded, err := strandedBranches(ctx, root, branch, git)
	if err != nil {
		return nil, err
	}
	remember(root, tip, len(stranded) > 0)
	return stranded, nil
}

func remember(root, tip string, stranded bool) {
	tipMu.Lock()
	tipMemory[root] = tipState{tip: tip, stranded: stranded}
	tipMu.Unlock()
}

// strandedBranches lists the project's task worktrees whose branch carries
// copies of main's commits AND work of its own.
//
// The second half is what keeps landed worktrees out of it. Endless retains a
// worktree after its land, and after a rewrite of main every landed branch is
// nothing but copies — patch-equivalent to main, with no work of its own and
// nothing left to land. Naming those would bury the branches that matter. A
// task branch claimed but not yet committed to looks the same and is left out
// too; `worktree check` names it from inside, before its land.
func strandedBranches(ctx context.Context, root, base string, git Git) ([]StrandedBranch, error) {
	out, err := git(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}
	var stranded []StrandedBranch
	for _, wt := range taskWorktrees(out) {
		if ctx.Err() != nil {
			return stranded, ctx.Err()
		}
		sides, serr := baserewrite.BranchSide(func(args ...string) (string, error) {
			return git(ctx, root, args...)
		}, base, "refs/heads/"+wt.branch)
		if serr != nil {
			// One unreadable branch must not hide the others; it is simply not
			// claimed as stranded.
			continue
		}
		if len(sides.Equivalent) > 0 && len(sides.Branch) > 0 {
			stranded = append(stranded, StrandedBranch{
				Branch: wt.branch, Worktree: wt.path, Copies: len(sides.Equivalent),
			})
		}
	}
	return stranded, nil
}

type worktreeRef struct{ path, branch string }

// taskWorktrees picks the task worktrees out of `git worktree list --porcelain`:
// a checkout under .endless/worktrees/ that has a branch. The main checkout and
// anything detached are left out.
func taskWorktrees(porcelain string) (out []worktreeRef) {
	var cur worktreeRef
	flush := func() {
		if cur.branch != "" && strings.Contains(filepath.ToSlash(cur.path), "/.endless/worktrees/") {
			out = append(out, cur)
		}
		cur = worktreeRef{}
	}
	for _, ln := range strings.Split(porcelain, "\n") {
		switch {
		case strings.HasPrefix(ln, "worktree "):
			flush()
			cur.path = strings.TrimPrefix(ln, "worktree ")
		case strings.HasPrefix(ln, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(ln, "branch "), "refs/heads/")
		}
	}
	flush()
	return out
}

// systemGit runs the real git.
//
// Three things about the child matter here and nowhere else in Endless, because
// this is the only background process that talks to a remote:
//
//   - GIT_TERMINAL_PROMPT=0, so an HTTPS remote with no stored credential fails
//     instead of waiting for a username nobody will type.
//   - A new session (setsid), so the child has no controlling terminal. ssh
//     asks for a passphrase on /dev/tty, not stdin, and the process that fires
//     this job is often a monitor painting a tmux pane: without this, the
//     prompt would appear in the middle of someone's dashboard. With it, ssh
//     fails and the failure is reported. Agents and keychains still work.
//   - The E-1309 sanitized environment, so an inherited GIT_DIR cannot point
//     the fetch, merge or push at some other repository.
func systemGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(events.SanitizedGitEnv(), "GIT_TERMINAL_PROMPT=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// enabled reads main_sync.enabled from the project's own config file. An
// unreadable file reads as off: a job that publishes history does not do so on
// a guess.
func enabled(root string) bool {
	cfg, err := config.LoadProject(dt.DirPath(root))
	return err == nil && cfg != nil && cfg.MainSync.Enabled
}

// loadInterval reads main_sync.interval from the user's config. Anything
// missing or unparsable falls back to the default: Schedule has no error
// return, and a typo in a preference must not stop the runner scheduling.
func loadInterval() time.Duration {
	d, _ := time.ParseDuration(config.DefaultMainSyncInterval)
	cfg, err := config.Load("")
	if err != nil || cfg == nil {
		return d
	}
	if v, perr := time.ParseDuration(cfg.MainSync.Interval); perr == nil && v > 0 {
		d = v
	}
	return d
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "ch") {
		return strconv.Itoa(n) + " " + noun + "es"
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
