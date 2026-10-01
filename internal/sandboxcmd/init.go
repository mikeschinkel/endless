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
	mode := fs.String("mode", "empty", "Initial state: empty | worktree | seed | clone")
	force := fs.Bool("force", false, "Recreate the sandbox if it already exists")
	parseVerbFlags(fs, "init", args)
	rest := fs.Args()

	switch *mode {
	case "empty":
		// supported — bare sandbox directory, no DB seeding.
	case "worktree":
		// supported — seeds projects/sessions from the current worktree's
		// main checkout so first-invocation CLI calls have a real project
		// row to resolve and a real session row to attribute to.
		initWorktree(rest, *force)
		return
	case "seed":
		refusal.NoReport(
			"endless-go sandbox init: --mode seed not yet implemented; use --mode empty or --mode worktree",
			"Re-run with --mode empty or --mode worktree",
		).Command("sandbox init").Exit(1)
	case "clone":
		refusal.NoReport(
			"endless-go sandbox init: --mode clone not yet implemented (see E-1087); use --mode empty or --mode worktree",
			"Re-run with --mode empty or --mode worktree",
		).Command("sandbox init").Exit(1)
	default:
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox init: unknown --mode %q (want: empty | worktree | seed | clone)", *mode),
			"Re-run with --mode empty or --mode worktree",
		).Command("sandbox init").Exit(1)
	}

	if len(rest) != 1 {
		refusal.NoReport(
			"endless-go sandbox init: expected exactly one positional arg <name>",
			"Pass exactly one sandbox name and retry",
		).Command("sandbox init").Exit(1)
	}
	name := rest[0]
	if err := validateName(name); err != nil {
		relayed("init", err).Exit(1)
	}

	dir := filepath.Join(sandboxesDir(), name)
	if _, err := os.Stat(dir); err == nil {
		if *force {
			if err := os.RemoveAll(dir); err != nil {
				refusal.Report(
					fmt.Sprintf("endless-go sandbox init: removing existing %s: %v", dir, err),
					"how to clear the filesystem error blocking removal of the existing sandbox",
				).Command("sandbox init").Exit(1)
			}
		} else {
			// Idempotent: existing sandbox with the same name is treated as a no-op.
			fmt.Println(dir)
			return
		}
	} else if !os.IsNotExist(err) {
		// The cache directory would not answer at all, which is the user's
		// disk rather than anything about this argv.
		refusal.Report(
			fmt.Sprintf("endless-go sandbox init: %v", err),
			"how to clear the permission or I/O error on the sandbox cache directory",
		).Command("sandbox init").Exit(1)
	}

	sb, err := Provision(name, modePersistent)
	if err != nil {
		relayed("init", err).Exit(1)
	}
	// Close the root handle now; init does not keep the sandbox open.
	if sb.root != nil {
		sb.root.Close()
		sb.root = nil
	}

	fmt.Println(sb.Dir)
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
			"Run it from inside a task worktree, or use --mode empty",
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
