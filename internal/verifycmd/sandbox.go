package verifycmd

import (
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/sandboxcmd"
	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// resetSandbox resets the canonical sandbox of the worktree at root, so every
// run starts from the same seeded state (E-1608). It goes through
// sandboxcmd.Reset — the front door `endless sandbox reset` uses — and never
// runs the project's seed hook itself.
//
// A root outside any task worktree has no canonical sandbox, so there is
// nothing to reset: a project not using worktrees still verifies. The reset
// runs under the CALLER's environment, before the per-run isolation exists,
// because seeding may need what isolation hides (Endless's own hook reads the
// main database to copy the project row). Hook output goes to stderr.
func resetSandbox(root dt.DirPath) (err error) {
	if monitor.WorktreeRoot(string(root)) == "" {
		goto end
	}
	// The seed hook's output is the PROJECT's, not Endless's: it is whatever a
	// given project's seed-sandbox.sh prints, and nothing here can classify
	// text it has never seen. Passthrough is how that is said out loud — the
	// child's stderr is its own, and it reaches the reader unchanged, exactly
	// as it did when this line named os.Stderr.
	_, err = sandboxcmd.Reset(string(root), refusal.Passthrough())
	if err != nil {
		err = doterr.NewErr(ErrResettingSandbox, err, "worktree", root)
	}
end:
	return err
}
