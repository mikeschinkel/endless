package sandboxcmd

import (
	"fmt"
	"os"
	"path/filepath"
)

// ReapSandboxForWorktree destroys the dev sandbox bound to a reaped worktree.
//
// Wired into monitor.ReapSandbox at process start (monitor cannot import this
// package — sandboxcmd already imports monitor). Before E-1904 nothing in the
// worktree drop/land/reap path called Destroy at all, so every reaped worktree
// left its sandbox behind indefinitely: 184 orphans / 228 MB had accumulated on
// the author's machine, the oldest seven weeks stale.
//
// Safety is NOT inherited from the caller. The reaper's own conditions (clean
// tree, no unmerged commits, no live process in the dir) concern the worktree
// directory, and two of this guard's conditions — a live tmux window, an
// unmerged branch — survive that directory's removal. So the guard is rebuilt
// and consulted here, at the point of deletion.
//
// Reaping is best-effort and idempotent: an absent sandbox, a protected one, or
// a sandbox with files still open all return nil. Only a genuine removal
// failure is an error, so a sweep is never aborted by one stubborn directory.
func ReapSandboxForWorktree(worktreeName string) (err error) {
	var guard *ReapGuard
	var protected bool
	var reason reapReason
	var writers []liveWriter
	var dir string

	err = validateName(worktreeName)
	if err != nil {
		goto end
	}

	dir = filepath.Join(sandboxesDir(), worktreeName)
	if !dirExists(dir) {
		goto end
	}

	guard, err = NewReapGuard(mainCheckoutRoot())
	if err != nil {
		goto end
	}

	protected, reason = guard.Protected(worktreeName)
	if protected {
		fmt.Fprintf(os.Stderr, "reap sandbox: sparing %s: %s\n", worktreeName, reason)
		goto end
	}

	// Mirrors destroy's refusal rather than forcing: a process holding files
	// open is the one condition the guard cannot see, and forcing would yank
	// the DB out from under a live writer.
	writers = findLiveWriters(dir)
	if len(writers) > 0 {
		fmt.Fprintf(os.Stderr, "reap sandbox: sparing %s: %d process(es) still have files open in it\n",
			worktreeName, len(writers))
		goto end
	}

	err = os.RemoveAll(dir)
	if err != nil {
		goto end
	}

end:
	if err != nil {
		err = fmt.Errorf("reap sandbox %q: %w", worktreeName, err)
	}
	return err
}
