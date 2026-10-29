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
	if seedArgs, ok := legacyInit(args); ok {
		seedCmd(seedArgs)
		return
	}
	switch args[0] {
	case "seed":
		seedCmd(args[1:])
	case "reset":
		resetCmd(args[1:])
	case "claude-settings-repair":
		claudeSettingsRepairCmd(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		// Not the version-skew case endless-go's own dispatcher has to allow
		// for: what relays a verb here is `endless sandbox reset`, which only
		// ever passes `reset`, and Endless's own seed hook, which asks before
		// it passes `seed`. An unknown command is therefore something the
		// caller typed, and typing a different one is the whole remedy.
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox: unknown command %q", args[0]),
			"Re-run with one of the listed subcommands",
		).Command("sandbox").Detail(usageText()).Exit(1)
	}
}

// legacyInit bridges `init --mode worktree [--force]`, the spelling `seed`
// replaced (E-2481), and returns seed's argv when args are that form.
//
// A bridge, not a feature: `endless sandbox reset` runs the seed hook from the
// MAIN checkout against the WORKTREE's binary, so until every worktree's binary
// and main's hook agree, one side still speaks the old verb. Hidden from the
// usage text; E-2484 removes it. Any other `init` form is not bridged and is
// refused as an unknown command.
func legacyInit(args []string) (seedArgs []string, ok bool) {
	if len(args) == 0 || args[0] != "init" {
		return nil, false
	}
	seedArgs = []string{}
	var sawMode bool
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--mode=worktree" || a == "-mode=worktree":
			sawMode = true
		case (a == "--mode" || a == "-mode") && i+1 < len(args) && args[i+1] == "worktree":
			sawMode = true
			i++
		case a == "--force" || a == "-force":
			seedArgs = append(seedArgs, "--force")
		default:
			return nil, false
		}
	}
	if !sawMode {
		return nil, false
	}
	return seedArgs, true
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go sandbox <command> [flags] [args]",
		"Commands:",
		"  seed    [--force]  (seed Endless's database into this worktree's sandbox)",
		"  reset              (clear + reseed this worktree's sandbox)",
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
// The class travels with the error rather than being re-decided here: an error
// constructed as a classified refusal keeps its class all the way to the print
// site, and refusal.From faults anything unclassified, which is the right
// reading of a mkdir or seeding failure underneath — that means the sandbox
// itself is broken, not that the command was misused.
func relayed(verb string, err error) *refusal.Error {
	return refusal.From(err).
		Command("sandbox " + verb).
		Text(fmt.Sprintf("endless-go sandbox %s: %v", verb, err))
}
