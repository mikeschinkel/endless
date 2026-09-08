package events

// Orphaned ledger commits at a task branch's base, identified by CONTENT.
//
// ED-1553 settled the rule this file applies: a worktree's fork point is
// identified by what the ledger holds, never by the SHA that held it. canAmend
// applies that rule forward — it refuses to amend a ledger tip whose content a
// task branch still carries. LedgerOrphans applies the same rule backward, after
// the fact: given a branch that already conflicts, which of its ledger commits
// carry content the base branch demonstrably already has?
//
// The comparison is a byte-prefix test, not the tree-identity test canAmend
// uses, and the difference is the passage of time. At amend time the two ledger
// trees are still identical, so identity is enough. By the time a land conflicts
// the amend has happened: the branch holds the segment as it was at the fork
// point and the base holds that same segment with more events appended after
// it. Ledger segments are append-only JSONL, so "already on base" means the
// branch's bytes are a prefix of the base's bytes — identity is just the
// degenerate case where nothing was appended.
//
// Proving that is what makes a prescription safe to print. `endless worktree
// diagnose` (E-1957) refuses to suggest a recovery it cannot prove, and
// "dropping these commits loses nothing" is exactly the kind of claim that
// needs a proof rather than a guess about what a subject line means.

import (
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"strings"
)

// LedgerCommit is one commit in `base..branch`, with the evidence for whether
// the base branch already holds its ledger content.
type LedgerCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	// Amendable is true when Subject is one an auto-commit may amend in place,
	// which is what makes an orphan possible in the first place.
	Amendable bool `json:"amendable"`
	// LedgerPaths are the repo-relative ledger files this commit touches.
	LedgerPaths []string `json:"ledger_paths"`
	// SubsumedByBase is true when every path in LedgerPaths is, at this commit,
	// a byte-prefix of the same path on base. Never true for a commit that
	// touches no ledger path — there is nothing to have been subsumed.
	SubsumedByBase bool `json:"subsumed_by_base"`
	// Reason names the first path that failed the prefix test, so a "no" is as
	// inspectable as a "yes". Empty when SubsumedByBase is true.
	Reason string `json:"reason,omitempty"`
}

// LedgerOrphanReport is the wire shape of `endless-go worktree ledger-orphans`.
type LedgerOrphanReport struct {
	Base    string         `json:"base"`
	Branch  string         `json:"branch"`
	Commits []LedgerCommit `json:"commits"`
	// Orphans holds the SHAs of commits that are both Amendable and
	// SubsumedByBase — the ones whose removal is provably lossless.
	Orphans []string `json:"orphans"`
	// ContiguousAtBase is true when the orphans are exactly a prefix of
	// `base..branch`, which is the shape land's own orphan-drop handles.
	ContiguousAtBase bool `json:"contiguous_at_base"`
	// MidBranch is true when at least one orphan sits behind a non-orphan.
	// Land deliberately leaves those alone (dropping a mid-branch commit can
	// delete work), so they are reported rather than removed.
	MidBranch bool `json:"mid_branch"`
	// LastContiguousOrphan is the newest SHA of the contiguous-at-base run, or
	// "" when there is none. It is the argument to `git rebase --onto <base>`.
	LastContiguousOrphan string `json:"last_contiguous_orphan,omitempty"`
}

// ledgerDirRel is the ledger directory as a repo-relative git pathspec.
var ledgerDirRel = path.Join(".endless", LedgerDirName)

