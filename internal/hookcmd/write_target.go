package hookcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/docmirror"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// E-940 (merged with E-1703) — one decision about WHERE a session may write,
// asked of every write target a PreToolUse hook can see: the file_path of a
// Write/Edit/NotebookEdit, and each target bashWriteTargets recognizes in a
// Bash command.
//
// Before this, every location gate judged payload.CWD. enforceClaimedCwd keeps a
// claimed session's cwd inside its worktree, but a session sitting correctly in
// its worktree could still Write the main checkout by absolute path, or `sed -i`
// it, with nothing to stop it. This closes that gap by judging the TARGET.
//
// "The same decision" is literal: both tool paths call writeTargetDecision, and
// TestBashWriteGateMatchesWriteGate holds them to the same answer and the same
// message. A Bash write is refused exactly when a Write to the same path would
// be.

// writeScope is everything writeTargetDecision needs to know about the session,
// computed once per hook call by newWriteScope.
type writeScope struct {
	// taskID and worktree name the session's claimed, non-terminal task and its
	// symlink-resolved worktree. worktree is "" when the session owns none — no
	// task, a terminal task, no worktree on disk, or one another live session
	// holds — and then the containment rule has nothing to say.
	taskID   int64
	worktree string

	// gitDirs are the parts of the shared git directory that belong to this
	// session's worktree: its own admin dir (`<common>/worktrees/<name>/`,
	// where a stale index.lock lives) and Endless's git-side state
	// (`<common>/info/endless/`). The rest of .git — config, hooks, refs —
	// is shared by every worktree, and stays refused.
	gitDirs []string

	// projectRoot is the resolved main checkout. A target inside it is never
	// exempt, even when the project itself lives under a temp dir.
	projectRoot string

	// exempt are resolved roots that are always writable: OS temp dirs, Claude
	// Code's own config, and Endless's config dir.
	exempt []string

	// landed answers the landed-suite question (E-1916 Arm 1) for a suite task.
	// A field rather than a direct call so the decision is testable without a
	// landings table.
	landed func(suiteTask int64) (msg string, block bool)
}

// newWriteScope builds the scope for this hook call.
func newWriteScope(projectID int64, payload claudePayload) writeScope {
	s := writeScope{
		exempt: exemptWriteRoots(),
		landed: func(suiteTask int64) (string, bool) {
			return landedSuiteDecision(payload, landedSuiteEdit, suiteTask)
		},
	}
	if root, err := monitor.ProjectPath(projectID); err == nil && root != "" {
		s.projectRoot = resolveExisting(root)
	}
	taskID, wt := sessionOwnedWorktree(projectID, payload)
	if wt != "" {
		s.taskID = taskID
		s.worktree = resolveExisting(wt)
		s.gitDirs = worktreeGitDirs(s.worktree)
	}
	return s
}

// worktreeGitDirs returns the worktree's own admin dir and the common dir's
// info/endless/, read from the worktree's `.git` file (`gitdir: <admin>`) and
// the admin dir's `commondir` — file reads only, no git subprocess on every
// tool call. A worktree whose `.git` is not a linked-worktree file yields none.
func worktreeGitDirs(worktree string) []string {
	data, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return nil
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return nil
	}
	admin := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(worktree, admin)
	}
	admin = resolveExisting(filepath.Clean(admin))
	common := filepath.Dir(filepath.Dir(admin))
	if c, err := os.ReadFile(filepath.Join(admin, "commondir")); err == nil {
		common = strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(admin, common)
		}
		common = resolveExisting(filepath.Clean(common))
	}
	return []string{admin, filepath.Join(common, "info", "endless")}
}

// sessionOwnedWorktree returns the session's claimed task and that task's
// worktree, or "" when any precondition fails: no session, no task, a terminal
// task (a display-only bind of done work, a landed worktree), no worktree on
// disk, or a worktree whose lock a different live session holds. Shared by
// enforceClaimedCwd (the cwd invariant) and newWriteScope (the target
// invariant), so both answer "which worktree is this session's?" the same way.
func sessionOwnedWorktree(projectID int64, payload claudePayload) (taskID int64, worktreePath string) {
	session, _ := monitor.GetActiveSession(payload.SessionID)
	if session == nil || session.TaskID == nil {
		return 0, ""
	}
	status, _ := monitor.GetTaskStatus(*session.TaskID)
	if status == "" || monitor.IsTerminalTaskStatus(status) {
		return 0, ""
	}
	worktreePath, _ = monitor.WorktreePathForTask(projectID, *session.TaskID)
	if worktreePath == "" {
		return 0, ""
	}
	if lock, err := monitor.ReadWorktreeLock(worktreePath); err == nil && lock != nil &&
		lock.SessionID != payload.SessionID && !monitor.IsWorktreeLockStale(lock) {
		return 0, ""
	}
	return *session.TaskID, worktreePath
}

