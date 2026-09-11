package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// E-1940 — one answer to "what is this repo's default branch?".
//
// Two probes used to hardcode `main`: the unsettled probe's rev-list and the
// reaper's condition 4. On a repo whose default branch is anything else, both
// exit 128 forever — the display showed a permanent false all-clear and the
// reaper skipped every candidate it was asked about. Absorbs E-1166, whose
// Python-side finding (origin/HEAD is unset on a fresh clone) is why the order
// below has four steps rather than one.
//
// The order is mirrored by endless.worktree_cmd._default_base_branch on the
// Python side, and tests/test_default_branch_parity.py asserts the two agree
// case for case rather than trusting this comment.
//
// One behaviour is deliberately NOT mirrored: the interrupt short-circuit below
// (E-2130). It exists because this resolver runs inside long-lived processes —
// `session monitor`'s 2s render loop, and the background jobs the monitor's
// refresh fires — that outlive the signal and would otherwise file an incident
// about it. (It named the PreToolUse/PostToolUse reaper until E-2128 took the
// reaper off the hook path entirely; the argument is unchanged, only its
// example.) The
// Python resolver runs inside `worktree land`, a foreground command a Ctrl-C
// kills outright, so there is no surviving process to mislead. The parity cases
// are all about which branch a repository resolves to, and none of them signal
// anything, so this adds no case for the Python side to match.

// ErrDefaultBranchUnresolved is returned when every resolution step fell
// through. It is a real error on purpose: substituting `main` here is exactly
// the bug this resolver exists to remove, so the caller decides what to do with
// "I don't know" and no caller may silently guess.
var ErrDefaultBranchUnresolved = errors.New("cannot resolve the repository's default branch")

// defaultBranchCache memoizes resolution per REPOSITORY, keyed on the git
// common dir. `session monitor` re-probes every row every 2s; without this each
// row would pay two to four git invocations per tick just to re-derive a
// constant.
//
// The key was the worktree directory until E-2128. A repository with 135
// worktrees therefore held 135 identical entries and paid the resolution 135
// times per process, even though the answer is a property of the repository and
// every one of those directories shares its refs. Keying on the common dir — the
// same identity the unlanded cache is addressed by — collapses them to one.
//
// The CONTEXT is deliberately not part of the key, and must never become part of
// it: two callers holding different contexts would miss each other's entries
// forever, which is a memo that grows without ever being read.
var defaultBranchCache sync.Map // gitCommonDir (or repoDir) -> defaultBranchResult

type defaultBranchResult struct {
	branch string
	err    error
}

// DefaultBranch returns the branch that repoDir's work lands into, resolving in
// this order:
//
//  1. `.endless/config.json`'s `default_branch`, when set — explicit beats
//     detection.
//  2. `git symbolic-ref --short refs/remotes/origin/HEAD`, minus the `origin/`.
//  3. `git config init.defaultBranch`.
//  4. `main`, then `master`, whichever exists.
//
// Every step requires the name to resolve to a commit here, and every caller
// may assume the returned branch exists. That check is what makes step 3 safe:
// `init.defaultBranch` is a preference belonging to the machine, not a fact
// about this repo, and it is very commonly `main` on a machine that also has
// `master` repos — accepting it unverified would reproduce the hardcoded-`main`
// bug through a different door.
//
// Step 1 is the one place a failed check does NOT fall through: an explicit
// `default_branch` naming a branch that does not exist is a typo in the
// project's own config, and quietly detecting around it would hide the very
// thing the operator wrote down.
//
// repoDir may be a worktree; `.endless/config.json` is tracked, so it is
// present there too, and git resolves refs through the shared object store.
func DefaultBranch(ctx context.Context, repoDir string) (string, error) {
	key := defaultBranchCacheKey(ctx, repoDir)
	if cached, ok := defaultBranchCache.Load(key); ok {
		res := cached.(defaultBranchResult)
		return res.branch, res.err
	}
	branch, err := resolveDefaultBranch(ctx, repoDir)
	if errors.Is(err, ErrGitInterrupted) {
		// Not memoized, and deliberately so (E-2130). Every other outcome here is
		// a fact about the repository and stays true until its refs change; an
		// interrupt is a fact about the process that asked, and it stops being
		// true the moment the signal has been handled. Caching it would let one
		// Ctrl-C answer for every later probe of this directory — the reaper runs
		// on PreToolUse/PostToolUse in a process that keeps going, so that is not
		// one lost tick but a permanently poisoned cache.
		return "", err
	}
	defaultBranchCache.Store(key, defaultBranchResult{branch: branch, err: err})
	return branch, err
}

// defaultBranchCacheKey identifies the REPOSITORY repoDir belongs to, so every
// worktree of one repo shares a single memo entry.
//
// It falls back to repoDir itself when the common dir cannot be resolved. That
// keeps keying a pure optimization: a directory git will not answer about still
// gets a correct (if unshared) memo, rather than every such directory colliding
// on one empty key and answering for each other.
func defaultBranchCacheKey(ctx context.Context, repoDir string) string {
	common, err := gitCommonDir(ctx, repoDir)
	if err != nil || common == "" {
		return repoDir
	}
	return common
}

