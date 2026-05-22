// Package events: write-time auto-commit of endless-managed files (E-1206).
//
// One caller:
//   - cmd/endless-event: commits the just-appended db-ledger segment after every
//     Writer.Append (E-1206).
//
// It flows through commitPaths, which decides amend-vs-new-commit based on
// HEAD's subject (must match the subject we're about to commit), pushed
// status (never amend a commit reachable from origin/*), and index hygiene
// (never bundle unrelated user-staged work into our amend).

package events

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LedgerCommitSubject is the exact `git log --format=%s` value for ledger
// auto-commits (E-1206). The amend decision keys off this prefix.
const LedgerCommitSubject = "Endless: record ledger entry"

// gitRedirectVars lists env vars that override git's repo resolution
// (E-1309). Stripped from the subprocess env so `git -C <projectRoot>`
// is authoritative. Without this, a stray GIT_DIR somewhere in the
// caller chain silently redirects auto-commits to a linked worktree's
// gitdir.
var gitRedirectVars = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_COMMON_DIR",
	"GIT_NAMESPACE",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

// CommitLedgerSegment commits the given ledger segment path on the
// project's git repo (E-1206). Thin wrapper around commitPaths.
func CommitLedgerSegment(projectRoot, segmentRelPath string) error {
	return commitPaths(
		projectRoot,
		[]string{segmentRelPath},
		LedgerCommitSubject,
		".endless/db-ledger/*.jsonl",
	)
}

// commitPaths makes one commit containing exactly the named paths.
//
// Decision:
//
//	HEAD subject == subject
//	AND HEAD not reachable from any origin/* ref
//	AND index has no staged paths outside excludeGlob
//	→ git add -- <paths>...
//	  git commit -o <paths>... --amend --no-edit
//	otherwise
//	→ git add -- <paths>...
//	  git commit -o <paths>... -m subject
//
// The `git add` step happens in both branches because `git commit -o
// <path>` can't resolve a pathspec for an untracked file — even on amend.
// For ledger writes (E-1206) the file already exists after the first commit,
// but `git add` is still a cheap no-op for unchanged content.
//
// excludeGlob is a single pathspec glob (e.g. ".endless/db-ledger/*.jsonl")
// that scopes the index-cleanliness check — staged changes inside that glob
// don't block amend; staged changes outside it do.
//
// Fails loudly: returns a non-nil error if the project is not a git repo,
// if projectRoot resolves to a linked worktree (E-1309), or if any git
// subprocess returns non-zero. Per Mike's "fail loudly until we know what
// failure modes look like" stance.
func commitPaths(projectRoot string, paths []string, subject, excludeGlob string) error {
	if err := ensureGitRepo(projectRoot); err != nil {
		return err
	}
	if err := ensureMainCheckout(projectRoot, paths); err != nil {
		return err
	}

	canAmend, err := canAmend(projectRoot, subject, excludeGlob)
	if err != nil {
		return err
	}

	addArgs := append([]string{"add", "--"}, paths...)
	if err := runGit(projectRoot, addArgs...); err != nil {
		return err
	}

	commitArgs := append([]string{"commit"}, prefixOnly(paths)...)
	if canAmend {
		commitArgs = append(commitArgs, "--amend", "--no-edit")
	} else {
		commitArgs = append(commitArgs, "-m", subject)
	}
	return runGit(projectRoot, commitArgs...)
}

// prefixOnly returns a flat slice of "-o", path, "-o", path, ... so the
// resulting `git commit -o A -o B ...` stages only those paths from the
// working tree and commits exactly that set.
func prefixOnly(paths []string) []string {
	out := make([]string, 0, len(paths)*2)
	for _, p := range paths {
		out = append(out, "-o", p)
	}
	return out
}

// ensureGitRepo returns nil if projectRoot is inside a git work tree,
// otherwise returns a descriptive error.
func ensureGitRepo(projectRoot string) error {
	out, err := runGitOutput(projectRoot, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return fmt.Errorf("project root %q is not a git work tree: %s",
			projectRoot, strings.TrimSpace(out))
	}
	return nil
}

