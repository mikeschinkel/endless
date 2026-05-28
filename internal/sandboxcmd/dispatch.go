// Package sandboxcmd implements the `endless-go sandbox` subcommand
// and the per-worktree sandbox tooling (E-1281).
package sandboxcmd

import (
	"fmt"
	"os"
)

func Run(args []string) {
	if len(args) < 1 {
		usage(os.Stderr)
		os.Exit(1)
	}
	switch args[0] {
	case "run":
		runCmd(args[1:])
	case "enter":
		enterCmd(args[1:])
	case "init":
		initCmd(args[1:])
	case "bind":
		bindCmd(args[1:])
	case "list":
		listCmd(args[1:])
	case "prune":
		pruneCmd(args[1:])
	case "destroy":
		destroyCmd(args[1:])
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-go sandbox: unknown command %q\n", args[0])
		usage(os.Stderr)
		os.Exit(1)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go sandbox <command> [flags] [args]")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  run     [--clone] [--name N] [--keep] -- <cmd> [args]")
	fmt.Fprintln(w, "  enter   [--clone] <name>")
	fmt.Fprintln(w, "  init    [--mode empty|seed|clone] [--force] <name>")
	fmt.Fprintln(w, "  bind    <worktree-path> [<sandbox-name>]")
	fmt.Fprintln(w, "  list")
	fmt.Fprintln(w, "  prune   [--older-than DURATION]")
	fmt.Fprintln(w, "  destroy [--force] [--if-exists] <name>")
}
