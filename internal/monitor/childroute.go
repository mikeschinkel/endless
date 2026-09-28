package monitor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikeschinkel/endless/internal/dbcontext"
)

// ErrNoChildDBRoute is returned by ChildDBRoute when this process's database is
// one the Python CLI cannot be pointed at: neither main nor a worktree's
// sandbox. In practice that is a Go test on an arbitrary --db-dir.
var ErrNoChildDBRoute = errors.New(
	"no --db word names this process's database; a Python child cannot be routed to it")

// ChildDBRoute says how to start a Python `endless` child so it opens THIS
// process's database: the leading argv to pass, and the working directory to
// run it in.
//
// The route is a flag, never the environment (E-2186). Endless used to hand the
// child an XDG_CONFIG_HOME pointing at its own config root; an injected env var
// that routes a database is exactly what E-1429/E-1668 ruled out, because the
// same variable also meant "isolate this" to everything that inherited it.
//
//   - main: no flag, run from os.TempDir(). The Python CLI refuses --db outside
//     a self-dev project, and the child's DEFAULT is main — it resolves main by
//     the same rule from the same environment (dbcontext, "Main is the
//     default"). The neutral directory keeps the E-1429 gate out of it.
//   - a worktree's sandbox: `--db sandbox`, run from that worktree's root,
//     because the sandbox is addressed from cwd.
//   - anything else: ErrNoChildDBRoute. Refusing beats a child that silently
//     opens main instead.
func ChildDBRoute() (args []string, dir string, err error) {
	var db, main, cwd, worktree string

	db = DBPath()
	main = realDBPath()
	if main != "" && db == main {
		dir = os.TempDir()
		goto end
	}

	cwd, err = os.Getwd()
	if err != nil {
		err = fmt.Errorf("resolving working directory for a child's --db: %w", err)
		goto end
	}
	worktree = sandboxWorktreeFor(db, cwd)
	if worktree == "" {
		worktree = sandboxWorktreeFor(db, ConfigDir())
	}
	if worktree == "" {
		err = fmt.Errorf("%w: %s", ErrNoChildDBRoute, db)
		goto end
	}
	args = []string{dbcontext.DBFlag, dbcontext.ChoiceSandbox.String()}
	dir = worktree

end:
	return args, dir, err
}

// sandboxWorktreeFor returns the root of the worktree enclosing dir when db is
// that worktree's sandbox database, else "". Two probes because a sandbox under
// a project's out-of-tree override names no worktree in its own path, so only
// cwd can say which worktree it belongs to (the same split IsSandboxActive
// makes).
func sandboxWorktreeFor(db, dir string) string {
	sandbox := WorktreeSandboxConfigDir(dir)
	if sandbox == "" || db != filepath.Join(sandbox, dbcontext.DBFileName) {
		return ""
	}
	return WorktreeRoot(dir)
}