// LedgerOrphans classifies every commit in `base..branch` by whether the base
// branch already holds its ledger content.
//
// base and branch are anything git resolves — a branch name or a SHA. A land
// passes the SHAs captured at the moment of failure, so the answer describes
// the conflict rather than whatever the refs have moved on to since.
func LedgerOrphans(projectRoot, base, branch string) (report *LedgerOrphanReport, err error) {
	report = &LedgerOrphanReport{
		Base:    base,
		Branch:  branch,
		Commits: []LedgerCommit{},
		Orphans: []string{},
	}

	// --reverse: oldest first, so index 0 is the commit at the branch's base
	// and "contiguous at base" is a prefix of the slice.
	out, err := runGitOutput(projectRoot,
		"log", "--reverse", "--format=%H%x1f%s", base+".."+branch,
	)
	if err != nil {
		return nil, fmt.Errorf("list commits %s..%s: %w", base, branch, err)
	}

	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sha, subject, found := strings.Cut(line, "\x1f")
		if !found {
			return nil, fmt.Errorf("unparsable git log line %q", line)
		}
		lc := LedgerCommit{
			SHA:         sha,
			Subject:     subject,
			Amendable:   subject == LedgerCommitSubject,
			LedgerPaths: []string{},
		}
		lc.LedgerPaths, err = ledgerPathsAt(projectRoot, sha)
		if err != nil {
			return nil, err
		}
		lc.SubsumedByBase, lc.Reason, err = subsumedByBase(
			projectRoot, base, sha, lc.LedgerPaths,
		)
		if err != nil {
			return nil, err
		}
		report.Commits = append(report.Commits, lc)
	}

	prefix := true
	for _, c := range report.Commits {
		orphan := c.Amendable && c.SubsumedByBase
		if orphan {
			report.Orphans = append(report.Orphans, c.SHA)
			if prefix {
				report.LastContiguousOrphan = c.SHA
			} else {
				report.MidBranch = true
			}
			continue
		}
		prefix = false
	}
	report.ContiguousAtBase = len(report.Orphans) > 0 && !report.MidBranch
	return report, nil
}

// ledgerPathsAt returns the ledger files one commit touches, repo-relative.
//
// --root is required: a branch whose first commit is the repository root
// commit has no parent to diff against, and without it diff-tree reports
// nothing rather than the commit's whole tree.
func ledgerPathsAt(projectRoot, sha string) (paths []string, err error) {
	out, err := runGitOutput(projectRoot,
		"diff-tree", "--root", "-r", "--no-commit-id", "--name-only", sha,
		"--", ledgerDirRel,
	)
	if err != nil {
		return nil, fmt.Errorf("list ledger paths at %s: %w", sha, err)
	}
	paths = []string{}
	for _, p := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(p) != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// subsumedByBase reports whether every one of paths, as it stands at sha, is a
// byte-prefix of the same path on base. Returns the failing path as the reason
// when it is not.
//
// A commit that touches no ledger path is never subsumed: the question is
// whether base already holds this commit's ledger content, and a commit with no
// ledger content has not answered it.
func subsumedByBase(projectRoot, base, sha string, paths []string) (ok bool, reason string, err error) {
	if len(paths) == 0 {
		return false, "commit touches no ledger file", nil
	}
	for _, p := range paths {
		var mine, theirs []byte
		mine, err = blobAt(projectRoot, sha, p)
		if err != nil {
			return false, "", err
		}
		if mine == nil {
			// The commit DELETED the path. Deletion is not append-only ledger
			// content and nothing on base can subsume it.
			return false, fmt.Sprintf("%s is deleted by this commit", p), nil
		}
		theirs, err = blobAt(projectRoot, base, p)
		if err != nil {
			return false, "", err
		}
		if theirs == nil {
			return false, fmt.Sprintf("%s does not exist on %s", p, base), nil
		}
		if !bytes.HasPrefix(theirs, mine) {
			return false, fmt.Sprintf(
				"%s on %s is not an append-only extension of this commit's copy",
				p, base,
			), nil
		}
	}
	return true, "", nil
}

// blobAt returns the bytes of one path at one revision, or nil when the path
// does not exist there. A missing path is an ordinary answer, not an error:
// asking "does base still have this file" is the whole point of the call.
func blobAt(projectRoot, rev, relPath string) (content []byte, err error) {
	spec := rev + ":" + relPath
	out, stderr, err := runGitBytes(projectRoot, "cat-file", "blob", spec)
	if err == nil {
		return out, nil
	}
	// git reports a missing path several ways depending on whether the tree,
	// the entry, or the revision is the thing that is absent.
	msg := string(stderr)
	for _, miss := range []string{
		"does not exist", "exists on disk, but not in", "unknown revision",
		"Not a valid object name", "not a valid object name",
	} {
		if strings.Contains(msg, miss) {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("read %s: %w: %s", spec, err, strings.TrimSpace(msg))
}

// runGitBytes runs git with stdout and stderr kept apart, returning raw bytes.
//
// Distinct from runGitOutput, which folds stderr into stdout: that is fine for
// text output a human reads, and wrong for `cat-file blob`, whose stdout is
// file content a byte-comparison depends on.
func runGitBytes(projectRoot string, args ...string) (stdout, stderr []byte, err error) {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = sanitizedGitEnv()
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	debugLogGit(projectRoot, args)
	stdout, err = cmd.Output()
	return stdout, errBuf.Bytes(), err
}
