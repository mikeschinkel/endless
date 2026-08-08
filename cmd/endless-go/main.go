// Command endless-go is the single Go binary for endless's seven former
// per-binary tools, collapsed into one dispatcher with seven subcommands
// (E-1367).
//
// Subcommand layout — preserves the inner verbs each former binary
// already parsed:
//
//	endless-go event         emit|validate-db|rebuild-db|apply-change|backup|reap-worktrees
//	endless-go hook          prompt|claude|codex
//	endless-go channel       (MCP server; no verbs)
//	endless-go sandbox       run|enter|init|bind|list|prune|destroy
//	endless-go serve         [port]
//	endless-go tmux          apply|status-line|active-id|show-menu
//	endless-go session-query list-live|task-text|reopen-context
//	endless-go session-status  (renders the per-session status view; --monitor loops it)
//	endless-go spawn-window  (the multiplexer seam: creates the tmux window that launches Claude on a task)
//	endless-go spawn-launch  (internal: sets @endless_* window options, then execs claude inside the window)
//	endless-go template      render
//	endless-go markdown      render
//	endless-go jobs          list|run|retry   (E-698 fire-once background job runner)
//	endless-go errors        show|clear|codes (E-698 machine-local fault record)
//
// Per-subcommand DB-context contract (must run BEFORE the subcommand
// body):
//
//   - hook → ENDLESS_NO_HOOKS=true short-circuit (E-1470), then PinMainDB (E-1450/E-1429).
//   - channel, tmux → PinMainDB (E-1429).
//   - session-status → PinMainDB on its normal path (it reads the live sessions
//     table, which hook writes pin to main regardless of cwd), but with --task
//     (headless/tests) it skips the pin and reads the resolved sandbox/
//     --config-dir context; the decision lives in sessionstatuscmd.Run (E-1685).
//   - event, serve, session-query → ConsumeDBContextFlag (E-1429).
//   - sandbox → no DB-context init.
//
// The ENDLESS_NO_HOOKS gate is scoped to the `hook` subcommand only —
// it must not bleed into other subcommands.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/channelcmd"
	"github.com/mikeschinkel/endless/internal/errorscmd"
	"github.com/mikeschinkel/endless/internal/eventcmd"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/hookcmd"
	"github.com/mikeschinkel/endless/internal/jobscmd"
	"github.com/mikeschinkel/endless/internal/markdowncmd"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/outputstylecmd"
	"github.com/mikeschinkel/endless/internal/sandboxcmd"
	"github.com/mikeschinkel/endless/internal/servecmd"
	"github.com/mikeschinkel/endless/internal/sessionquerycmd"
	"github.com/mikeschinkel/endless/internal/sessionstatuscmd"
	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
	"github.com/mikeschinkel/endless/internal/templatecmd"
	"github.com/mikeschinkel/endless/internal/tmuxcmd"

	// Job registrations (E-698). Imported for side effect only: each package's
	// init() adds itself to the jobs registry. This is the ONE place the
	// registry is populated, so both triggers in this binary — `jobs run` and
	// the session monitor's per-refresh RunDue — see the same set.
	_ "github.com/mikeschinkel/endless/internal/triagejob"
	"github.com/mikeschinkel/endless/internal/verifycmd"
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
	// internal headless `claude -p` calls (the verb-check) set this
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
		// An explicit --config-dir wins over the main pin (E-1429: a
		// per-invocation flag is trustworthy; the env-driven pin is the
		// fallback). Production invokers of these binaries never pass
		// --config-dir, so the pin still applies for real hook/channel/tmux
		// traffic; only tests and sandbox tooling (e.g. the E-1682 nav-trail
		// verify driving `tmux record-nav` against a sandbox DB) flip this.
		if !monitor.HasExplicitDBContext() {
			monitor.PinMainDB()
		}
	}
	// E-698: wire the fault recorder. faults imports nothing from the rest of
	// Endless — a dependency on monitor there would become an import cycle the
	// moment monitor itself reports a fault (E-1884) — so the DB accessor and the
	// detail-log directory are injected here, once, for every subcommand. It must
	// run AFTER the DB-context resolution above so a fault raised in a self-dev
	// worktree lands in that worktree's sandbox rather than the real ledger.
	// Both funcs are stored, not called, so this costs nothing in a process that
	// never records a fault.
	faults.Bind(monitor.DB, func() string {
		return filepath.Join(monitor.ConfigDir(), "log")
	})

	// session-status pins main itself, but only on its normal tmux-resolved path;
	// with --task (headless/tests) it deliberately reads the resolved sandbox
	// context instead, so the decision lives inside sessionstatuscmd.Run (E-1685).

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
	case "session-status":
		sessionstatuscmd.Run(rest)
	case "spawn-window", "spawn-launch":
		spawnlaunchcmd.Run(sub, rest)
	case "template":
		templatecmd.Run(rest)
	case "outputstyle":
		outputstylecmd.Run(rest)
	case "markdown":
		markdowncmd.Run(rest)
	case "verify":
		verifycmd.Run(rest)
	case "jobs":
		jobscmd.Run(rest)
	case "errors":
		errorscmd.Run(rest)
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
	fmt.Fprintln(w, "  hook           prompt|claude|codex")
	fmt.Fprintln(w, "  channel        MCP server for inter-session channels")
	fmt.Fprintln(w, "  sandbox        run|enter|init|bind|list|prune|destroy")
	fmt.Fprintln(w, "  serve          [port]  (web dashboard)")
	fmt.Fprintln(w, "  tmux           apply|status-line|active-id|show-menu")
	fmt.Fprintln(w, "  session-query  list-live|task-text|reopen-context")
	fmt.Fprintln(w, "  session-status render the per-session status view (--monitor loops it)")
	fmt.Fprintln(w, "  spawn-window   create the tmux window that launches Claude on a task")
	fmt.Fprintln(w, "  spawn-launch   (internal) set window options and exec claude inside the window")
	fmt.Fprintln(w, "  template       render")
	fmt.Fprintln(w, "  markdown       render (markdown → colorized ANSI)")
	fmt.Fprintln(w, "  verify         [--keep] <task-id>  (run a task's Tier-0 verification suite)")
	fmt.Fprintln(w, "  jobs           list|run|retry  (the fire-once background job runner)")
	fmt.Fprintln(w, "  errors         show|clear|codes  (machine-local fault record)")
}
