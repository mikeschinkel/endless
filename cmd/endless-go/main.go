// Command endless-go is the single Go binary for endless's former
// per-binary tools, collapsed into one dispatcher with a subcommand each
// (E-1367).
//
// Subcommand layout — preserves the inner verbs each former binary
// already parsed:
//
//	endless-go event         emit|validate-db|rebuild-db|apply-change|backup|reap-worktrees
//	endless-go hook          prompt|claude|codex
//	endless-go sandbox       run|enter|init|bind|list|prune|destroy
//	endless-go tmux          apply|status-line|active-id|show-menu
//	endless-go session-query list-live|task-plan|resume-target
//	endless-go worktree      in-use|ledger-orphans  (the shared "is this worktree
//	                         still in use" guard; and which of a branch's ledger
//	                         commits the base branch provably already holds)
//	endless-go session-status  (renders the per-session status view; --monitor loops it)
//	endless-go project-status  (renders the project status view; --monitor loops it)
//	endless-go project-window  (creates the dedicated two-pane tmux session behind `project monitor --tmux`)
//	endless-go spawn-window  (the multiplexer seam: creates the tmux window that launches Claude on a task)
//	endless-go spawn-layout  (the pane layout around an existing Claude pane — resume and claim reach it too)
//	endless-go spawn-launch  (internal: sets @endless_* window options, then execs claude inside the window)
//	endless-go template      render
//	endless-go markdown      render
//	endless-go task-status   groups|get|has|sql-list|rank|label|glyph  (the status vocabulary; no DB)
//	endless-go session-state groups|get|has|sql-list|rank|label|glyph  (the session state vocabulary; no DB)
//	endless-go jobs          list|run|retry   (E-698 fire-once background job runner)
//	endless-go errors        show|clear|codes (E-698 machine-local fault record)
//
// Per-subcommand DB-context contract (must run BEFORE the subcommand
// body):
//
//   - hook → ENDLESS_NO_HOOKS=true short-circuit (E-1470), then PinMainDB (E-1450/E-1429).
//   - tmux → PinMainDB (E-1429).
//   - session-status → PinMainDB on its normal path (it reads the live sessions
//     table, which hook writes pin to main regardless of cwd), but with --task
//     (headless/tests) it skips the pin and reads the resolved sandbox/
//     --config-dir context; the decision lives in sessionstatuscmd.Run (E-1685).
//   - project-status, project-window → the same rule and the same reason, for
//     the same single-database join; the headless escape is --project-id and the
//     decision lives in projectstatuscmd.resolveProject (E-1976).
//   - event, session-query, worktree → ConsumeDBContextFlag (E-1429).
//   - sandbox → no DB-context init.
//
// The ENDLESS_NO_HOOKS gate is scoped to the `hook` subcommand only —
// it must not bleed into other subcommands.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mikeschinkel/go-cfgstore"
	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/errorscmd"
	"github.com/mikeschinkel/endless/internal/eventcmd"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/hookcmd"
	"github.com/mikeschinkel/endless/internal/jobscmd"
	"github.com/mikeschinkel/endless/internal/markdowncmd"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/outputstylecmd"
	"github.com/mikeschinkel/endless/internal/projectstatuscmd"
	"github.com/mikeschinkel/endless/internal/sandboxcmd"
	"github.com/mikeschinkel/endless/internal/sessionquerycmd"
	"github.com/mikeschinkel/endless/internal/sessionstatecmd"
	"github.com/mikeschinkel/endless/internal/sessionstatuscmd"
	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
	"github.com/mikeschinkel/endless/internal/taskstatuscmd"
	"github.com/mikeschinkel/endless/internal/templatecmd"
	"github.com/mikeschinkel/endless/internal/tmuxcmd"
	"github.com/mikeschinkel/endless/internal/worktreecmd"

	// Job registrations (E-698). Imported for side effect only: each package's
	// init() adds itself to the jobs registry. This is the ONE place the
	// registry is populated, so both triggers in this binary — `jobs run` and
	// the session monitor's per-refresh RunDue — see the same set.
	_ "github.com/mikeschinkel/endless/internal/backupjob"
	_ "github.com/mikeschinkel/endless/internal/minimizerjob"
	_ "github.com/mikeschinkel/endless/internal/triagejob"
	_ "github.com/mikeschinkel/endless/internal/unlandedjob"
	"github.com/mikeschinkel/endless/internal/verifycmd"
)

