// Command endless-go is the single Go binary for endless's seven former
// per-binary tools, collapsed into one dispatcher with seven subcommands
// (E-1367).
//
// Subcommand layout — preserves the inner verbs each former binary
// already parsed:
//
//	endless-go event         emit|validate-db|rebuild-db|apply-change|backup|reap-worktrees
//	endless-go hook          prompt|claude|codex|recap
//	endless-go channel       (MCP server; no verbs)
//	endless-go sandbox       run|enter|init|bind|list|prune|destroy
//	endless-go serve         [port]
//	endless-go tmux          apply|status-line|active-id|show-menu
//	endless-go session-query list-live|task-text|reopen-context
//	endless-go template      render
//
// Per-subcommand DB-context contract (must run BEFORE the subcommand
// body):
//
//   - hook → ENDLESS_NO_HOOKS=true short-circuit (E-1470), then PinMainDB (E-1450/E-1429).
//   - channel, tmux → PinMainDB (E-1429).
//   - event, serve, session-query → ConsumeDBContextFlag (E-1429).
//   - sandbox → no DB-context init.
//
// The ENDLESS_NO_HOOKS gate is scoped to the `hook` subcommand only —
// it must not bleed into other subcommands.
package main

import (
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/channelcmd"
	"github.com/mikeschinkel/endless/internal/eventcmd"
	"github.com/mikeschinkel/endless/internal/hookcmd"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sandboxcmd"
	"github.com/mikeschinkel/endless/internal/servecmd"
	"github.com/mikeschinkel/endless/internal/sessionquerycmd"
	"github.com/mikeschinkel/endless/internal/templatecmd"
	"github.com/mikeschinkel/endless/internal/tmuxcmd"
)

func main() {
	// E-1429: the Python CLI threads --db main|sandbox through as
	// --config-dir <dir>. Consume scans os.Args, strips the flag, and
	// applies the config dir. Must run BEFORE reading os.Args[1] so
	// the subcommand is identified after the flag has been removed —
	// otherwise `endless-go --config-dir /path event emit ...` would
	// mistake "--config-dir" for the subcommand. Safe to always call:
	// when the flag is absent it is a no-op, and for hook/channel/tmux
	// the PinMainDB override below still wins via dbPathOverride.
	monitor.ConsumeDBContextFlag()

	// E-1368: when no explicit --config-dir was given, self-detect the
	// per-worktree sandbox from cwd and route to it. Replaces the bin-sandbox/
	// wrapper scripts (which set XDG_CONFIG_HOME and exec'd the worktree
	// binary). No-op outside a self-dev worktree or when its sandbox doesn't
	// exist; explicit --config-dir already won above and is left untouched.
	// Runs before the PinMainDB switch so hook/channel/tmux still move the DB
	// to main while their config.json/logs follow the self-detected sandbox.
	monitor.SelfDetectWorktreeSandbox()

	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	sub := os.Args[1]
	rest := os.Args[2:]

	switch sub {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return
	}

	// E-1470: ENDLESS_NO_HOOKS short-circuit. Scoped to `hook` only —
	// internal headless `claude -p` calls (verb-check, recap) set this
	// env var to suppress hook side effects (session registration,
	// activity, pane-collision). Must run BEFORE PinMainDB and before
	// any DB work.
	if sub == "hook" && os.Getenv("ENDLESS_NO_HOOKS") == "true" {
		return
	}

	// E-1669: never-silent backstop. When a hook fires inside a self_dev
	// worktree but from a FOREIGN endless-go build (the global/main one, because
	// provisioning was skipped/failed and .claude/settings.json never got
	// repointed), warn loudly to stderr — the session would otherwise dogfood
	// main's hook code, not the candidate. A warning, NOT a refuse: refusing in
	// the hook path blocks every tool call. No-op outside a self_dev worktree or
	// when the worktree's own binary is already running.
	if sub == "hook" {
		warnForeignHookBuild()
	}

	// E-1450/E-1429: PinMainDB for surfaces whose writes are real-world
	// activity in the real ledger regardless of cwd or XDG_CONFIG_HOME
	// (hook-fired writes, MCP channel state, tmux pane/task status).
	// Pin pins the DB to main unconditionally and satisfies the
	// worktree gate via dbPathOverride. Other subcommands stay on
	// whatever --config-dir (or absence of one) ConsumeDBContextFlag
	// already established above.
	switch sub {
	case "hook", "channel", "tmux":
		monitor.PinMainDB()
	}

	switch sub {
	case "event":
		eventcmd.Run(rest)
	case "hook":
		hookcmd.Run(rest)
	case "channel":
		channelcmd.Run(rest)
	case "sandbox":
		sandboxcmd.Run(rest)
	case "serve":
		servecmd.Run(rest)
	case "tmux":
		tmuxcmd.Run(rest)
	case "session-query":
		sessionquerycmd.Run(rest)
	case "template":
		templatecmd.Run(rest)
	default:
		fmt.Fprintf(os.Stderr, "endless-go: unknown subcommand %q\n", sub)
		usage(os.Stderr)
		os.Exit(2)
	}
}

// warnForeignHookBuild prints a one-line stderr warning when this hook process
// is a foreign endless-go build serving a self_dev worktree (E-1669). It never
// blocks: a warning, not a refuse, since refusing the hook would block every
// tool call. No-op outside a self_dev worktree or when the worktree's own
// binary is running. Best-effort — any error resolving the executable or cwd
// silently skips the check rather than risk noise on a healthy session.
func warnForeignHookBuild() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	expected, foreign := monitor.ForeignHookBuild(cwd, exe)
	if !foreign {
		return
	}
	fmt.Fprintf(os.Stderr,
		"endless-go: WARNING: hook is running a foreign build (%s) inside a "+
			"self_dev worktree; expected %s. The worktree was not fully "+
			"provisioned — re-run its .endless/hooks/post-worktree-create.sh "+
			"(or `just build` + `just claude-settings-init`) so the session "+
			"dogfoods candidate code.\n",
		exe, expected)
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go <subcommand> [args...]")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  event          emit|validate-db|rebuild-db|apply-change|backup|reap-worktrees")
	fmt.Fprintln(w, "  hook           prompt|claude|codex|recap")
	fmt.Fprintln(w, "  channel        MCP server for inter-session channels")
	fmt.Fprintln(w, "  sandbox        run|enter|init|bind|list|prune|destroy")
	fmt.Fprintln(w, "  serve          [port]  (web dashboard)")
	fmt.Fprintln(w, "  tmux           apply|status-line|active-id|show-menu")
	fmt.Fprintln(w, "  session-query  list-live|task-text|reopen-context")
	fmt.Fprintln(w, "  template       render")
}
