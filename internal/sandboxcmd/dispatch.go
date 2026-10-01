// Package sandboxcmd implements the `endless-go sandbox` subcommand
// and the per-worktree sandbox tooling (E-1281).
package sandboxcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
)

func Run(args []string) {
	if len(args) < 1 {
		// Today this prints the usage block with nothing above it, so the human
		// rendering is pinned verbatim and the summary exists only to give the
		// agent's verdict a line to carry.
		refusal.NoReport(
			"endless-go sandbox: no command given",
			"Re-run with one of the listed subcommands",
		).Command("sandbox").Text(usageText()).Exit(1)
	}
	switch args[0] {
	case "run":
		runCmd(args[1:])
	case "enter":
		enterCmd(args[1:])
	case "init":
		initCmd(args[1:])
	case "reset":
		resetCmd(args[1:])
	case "list":
		listCmd(args[1:])
	case "prune":
		pruneCmd(args[1:])
	case "destroy":
		destroyCmd(args[1:])
	case "claude-settings-repair":
		claudeSettingsRepairCmd(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		// Not the version-skew case endless-go's own dispatcher has to allow
		// for: nothing relays a verb here except the Python task-claim path,
		// and that only ever passes `init`. An unknown command is therefore
		// something the caller typed, and typing a different one is the whole
		// remedy.
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox: unknown command %q", args[0]),
			"Re-run with one of the listed subcommands",
		).Command("sandbox").Detail(usageText()).Exit(1)
	}
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go sandbox <command> [flags] [args]",
		"Commands:",
		"  run     [--clone] [--name N] [--keep] -- <cmd> [args]",
		"  enter   [--clone] <name>",
		"  init    --mode worktree [--force]           (this worktree's sandbox)",
		"  init    [--mode empty|seed|clone] [--force] <name>",
		"  reset                                        (clear + reseed this worktree's sandbox)",
		"  list",
		"  prune   [--older-than DURATION]",
		"  destroy [--force] [--if-exists] <name>",
		"  claude-settings-repair [--all] [<worktree> ...]",
	}, "\n") + "\n"
}

// parseVerbFlags parses one verb's argv and names a class for everything the
// flag package used to decide on its own.
//
// Every verb here was built with flag.ExitOnError, which printed to stderr and
// exited from inside the flag package — where no class could be chosen and
// TestNoStderrOutsideThisPackage could not see the write. refusal.NewFlags
// captures that text instead, leaving the two outcomes ExitOnError used to
// handle: -h is a request rather than a refusal, so it keeps its exit 0, and a
// mistyped flag is the caller's to correct, so it keeps its exit 2. Both print
// the bytes the flag package itself produced.
func parseVerbFlags(fs *refusal.Flags, verb string, args []string) {
	err := fs.Parse(args)
	if err == nil {
		return
	}
	if errors.Is(err, flag.ErrHelp) {
		refusal.Info(fs.Output()).Print()
		os.Exit(0)
	}
	refusal.NoReport(
		fmt.Sprintf("endless-go sandbox %s: %s", verb, err),
		"Correct the flag and retry",
	).Command("sandbox " + verb).Text(fs.Output()).Exit(2)
}

// relayed prepares an error raised deeper in this package for printing, with
// the verb prefix a person has always read in front of it.
//
// The class travels with the error rather than being re-decided here:
// validateName and Provision construct classified refusals, so a sandbox name
// the caller can simply correct stays NO-REPORT all the way to the print site.
// refusal.From faults anything unclassified, which is the right reading of the
// mkdir / OpenRoot / meta-write failures underneath — those mean the sandbox
// tree itself is broken, not that the command was misused.
func relayed(verb string, err error) *refusal.Error {
	return refusal.From(err).
		Command("sandbox " + verb).
		Text(fmt.Sprintf("endless-go sandbox %s: %v", verb, err))
}
