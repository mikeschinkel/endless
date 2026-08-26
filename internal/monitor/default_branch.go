package monitor

import (
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

// ErrDefaultBranchUnresolved is returned when every resolution step fell
// through. It is a real error on purpose: substituting `main` here is exactly
// the bug this resolver exists to remove, so the caller decides what to do with
// "I don't know" and no caller may silently guess.
var ErrDefaultBranchUnresolved = errors.New("cannot resolve the repository's default branch")

// defaultBranchCache memoizes resolution per repo directory. `session monitor`
// re-probes every row every 2s; without this each row would pay two to four git
// invocations per tick just to re-derive a constant.
var defaultBranchCache sync.Map // repoDir -> defaultBranchResult

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
func DefaultBranch(repoDir string) (string, error) {
	if cached, ok := defaultBranchCache.Load(repoDir); ok {
		res := cached.(defaultBranchResult)
		return res.branch, res.err
	}
	branch, err := resolveDefaultBranch(repoDir)
	defaultBranchCache.Store(repoDir, defaultBranchResult{branch: branch, err: err})
	return branch, err
}

// resetDefaultBranchCache drops every memoized answer. Tests only: the cache is
// keyed by directory, and a test that rewrites a fixture repo's refs would
// otherwise read the previous test's verdict.
func resetDefaultBranchCache() {
	defaultBranchCache.Range(func(k, _ any) bool {
		defaultBranchCache.Delete(k)
		return true
	})
}

// resolveDefaultBranch is DefaultBranch without the memoization.
func resolveDefaultBranch(repoDir string) (string, error) {
	if b := ReadDefaultBranchConfig(repoDir); b != "" {
		if branchIfExists(repoDir, b) == "" {
			return "", fmt.Errorf(
				"%w: .endless/config.json sets default_branch %q, which does not exist here",
				ErrDefaultBranchUnresolved, b)
		}
		return b, nil
	}
	if b := branchIfExists(repoDir, originHeadBranch(repoDir)); b != "" {
		return b, nil
	}
	if b := branchIfExists(repoDir, gitConfigValue(repoDir, "init.defaultBranch")); b != "" {
		return b, nil
	}
	for _, candidate := range []string{"main", "master"} {
		if b := branchIfExists(repoDir, candidate); b != "" {
			return b, nil
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
func originHeadBranch(repoDir string) string {
	out, err := runGit(repoDir, "symbolic-ref", "--short", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "origin/")
}

// gitConfigValue returns a git config value, or "" when unset.
func gitConfigValue(repoDir, key string) string {
	out, err := runGit(repoDir, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// branchIfExists returns name when it resolves to a commit in repoDir, else "".
// Empty input is "" straight back, so callers can chain candidate sources
// without checking each one first.
func branchIfExists(repoDir, name string) string {
	if name == "" {
		return ""
	}
	if _, err := runGit(repoDir, "rev-parse", "--verify", "--quiet", name+"^{commit}"); err != nil {
		return ""
	}
	return name
}
