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
	"io"
	"os"

	"github.com/mikeschinkel/endless/internal/events"
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
	case "reap-dead-panes":
		if err := runReapDeadPanes(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "task-text":
		if err := runTaskText(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "task-field":
		if err := runTaskField(args[1:]); err != nil {
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
	case "reopen-context":
		if err := runReopenContext(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "worktree-anomalies":
		os.Exit(runWorktreeAnomalies(args[1:]))
	case "worktree-unsettled":
		if err := runWorktreeUnsettled(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "relay-checkpoint":
		if err := runRelayCheckpoint(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "task-report":
		if err := runTaskReport(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "untriaged-tasks":
		if err := runUntriagedTasks(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "triage-context":
		if err := runTriageContext(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "trail":
		if err := runTrail(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "resume-target":
		if err := runResumeTarget(args[1:]); err != nil {
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
	fmt.Fprintln(os.Stderr, "  reap-dead-panes --project-root <path>")
	fmt.Fprintln(os.Stderr, "                                    end non-ended sessions whose tmux pane is gone (silent; DB error → exit 1)")
	fmt.Fprintln(os.Stderr, "  task-text --id <task-id>          raw tasks.text for the task (empty if none)")
	fmt.Fprintln(os.Stderr, "  task-field --id <task-id> --name <text|outcome|analysis>")
	fmt.Fprintln(os.Stderr, "                                    raw value of one multiline doc column (empty if none)")
	fmt.Fprintln(os.Stderr, "  ensure-claude-id --session-id <uuid> --project-root <path> [--process <pane>]")
	fmt.Fprintln(os.Stderr, "                                    look up (or lazy-create) sessions.id; prints integer id")
	fmt.Fprintln(os.Stderr, "  record-bg-agent --task-id <id> --short-id <handle>")
	fmt.Fprintln(os.Stderr, "                                    insert a background-agent dispatch row; prints sessions.id")
	fmt.Fprintln(os.Stderr, "  count-bg-agents --task-id <id>    count `working` bg agents in the task's project; prints the integer")
	fmt.Fprintln(os.Stderr, "  list-bg-agents (--session-id <id> | --epic-id <id> | --all --project-root <path>)")
	fmt.Fprintln(os.Stderr, "                                    JSON {scope, epic_id, agents} of working bg agents (E-1621)")
	fmt.Fprintln(os.Stderr, "  gate-clear --session-id <id> --kind <slug> --cleared-by <reason>")
	fmt.Fprintln(os.Stderr, "                                    clear the session's open gate of the kind; prints rows cleared")
	fmt.Fprintln(os.Stderr, "  reopen-context --task-id <id>     JSON {inherited_session_id, prior_outcome, last_status_snapshot} for a reopen")
	fmt.Fprintln(os.Stderr, "  worktree-anomalies --worktree-path <path> [--project-root <path>]")
	fmt.Fprintln(os.Stderr, "                                    terse line per genuine handoff anomaly; nothing when clean")
	fmt.Fprintln(os.Stderr, "                                    exit 0 clean, 1 anomalies present, 2 on error")
	fmt.Fprintln(os.Stderr, "  worktree-unsettled <worktree-path>...")
	fmt.Fprintln(os.Stderr, "                                    JSON array of per-worktree unsettled breakdowns (E-1865):")
	fmt.Fprintln(os.Stderr, "                                    {unsettled, modified, unlanded, reason, modified_files, unlanded_log, …}")
	fmt.Fprintln(os.Stderr, "  trail [--client <name>] [--limit N]")
	fmt.Fprintln(os.Stderr, "                                    JSON array of navigation edges newest-first (no --client = all clients)")
	fmt.Fprintln(os.Stderr, "  resume-target --ref <task-id|session-id|uuid>")
	fmt.Fprintln(os.Stderr, "                                    JSON {endless_id, session_id, active_task_id, worktree_path, state,")
	fmt.Fprintln(os.Stderr, "                                    task_type, task_status, task_title, landed_sha} to relaunch (or recover) a lost session")
	fmt.Fprintln(os.Stderr, "  task-report --id <task-id>        JSON {task_id, status, type, landed, successors[]} of a task's computed report facts (E-1771)")
	fmt.Fprintln(os.Stderr, "  untriaged-tasks [--project <name>] [--limit N]")
	fmt.Fprintln(os.Stderr, "                                    JSON array [{id, project, title}] of the triage queue, oldest first (E-1859)")
	fmt.Fprintln(os.Stderr, "  triage-context --id <task-id>     JSON {task_id, project, title, description, type, phase, status, has_text,")
	fmt.Fprintln(os.Stderr, "                                    parent, siblings[], decisions[]} — the persisted artifacts triage may judge (E-1859)")
	fmt.Fprintln(os.Stderr, "  relay-checkpoint --session-id <id>")
	fmt.Fprintln(os.Stderr, "                                    record the sanctioned report text (read from STDIN) the session")
	fmt.Fprintln(os.Stderr, "                                    owes as its final message; the Stop gate enforces it (E-1901)")
}

// runRelayCheckpoint records the verbatim-relay checkpoint written by `endless
// task report` (E-1901): the exact text the session now owes the user as its
// final message. The Stop hook reads it back and blocks the turn if the agent
// appended to it.
//
// The sanctioned text arrives on STDIN, not as a flag. A report block is
// unbounded (follow-ups, notes, questions, a verify command) and contains
// newlines and quotes, so passing it through argv would invite both quoting bugs
// and ARG_MAX truncation — and a truncated sanctioned text would silently gate
// against the wrong string, bouncing a compliant agent forever.
func runRelayCheckpoint(args []string) error {
	fs := flag.NewFlagSet("relay-checkpoint", flag.ContinueOnError)
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK) recording the checkpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return fmt.Errorf("--session-id is required")
	}
	sanctioned, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading sanctioned text from stdin: %w", err)
	}
	return monitor.SetRelayCheckpoint(*sessionID, string(sanctioned))
}

// runTaskReport prints the computed, non-agent-supplied facts for a `task
// report` (E-1771) as JSON: the focal task's status and type, whether it has
// landed, and its downstream successors, each carrying its current status. The
// Python reporting command renders these into a steering prompt so the agent
// never types a fact the tool can compute; it emits only the subset the user
// could not already know. Children were on the wire until E-1911 removed them —
// `session status` is where a task's children belong. Read-only; no persistence
// (that is E-1777).
func runTaskReport(args []string) error {
	fs := flag.NewFlagSet("task-report", flag.ContinueOnError)
	id := fs.Int64("id", 0, "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	facts, err := monitor.BuildTaskReportFacts(*id)
	if err != nil {
		return fmt.Errorf("build report facts for E-%d: %w", *id, err)
	}
	return json.NewEncoder(os.Stdout).Encode(facts)
}

// defaultUntriagedLimit caps a triage sweep that names no limit. It exists so
// a caller that forgets --limit cannot walk an unbounded backlog: every task
// selected here becomes a model call downstream.
const defaultUntriagedLimit = 10

// runUntriagedTasks prints the triage queue (E-1859) as JSON — tasks in
// `untriaged`, oldest first, capped by --limit. No --project means every
// project: the background sweep runs from the job runner, which has a database
// but no cwd, so ledger-wide is the only scope it can express. A human running
// `endless triage run` inside a project passes --project.
func runUntriagedTasks(args []string) error {
	fs := flag.NewFlagSet("untriaged-tasks", flag.ContinueOnError)
	project := fs.String("project", "", "registered project name (default: every project)")
	limit := fs.Int("limit", defaultUntriagedLimit, "max tasks to return")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit <= 0 {
		return fmt.Errorf("--limit must be positive")
	}
	tasks, err := monitor.UntriagedTasks(*project, *limit)
	if err != nil {
		return fmt.Errorf("read untriaged queue: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(tasks)
}

// runTriageContext prints one task's triage context (E-1859) as JSON: the
// persisted artifacts the sufficiency prompt is allowed to judge — description,
// parent, sibling titles, linked decisions. The filing session's transcript is
// deliberately absent; triage judges what is written down, not what was said.
func runTriageContext(args []string) error {
	fs := flag.NewFlagSet("triage-context", flag.ContinueOnError)
	id := fs.Int64("id", 0, "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	ctx, err := monitor.BuildTriageContext(*id)
	if err != nil {
		return fmt.Errorf("build triage context for E-%d: %w", *id, err)
	}
	return json.NewEncoder(os.Stdout).Encode(ctx)
}

// runResumeTarget prints the JSON a `session resume` needs to relaunch a lost
// Claude session: the harness UUID and the task worktree to cd into. The ref
// is resolved task-first (the tmux-tab task id is the primary handle) but also
// accepts a session id or UUID prefix. The DB read stays Go-side (E-1486); the
// Python caller cd's to worktree_path and execs `claude --resume <session_id>`.
func runResumeTarget(args []string) error {
	fs := flag.NewFlagSet("resume-target", flag.ContinueOnError)
	ref := fs.String("ref", "", "task id (E-NNNN/NNNN), session id, or Claude UUID prefix")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ref == "" {
		return fmt.Errorf("--ref is required")
	}
	target, err := monitor.ResolveResumeTarget(*ref)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(target)
}

// runTrail prints the durable session-navigation trail as a JSON array of
// edges, newest-first (E-1682). It backs `endless session trail`: the Python
// viewer resolves the current tmux client_name and passes it as --client
// (scoping to this navigator), or omits it for --all (every client). The DB
// read stays Go-side (no Python SQLite read, per E-1486). Endpoint task labels
// and relative time are rendered Python-side from the returned fields.
func runTrail(args []string) error {
	fs := flag.NewFlagSet("trail", flag.ContinueOnError)
	client := fs.String("client", "", "tmux client_name to scope to (empty = all clients)")
	limit := fs.Int("limit", 50, "max rows to return")
	if err := fs.Parse(args); err != nil {
		return err
	}
	edges, err := monitor.ListNavTrail(*client, *limit)
	if err != nil {
		return fmt.Errorf("list nav trail: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(edges)
}

// runReopenContext prints the read-only restore context for `task spawn
// --reopen` as JSON: the most-applicable prior ended session to inherit (0 if
// none), the task's outcome, and the inherited session's latest status snapshot
// rendered as markdown. The Python reopen path feeds these into the respawn
// handoff without a Python DB read (E-894 / E-1486). The ghost-skip pick lives
// in events.ResolveReopenContext.
func runReopenContext(args []string) error {
	fs := flag.NewFlagSet("reopen-context", flag.ContinueOnError)
	taskID := fs.Int64("task-id", 0, "task id being reopened")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *taskID == 0 {
		return fmt.Errorf("--task-id is required")
	}
	ctx, err := events.ResolveReopenContext(*taskID)
	if err != nil {
		return fmt.Errorf("reopen-context for E-%d: %w", *taskID, err)
	}
	return json.NewEncoder(os.Stdout).Encode(ctx)
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

// runTaskField prints the raw value of one whitelisted multiline document
// column (text/outcome/analysis) for a task. Backs E-1747's birth-time mirror
// seeding: the Python worktree-create path reads each field this way instead
// of doing a forbidden Python DB read (E-894/E-1486).
func runTaskField(args []string) error {
	fs := flag.NewFlagSet("task-field", flag.ContinueOnError)
	id := fs.Int64("id", 0, "task id")
	name := fs.String("name", "", "column name: text|outcome|analysis")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	value, err := monitor.TaskField(*id, *name)
	if err != nil {
		return fmt.Errorf("read task field %q for E-%d: %w", *name, *id, err)
	}
	_, err = os.Stdout.WriteString(value)
	return err
}

// runWorktreeAnomalies prints one terse line per genuine handoff anomaly in the
// worktree and returns the process exit code: 0 clean, 1 anomalies present, 2 on
// error. It backs Python's `endless worktree check`, which relays stdout
// verbatim and propagates this exit code so the agent can script on it. Empty
// output IS the representation of "clean" (E-1758).
//
// Inputs are the worktree path and (optionally) the repo main checkout, both
// already resolved from cwd by the Python caller — so this command touches NO DB
// (E-1766). The former --task-id form resolved the project/worktree via the DB,
// which in a self-dev worktree routed to the per-worktree sandbox (lacking the
// task row) and errored. --project-root enables the repo-level prunable probe;
// omitting it disables only that probe.
func runWorktreeAnomalies(args []string) int {
	fs := flag.NewFlagSet("worktree-anomalies", flag.ContinueOnError)
	worktreePath := fs.String("worktree-path", "", "absolute path of the worktree to inspect")
	projectRoot := fs.String("project-root", "", "absolute path of the repo main checkout (enables the prunable probe)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *worktreePath == "" {
		fmt.Fprintln(os.Stderr, "--worktree-path is required")
		return 2
	}
	anomalies := monitor.WorktreeAnomaliesAt(*projectRoot, *worktreePath)
	for _, a := range anomalies {
		fmt.Println(a.Line())
	}
	if len(anomalies) > 0 {
		return 1
	}
	return 0
}

// runWorktreeUnsettled emits the unsettled breakdown for each worktree path
// given as a positional argument, as a JSON array in the same order (E-1865).
//
// Path-based and DB-free for the same reason worktree-anomalies is (E-1766): in
// a self-dev worktree a DB lookup routes to the per-worktree sandbox, which
// lacks the task row. The Python caller already resolves task → worktree path,
// and passes every path in ONE invocation so the list view costs a single
// subprocess rather than one per worktree.
//
// Always exits 0 when it ran: "settled" is a legitimate answer, not a failure,
// and the caller reads the verdict from the JSON rather than the exit code.
func runWorktreeUnsettled(args []string) error {
	fs := flag.NewFlagSet("worktree-unsettled", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return fmt.Errorf("at least one worktree path argument is required")
	}
	out := make([]worktreeUnsettledJSON, 0, len(paths))
	for _, p := range paths {
		out = append(out, newWorktreeUnsettledJSON(monitor.WorktreeUnsettledDetailAt(p)))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// worktreeUnsettledJSON is the wire shape of one worktree's breakdown. Declared
// explicitly (rather than tagging monitor.UnsettledDetail) to keep the Go/Python
// contract in one readable place, and because the derived verdict fields —
// unsettled/modified/unlanded/reason — are computed by the Go methods so the
// Python renderer never re-derives the predicate.
type worktreeUnsettledJSON struct {
	WorktreePath  string   `json:"worktree_path"`
	HasWorktree   bool     `json:"has_worktree"`
	Unsettled     bool     `json:"unsettled"`
	Modified      bool     `json:"modified"`
	Unlanded      bool     `json:"unlanded"`
	Reason        string   `json:"reason"`
	Branch        string   `json:"branch"`
	ModifiedFiles []string `json:"modified_files"`
	AutoManaged   []string `json:"auto_managed_files"`
	UnlandedCount int      `json:"unlanded_count"`
	UnlandedLog   []string `json:"unlanded_log"`
	StatusErr     string   `json:"status_error,omitempty"`
	RevListErr    string   `json:"rev_list_error,omitempty"`
}

func newWorktreeUnsettledJSON(d monitor.UnsettledDetail) worktreeUnsettledJSON {
	// Nil slices marshal as null; the Python side wants lists it can iterate
	// unconditionally, so normalize to empty.
	mod, auto, log := d.Modified, d.AutoManaged, d.UnlandedLog
	if mod == nil {
		mod = []string{}
	}
	if auto == nil {
		auto = []string{}
	}
	if log == nil {
		log = []string{}
	}
	return worktreeUnsettledJSON{
		WorktreePath:  d.WorktreePath,
		HasWorktree:   d.HasWorktree,
		Unsettled:     d.Unsettled(),
		Modified:      d.IsModified(),
		Unlanded:      d.IsUnlanded(),
		Reason:        d.Reason(),
		Branch:        d.Branch,
		ModifiedFiles: mod,
		AutoManaged:   auto,
		UnlandedCount: d.UnlandedCount,
		UnlandedLog:   log,
		StatusErr:     d.StatusErr,
		RevListErr:    d.RevListErr,
	}
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

// runReapDeadPanes ends any non-ended sessions for the project whose owning
// tmux pane no longer exists (E-1807), then exits 0 with no output. It exposes
// monitor.ReapDeadTmuxPanes to the Python spawn/claim ownership guard, which
// calls it before reading ownership so a ghost owner (a session that died
// without firing SessionEnd, leaving a non-ended row pointing at a now-dead
// pane) self-heals to free instead of reading as a live collision.
//
// Deliberately carries NO $TMUX guard (unlike `endless-go tmux reset`): the
// reaper already no-ops when tmux is unavailable (list-panes fails → returns
// nil), which is the correct behavior for an internal opportunistic call. An
// unregistered project root is a silent no-op, mirroring list-live. Nonzero
// exit only on a real DB error.
func runReapDeadPanes(args []string) error {
	fs := flag.NewFlagSet("reap-dead-panes", flag.ContinueOnError)
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
		// Unregistered cwd: nothing to reap. Silent no-op so the Python
		// caller can invoke this unconditionally before the ownership read.
		return nil
	}

	return monitor.ReapDeadTmuxPanes(projectID)
}

// runEnsureClaudeID prints the integer sessions.id for an env-identified
// Claude session, lazy-creating the row when no hook event has fired yet
// (E-1455). The Python resolver invokes this when CLAUDECODE=1 and
// CLAUDE_CODE_SESSION_ID are set, treating the env vars as authoritative
// identification of the current pane.
//
// --session-id is the Claude harness session UUID (required).
// --project-root is the cwd-resolved project path (required); the
//
//	helper passes it through monitor.ProjectIDForPath which auto-registers
//	unknown paths.
//
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
