package sandboxcmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

func initCmd(args []string) {
	fs := refusal.NewFlags("init")
	mode := fs.String("mode", "", "Initial state: worktree (the only mode)")
	force := fs.Bool("force", false, "Reseed the sandbox if it is already seeded")
	parseVerbFlags(fs, "init", args)

	// Named sandboxes under the cache directory, and the empty/seed/clone modes
	// that made them, went with E-2481: a sandbox lives inside its worktree, so
	// the worktree is the only thing init can be asked to fill.
	if *mode != "worktree" {
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox init: --mode %q is not supported; the only mode is --mode worktree", *mode),
			"Re-run with --mode worktree from inside a task worktree",
		).Command("sandbox init").Exit(1)
	}
	initWorktree(fs.Args(), *force)
}

// initWorktree fills the CURRENT worktree's sandbox with the endless database a
// self-dev session needs: schema, the project row copied from the main
// database, and a session row to attribute writes to.
//
// It takes no name (E-1964). A worktree's sandbox is resolved from the worktree
// itself, so naming one would be naming the directory you are standing in — and
// a name that could disagree with cwd is a name that eventually does.
//
// This is a SEEDING verb, and seeding is the project's business rather than
// endless's: `endless sandbox reset` clears the sandbox and runs the project's
// seed-sandbox hook, and it is that hook — endless's own, in endless's repo —
// that calls this. A downstream project calls whatever fills ITS sandbox
// instead, and never this.
//
// Creating the directory here is not provision-on-miss: an explicit init is
// exactly the moment to build what is missing, which is what makes it safe for
// path resolution never to.
func initWorktree(rest []string, force bool) {
	if len(rest) > 0 {
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox init --mode worktree: unexpected argument %q; "+
				"the sandbox is resolved from the current worktree and takes no name",
				rest[0]),
			"Drop the argument and retry",
		).Command("sandbox init").Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		// Getwd fails when the directory this process was started in has been
		// removed underneath it, which is Endless's footing gone rather than a
		// command anyone can retype.
		relayed("init", err).Exit(1)
	}
	sandboxDir := monitor.WorktreeSandboxDir(cwd)
	if sandboxDir == "" {
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox init --mode worktree: %s is not inside a task "+
				"worktree (.endless/worktrees/e-NNN), so there is no sandbox to seed",
				cwd),
			"Run it from inside a task worktree",
		).Command("sandbox init").Exit(1)
	}

	configDir := filepath.Join(sandboxDir, "endless")
	if force {
		if err := os.RemoveAll(configDir); err != nil {
			refusal.Report(
				fmt.Sprintf("endless-go sandbox init: removing %s: %v", configDir, err),
				"how to clear the filesystem error blocking removal of the seeded config directory",
			).Command("sandbox init").Exit(1)
		}
	} else if _, err := os.Stat(filepath.Join(configDir, "endless.db")); err == nil {
		// Idempotent: an already-seeded sandbox is a no-op, so the hook that
		// calls this can be re-run as its contract requires.
		fmt.Println(sandboxDir)
		return
	}

	if err := EnsureSandboxDir(sandboxDir); err != nil {
		relayed("init", err).Exit(1)
	}
	if err := seedFromWorktree(sandboxDir); err != nil {
		// Relayed rather than classified here because the branches live in
		// seed_worktree.go: a worktree the caller can cd out of is one thing, a
		// failed migration or a project nobody registered is another. Until
		// those constructions carry a class this faults, which is the safe
		// reading — a half-seeded sandbox is not something to retry blindly,
		// and only --force retries it at all.
		relayed("init", fmt.Errorf("seeding from worktree: %w", err)).Exit(1)
	}
	fmt.Println(sandboxDir)
}
