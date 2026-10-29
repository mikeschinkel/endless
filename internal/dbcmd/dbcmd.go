// Package dbcmd implements the `endless-go db` subcommand: database work that
// is not an event, starting with seeding a worktree sandbox's database.
package dbcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/sandboxcmd"
)

func Run(args []string) {
	if len(args) < 1 {
		refusal.NoReport(
			"endless-go db: no command given",
			"Re-run with one of the listed subcommands",
		).Command("db").Text(usageText()).Exit(1)
	}
	switch args[0] {
	case "seed-sandbox":
		seedSandboxCmd(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		refusal.NoReport(
			fmt.Sprintf("endless-go db: unknown command %q", args[0]),
			"Re-run with one of the listed subcommands",
		).Command("db").Detail(usageText()).Exit(1)
	}
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go db <command> [flags]",
		"Commands:",
		"  seed-sandbox [--force]  (seed Endless's database into this worktree's sandbox)",
	}, "\n") + "\n"
}

// seedSandboxCmd fills the CURRENT worktree's sandbox with the endless database
// a self-dev session needs: schema, the project row copied from the main
// database, and a session row to attribute writes to.
//
// It was `endless-go sandbox init --mode worktree` until E-2481. A sandbox is a
// feature of its worktree — `endless sandbox reset` creates and clears it — so
// nothing about it needs initializing; what is left is database work, and it
// lives with the database.
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
// Creating the directory here is not provision-on-miss: an explicit seed is
// exactly the moment to build what is missing, which is what makes it safe for
// path resolution never to.
func seedSandboxCmd(args []string) {
	fs := refusal.NewFlags("seed-sandbox")
	force := fs.Bool("force", false, "Reseed even if the sandbox database already exists")
	parseFlags(fs, args)
	if fs.NArg() > 0 {
		refusal.NoReport(
			fmt.Sprintf("endless-go db seed-sandbox: unexpected argument %q; "+
				"the sandbox is resolved from the current worktree and takes no name",
				fs.Arg(0)),
			"Drop the argument and retry",
		).Command("db seed-sandbox").Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		// Getwd fails when the directory this process was started in has been
		// removed underneath it, which is Endless's footing gone rather than a
		// command anyone can retype.
		relayed(err).Exit(1)
	}
	sandboxDir := monitor.WorktreeSandboxDir(cwd)
	if sandboxDir == "" {
		refusal.NoReport(
			fmt.Sprintf("endless-go db seed-sandbox: %s is not inside a task "+
				"worktree (.endless/worktrees/e-NNN), so there is no sandbox to seed",
				cwd),
			"Run it from inside a task worktree",
		).Command("db seed-sandbox").Exit(1)
	}

	configDir := filepath.Join(sandboxDir, "endless")
	if *force {
		if err := os.RemoveAll(configDir); err != nil {
			refusal.Report(
				fmt.Sprintf("endless-go db seed-sandbox: removing %s: %v", configDir, err),
				"how to clear the filesystem error blocking removal of the seeded config directory",
			).Command("db seed-sandbox").Exit(1)
		}
	} else if _, err := os.Stat(filepath.Join(configDir, "endless.db")); err == nil {
		// Idempotent: an already-seeded sandbox is a no-op, so the hook that
		// calls this can be re-run as its contract requires.
		fmt.Println(sandboxDir)
		return
	}

	if err := sandboxcmd.EnsureSandboxDir(sandboxDir); err != nil {
		relayed(err).Exit(1)
	}
	if err := seedFromWorktree(sandboxDir); err != nil {
		// Relayed rather than classified here because the branches live in
		// seed_worktree.go: a worktree the caller can cd out of is one thing, a
		// failed migration or a project nobody registered is another. Until
		// those constructions carry a class this faults, which is the safe
		// reading — a half-seeded sandbox is not something to retry blindly,
		// and only --force retries it at all.
		relayed(fmt.Errorf("seeding from worktree: %w", err)).Exit(1)
	}
	fmt.Println(sandboxDir)
}

// parseFlags parses seed-sandbox's argv the way sandboxcmd's verbs do: -h is a
// request rather than a refusal, so it keeps its exit 0, and a mistyped flag is
// the caller's to correct, so it keeps its exit 2.
func parseFlags(fs *refusal.Flags, args []string) {
	err := fs.Parse(args)
	if err == nil {
		return
	}
	if errors.Is(err, flag.ErrHelp) {
		refusal.Info(fs.Output()).Print()
		os.Exit(0)
	}
	refusal.NoReport(
		fmt.Sprintf("endless-go db seed-sandbox: %s", err),
		"Correct the flag and retry",
	).Command("db seed-sandbox").Text(fs.Output()).Exit(2)
}

// relayed prepares an error raised deeper in this package for printing. The
// class travels with the error; refusal.From faults anything unclassified,
// which is the right reading of a mkdir or seeding failure underneath.
func relayed(err error) *refusal.Error {
	return refusal.From(err).
		Command("db seed-sandbox").
		Text(fmt.Sprintf("endless-go db seed-sandbox: %v", err))
}
