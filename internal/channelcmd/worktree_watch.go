package channelcmd

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// worktreeDirRe matches the .endless/worktrees/e-NNNN naming convention.
var worktreeDirRe = regexp.MustCompile(`^e-\d+$`)

// worktreeWatchInterval is how often the watchdog re-checks. Generous because
// a stranded channel process wastes resources but breaks nothing, so there is
// no reason to poll aggressively for the whole life of a healthy session.
const worktreeWatchInterval = 5 * time.Minute

// watchWorktreeRemoval returns a channel that is closed once dir stops being a
// directory, or nil when dir is not a worktree path.
//
// A nil channel blocks forever in select, so a channel process launched from
// the main checkout keeps exactly its old lifecycle.
//
// Why this exists: a channel process holds its worktree as cwd and keeps the
// worktree's sandbox DB open, but Run only ever exited on a signal or MCP
// session end — neither of which fires when the worktree is reaped out from
// under it. Four such processes were found alive 18-24 days after their
// worktrees were removed, each pinning a sandbox that could not be reclaimed
// (E-1904).
func watchWorktreeRemoval(dir string, interval time.Duration) (gone chan struct{}) {
	if !isWorktreeDir(dir) {
		goto end
	}

	gone = make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if isDir(dir) {
				continue
			}
			close(gone)
			return
		}
	}()

end:
	return gone
}

// isWorktreeDir reports whether dir is an existing .endless/worktrees/e-NNNN
// path. Both the basename convention and the parent directory must match, so
// an unrelated directory that happens to be named e-1 is not watched.
func isWorktreeDir(dir string) (ok bool) {
	if dir == "" {
		goto end
	}
	if !worktreeDirRe.MatchString(filepath.Base(dir)) {
		goto end
	}
	if filepath.Base(filepath.Dir(dir)) != "worktrees" {
		goto end
	}
	ok = isDir(dir)

end:
	return ok
}

// isDir reports whether path is currently an existing directory.
func isDir(path string) (ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		goto end
	}
	ok = info.IsDir()

end:
	return ok
}