// exemptWriteRoots lists the locations outside any worktree that a session may
// always write to, symlink-resolved (macOS reaches /tmp and /var through
// /private). Endless's config dir comes from monitor.ConfigDir, the resolver
// Endless itself uses, so a relocated XDG_CONFIG_HOME moves the exemption with
// it.
func exemptWriteRoots() []string {
	candidates := []string{"/tmp", "/private/tmp", "/var/folders", os.Getenv("TMPDIR"), os.TempDir()}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".claude"))
	}
	candidates = append(candidates, monitor.ConfigDir())

	var roots []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || !filepath.IsAbs(c) {
			continue
		}
		r := resolveExisting(c)
		if !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	return roots
}

// isDeviceFile reports whether target is a device a command writes to without
// creating anything: /dev/null, the standard streams, the terminal, /dev/fd/N.
func isDeviceFile(target string) bool {
	switch target {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty":
		return true
	}
	return strings.HasPrefix(target, "/dev/fd/")
}

// writeTargetDecision answers: may this session write to target? target is the
// output of resolveWriteTarget. It returns the refusal text when it may not.
//
// In order:
//
//  1. a task document mirror — the database's file, never a session's;
//  2. a landed, foreign task's verification suite (E-1916 Arm 1);
//  3. a device file, this worktree's own part of the shared .git, or an exempt
//     location outside the project — always writable;
//  4. containment — a session holding a claimed worktree writes inside it.
//
// 1 and 2 apply to every session. 3 and 4 apply only to a session that owns a
// worktree; a session with no claimed task keeps the rules it had.
func writeTargetDecision(s writeScope, target string) (msg string, block bool) {
	if target == "" {
		return "", false
	}
	if docmirror.TaskDocRe.MatchString(target) || docmirror.LegacyTaskDocRe.MatchString(target) {
		return docMirrorBlockMessage(), true
	}
	if id := suiteTaskFromPath(target); id != 0 && s.landed != nil {
		if msg, block := s.landed(id); block {
			return msg, true
		}
	}
	if s.worktree == "" || !filepath.IsAbs(target) {
		return "", false
	}
	if pathWithin(s.worktree, target) || isDeviceFile(target) {
		return "", false
	}
	for _, d := range s.gitDirs {
		if pathWithin(d, target) {
			return "", false
		}
	}
	if s.projectRoot == "" || !pathWithin(s.projectRoot, target) {
		for _, root := range s.exempt {
			if pathWithin(root, target) {
				return "", false
			}
		}
	}
	return writeTargetRedirect(s.taskID, s.worktree, target), true
}

// resolveWriteTarget makes p absolute against cwd and resolves symlinks through
// its nearest existing ancestor, so a target that does not exist yet still
// compares correctly against a symlinked root (E-2002: /var is /private/var on
// macOS). A relative p with no cwd stays relative: the path-pattern checks can
// still judge it, containment cannot.
func resolveWriteTarget(cwd, p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if cwd == "" {
			return filepath.Clean(p)
		}
		p = filepath.Join(cwd, p)
	}
	return resolveExisting(filepath.Clean(p))
}

// resolveExisting resolves symlinks in the deepest existing ancestor of the
// absolute path p and rejoins the rest. Falls back to p unchanged.
func resolveExisting(p string) string {
	tail := ""
	dir := p
	for {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, tail)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		tail = filepath.Join(filepath.Base(dir), tail)
		dir = parent
	}
}

// writeTargetRedirect is the containment refusal. Display paths render
// home-relative; the worktree path is also given literally for paste.
func writeTargetRedirect(taskID int64, worktreePath, target string) string {
	return fmt.Sprintf(
		"BLOCKED: %s is outside your worktree.\n\n"+
			"You have task E-%d claimed, and every edit for it must land inside its "+
			"worktree:\n  %s\n\n"+
			"Re-target the path under that directory. Scratch files belong in a temp "+
			"dir ($TMPDIR, /tmp, or your scratchpad), which is always writable.\n\n"+
			"If this write genuinely belongs in another tree, it is not part of "+
			"E-%d — say so rather than routing around this refusal.",
		tildePath(target), taskID, worktreePath, taskID)
}

// writeToolDecision is the Write/Edit/NotebookEdit side of the one decision.
func writeToolDecision(projectID int64, payload claudePayload) (msg string, block bool) {
	raw := extractFilePath(payload.ToolName, payload.ToolInput)
	if raw == "" {
		return "", false
	}
	return writeTargetDecision(newWriteScope(projectID, payload), resolveWriteTarget(payload.CWD, raw))
}

// enforceWriteTarget refuses a write tool whose target writeTargetDecision
// refuses. Independent of tracking_mode, like the worktree gates around it.
func enforceWriteTarget(projectID int64, payload claudePayload) {
	if msg, block := writeToolDecision(projectID, payload); block {
		blockToolUse(msg)
	}
}
