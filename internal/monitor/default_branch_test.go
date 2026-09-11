package monitor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// E-1940 — the resolver is tested against REAL git repositories, not a stubbed
// runGit. Every step it implements is a git behaviour (`symbolic-ref` on an
// unset ref, `rev-parse --verify` on a name that is a config value rather than
// a branch), and a stub would only assert that the test author and the
// implementation share the same belief about git.
//
// tests/test_default_branch_parity.py runs the same table against the Python
// resolver. The two implementations must agree case for case; that is asserted
// there rather than trusted to prose in either file.

// fixtureRepo builds a git repo with one commit on branchName and returns its
// path. Isolated from the developer's own git config — HOME and the system
// config are redirected, so a machine-wide init.defaultBranch cannot leak in
// and silently change what these tests measure.
func fixtureRepo(t *testing.T, branchName string) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"HOME="+dir,
		"XDG_CONFIG_HOME="+filepath.Join(dir, "xdg"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--initial-branch="+branchName)
	// Repo-local identity so every LATER git call in a test can be a plain
	// `git -C <dir>` without rebuilding this environment.
	run("config", "user.name", "t")
	run("config", "user.email", "t@example.com")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	run("add", "f.txt")
	run("commit", "-m", "initial")
	return dir
}

// mustGit runs a git command in dir and returns its trimmed stdout, failing the
// test on a non-zero exit. For fixture setup, where any failure is a broken
// test rather than a condition under test.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitConfigSet writes a repo-local config value. Repo-local rather than global
// because the fixture's HOME is a temp dir the resolver's own git invocations
// (which inherit the process environment, not the fixture's) would not read.
func gitConfigSet(t *testing.T, dir, key, value string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "config", key, value)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config %s: %v: %s", key, err, out)
	}
}

// writeProjectConfig writes <dir>/.endless/config.json with a default_branch.
func writeProjectConfig(t *testing.T, dir, defaultBranch string) {
	t.Helper()
	cfgDir := filepath.Join(dir, ".endless")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir .endless: %v", err)
	}
	body := `{"name":"fixture","default_branch":"` + defaultBranch + `"}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
}

// TestDefaultBranchFallsBackToMaster is the regression that proves the product
// works outside `main`. Before E-1940 the probe hardcoded `main`, so on this
// repo `rev-list main..HEAD` exited 128 on every tick: a permanent false
// all-clear, and a ◆ that could never appear.
func TestDefaultBranchFallsBackToMaster(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	got, err := DefaultBranch(context.Background(), fixtureRepo(t, "master"))
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "master" {
		t.Errorf("DefaultBranch = %q, want master", got)
	}
}

// TestDefaultBranchIgnoresInitDefaultBranchThatDoesNotExist is why step 3
// verifies its answer. `init.defaultBranch` is a preference belonging to the
// machine, not a fact about the repo, and setting it to `main` on a machine
// that also has `master` repos is common — taking it on trust would reproduce
// the hardcoded-`main` bug through a different door.
func TestDefaultBranchIgnoresInitDefaultBranchThatDoesNotExist(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	dir := fixtureRepo(t, "master")
	gitConfigSet(t, dir, "init.defaultBranch", "main")

	got, err := DefaultBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "master" {
		t.Errorf("DefaultBranch = %q, want master — init.defaultBranch names no branch here", got)
	}
}

// TestDefaultBranchUsesInitDefaultBranchWhenItExists is the other half: the
// existence check must not neuter step 3 where it is genuinely right.
func TestDefaultBranchUsesInitDefaultBranchWhenItExists(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	dir := fixtureRepo(t, "trunk")
	gitConfigSet(t, dir, "init.defaultBranch", "trunk")

	got, err := DefaultBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "trunk" {
		t.Errorf("DefaultBranch = %q, want trunk", got)
	}
}

// TestDefaultBranchConfigWins covers step 1: an explicit project setting beats
// every detection step, including a `main` that exists.
func TestDefaultBranchConfigWins(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	dir := fixtureRepo(t, "release")
	gitConfigSet(t, dir, "init.defaultBranch", "release")
	writeProjectConfig(t, dir, "release")

	got, err := DefaultBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "release" {
		t.Errorf("DefaultBranch = %q, want release", got)
	}
}

// TestDefaultBranchConfigTypoDoesNotFallThrough pins step 1's one asymmetry: a
// configured branch that does not exist is a typo in the project's own config,
// and detecting around it would hide the thing the operator wrote down.
func TestDefaultBranchConfigTypoDoesNotFallThrough(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	dir := fixtureRepo(t, "main")
	writeProjectConfig(t, dir, "mian")

	got, err := DefaultBranch(context.Background(), dir)
	if !errors.Is(err, ErrDefaultBranchUnresolved) {
		t.Fatalf("DefaultBranch = (%q, %v), want ErrDefaultBranchUnresolved", got, err)
	}
	if got != "" {
		t.Errorf("DefaultBranch returned %q alongside an error", got)
	}
}

// TestDefaultBranchErrorsRatherThanGuessingMain is the decision the whole
// resolver exists to enforce: when nothing can name the branch, say so. The
// caller decides what to do with "I don't know"; substituting `main` is the
// bug.
func TestDefaultBranchErrorsRatherThanGuessingMain(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	got, err := DefaultBranch(context.Background(), fixtureRepo(t, "develop"))
	if !errors.Is(err, ErrDefaultBranchUnresolved) {
		t.Fatalf("DefaultBranch = (%q, %v), want ErrDefaultBranchUnresolved", got, err)
	}
	if got == "main" {
		t.Error("resolver substituted main — the exact failure E-1940 removes")
	}
}

// TestDefaultBranchMemoizes guards the hot path: `session monitor` re-probes
// every row every two seconds, and an unmemoized resolver would add up to four
// git invocations per row per tick to re-derive a constant.
//
// Zero calls on the second resolution covers the git-common-dir lookup the key
// is derived from as well (E-2128) — that lookup is memoized per directory, so a
// repeat resolution of the same directory must not re-run it either.
func TestDefaultBranchMemoizes(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	dir := fixtureRepo(t, "master")
	if _, err := DefaultBranch(context.Background(), dir); err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}

	var calls int
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	runGit = func(ctx context.Context, d string, args ...string) (string, error) {
		calls++
		return prev(ctx, d, args...)
	}

	if _, err := DefaultBranch(context.Background(), dir); err != nil {
		t.Fatalf("DefaultBranch (cached): %v", err)
	}
	if calls != 0 {
		t.Errorf("cached resolution ran %d git calls, want 0", calls)
	}
}
