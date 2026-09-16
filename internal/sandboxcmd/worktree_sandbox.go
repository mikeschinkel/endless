package sandboxcmd

import (
	"fmt"
	"os"
	"path/filepath"
)

// SandboxGitignore is the content endless writes at the root of every sandbox
// it creates. Mirrored by Python's worktree_cmd._SANDBOX_GITIGNORE; the two
// must stay identical, because a worktree provisioned by one and re-provisioned
// by the other must not show a diff.
//
// The sandbox self-ignores rather than being listed in the project's own
// .gitignore, and that is the whole reason a downstream project needs to do
// nothing to adopt this: `*` ignores every path in this directory including
// this file, so git sees the directory as empty and never reports it. A rule in
// the project's .gitignore would be a file endless edits in a repo that is not
// its own, and one more thing to get wrong for every project that ever adopts
// worktrees.
const SandboxGitignore = `# Endless per-worktree sandbox (ED-1554): isolated state this worktree's task
# is exercised against, with a lifetime exactly equal to this worktree's.
# Self-ignoring — '*' covers every path here, this file included — so the
# project's own .gitignore needs no entry for it.
*
`

// EnsureSandboxDir creates a sandbox directory and its self-ignoring
// .gitignore, and is idempotent: an existing sandbox keeps its contents and its
// .gitignore is rewritten only if it is absent or has drifted.
//
// The directory is left EMPTY. Endless cannot know which of a checkout's files
// a task needs in order to be verified, and copying them in is the exact
// failure a sandbox exists to prevent — a worktree quietly pointed at the real
// database or a live account. What goes in here is declared by the project, in
// its own post-worktree-create hook.
func EnsureSandboxDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating sandbox dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, ".gitignore")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == SandboxGitignore {
		return nil
	}
	if err := os.WriteFile(path, []byte(SandboxGitignore), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
