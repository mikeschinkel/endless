// Package events: write-time auto-commit of endless-managed files (E-1206).
//
// One caller:
//   - cmd/endless-event: commits the just-appended db-ledger segment after every
//     Writer.Append (E-1206).
//
// It flows through commitPaths, which decides amend-vs-new-commit based on
// HEAD's subject (must match the subject we're about to commit), shared-ref
// status (never amend a commit reachable from any ref besides the current
// branch — a landed worktree branch, a remote-tracking ref, or a tag),
// ledger-content sharing (never amend a ledger tip whose content a task branch
// still holds, even once a history rewrite has erased the SHA that proved it —
// E-1955), and index hygiene (never bundle unrelated user-staged work into our
// amend).

package events

import (
	"fmt"
	"os"
	"os/exec"
	"path"
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

// CommitDoc commits a single version-controlled document mirror file
// (E-1747: `.endless/<kind>/<ID>.md`) directly on the project's main checkout.
// Used for content that has no worktree of its own — a decision body authored
// from outside any task worktree. Thin wrapper around commitPaths; inherits
// its main-checkout enforcement (ensureMainCheckout) and GIT_DIR-family env
// stripping, so callers don't re-implement that safety. The amend-scope glob
// is the doc file's own directory, so a re-commit of the same subject folds in
// rather than piling up (canAmend also requires the subject to match, and each
// doc's subject is ID-specific, so distinct docs never amend over each other).
func CommitDoc(projectRoot, relPath, subject string) error {
	excludeGlob := path.Dir(relPath) + "/*.md"
	return commitPaths(projectRoot, []string{relPath}, subject, excludeGlob)
}

// commitPaths makes one commit containing exactly the named paths.
//
// Decision:
//
//	HEAD subject == subject
//	AND HEAD not reachable from any ref besides the current branch
//	AND (ledger commits only) no task branch holds HEAD's ledger tree
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
// Note (E-1281 sandbox): when the per-worktree sandbox is active, sandbox
// emits route their ledger to the sandbox's own db-ledger dir and skip the
// auto-commit entirely (E-1729: the sandbox dir is disposable and not a git
// repo), so CommitLedgerSegment — and therefore this guard — is never reached
// in sandbox mode. It only fires for the real-DB path.
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

// canAmend returns true iff all four preconditions hold:
//  1. HEAD's subject equals the subject we're about to commit.
//  2. HEAD is not reachable from any ref besides the current branch (a landed
//     worktree branch, a remote-tracking ref, or a tag all disqualify it).
//  3. No refs/heads/task/* tip holds a .endless/db-ledger tree byte-identical
//     to HEAD's (ledger commits only — E-1955).
//  4. Index has no staged paths outside excludeGlob.
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

	// Resolve the current branch's full refname (e.g. "refs/heads/main"). On a
	// detached HEAD, `symbolic-ref` exits non-zero and runGitOutput errors — we
	// treat that as "no current branch" so every containing ref counts as an
	// "other" ref (conservative: never amend a tip we can't prove is unshared).
	curRef, err := runGitOutput(projectRoot, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		curRef = ""
	}
	curRef = strings.TrimSpace(curRef)

	// HEAD must not be reachable from any ref BESIDES the current branch. A
	// worktree branch left sitting on main's ledger tip (post-`land` rebase), a
	// remote-tracking ref (already pushed), or a tag all count: amending would
	// rewrite a commit another ref points at — the classic "never amend
	// published history" rule, applied to every shared ref, not just origin/*.
	refs, err := runGitOutput(projectRoot,
		"for-each-ref", "--contains", "HEAD", "--format=%(refname)",
	)
	if err != nil {
		return false, fmt.Errorf("check ref reachability: %w", err)
	}
	for _, r := range strings.Split(strings.TrimSpace(refs), "\n") {
		r = strings.TrimSpace(r)
		if r == "" || r == curRef {
			continue
		}
		// Some other ref contains HEAD → the tip is shared; append instead.
		return false, nil
	}

	// The reachability test above is SHA-level, so any rewrite of main's
	// history blinds it (E-1955): `git pull --rebase` reassigns every local
	// SHA, after which no task branch "contains" main's ledger tip even though
	// every one of them still carries the very ledger content that tip holds —
	// amending it diverges main from their base and conflicts on the ledger
	// segment at land. A tree hash is content-addressed, so it survives the
	// rewrite that invalidates a SHA: ask the same question ("does a task
	// branch depend on the commit I'm about to amend?") in those terms.
	//
	// This is additive, not a replacement. It scans only refs/heads/task/*, so
	// it is structurally blind to a tip already pushed to origin/main or
	// carrying a tag — exactly the cases the reachability test gets right.
	if subject == LedgerCommitSubject {
		shared, err := ledgerTreeSharedWithTaskBranch(projectRoot, curRef)
		if err != nil {
			return false, err
		}
		if shared {
			return false, nil
		}
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

// ledgerTreeSharedWithTaskBranch reports whether any `refs/heads/task/*` tip
// other than curRef holds a `.endless/<LedgerDirName>` tree byte-identical to
// the one at HEAD (E-1955). A tree OID is content-addressed, so — unlike the
// commit SHA the reachability test compares — it is unchanged by a rebase,
// amend, or any other rewrite of the history the tree hangs off.
//
// Resolves every candidate in ONE `git cat-file --batch-check` rather than a
// `rev-parse` per branch: measured on this repo (147 task branches) at 30ms
// batched vs 1.4s for 148 spawns. This runs on EVERY ledger event, so the
// per-branch form would be a visible tax on ordinary `endless` commands.
//
// A missing tree — at HEAD or on a branch — reads as "no match", never as a
// match with another missing one and never as an error: `--batch-check` prints
// `<rev> missing` and still exits 0.
func ledgerTreeSharedWithTaskBranch(projectRoot, curRef string) (bool, error) {
	refs, err := runGitOutput(projectRoot,
		"for-each-ref", "--format=%(refname)", "refs/heads/task",
	)
	if err != nil {
		return false, fmt.Errorf("list task branches: %w", err)
	}

	ledgerSuffix := ":.endless/" + LedgerDirName
	specs := []string{"HEAD" + ledgerSuffix}
	for _, r := range strings.Split(strings.TrimSpace(refs), "\n") {
		r = strings.TrimSpace(r)
		if r == "" || r == curRef {
			// Skipping curRef mirrors the reachability test's own exclusion: if
			// the main checkout happens to sit ON a task branch, that branch
			// trivially holds HEAD's tree, and counting it would refuse every
			// amend forever.
			continue
		}
		specs = append(specs, r+ledgerSuffix)
	}
	if len(specs) == 1 {
		// No task branches: nothing to be identical to.
		return false, nil
	}

	out, err := runGitInput(projectRoot, strings.Join(specs, "\n")+"\n",
		"cat-file", "--batch-check",
	)
	if err != nil {
		return false, fmt.Errorf("batch-resolve task branch ledger trees: %w", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(specs) {
		return false, fmt.Errorf(
			"cat-file --batch-check returned %d lines for %d revisions",
			len(lines), len(specs))
	}

	headTree := batchCheckOID(lines[0])
	if headTree == "" {
		// HEAD carries no ledger tree at all — there is nothing for a task
		// branch to be holding identically. Do not suppress the amend.
		return false, nil
	}
	for _, line := range lines[1:] {
		if batchCheckOID(line) == headTree {
			return true, nil
		}
	}
	return false, nil
}

// batchCheckOID returns the object id from one `git cat-file --batch-check`
// output line ("<oid> <type> <size>"), or "" for an unresolvable revision
// (git prints "<rev> missing" — two fields — and keeps going).
func batchCheckOID(line string) string {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return ""
	}
	return fields[0]
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

// runGitInput runs `git -C projectRoot <args>` with a sanitized env (E-1309),
// feeding stdin, and returns stdout. Separate from runGitOutput because stderr
// must stay OUT of stdout here — callers parse the output line-for-line
// against the input they fed (E-1955). stderr is folded into the error on
// non-zero exit.
func runGitInput(projectRoot, stdin string, args ...string) (string, error) {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = sanitizedGitEnv()
	cmd.Stdin = strings.NewReader(stdin)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	debugLogGit(projectRoot, args)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
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
