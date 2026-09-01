package sandboxcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// sandboxTaskRe matches the per-worktree sandbox naming convention e-NNNN,
// whose basename mirrors the worktree directory's basename (see CLAUDE.md,
// "Self-dev DB sandbox"). Only these are worktree-bound; an ephemeral
// sandbox has a random hex name and never matches.
var sandboxTaskRe = regexp.MustCompile(`^e-(\d+)$`)

// tmuxWindowTaskRe extracts the task ID a tmux window is dedicated to. A
// window name is the user's own record that the task is still in play, and it
// outlives the worktree dir.
//
// Two forms are matched, because both are on screen at once:
//
//	E-NNNN                     what a window is named today (E-2102)
//	<project>_<slug>[E-NNNN]   what it was named before E-2102 landed
//
// The old form stays because windows opened before the rename are still open
// on the user's machine and age out only as their sessions end. A guard that
// protected only the newly-named ones would be a guard with a migration-shaped
// hole, and the sandbox it dropped would go without a word.
//
// Both alternatives are anchored to the whole name, so a window that merely
// MENTIONS a task in passing does not spare that task's sandbox. Only one of
// the two groups is populated per match; tmuxWindowTaskID picks the non-empty
// one.
var tmuxWindowTaskRe = regexp.MustCompile(`^(?:E-(\d+)|.*\[E-(\d+)])$`)

// tmuxWindowTaskID returns the task ID in a tmux window name, or "" if the
// name is not one of the forms tmuxWindowTaskRe recognizes.
func tmuxWindowTaskID(name string) (id string) {
	var match []string

	match = tmuxWindowTaskRe.FindStringSubmatch(strings.TrimSpace(name))
	if match == nil {
		goto end
	}

	id = match[1]
	if id == "" {
		id = match[2]
	}

end:
	return id
}

// taskBranchRe matches the branch naming convention task/NNNN-<slug>.
var taskBranchRe = regexp.MustCompile(`^task/(\d+)-`)

// reapReason names the condition that spared a sandbox, for operator output.
type reapReason string

const (
	reasonWorktreeDir    reapReason = "worktree directory exists"
	reasonGitWorktree    reapReason = "git still tracks a worktree of that name"
	reasonTmuxWindow     reapReason = "a live tmux window references the task"
	reasonUnmergedBranch reapReason = "the task branch is not merged to main"
)

// ReapGuard answers whether a worktree-bound sandbox may be removed.
//
// A sandbox outlives its worktree directory, so directory existence alone is
// NOT a sufficient safety check — two of the four conditions below survive
// worktree deletion, and each has destroyed real work when omitted (E-1904):
//
//  1. the worktree directory exists
//  2. git still tracks a worktree of that name
//  3. a live tmux window references the task ID
//  4. the task's branch has commits not merged to main
//
// A fifth condition — a live process holding files open in the sandbox — is
// enforced separately by Sandbox.Destroy, which refuses without --force.
//
// The environment is sampled ONCE at construction so a sweep over hundreds of
// sandboxes does not re-shell per candidate. That makes a guard a point-in-time
// snapshot: build a fresh one per sweep, never cache it across sweeps.
type ReapGuard struct {
	worktreeRoot string
	gitWorktrees map[string]struct{}
	tmuxTasks    map[string]struct{}
	unmerged     map[string]struct{}
}

// NewReapGuard samples the environment and returns a guard for projectRoot,
// which must be the MAIN checkout (worktrees and branches are enumerated
// relative to it). An error means the environment could not be established;
// callers MUST treat that as "reap nothing" rather than reaping unguarded.
func NewReapGuard(projectRoot string) (guard *ReapGuard, err error) {
	var gitWorktrees, tmuxTasks, unmerged map[string]struct{}

	gitWorktrees, err = gitTrackedWorktrees(projectRoot)
	if err != nil {
		goto end
	}

	tmuxTasks, err = liveTmuxTasks()
	if err != nil {
		goto end
	}

	unmerged, err = unmergedTaskBranches(projectRoot)
	if err != nil {
		goto end
	}

	guard = &ReapGuard{
		worktreeRoot: filepath.Join(projectRoot, ".endless", "worktrees"),
		gitWorktrees: gitWorktrees,
		tmuxTasks:    tmuxTasks,
		unmerged:     unmerged,
	}

end:
	if err != nil {
		err = fmt.Errorf("sandbox reap guard: %w", err)
	}
	return guard, err
}

