package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "enter":
		enterCmd(os.Args[2:])
	case "init":
		initCmd(os.Args[2:])
	case "bind":
		bindCmd(os.Args[2:])
	case "list":
		listCmd(os.Args[2:])
	case "prune":
		pruneCmd(os.Args[2:])
	case "destroy":
		destroyCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-sandbox: unknown command %q\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(1)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-sandbox <command> [flags] [args]")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  run     [--clone] [--name N] [--keep] -- <cmd> [args]")
	fmt.Fprintln(w, "  enter   [--clone] <name>")
	fmt.Fprintln(w, "  init    [--mode empty|seed|clone] [--force] <name>")
	fmt.Fprintln(w, "  bind    <worktree-path> [<sandbox-name>]")
	fmt.Fprintln(w, "  list")
	fmt.Fprintln(w, "  prune   [--older-than DURATION]")
	fmt.Fprintln(w, "  destroy [--force] [--if-exists] <name>")
}
