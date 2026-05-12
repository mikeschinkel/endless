// Package events: write-time auto-commit of db-ledger segments (E-1206).
//
// CommitLedgerSegment is called after every successful Writer.Append so each
// ledger event becomes part of git history immediately. To bound commit
// volume, successive ledger commits between non-ledger commits are amended
// into a single rolling commit — never amends a pushed commit, never
// bundles unrelated staged user work.

package events

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// LedgerCommitSubject is the exact `git log --format=%s` value for ledger
// auto-commits. The amend decision keys off this prefix.
const LedgerCommitSubject = "Endless: record ledger entry"

// CommitLedgerSegment commits the given ledger segment path on the
// project's git repo.
//
// Decision:
//
//	HEAD subject == LedgerCommitSubject
//	AND HEAD not reachable from any origin/* ref
//	AND index has no staged paths outside the ledger dir
//	→ git commit -o <path> --amend --no-edit
//	otherwise
//	→ git add <path>
//	  git commit -o <path> -m LedgerCommitSubject
//
// Fails loudly: returns a non-nil error if the project is not a git repo,
// or if any git subprocess returns non-zero. Per E-1208's pattern.
//
// projectRoot must be an absolute path to a directory containing a .git
// (or pointing into a worktree). segmentRelPath is the segment path
// relative to projectRoot (e.g. ".endless/db-ledger/db-entries-a7f3-000001.jsonl").
func CommitLedgerSegment(projectRoot, segmentRelPath string) error {
	if err := ensureGitRepo(projectRoot); err != nil {
		return err
	}

	canAmend, err := canAmendLedgerCommit(projectRoot, segmentRelPath)
	if err != nil {
		return err
	}

	if canAmend {
		return runGit(projectRoot, "commit", "-o", segmentRelPath, "--amend", "--no-edit")
	}

	if err := runGit(projectRoot, "add", "--", segmentRelPath); err != nil {
		return err
	}
	return runGit(projectRoot, "commit", "-o", segmentRelPath, "-m", LedgerCommitSubject)
}

// ensureGitRepo returns nil if projectRoot is inside a git work tree,
// otherwise returns a descriptive error. Fail-loudly is intentional per
// E-1206's resolved design: Mike's projects are all git-tracked, and
// surfacing the failure beats silently dropping commits in v1.
func ensureGitRepo(projectRoot string) error {
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("project root %q is not a git work tree: %s",
			projectRoot, strings.TrimSpace(string(out)))
	}
	return nil
}

// canAmendLedgerCommit returns true iff all three preconditions hold:
//  1. HEAD's subject == LedgerCommitSubject
//  2. HEAD is not reachable from any origin/* remote-tracking ref
//  3. Index has no staged changes outside the ledger directory
//
// Errors only on subprocess failure; a "no" answer to any precondition
// returns (false, nil).
func canAmendLedgerCommit(projectRoot, segmentRelPath string) (bool, error) {
	subj, err := runGitOutput(projectRoot, "log", "-1", "--format=%s")
	if err != nil {
		// New repo with no commits yet: HEAD doesn't exist. Cannot amend.
		return false, nil
	}
	if strings.TrimSpace(subj) != LedgerCommitSubject {
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

	ledgerDir := filepath.Dir(segmentRelPath)
	staged, err := runGitOutput(projectRoot,
		"diff-index", "--cached", "--name-only", "HEAD",
		"--", ":!"+ledgerDir+"/*.jsonl",
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
