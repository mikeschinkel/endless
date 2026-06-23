// Package sessionquerycmd implements the `endless-go session-query`
// subcommand: an internal helper that exposes monitor.* read operations
// as JSON for the Python CLI. It exists so the Python side can avoid
// extending the legacy `db.query` pattern (E-894). Subcommands are
// intentionally narrow — one verb per Python need.
package sessionquerycmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mikeschinkel/endless/internal/gatekind"
	"github.com/mikeschinkel/endless/internal/monitor"
	_ "modernc.org/sqlite"
)

func Run(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "list-live":
		if err := runListLive(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "task-text":
		if err := runTaskText(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "ensure-claude-id":
		if err := runEnsureClaudeID(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "record-bg-agent":
		if err := runRecordBgAgent(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "count-bg-agents":
		if err := runCountBgAgents(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "list-bg-agents":
		if err := runListBgAgents(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "gate-clear":
		if err := runGateClear(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: endless-go session-query <subcommand>")
	fmt.Fprintln(os.Stderr, "subcommands:")
	fmt.Fprintln(os.Stderr, "  list-live --project-root <path>   JSON array of live sessions for the project")
	fmt.Fprintln(os.Stderr, "  task-text --id <task-id>          raw tasks.text for the task (empty if none)")
	fmt.Fprintln(os.Stderr, "  ensure-claude-id --session-id <uuid> --project-root <path> [--process <pane>]")
	fmt.Fprintln(os.Stderr, "                                    look up (or lazy-create) sessions.id; prints integer id")
	fmt.Fprintln(os.Stderr, "  record-bg-agent --task-id <id> --short-id <handle>")
	fmt.Fprintln(os.Stderr, "                                    insert a background-agent dispatch row; prints sessions.id")
	fmt.Fprintln(os.Stderr, "  count-bg-agents --task-id <id>    count `working` bg agents in the task's project; prints the integer")
	fmt.Fprintln(os.Stderr, "  list-bg-agents (--session-id <id> | --epic-id <id> | --all --project-root <path>)")
	fmt.Fprintln(os.Stderr, "                                    JSON {scope, epic_id, agents} of working bg agents (E-1621)")
	fmt.Fprintln(os.Stderr, "  gate-clear --session-id <id> --kind <slug> --cleared-by <reason>")
	fmt.Fprintln(os.Stderr, "                                    clear the session's open gate of the kind; prints rows cleared")
}

// runGateClear closes the session's open gate of the given kind, recording the
// cleared_by reason, and prints how many open rows were cleared (0 = nothing was
// pending). It backs the `endless task continue` / `endless task pause` verbs so
// the Python side clears a gate without a Python DB write (E-1486 / E-1542).
// --session-id is the integer sessions.id PK (the Python resolver supplies it).
func runGateClear(args []string) error {
	fs := flag.NewFlagSet("gate-clear", flag.ContinueOnError)
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK) to clear")
	kind := fs.String("kind", "", "gate kind slug (e.g. revisit)")
	clearedBy := fs.String("cleared-by", "", "reason recorded in cleared_by")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return fmt.Errorf("--session-id is required")
	}
	gk, err := gatekind.Parse(*kind)
	if err != nil {
		return err
	}
	// The verb-driven clear reasons are the only ones valid on this CLI surface;
	// revisit_resolved / superseded are set by the hook directly via the monitor
	// helper, never through here.
	if *clearedBy != "revisit_continue" && *clearedBy != "revisit_pause" {
		return fmt.Errorf("--cleared-by must be revisit_continue or revisit_pause")
	}
	switch gk {
	case gatekind.GateKindRevisit:
		n, err := monitor.ClearRevisitGate(*sessionID, *clearedBy)
		if err != nil {
			return err
		}
		fmt.Println(n)
		return nil
	default:
		return fmt.Errorf("gate-clear: unsupported kind %q", gk)
	}
}

// runRecordBgAgent inserts the dispatch-time sessions row for a background
// agent launched by `task spawn --bg` (E-1568). The Python side has the task id
// (from the spawn target) and the short id (parsed from `claude --bg` stdout);
// project_id and the epic ancestor are resolved Go-side so the Python flow
// needs no DB read (E-1486). Prints the inserted sessions.id on success.
func runRecordBgAgent(args []string) error {
	fs := flag.NewFlagSet("record-bg-agent", flag.ContinueOnError)
	taskID := fs.Int64("task-id", 0, "spawn target task id")
	shortID := fs.String("short-id", "", "dispatch short id from `claude --bg` stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *taskID == 0 {
		return fmt.Errorf("--task-id is required")
	}
	if *shortID == "" {
		return fmt.Errorf("--short-id is required")
	}

	id, err := monitor.RecordBgAgentSession(*taskID, *shortID)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// runCountBgAgents prints the number of `working` background-agent sessions in
// the task's project (E-1572). The Python `task spawn --bg` soft-throttle
// warning reads this before dispatch; project_id is resolved Go-side from the
// task so the Python flow needs no DB read (E-1486). Prints the integer count.
func runCountBgAgents(args []string) error {
	fs := flag.NewFlagSet("count-bg-agents", flag.ContinueOnError)
	taskID := fs.Int64("task-id", 0, "spawn target task id (project scope resolved from it)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *taskID == 0 {
		return fmt.Errorf("--task-id is required")
	}
	n, err := monitor.CountActiveBgAgents(*taskID)
	if err != nil {
		return err
	}
	fmt.Println(n)
	return nil
}

// runTaskText prints the raw tasks.text for a task id to stdout, so the Python
// side can materialize a plan file at claim time without a Python DB read
// (E-894 / E-1445). Output is the raw text (not JSON) — it is written verbatim
// to <worktree>/.endless/plans/E-NNN.md. Empty output means "no plan".
func runTaskText(args []string) error {
	fs := flag.NewFlagSet("task-text", flag.ContinueOnError)
	id := fs.Int64("id", 0, "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	text, err := monitor.TaskText(*id)
	if err != nil {
		return fmt.Errorf("read task text for E-%d: %w", *id, err)
	}
	_, err = os.Stdout.WriteString(text)
	return err
}

func runListLive(args []string) error {
	fs := flag.NewFlagSet("list-live", flag.ContinueOnError)
	projectRoot := fs.String("project-root", "", "absolute path of the project root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectRoot == "" {
		return fmt.Errorf("--project-root is required")
	}

	projectID, _, err := monitor.ProjectIDForPath(*projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project for %s: %w", *projectRoot, err)
	}
	if projectID == 0 {
		// Unregistered cwd: empty result rather than error so the Python
		// caller can treat "no project" and "no live sessions" uniformly.
		return json.NewEncoder(os.Stdout).Encode([]monitor.LiveSession{})
	}

	sessions, err := monitor.ListLiveSessions(projectID)
	if err != nil {
		return fmt.Errorf("list live sessions: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(sessions)
}

// runEnsureClaudeID prints the integer sessions.id for an env-identified
// Claude session, lazy-creating the row when no hook event has fired yet
// (E-1455). The Python resolver invokes this when CLAUDECODE=1 and
// CLAUDE_CODE_SESSION_ID are set, treating the env vars as authoritative
// identification of the current pane.
//
// --session-id is the Claude harness session UUID (required).
// --project-root is the cwd-resolved project path (required); the
//   helper passes it through monitor.ProjectIDForPath which auto-registers
//   unknown paths.
// --process is the TMUX_PANE value (optional; absent outside tmux).
//
// Output is the integer id followed by a newline. Exit 0 on success.
func runEnsureClaudeID(args []string) error {
	fs := flag.NewFlagSet("ensure-claude-id", flag.ContinueOnError)
	sessionID := fs.String("session-id", "", "Claude harness session UUID")
	projectRoot := fs.String("project-root", "", "absolute path of the project root")
	process := fs.String("process", "", "TMUX_PANE value (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("--session-id is required")
	}
	if *projectRoot == "" {
		return fmt.Errorf("--project-root is required")
	}

	projectID, _, err := monitor.ProjectIDForPath(*projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project for %s: %w", *projectRoot, err)
	}

	id, err := monitor.EnsureClaudeSessionID(*sessionID, *process, projectID)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// bgAgentList is the JSON contract for `list-bg-agents` (E-1621). Scope is
// "epic" (filtered by EpicID) or "all" (the project-scoped --all path). EpicID
// is null when scope is "all", or when a --session-id caller has no active epic
// to resolve — the Python side renders the latter as a guidance error.
type bgAgentList struct {
	Scope  string            `json:"scope"`
	EpicID *int64            `json:"epic_id"`
	Agents []monitor.BgAgent `json:"agents"`
}

// runListBgAgents lists working background-agent sessions for `endless agents`.
// Exactly one of --session-id / --epic-id / --all selects the scope:
//   - --epic-id <id>   : agents whose active_epic_id = id.
//   - --session-id <id>: resolve the caller's active_epic_id, then as above;
//     a NULL epic returns {scope:"epic", epic_id:null, agents:[]}.
//   - --all            : every working bg agent in --project-root's project.
//
// The DB read stays Go-side (no Python DB read, per E-1486); Python formats the
// returned JSON as a plain-text table.
func runListBgAgents(args []string) error {
	fs := flag.NewFlagSet("list-bg-agents", flag.ContinueOnError)
	sessionID := fs.Int64("session-id", 0, "caller's sessions.id; auto-resolves the active epic")
	epicID := fs.Int64("epic-id", 0, "epic task id to scope by (overrides auto-resolve)")
	all := fs.Bool("all", false, "drop the epic filter; list all bg agents in --project-root's project")
	projectRoot := fs.String("project-root", "", "absolute path of the project root (required with --all)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	selected := 0
	for _, on := range []bool{*sessionID != 0, *epicID != 0, *all} {
		if on {
			selected++
		}
	}
	if selected != 1 {
		return fmt.Errorf("exactly one of --session-id, --epic-id, or --all is required")
	}

	if *all {
		if *projectRoot == "" {
			return fmt.Errorf("--project-root is required with --all")
		}
		projectID, _, err := monitor.ProjectIDForPath(*projectRoot)
		if err != nil {
			return fmt.Errorf("resolve project for %s: %w", *projectRoot, err)
		}
		out := bgAgentList{Scope: "all", Agents: []monitor.BgAgent{}}
		if projectID != 0 {
			agents, err := monitor.ListBgAgentsForProject(projectID)
			if err != nil {
				return err
			}
			out.Agents = agents
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	resolved := epicID
	if *sessionID != 0 {
		ep, err := monitor.SessionActiveEpic(*sessionID)
		if err != nil {
			return err
		}
		resolved = ep
	}

	out := bgAgentList{Scope: "epic", EpicID: resolved, Agents: []monitor.BgAgent{}}
	if resolved != nil {
		agents, err := monitor.ListBgAgentsForEpic(*resolved)
		if err != nil {
			return err
		}
		out.Agents = agents
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