// ensureMainCheckout verifies projectRoot resolves to the main checkout's
// git repo, not a linked worktree (E-1309). In a main checkout,
// `--git-dir` and `--git-common-dir` are equal; in a linked worktree
// they differ. Auto-commits must always land on main — a commit that
// lands on a linked worktree's branch creates a rogue commit that
// conflicts on rebase. Refuse loudly with the resolved values and the
// paths we were about to stage, so future occurrences are self-diagnosing.
//
// Note (E-1281 sandbox): when the per-worktree sandbox is active for an
// endless self-dev worktree, ledger writes are routed to the sandbox
// DB at ~/.cache/endless/sandboxes/... and don't reach this function
// at all. This guard only fires for the real-DB path.
func ensureMainCheckout(projectRoot string, paths []string) error {
	gitDir, err := runGitOutput(projectRoot, "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("rev-parse --git-dir at %q: %w", projectRoot, err)
	}
	commonDir, err := runGitOutput(projectRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("rev-parse --git-common-dir at %q: %w", projectRoot, err)
	}
	gd := strings.TrimSpace(gitDir)
	cd := strings.TrimSpace(commonDir)
	if gd != cd {
		return fmt.Errorf(
			"refusing auto-commit: projectRoot %q resolves to a linked worktree "+
				"(git-dir=%q, common-dir=%q, paths=%v). Auto-commits must land on "+
				"main, not on a task branch. See E-1309.",
			projectRoot, gd, cd, paths,
		)
	}
	return nil
}

// canAmend returns true iff all three preconditions hold:
//  1. HEAD's subject equals the subject we're about to commit.
//  2. HEAD is not reachable from any origin/* remote-tracking ref.
//  3. Index has no staged paths outside excludeGlob.
//
// Errors only on subprocess failure; a "no" answer to any precondition
// returns (false, nil).
func canAmend(projectRoot, subject, excludeGlob string) (bool, error) {
	headSubj, err := runGitOutput(projectRoot, "log", "-1", "--format=%s")
	if err != nil {
		// New repo with no commits yet: HEAD doesn't exist. Cannot amend.
		return false, nil
	}
	if strings.TrimSpace(headSubj) != subject {
		return false, nil
	}

	pushed, err := runGitOutput(projectRoot,
		"for-each-ref", "--contains", "HEAD", "refs/remotes/origin/",
	)
	if err != nil {
		return false, fmt.Errorf("check origin reachability: %w", err)
	}
	if strings.TrimSpace(pushed) != "" {
		return false, nil
	}

	staged, err := runGitOutput(projectRoot,
		"diff-index", "--cached", "--name-only", "HEAD",
		"--", ":!"+excludeGlob,
	)
	if err != nil {
		return false, fmt.Errorf("check staged paths: %w", err)
	}
	if strings.TrimSpace(staged) != "" {
		return false, nil
	}

	return true, nil
}

// runGit runs `git -C projectRoot <args>` with a sanitized env (E-1309)
// and returns an error with stderr included if the command fails.
func runGit(projectRoot string, args ...string) error {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = sanitizedGitEnv()
	debugLogGit(projectRoot, args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGitOutput runs `git -C projectRoot <args>` with a sanitized env
// (E-1309) and returns stdout. stderr is folded into the error on
// non-zero exit.
func runGitOutput(projectRoot string, args ...string) (string, error) {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = sanitizedGitEnv()
	debugLogGit(projectRoot, args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// sanitizedGitEnv returns os.Environ() with every variable in
// gitRedirectVars stripped (E-1309). Strips at the subprocess boundary
// so `git -C <projectRoot>` cannot be overridden by an inherited
// GIT_DIR or sibling var pointing at a linked worktree's gitdir.
func sanitizedGitEnv() []string {
	skip := make(map[string]struct{}, len(gitRedirectVars))
	for _, k := range gitRedirectVars {
		skip[k] = struct{}{}
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// debugLogGit logs the git invocation to stderr when ENDLESS_DEBUG_GIT=1
// (E-1309). Off by default. Use to capture the env+args at the moment of
// a misrouted auto-commit, so the source of misdirection becomes visible.
func debugLogGit(projectRoot string, args []string) {
	if os.Getenv("ENDLESS_DEBUG_GIT") != "1" {
		return
	}
	fmt.Fprintf(os.Stderr,
		"[endless-debug-git] -C %s %s (parent GIT_DIR=%q GIT_WORK_TREE=%q)\n",
		projectRoot, strings.Join(args, " "),
		os.Getenv("GIT_DIR"), os.Getenv("GIT_WORK_TREE"),
	)
}
