// Package events: write-time auto-commit of endless-managed files (E-1206 / E-1275).
//
// Two callers:
//   - cmd/endless-event: commits the just-appended db-ledger segment after every
//     Writer.Append (E-1206).
//   - cmd/endless-hook:  commits the just-written plan snapshot pair (md + json)
//     after every snapshotPlanFile call (E-1275).
//
// Both flow through commitPaths, which decides amend-vs-new-commit based on
// HEAD's subject (must match the subject we're about to commit), pushed
// status (never amend a commit reachable from origin/*), and index hygiene
// (never bundle unrelated user-staged work into our amend).

package events

import (
	"fmt"
	"os/exec"
	"strings"
)

// LedgerCommitSubject is the exact `git log --format=%s` value for ledger
// auto-commits (E-1206). The amend decision keys off this prefix.
const LedgerCommitSubject = "Endless: record ledger entry"

// SnapshotCommitSubject is the exact `git log --format=%s` value for plan
// snapshot auto-commits (E-1275).
const SnapshotCommitSubject = "Endless: snapshot plan"

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

// CommitSnapshotPair commits the .md/.json snapshot pair on the project's
// git repo (E-1275). Thin wrapper around commitPaths.
func CommitSnapshotPair(projectRoot, mdRelPath, jsonRelPath string) error {
	return commitPaths(
		projectRoot,
		[]string{mdRelPath, jsonRelPath},
		SnapshotCommitSubject,
		".endless/plans/snapshots/*",
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
// For snapshot writes (E-1275) each call introduces brand-new files; for
// ledger writes (E-1206) the file already exists after the first commit
// but `git add` is still a cheap no-op for unchanged content.
//
// excludeGlob is a single pathspec glob (e.g. ".endless/db-ledger/*.jsonl")
// that scopes the index-cleanliness check — staged changes inside that glob
// don't block amend; staged changes outside it do.
//
// Fails loudly: returns a non-nil error if the project is not a git repo,
// or if any git subprocess returns non-zero. Per Mike's "fail loudly until
// we know what failure modes look like" stance.
func commitPaths(projectRoot string, paths []string, subject, excludeGlob string) error {
	if err := ensureGitRepo(projectRoot); err != nil {
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
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("project root %q is not a git work tree: %s",
			projectRoot, strings.TrimSpace(string(out)))
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

// runGit runs `git -C projectRoot <args>` and returns an error with stderr
// included if the command fails.
func runGit(projectRoot string, args ...string) error {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGitOutput runs `git -C projectRoot <args>` and returns stdout. stderr
// is folded into the error on non-zero exit.
func runGitOutput(projectRoot string, args ...string) (string, error) {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
