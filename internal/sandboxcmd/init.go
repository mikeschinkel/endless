package sandboxcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func initCmd(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	mode := fs.String("mode", "empty", "Initial state: empty | worktree | seed | clone")
	force := fs.Bool("force", false, "Recreate the sandbox if it already exists")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
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
		fmt.Fprintln(os.Stderr, "endless-go sandbox init: --mode seed not yet implemented; use --mode empty or --mode worktree")
		os.Exit(1)
	case "clone":
		fmt.Fprintln(os.Stderr, "endless-go sandbox init: --mode clone not yet implemented (see E-1087); use --mode empty or --mode worktree")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "endless-go sandbox init: unknown --mode %q (want: empty | worktree | seed | clone)\n", *mode)
		os.Exit(1)
	}

	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "endless-go sandbox init: expected exactly one positional arg <name>")
		os.Exit(1)
	}
	name := rest[0]
	if err := validateName(name); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go sandbox init: %v\n", err)
		os.Exit(1)
	}

	dir := filepath.Join(sandboxesDir(), name)
	if _, err := os.Stat(dir); err == nil {
		if *force {
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintf(os.Stderr, "endless-go sandbox init: removing existing %s: %v\n", dir, err)
				os.Exit(1)
			}
		} else {
			// Idempotent: existing sandbox with the same name is treated as a no-op.
			fmt.Println(dir)
			return
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "endless-go sandbox init: %v\n", err)
		os.Exit(1)
	}

	sb, err := Provision(name, modePersistent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go sandbox init: %v\n", err)
		os.Exit(1)
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
// endless's: endless creates an empty sandbox at worktree-create and runs the
// project's post-worktree-create hook, and it is that hook — endless's own, in
// endless's repo — that calls this. A downstream project calls whatever fills
// ITS sandbox instead, and never this.
//
// Creating the directory here is not provision-on-miss: an explicit init is
// exactly the moment to build what is missing, which is what makes it safe for
// path resolution never to.
func initWorktree(rest []string, force bool) {
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr,
			"endless-go sandbox init --mode worktree: unexpected argument %q; "+
				"the sandbox is resolved from the current worktree and takes no name\n",
			rest[0])
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go sandbox init: %v\n", err)
		os.Exit(1)
	}
	sandboxDir := monitor.WorktreeSandboxDir(cwd)
	if sandboxDir == "" {
		fmt.Fprintf(os.Stderr,
			"endless-go sandbox init --mode worktree: %s is not inside a task "+
				"worktree (.endless/worktrees/e-NNN), so there is no sandbox to seed\n",
			cwd)
		os.Exit(1)
	}

	configDir := filepath.Join(sandboxDir, "endless")
	if force {
		if err := os.RemoveAll(configDir); err != nil {
			fmt.Fprintf(os.Stderr, "endless-go sandbox init: removing %s: %v\n", configDir, err)
			os.Exit(1)
		}
	} else if _, err := os.Stat(filepath.Join(configDir, "endless.db")); err == nil {
		// Idempotent: an already-seeded sandbox is a no-op, so the hook that
		// calls this can be re-run as its contract requires.
		fmt.Println(sandboxDir)
		return
	}

	if err := EnsureSandboxDir(sandboxDir); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go sandbox init: %v\n", err)
		os.Exit(1)
	}
	if err := seedFromWorktree(sandboxDir); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go sandbox init: seeding from worktree: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(sandboxDir)
}