// Protected reports whether the named sandbox must be spared, and why.
//
// A name that is not worktree-bound (no e-NNNN form) is unprotected here:
// ephemeral sandboxes are governed by creator-PID liveness instead, which
// classify() applies.
func (g *ReapGuard) Protected(name string) (protected bool, reason reapReason) {
	var dir string

	if !sandboxTaskRe.MatchString(name) {
		goto end
	}

	dir = filepath.Join(g.worktreeRoot, name)
	if dirExists(dir) {
		protected = true
		reason = reasonWorktreeDir
		goto end
	}

	_, protected = g.gitWorktrees[name]
	if protected {
		reason = reasonGitWorktree
		goto end
	}

	_, protected = g.tmuxTasks[name]
	if protected {
		reason = reasonTmuxWindow
		goto end
	}

	_, protected = g.unmerged[name]
	if protected {
		reason = reasonUnmergedBranch
		goto end
	}

end:
	return protected, reason
}

// dirExists reports whether path is an existing directory. A symlink is NOT
// treated as a directory: the guard must not be satisfied by a dangling or
// redirected link where a real worktree is expected.
func dirExists(path string) (exists bool) {
	info, err := os.Lstat(path)
	if err != nil {
		goto end
	}
	exists = info.IsDir()

end:
	return exists
}

// gitTrackedWorktrees returns the set of worktree basenames git still knows
// about, which can outlive the directory when a removal left the admin record
// behind.
func gitTrackedWorktrees(projectRoot string) (names map[string]struct{}, err error) {
	var out string
	var line string

	names = make(map[string]struct{})

	out, err = runGuardCmd(projectRoot, "git", "worktree", "list", "--porcelain")
	if err != nil {
		err = fmt.Errorf("git worktree list: %w", err)
		goto end
	}

	for _, line = range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		names[filepath.Base(strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))] = struct{}{}
	}

end:
	return names, err
}

// liveTmuxTasks returns the set of sandbox names whose task ID appears in a
// live tmux window name, across every session on the default server.
//
// Windows are enumerated rather than panes because grouped sessions (endless
// runs `active` grouped with `active-6`) share windows, and listing panes would
// visit each shared window once per session.
//
// No tmux server means no live windows, which is a legitimately empty set — not
// an error. Any other tmux failure IS an error, so a broken probe fails closed
// at the call site instead of silently unprotecting every task.
func liveTmuxTasks() (names map[string]struct{}, err error) {
	var out string
	var line string
	var id string

	names = make(map[string]struct{})

	out, err = runGuardCmd("", "tmux", "list-windows", "-a", "-F", "#{window_name}")
	if err != nil {
		if noTmuxServer(out) {
			err = nil
		}
		if err != nil {
			err = fmt.Errorf("tmux list-windows: %w", err)
		}
		goto end
	}

	for _, line = range strings.Split(out, "\n") {
		id = tmuxWindowTaskID(line)
		if id == "" {
			continue
		}
		names["e-"+id] = struct{}{}
	}

end:
	return names, err
}

// noTmuxServer reports whether tmux failed merely because nothing is running.
func noTmuxServer(out string) bool {
	return strings.Contains(out, "no server running") ||
		strings.Contains(out, "error connecting to")
}

// unmergedTaskBranches returns the set of sandbox names whose task branch has
// commits not yet on main. A removed worktree whose branch never merged still
// holds the only copy of that work, so its sandbox must be spared.
func unmergedTaskBranches(projectRoot string) (names map[string]struct{}, err error) {
	var out string
	var line string
	var match []string

	names = make(map[string]struct{})

	out, err = runGuardCmd(projectRoot, "git", "branch", "--no-merged", "main", "--format=%(refname:short)")
	if err != nil {
		err = fmt.Errorf("git branch --no-merged: %w", err)
		goto end
	}

	for _, line = range strings.Split(out, "\n") {
		match = taskBranchRe.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		names["e-"+match[1]] = struct{}{}
	}

end:
	return names, err
}

// runGuardCmd executes name with args, optionally in dir, returning combined output.
// Held in a var so guard tests can substitute a fixture without building real
// git repos or a tmux server.
var runGuardCmd = func(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
