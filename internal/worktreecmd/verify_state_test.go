package worktreecmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// gitRepo makes a repository with one commit on main and a task branch, and
// returns its path.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.email", "t@example.com")
	run(t, dir, "config", "user.name", "t")
	write(t, dir, "code.go", "package x\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "base")
	run(t, dir, "checkout", "-q", "-b", "task/7")
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = events.SanitizedGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, rel, content string) {
	t.Helper()
	write(t, dir, rel, content)
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "change "+rel)
}

func TestPassIsFreshAtThePassedCommit(t *testing.T) {
	repo := gitRepo(t)
	sha := run(t, repo, "rev-parse", "HEAD")
	if stale, why := PassIsStale(repo, sha, "task/7"); stale {
		t.Errorf("a pass at the branch tip reads stale: %s", why)
	}
}

// TestLedgerCommitsDoNotStaleThePass is the reason the check is a content diff:
// Endless commits its own ledger entries onto the branch after a verify.
func TestLedgerCommitsDoNotStaleThePass(t *testing.T) {
	repo := gitRepo(t)
	sha := run(t, repo, "rev-parse", "HEAD")
	commit(t, repo, ".endless/db-ledger/2026-10.jsonl", "{}\n")
	commit(t, repo, ".endless/verbs.jsonl", "{}\n")
	if stale, why := PassIsStale(repo, sha, "task/7"); stale {
		t.Errorf("Endless's own ledger commits staled the pass: %s", why)
	}
}

func TestACodeCommitStalesThePass(t *testing.T) {
	repo := gitRepo(t)
	sha := run(t, repo, "rev-parse", "HEAD")
	commit(t, repo, "code.go", "package x\n\nvar y = 1\n")
	stale, why := PassIsStale(repo, sha, "task/7")
	if !stale {
		t.Fatal("a code commit after the pass did not stale it")
	}
	if !strings.Contains(why, "has changed since the verify passed") {
		t.Errorf("reason does not say what happened: %s", why)
	}
}

// TestASuiteEditStalesThePass pins that a task's own suite is NOT an Endless
// file: rewriting the test after it passed is exactly what must be re-run.
func TestASuiteEditStalesThePass(t *testing.T) {
	repo := gitRepo(t)
	sha := run(t, repo, "rev-parse", "HEAD")
	commit(t, repo, ".endless/tasks/e-7/verify.sh", "exit 0\n")
	if stale, _ := PassIsStale(repo, sha, "task/7"); !stale {
		t.Error("editing the verify suite after the pass did not stale it")
	}
}

func TestAnUnprovablePassIsStale(t *testing.T) {
	repo := gitRepo(t)
	for name, sha := range map[string]string{
		"no commit recorded": "",
		"unknown commit":     "0000000000000000000000000000000000000001",
	} {
		t.Run(name, func(t *testing.T) {
			if stale, _ := PassIsStale(repo, sha, "task/7"); !stale {
				t.Error("a pass that cannot be compared read as fresh")
			}
		})
	}
}