func main() {
	// go-cfgstore refuses to run without a package-global logger and PANICS in
	// EnsureLogger rather than degrading. Set it first, before anything can reach
	// config.Load.
	//
	// This is not new surface for E-1976, it is a latent gap that task made
	// reachable. Nothing set the logger, and the three config.Load call sites
	// (monitor.GetTrackingMode, monitor.IsCheckEnabled, and now the board's
	// tmux.session_name lookup) survived only because ~/.config/endless/config.json
	// happens to exist on a developed machine: cfgstore reaches the logger on the
	// path where it CREATES a missing config, so a fresh install — or any run
	// under a temp HOME, which is exactly what the verify runner builds — panics.
	//
	// Warn level to stderr: cfgstore logs real problems (a config file it could
	// not close), and stderr is safe for the hook, whose stdout is a single JSON
	// document.
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	// E-1429: the Python CLI threads --db main|sandbox through as
	// --config-dir <dir>. Consume scans os.Args, strips the flag, and
	// applies the config dir. Must run BEFORE reading os.Args[1] so
	// the subcommand is identified after the flag has been removed —
	// otherwise `endless-go --config-dir /path event emit ...` would
	// mistake "--config-dir" for the subcommand. Safe to always call:
	// when the flag is absent it is a no-op, and for hook/tmux
	// the PinMainDB override below still wins via dbPathOverride.
	monitor.ConsumeDBContextFlag()

	// E-1368: when no explicit --config-dir was given, self-detect the
	// per-worktree sandbox from cwd and route to it. Replaces the bin-sandbox/
	// wrapper scripts (which set XDG_CONFIG_HOME and exec'd the worktree
	// binary). No-op outside a self-dev worktree or when its sandbox doesn't
	// exist; explicit --config-dir already won above and is left untouched.
	// Runs before the PinMainDB switch so hook/tmux still move the DB
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
	// activity in the main database regardless of cwd or XDG_CONFIG_HOME
	// (hook-fired writes, tmux pane/task status). Pin pins the DB to main
	// unconditionally and satisfies the worktree gate via dbPathOverride.
	// Other subcommands stay on whatever --config-dir (or absence of one)
	// ConsumeDBContextFlag already established above.
	switch sub {
	case "hook", "tmux":
		// An explicit --config-dir wins over the main pin (E-1429: a
		// per-invocation flag is trustworthy; the env-driven pin is the
		// fallback). Production invokers of these binaries never pass
		// --config-dir, so the pin still applies for real hook/tmux
		// traffic; only tests and sandbox tooling flip this.
		//
		// `errors` MUST NOT be added here (tried and reverted under E-1950).
		// These two are machine-invoked: a hook fires, tmux redraws. Nobody
		// types them, so pinning main cannot surprise anyone. `errors` is
		// typed by a human — and pinning it made `endless errors clear`, run
		// from a worktree with no --db, silently dismiss incidents in the
		// REAL record. PinMainDB satisfies
		// dbContextExplicit(), so adding a user-facing verb here does not just
		// choose a database: it switches off the E-1429 gate for that verb.
		// The coherence problem that motivated it is solved by REQUIRING --db
		// (see errors_cmd in cli.py), not by choosing a database on the user's
		// behalf.
		if !monitor.HasExplicitDBContext() {
			monitor.PinMainDB()
		}
	}
	// E-698: wire the fault recorder. faults imports nothing from the rest of
	// Endless — a dependency on monitor there would become an import cycle the
	// moment monitor itself reports a fault (E-1884) — so the DB accessor, the
	// detail-log directory and the project resolver are injected here, once, for
	// every subcommand. It must run AFTER the DB-context resolution above so a
	// fault raised in a self-dev worktree lands in that worktree's sandbox rather
	// than the main database. All three funcs are stored, not called, so this
	// costs nothing in a process that never records a fault.
	faults.Bind(monitor.DB, func() string {
		return filepath.Join(monitor.ConfigDir(), "log")
	}, resolveFaultProject)

	// session-status pins main itself, but only on its normal tmux-resolved path;
	// with --task (headless/tests) it deliberately reads the resolved sandbox
	// context instead, so the decision lives inside sessionstatuscmd.Run (E-1685).
	// project-status/project-window do the same, keyed off --project-id (E-1976).

	// Let the worktree reaper clean up each reaped worktree's sandbox (E-1904).
	// Wired here because sandboxcmd imports monitor, so monitor cannot call into
	// it directly. Stored, not called — free in a process that never reaps.
	monitor.ReapSandbox = sandboxcmd.ReapSandboxForWorktree

	switch sub {
	case "event":
		eventcmd.Run(rest)
	case "hook":
		hookcmd.Run(rest)
	case "sandbox":
		sandboxcmd.Run(rest)
	case "tmux":
		tmuxcmd.Run(rest)
	case "session-query":
		sessionquerycmd.Run(rest)
	case "worktree":
		worktreecmd.Run(rest)
	case "session-status":
		sessionstatuscmd.Run(rest)
	case "project-status", "project-window":
		projectstatuscmd.Run(sub, rest)
	case "spawn-window", "spawn-layout", "spawn-launch":
		spawnlaunchcmd.Run(sub, rest)
	case "template":
		templatecmd.Run(rest)
	case "outputstyle":
		outputstylecmd.Run(rest)
	case "markdown":
		markdowncmd.Run(rest)
	case "task-status":
		taskstatuscmd.Run(rest)
	case "session-state":
		sessionstatecmd.Run(rest)
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

// resolveFaultProject is the faults package's ProjectResolver (E-1960): it turns
// a producer's explicit project id — or 0, meaning "you decide" — into the
// (id, name) pair recorded against a fault.
//
// It lives here rather than in internal/faults because that package imports
// nothing from the rest of Endless, and rather than in internal/monitor because
// the policy is the process's, not the store's: which project a fault belongs to
// when nobody said is "the one this process is working in", and only main knows
// that this process is a CLI invoked from a working directory.
//
// Read-only by construction. monitor.ProjectForCwd walks up from the cwd and
// reports nothing when no registered project encloses it; the otherwise-identical
// ProjectIDForPath auto-registers on a miss, which is right for a hook that must
// record activity somewhere and catastrophic here — recording a diagnostic would
// mint a project row for every directory anyone ever ran `endless` in.
//
// Every failure is answered with (0, ""), never with an error: attribution is a
// refinement of a fault report, and a fault that cannot say where it happened
// must still be recorded. That covers the honest cases too — a command run
// outside any registered project, or inside a sandbox whose database has no
// projects rows.
func resolveFaultProject(explicit int64) (projectID int64, name string) {
	if explicit != 0 {
		// The producer already decided WHICH project; the lookup is what proves
		// it exists. An id that resolves to no row is not attribution, it is a
		// dangling reference — errors.project_id is a foreign key, so recording
		// it would have the write rejected outright — so the fault falls back to
		// unattributed rather than to a claim nothing backs.
		_, name, err := monitor.ProjectNameByID(explicit)
		if err != nil {
			return 0, ""
		}
		return explicit, name
	}
	id, resolved, err := monitor.ProjectForCwd()
	if err != nil {
		return 0, ""
	}
	return id, resolved
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go <subcommand> [args...]")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  event          emit|validate-db|rebuild-db|apply-change|backup|reap-worktrees")
	fmt.Fprintln(w, "  hook           prompt|claude|codex")
	fmt.Fprintln(w, "  sandbox        run|enter|init|bind|list|prune|destroy")
	fmt.Fprintln(w, "  tmux           apply|status-line|active-id|show-menu")
	fmt.Fprintln(w, "  session-query  list-live|task-plan|resume-target")
	fmt.Fprintln(w, "  worktree       in-use  (is this worktree still in use?)")
	fmt.Fprintln(w, "  session-status render the per-session status view (--monitor loops it)")
	fmt.Fprintln(w, "  project-status render the project status view (--monitor loops it)")
	fmt.Fprintln(w, "  project-window create the two-pane tmux session behind project monitor --tmux")
	fmt.Fprintln(w, "  spawn-window   create the tmux window that launches Claude on a task")
	fmt.Fprintln(w, "  spawn-layout   build the standard pane layout around an existing Claude pane")
	fmt.Fprintln(w, "  spawn-launch   (internal) set window options and exec claude inside the window")
	fmt.Fprintln(w, "  template       render")
	fmt.Fprintln(w, "  markdown       render (markdown → colorized ANSI)")
	fmt.Fprintln(w, "  task-status    groups|get|has|sql-list|rank|label|glyph  (the task status vocabulary)")
	fmt.Fprintln(w, "  session-state  groups|get|has|sql-list|rank|label|glyph  (the session state vocabulary)")
	fmt.Fprintln(w, "  verify         [--keep] <task-id>  (run a task's Tier-0 verification suite)")
	fmt.Fprintln(w, "  jobs           list|run|retry  (the fire-once background job runner)")
	fmt.Fprintln(w, "  errors         show|clear|codes  (machine-local fault record)")
}