// resetDefaultBranchCache drops every memoized answer. Tests only: the cache is
// keyed by directory, and a test that rewrites a fixture repo's refs would
// otherwise read the previous test's verdict.
func resetDefaultBranchCache() {
	defaultBranchCache.Range(func(k, _ any) bool {
		defaultBranchCache.Delete(k)
		return true
	})
	// The key is now derived from the git common dir, so a stale common-dir memo
	// would hand the next resolution the previous fixture's key. Dropping both
	// together is what makes "reset the cache" mean what its name says.
	resetGitCommonDirCache()
}

// resolveDefaultBranch is DefaultBranch without the memoization.
//
// Every step below may fall through, and that is the whole design — a step that
// cannot name the branch hands the question to the next one. An INTERRUPTED step
// is the one thing that must not fall through (E-2130): it was killed before it
// could answer, so it has established nothing about this repository, and asking
// the next step is asking a question whose answer was already lost. Worse, the
// fall-through would end at ErrDefaultBranchUnresolved — a claim that the repo
// has no discoverable default branch, made on the strength of probes that never
// ran, which is what put ERR-0011 incidents on innocent worktrees.
//
// Each helper therefore returns an error for that case ALONE; see branchIfExists.
func resolveDefaultBranch(ctx context.Context, repoDir string) (string, error) {
	if name := ReadDefaultBranchConfig(repoDir); name != "" {
		b, err := branchIfExists(ctx, repoDir, name)
		if err != nil {
			return "", err
		}
		if b == "" {
			return "", fmt.Errorf(
				"%w: .endless/config.json sets default_branch %q, which does not exist here",
				ErrDefaultBranchUnresolved, name)
		}
		return b, nil
	}

	origin, err := originHeadBranch(ctx, repoDir)
	if err != nil {
		return "", err
	}
	if b, err := branchIfExists(ctx, repoDir, origin); err != nil || b != "" {
		return b, err
	}

	// Derived only now, not alongside origin/HEAD above: the steps stay lazy, so
	// a repo that resolves at step 2 still costs exactly the two git calls it
	// cost before, and the invocation order the Python mirror follows is intact.
	configured, err := gitConfigValue(ctx, repoDir, "init.defaultBranch")
	if err != nil {
		return "", err
	}
	if b, err := branchIfExists(ctx, repoDir, configured); err != nil || b != "" {
		return b, err
	}

	for _, candidate := range []string{"main", "master"} {
		if b, err := branchIfExists(ctx, repoDir, candidate); err != nil || b != "" {
			return b, err
		}
	}
	return "", ErrDefaultBranchUnresolved
}

// ReadDefaultBranchConfig reads the `default_branch` field from
// <repoDir>/.endless/config.json, or "" when the file is absent, unreadable, or
// has no such field. Sibling of ReadWorktreeTTLConfig, and deliberately silent
// for the same reason: an unreadable project config is not itself the failure
// the caller is asking about.
func ReadDefaultBranchConfig(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, ".endless", "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.DefaultBranch)
}

// originHeadBranch returns the branch origin/HEAD points at, without the
// `origin/` prefix, or "" when the symbolic ref is unset. It is unset on a
// fresh clone until `git remote set-head` runs — E-1166's original finding, and
// the whole reason the steps after it exist.
func originHeadBranch(ctx context.Context, repoDir string) (string, error) {
	out, err := runGit(ctx, repoDir, "symbolic-ref", "--short", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", interruptOnly(err)
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "origin/"), nil
}

// gitConfigValue returns a git config value, or "" when unset.
func gitConfigValue(ctx context.Context, repoDir, key string) (string, error) {
	out, err := runGit(ctx, repoDir, "config", "--get", key)
	if err != nil {
		return "", interruptOnly(err)
	}
	return strings.TrimSpace(out), nil
}

// branchIfExists returns name when it resolves to a commit in repoDir, else "".
// Empty input is "" straight back, so callers can chain candidate sources
// without checking each one first.
//
// The error return carries exactly one thing: a git child killed by a signal
// (E-2130). Every ORDINARY failure is still absorbed — `rev-parse --verify` exits
// non-zero for a name that is not a branch, and that is this resolver's normal
// answer of "not this candidate", not a fault to report. So ("", nil) means the
// step fell through, and a non-nil error always means interrupted. Callers can
// therefore bail on any error without re-testing what kind it is, and a
// non-nil error always comes back paired with an empty name.
func branchIfExists(ctx context.Context, repoDir, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if _, err := runGit(ctx, repoDir, "rev-parse", "--verify", "--quiet", name+"^{commit}"); err != nil {
		return "", interruptOnly(err)
	}
	return name, nil
}

// interruptOnly reduces a git failure to the single class the fall-through above
// must not absorb, returning err when a signal killed the child and nil for
// every ordinary failure. It is the one place the distinction is made, so the
// helpers state their contract by calling it rather than each re-deriving it.
func interruptOnly(err error) error {
	if errors.Is(err, ErrGitInterrupted) {
		return err
	}
	return nil
}
