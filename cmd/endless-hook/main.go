package main

import (
	"fmt"
	"log"
	"os"

	"github.com/mikeschinkel/endless/internal/monitor"

	_ "modernc.org/sqlite"
)

func main() {
	// E-1470: internal headless `claude -p` calls (verb-check, recap) run with
	// ENDLESS_NO_HOOKS=true. They inherit the caller's TMUX_PANE; if this hook
	// ran it would register a fresh session for the headless call, and the
	// pane-collision rule (internal/monitor) would then mark the live caller's
	// session ended (same pane, different UUID). Short-circuit before any DB
	// work so the hook fires no session registration, activity, or SessionEnd
	// side effect. Must be the first thing in main.
	if os.Getenv("ENDLESS_NO_HOOKS") == "true" {
		return
	}

	// E-1450/E-1429: hook-fired writes (session registration, activity, state
	// transitions) are real-world activity and must ALWAYS target the real DB,
	// regardless of cwd or XDG_CONFIG_HOME. PinMainDB pins the DB to main
	// unconditionally; it also satisfies the E-1429 self-dev-worktree gate so
	// monitor.DB() never refuses the hook. (The earlier ForceRealDB only
	// redirected when XDG pointed at a sandbox — so a hook firing with a
	// worktree cwd but no sandbox injection, e.g. a main-launched session
	// cd'd into the worktree, hit the gate and was refused. E-1429 regression.)
	// ConfigDir() is left on XDG, so config.json/logs still follow the
	// worktree. Must precede the first monitor.DB() call.
	monitor.PinMainDB()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: endless-hook <command> [args...]")
		fmt.Fprintln(os.Stderr, "Commands: prompt, claude, codex, recap")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "prompt":
		err = runPrompt(os.Args[2:])
	case "claude":
		err = runClaude(os.Args[2:])
	case "codex":
		err = runCodex(os.Args[2:])
	case "recap":
		err = runRecap(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}

	if err != nil {
		log.Printf("%s: %v", os.Args[1], err)
		os.Exit(1)
	}
}
