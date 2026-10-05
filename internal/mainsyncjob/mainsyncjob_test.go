package mainsyncjob

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
)

func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// isolateGit keeps the developer's own git config out of every repository
// these tests build: no signing, no hooks path, no pull.rebase of theirs.
func isolateGit(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	empty := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(k, v)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, file string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", file)
	git(t, dir, "commit", "-q", "-m", "add "+file)
	return git(t, dir, "rev-parse", "HEAD")
}

// fixture is a bare origin, the project's main checkout cloned from it, and a
// second clone standing in for another machine (or a web edit on the host).
type fixture struct {
	origin, main, other string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	isolateGit(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{
		origin: filepath.Join(base, "origin.git"),
		main:   filepath.Join(base, "project"),
		other:  filepath.Join(base, "other"),
	}
	git(t, base, "init", "-q", "--bare", "-b", "main", f.origin)
	git(t, base, "clone", "-q", f.origin, f.main)
	git(t, f.main, "checkout", "-q", "-b", "main")
	commit(t, f.main, "seed.txt")
	git(t, f.main, "push", "-q", "-u", "origin", "main")
	git(t, f.main, "remote", "set-head", "origin", "--auto")
	git(t, base, "clone", "-q", f.origin, f.other)
	return f
}

func (f fixture) originTip(t *testing.T) string {
	return git(t, f.origin, "rev-parse", "refs/heads/main")
}

// recorder wraps the real git, keeping every command line it was asked to run.
type recorder struct {
	mu   sync.Mutex
	runs []string
}

func (r *recorder) git(ctx context.Context, dir string, args ...string) (string, error) {
	r.mu.Lock()
	r.runs = append(r.runs, strings.Join(args, " "))
	r.mu.Unlock()
	return systemGit(ctx, dir, args...)
}

func (r *recorder) ran(prefix string) bool {
	for _, run := range r.runs {
		if strings.HasPrefix(run, prefix) {
			return true
		}
	}
	return false
}

// captureFaults swaps recordFault for one that keeps what a run reports.
func captureFaults(t *testing.T) *[]faults.Fault {
	t.Helper()
	var got []faults.Fault
	prev := recordFault
	recordFault = func(f faults.Fault) { got = append(got, f) }
	t.Cleanup(func() { recordFault = prev })
	return &got
}

func sync1(t *testing.T, f fixture, g Git) (Result, error) {
	t.Helper()
	return syncProject(context.Background(), 1, f.main, g)
}

// TestSync_FastForwardsWhenOnlyOriginMoved: a commit made elsewhere reaches
// main as a fast-forward, and main's working tree follows.
func TestSync_FastForwardsWhenOnlyOriginMoved(t *testing.T) {
	f := newFixture(t)
	got := captureFaults(t)
	commit(t, f.other, "remote.txt")
	git(t, f.other, "push", "-q", "origin", "main")

	res, err := sync1(t, f, systemGit)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Action != FastForwarded || res.Behind != 1 {
		t.Fatalf("result = %+v, want fast-forwarded by 1", res)
	}
	if head := git(t, f.main, "rev-parse", "HEAD"); head != f.originTip(t) {
		t.Errorf("main = %s, want origin's %s", head, f.originTip(t))
	}
	if _, err := os.Stat(filepath.Join(f.main, "remote.txt")); err != nil {
		t.Errorf("the fast-forward did not reach the working tree: %v", err)
	}
	if len(*got) != 0 {
		t.Errorf("recorded %d fault(s) on a clean fast-forward", len(*got))
	}
}

// TestSync_PushesWhenOnlyMainMoved: commits that landed on main reach origin.
func TestSync_PushesWhenOnlyMainMoved(t *testing.T) {
	f := newFixture(t)
	commit(t, f.main, "a.txt")
	tip := commit(t, f.main, "b.txt")

	res, err := sync1(t, f, systemGit)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Action != Pushed || res.Ahead != 2 {
		t.Fatalf("result = %+v, want pushed 2", res)
	}
	if f.originTip(t) != tip {
		t.Errorf("origin = %s, want main's %s", f.originTip(t), tip)
	}

	res, err = sync1(t, f, systemGit)
	if err != nil || res.Action != InSync {
		t.Errorf("second run = %+v, %v; want in sync", res, err)
	}
}

// TestSync_DivergedChangesNothingAndRecordsAFault: both sides moved, so the job
// neither merges nor rebases nor pushes; it reports both counts and both ways
// out, and leaves the choice to the user.
func TestSync_DivergedChangesNothingAndRecordsAFault(t *testing.T) {
	f := newFixture(t)
	got := captureFaults(t)
	commit(t, f.other, "remote.txt")
	git(t, f.other, "push", "-q", "origin", "main")
	originBefore := f.originTip(t)
	commit(t, f.main, "local1.txt")
	mainBefore := commit(t, f.main, "local2.txt")

	res, err := sync1(t, f, systemGit)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Action != Diverged || res.Ahead != 2 || res.Behind != 1 {
		t.Fatalf("result = %+v, want diverged 2/1", res)
	}
	if h := git(t, f.main, "rev-parse", "HEAD"); h != mainBefore {
		t.Errorf("main moved to %s on divergence", h)
	}
	if o := f.originTip(t); o != originBefore {
		t.Errorf("origin moved to %s on divergence", o)
	}
	if len(*got) != 1 || (*got)[0].Code.ID != faults.ErrCodeMainDiverged.ID {
		t.Fatalf("faults = %+v, want one %s", *got, faults.ErrCodeMainDiverged.ID)
	}
	fault := (*got)[0]
	for _, want := range []string{"2 commits", "1 commit", "origin/main"} {
		if !strings.Contains(fault.Summary, want) {
			t.Errorf("summary %q lacks %q", fault.Summary, want)
		}
	}
	for _, want := range []string{"git merge origin/main", "git rebase origin/main", "git rebase main"} {
		if !strings.Contains(fault.Detail, want) {
			t.Errorf("detail lacks %q:\n%s", want, fault.Detail)
		}
	}
	if fault.ProjectID != 1 {
		t.Errorf("fault attributed to project %d, want 1", fault.ProjectID)
	}
}

// TestSync_FailedPushIsTheRunsError: a push the remote rejects is returned for
// the runner to record and back off on; it is never forced through.
func TestSync_FailedPushIsTheRunsError(t *testing.T) {
	f := newFixture(t)
	got := captureFaults(t)
	hook := filepath.Join(f.origin, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho rejected by policy >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := f.originTip(t)
	commit(t, f.main, "a.txt")
	rec := &recorder{}

	_, err := sync1(t, f, rec.git)
	if err == nil {
		t.Fatal("sync succeeded past a rejected push")
	}
	if !strings.Contains(err.Error(), "push main") || !strings.Contains(err.Error(), "rejected by policy") {
		t.Errorf("error does not name the push and the remote's reason: %v", err)
	}
	if f.originTip(t) != before {
		t.Error("origin moved despite the rejection")
	}
	for _, run := range rec.runs {
		if strings.Contains(run, "--force") || strings.Contains(run, " +") {
			t.Errorf("forced: %q", run)
		}
	}
	if len(*got) != 0 {
		t.Errorf("a push failure recorded its own fault; the runner owns that: %+v", *got)
	}
	if (job{}).Schedule().MaxBackoff <= 0 {
		t.Error("no backoff: a failing push would retry at full rate forever")
	}
}

// TestSync_FailedFetchIsTheRunsError: an unreachable remote is reported, and
// nothing after the fetch runs on stale knowledge.
func TestSync_FailedFetchIsTheRunsError(t *testing.T) {
	f := newFixture(t)
	git(t, f.main, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	rec := &recorder{}

	_, err := sync1(t, f, rec.git)
	if err == nil || !strings.Contains(err.Error(), "fetch origin") {
		t.Fatalf("err = %v, want a fetch failure", err)
	}
	if rec.ran("merge") || rec.ran("push") {
		t.Errorf("acted after a failed fetch: %v", rec.runs)
	}
}

// TestRunProjects_DisabledDoesNothing: a project that has not opted in gets no
// git command at all — not even the fetch.
func TestRunProjects_DisabledDoesNothing(t *testing.T) {
	f := newFixture(t)
	commit(t, f.main, "a.txt")
	before := f.originTip(t)
	rec := &recorder{}

	note, err := runProjects(context.Background(),
		[]monitor.ProjectRef{{ID: 1, Root: f.main}},
		enabled, rec.git) // no .endless/config.json: off
	if err != nil {
		t.Fatalf("runProjects: %v", err)
	}
	if len(rec.runs) != 0 {
		t.Errorf("ran git for a disabled project: %v", rec.runs)
	}
	if f.originTip(t) != before {
		t.Error("pushed a disabled project")
	}
	if note != "no project has main_sync.enabled" {
		t.Errorf("note = %q", note)
	}

	// The same project, opted in through its own config file, is synced.
	if err := os.MkdirAll(filepath.Join(f.main, ".endless"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.main, ".endless", "config.json"),
		[]byte(`{"main_sync":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	note, err = runProjects(context.Background(),
		[]monitor.ProjectRef{{ID: 1, Root: f.main}}, enabled, rec.git)
	if err != nil {
		t.Fatalf("runProjects enabled: %v", err)
	}
	if !strings.Contains(note, "pushed 1") {
		t.Errorf("note = %q, want a push", note)
	}
}

// TestSync_UserPullConfigHasNoEffect: pull.rebase / pull.ff are the user's,
// and the job's outcome is the same whatever they say, because it never pulls.
func TestSync_UserPullConfigHasNoEffect(t *testing.T) {
	for _, cfg := range [][2]string{{"pull.rebase", "true"}, {"pull.ff", "only"}, {"pull.rebase", "merges"}} {
		t.Run(cfg[0]+"="+cfg[1], func(t *testing.T) {
			f := newFixture(t)
			git(t, f.main, "config", cfg[0], cfg[1])
			captureFaults(t)
			rec := &recorder{}

			// Diverged: under pull.rebase=true a pull would have rewritten main.
			commit(t, f.other, "remote.txt")
			git(t, f.other, "push", "-q", "origin", "main")
			local := commit(t, f.main, "local.txt")
			res, err := sync1(t, f, rec.git)
			if err != nil || res.Action != Diverged {
				t.Fatalf("diverged run = %+v, %v", res, err)
			}
			if h := git(t, f.main, "rev-parse", "HEAD"); h != local {
				t.Errorf("main rewritten to %s under %s=%s", h, cfg[0], cfg[1])
			}
			if rec.ran("pull") {
				t.Errorf("ran git pull: %v", rec.runs)
			}
		})
	}
}

// TestSync_RefusesWhenMainCheckoutIsNotOnMain: a fast-forward merges into HEAD,
// so with another branch checked out the job does nothing at all.
func TestSync_RefusesWhenMainCheckoutIsNotOnMain(t *testing.T) {
	f := newFixture(t)
	git(t, f.main, "checkout", "-q", "-b", "elsewhere")
	rec := &recorder{}
	_, err := sync1(t, f, rec.git)
	if err == nil || !strings.Contains(err.Error(), "on elsewhere, not main") {
		t.Fatalf("err = %v, want a refusal naming the branch", err)
	}
	if rec.ran("fetch") {
		t.Error("fetched despite refusing")
	}
}

// addTaskWorktree makes an Endless-shaped task worktree on task/<n>.
func addTaskWorktree(t *testing.T, f fixture, n string) string {
	t.Helper()
	wt := filepath.Join(f.main, ".endless", "worktrees", "e-"+n)
	git(t, f.main, "worktree", "add", "-q", "-b", "task/"+n, wt, "main")
	return wt
}

// TestSync_RewrittenMainNamesStrandedBranches is plan step 3 end to end: main
// is rebased under an open task branch (the pull.rebase accident), the job
// names that branch — and not a landed one — keeps naming it while it is
// stranded, and stops once it is rebased.
func TestSync_RewrittenMainNamesStrandedBranches(t *testing.T) {
	f := newFixture(t)
	got := captureFaults(t)
	t.Cleanup(func() { tipMu.Lock(); delete(tipMemory, f.main); tipMu.Unlock() })
	// Ignore the worktree directory the way Endless's own .gitignore does.
	if err := os.WriteFile(filepath.Join(f.main, ".git", "info", "exclude"), []byte(".endless/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	commit(t, f.main, "unpushed1.txt")
	commit(t, f.main, "unpushed2.txt")
	open := addTaskWorktree(t, f, "1")
	commit(t, open, "work.txt")
	addTaskWorktree(t, f, "2") // landed-and-retained: no work of its own

	// Someone edits on the host; main is then rebased onto it.
	commit(t, f.other, "host-edit.txt")
	git(t, f.other, "push", "-q", "origin", "main")
	git(t, f.main, "fetch", "-q", "origin")
	git(t, f.main, "rebase", "-q", "origin/main")

	res, err := sync1(t, f, systemGit)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Action != Pushed {
		t.Errorf("action = %s, want pushed (main now contains origin)", res.Action)
	}
	if len(res.Stranded) != 1 || res.Stranded[0].Branch != "task/1" || res.Stranded[0].Copies != 2 {
		t.Fatalf("stranded = %+v, want task/1 with 2 copies", res.Stranded)
	}
	if len(*got) != 1 || (*got)[0].Code.ID != faults.ErrCodeMainRewritten.ID {
		t.Fatalf("faults = %+v, want one %s", *got, faults.ErrCodeMainRewritten.ID)
	}
	if !strings.Contains((*got)[0].Detail, "git rebase main") || !strings.Contains((*got)[0].Detail, open) {
		t.Errorf("detail lacks the remedy or the worktree:\n%s", (*got)[0].Detail)
	}

	// Still stranded: the next run reports it again, though main has not moved.
	res, _ = sync1(t, f, systemGit)
	if len(res.Stranded) != 1 {
		t.Errorf("second run stranded = %+v, want task/1 again", res.Stranded)
	}

	// Rebased: the report clears.
	git(t, open, "rebase", "-q", "main")
	res, _ = sync1(t, f, systemGit)
	if len(res.Stranded) != 0 {
		t.Errorf("after the rebase stranded = %+v, want none", res.Stranded)
	}
}

// TestSync_LinearMainSkipsTheRewriteCheck: once main's tip is remembered and
// main only moves forward, no patch-id comparison runs — the gate that keeps a
// five-minute job cheap.
func TestSync_LinearMainSkipsTheRewriteCheck(t *testing.T) {
	f := newFixture(t)
	t.Cleanup(func() { tipMu.Lock(); delete(tipMemory, f.main); tipMu.Unlock() })
	wt := addTaskWorktree(t, f, "1")
	commit(t, wt, "work.txt")

	if _, err := sync1(t, f, systemGit); err != nil { // cold: full check
		t.Fatal(err)
	}
	commit(t, f.main, "more.txt")
	rec := &recorder{}
	if _, err := sync1(t, f, rec.git); err != nil {
		t.Fatal(err)
	}
	if rec.ran("log") {
		t.Errorf("ran the rewrite check though main only moved forward: %v", rec.runs)
	}
}

func TestTaskWorktrees(t *testing.T) {
	porcelain := "worktree /p\nHEAD a\nbranch refs/heads/main\n\n" +
		"worktree /p/.endless/worktrees/e-1\nHEAD b\nbranch refs/heads/task/1\n\n" +
		"worktree /p/.endless/worktrees/e-2\nHEAD c\ndetached\n\n" +
		"worktree /elsewhere\nHEAD d\nbranch refs/heads/x\n"
	got := taskWorktrees(porcelain)
	if len(got) != 1 || got[0].branch != "task/1" || got[0].path != "/p/.endless/worktrees/e-1" {
		t.Errorf("taskWorktrees = %+v", got)
	}
}
