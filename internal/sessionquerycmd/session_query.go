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
	"time"

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
	// `reap-dead-panes` was removed by E-1898. It existed so the Python
	// spawn/claim guard could WRITE a ghost owner to 'ended' before reading
	// ownership. `list-live` now excludes observably-dead sessions at read
	// time, so the ghost is absent without anything having been written.
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
	case "gate-clear":
		if err := runGateClear(args[1:]); err != nil {
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
	case "report-draft":
		if err := runReportDraft(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "report-prompt":
		if err := runReportPrompt(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "report-runs":
		if err := runReportRuns(args[1:]); err != nil {
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
	case "triage-claim":
		if err := runTriageClaim(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "triage-release":
		if err := runTriageRelease(args[1:]); err != nil {
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
	fmt.Fprintln(os.Stderr, "                                    end non-ended sessions whose tmux pane is gone (silent; DB error → exit 1)")
	fmt.Fprintln(os.Stderr, "  task-text --id <task-id>          raw tasks.text for the task (empty if none)")
	fmt.Fprintln(os.Stderr, "  task-field --id <task-id> --name <text|outcome|analysis>")
	fmt.Fprintln(os.Stderr, "                                    raw value of one multiline doc column (empty if none)")
	fmt.Fprintln(os.Stderr, "  ensure-claude-id --session-id <uuid> --project-root <path> [--process <pane>]")
	fmt.Fprintln(os.Stderr, "                                    look up (or lazy-create) sessions.id; prints integer id")
	fmt.Fprintln(os.Stderr, "  gate-clear --session-id <id> --kind <slug> --cleared-by <reason>")
	fmt.Fprintln(os.Stderr, "                                    clear the session's open gate of the kind; prints rows cleared")
	fmt.Fprintln(os.Stderr, "  worktree-anomalies --worktree-path <path> [--project-root <path>]")
	fmt.Fprintln(os.Stderr, "                                    terse line per genuine handoff anomaly; nothing when clean")
	fmt.Fprintln(os.Stderr, "                                    exit 0 clean, 1 anomalies present, 2 on error")
	fmt.Fprintln(os.Stderr, "  worktree-unsettled <worktree-path>...")
	fmt.Fprintln(os.Stderr, "                                    JSON array of per-worktree unsettled breakdowns (E-1865):")
	fmt.Fprintln(os.Stderr, "                                    {unsettled, modified, unlanded, reason, modified_files, unlanded_log, …}")
	fmt.Fprintln(os.Stderr, "  trail [--client <name>] [--limit N]")
	fmt.Fprintln(os.Stderr, "                                    JSON array of navigation edges newest-first (no --client = all clients)")
	fmt.Fprintln(os.Stderr, "  resume-target --ref <ES-session-id|task-id|session-id|uuid>")
	fmt.Fprintln(os.Stderr, "                                    JSON {endless_id, session_id, task_id, worktree_path, state,")
	fmt.Fprintln(os.Stderr, "                                    project_id, project_path, task_type, task_status, task_title, landed_sha}")
	fmt.Fprintln(os.Stderr, "                                    to relaunch (or recover) a lost session; ES-<n> is session-explicit")
	fmt.Fprintln(os.Stderr, "  task-report --id <task-id>        JSON {task_id, status, type, landed, successors[]} of a task's computed report facts (E-1771)")
	fmt.Fprintln(os.Stderr, "  untriaged-tasks [--project <name>] [--limit N]")
	fmt.Fprintln(os.Stderr, "                                    JSON array [{id, project, title}] of the triage queue, oldest first (E-1859)")
	fmt.Fprintln(os.Stderr, "  triage-context --id <task-id>     JSON {task_id, project, title, description, type, phase, status, has_text,")
	fmt.Fprintln(os.Stderr, "                                    parent, siblings[], decisions[]} — the persisted artifacts triage may judge (E-1859)")
	fmt.Fprintln(os.Stderr, "  triage-claim --id <task-id> --ttl-seconds N [--owner <id>]")
	fmt.Fprintln(os.Stderr, "                                    take the per-task triage claim; prints 1 if won, 0 if another holds it (E-1859)")
	fmt.Fprintln(os.Stderr, "  triage-release --id <task-id> [--owner <id>]")
	fmt.Fprintln(os.Stderr, "                                    drop this owner's triage claim (E-1859)")
	fmt.Fprintln(os.Stderr, "  relay-checkpoint --session-id <id> [--draft-file <path>] [--task-id <id>]")
	fmt.Fprintln(os.Stderr, "                                    record the minimized report text (read from STDIN) the session")
	fmt.Fprintln(os.Stderr, "                                    owes as its final message; the Stop gate enforces it (E-1901/E-1953).")
	fmt.Fprintln(os.Stderr, "                                    --draft-file completes the eval-corpus triple; the prompting user")
	fmt.Fprintln(os.Stderr, "                                    message is read from the session row, not passed in")
	fmt.Fprintln(os.Stderr, "  report-draft --session-id <id>    print the raw draft of the session's most recent report (E-1953);")
	fmt.Fprintln(os.Stderr, "                                    exit 1 when none exists")
	fmt.Fprintln(os.Stderr, "  report-prompt --session-id <id>   print the message that prompted the turn in flight (E-1953)")
	fmt.Fprintln(os.Stderr, "  report-runs --session-id <id>     print how many times `task report` produced output this turn (E-1953)")
}

// runRelayCheckpoint records the verbatim-relay checkpoint written by `endless
// task report` (E-1901, extended by E-1953): the exact text the session now owes
// the user as its final message. The Stop hook reads it back and blocks the turn
// if the agent sent anything else.
//
// The sanctioned text arrives on STDIN, not as a flag. It is unbounded and full
// of newlines and quotes, so passing it through argv would invite both quoting
// bugs and ARG_MAX truncation — and a truncated sanctioned text would silently
// gate against the wrong string, bouncing a compliant agent forever. The raw
// draft is larger still, so it arrives as a FILE path rather than competing for
// the one stdin.
//
// The prompting user message is deliberately NOT a parameter. It is read here
// from the session row where UserPromptSubmit staged it, because the caller is a
// subprocess of the agent and has no view of the conversation — asking it for
// the prompt would mean asking the agent to retype what the user said, which is
// a corpus of paraphrases rather than of prompts.
// checkpointJSON is the wire shape `--json` accepts on stdin (E-1975).
//
// The plain-text form stayed: it is one string on stdin and it is what every
// caller that only has one string should keep using. JSON exists because a
// paired turn carries a list of variants plus the fetched-context record, and
// squeezing that through flags would mean shell-escaping a JSON document into an
// argv — the exact escaping failure `task report --draft-file` was shaped to
// avoid.
type checkpointJSON struct {
	Emitted  string `json:"emitted"`
	TaskType string `json:"task_type"`
	Context  string `json:"context"`
	Variants []struct {
		Sanctioned  string `json:"sanctioned"`
		Slot        string `json:"slot"`
		VariantHash string `json:"variant_hash"`
		Bypassed    bool   `json:"bypassed"`
	} `json:"variants"`
}

func runRelayCheckpoint(args []string) error {
	fs := flag.NewFlagSet("relay-checkpoint", flag.ContinueOnError)
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK) recording the checkpoint")
	draftFile := fs.String("draft-file", "", "path to the raw draft this output was minimized from")
	taskID := fs.Int64("task-id", 0, "task the report is attributed to (0 = none)")
	asJSON := fs.Bool("json", false, "stdin is a checkpoint JSON document, not the sanctioned text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return fmt.Errorf("--session-id is required")
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading checkpoint from stdin: %w", err)
	}

	var cp monitor.ReportCheckpoint
	if *asJSON {
		var doc checkpointJSON
		if err = json.Unmarshal(stdin, &doc); err != nil {
			return fmt.Errorf("parsing checkpoint json: %w", err)
		}
		cp.Emitted, cp.TaskType, cp.Context = doc.Emitted, doc.TaskType, doc.Context
		for _, v := range doc.Variants {
			cp.Variants = append(cp.Variants, monitor.ReportVariant{
				Sanctioned:  v.Sanctioned,
				Slot:        v.Slot,
				VariantHash: v.VariantHash,
				Bypassed:    v.Bypassed,
			})
		}
		if len(cp.Variants) == 0 {
			return fmt.Errorf("checkpoint json carries no variants")
		}
	} else {
		cp.Variants = []monitor.ReportVariant{{Sanctioned: string(stdin)}}
	}

	if *draftFile != "" {
		draft, rerr := os.ReadFile(*draftFile)
		if rerr != nil {
			return fmt.Errorf("reading draft file %s: %w", *draftFile, rerr)
		}
		cp.RawDraft = string(draft)
	}
	if *taskID != 0 {
		cp.TaskID = taskID
	}
	// Best-effort: a missing prompt costs one leg of one corpus sample, whereas
	// refusing the checkpoint would leave the turn ungated over a nice-to-have.
	if prompt, found, perr := monitor.LastUserPrompt(*sessionID); perr == nil && found {
		cp.UserPrompt = prompt
	}
	return monitor.SetReportCheckpoint(*sessionID, cp)
}

// runReportDraft prints the raw draft of the session's most recent report
// (E-1953) — what `endless task report --raw` shows.
//
// This is the escape hatch that lets the minimizer be aggressive. With the draft
// retrievable, an over-cut is an inconvenience rather than lost work, so the
// prompt can be tuned toward cutting hard instead of hedging toward keeping.
//
// Exit 1 with nothing on stdout when no draft exists, so a caller can tell
// "never reported" from "reported an empty draft".
func runReportDraft(args []string) error {
	fs := flag.NewFlagSet("report-draft", flag.ContinueOnError)
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK) whose draft to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return fmt.Errorf("--session-id is required")
	}
	draft, found, err := monitor.LatestReportDraft(*sessionID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no persisted draft for session %d", *sessionID)
	}
	_, err = os.Stdout.WriteString(draft)
	return err
}

// runReportPrompt prints the message that prompted the turn in flight, staged on
// the session row by the UserPromptSubmit hook (E-1953).
//
// The minimizer needs it to judge what the user actually asked for — "delete
// what the user did not ask for" is unanswerable without knowing what they
// asked. It is read from the DB rather than passed by the caller because the
// caller is the agent, and an agent that supplies its own description of the
// request can describe the user as having asked for exactly what it wrote.
//
// Prints nothing and exits 0 when no prompt is staged: the minimizer degrades to
// judging the draft alone, which is worse but not wrong.
func runReportPrompt(args []string) error {
	fs := flag.NewFlagSet("report-prompt", flag.ContinueOnError)
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return fmt.Errorf("--session-id is required")
	}
	prompt, found, err := monitor.LastUserPrompt(*sessionID)
	if err != nil || !found {
		return err
	}
	_, err = os.Stdout.WriteString(prompt)
	return err
}

// runReportRuns prints how many times `task report` has produced output this
// turn (E-1953). The command reads it back to bound the appeal at one before
// spending a model call on a run it would refuse.
func runReportRuns(args []string) error {
	fs := flag.NewFlagSet("report-runs", flag.ContinueOnError)
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return fmt.Errorf("--session-id is required")
	}
	runs, err := monitor.ReportRunsThisTurn(*sessionID)
	if err != nil {
		return err
	}
	fmt.Println(runs)
	return nil
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
// but no cwd, so database-wide is the only scope it can express. A human running
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

// runTriageClaim takes the per-task triage claim (E-1859) and prints "1" when
// this process won it, "0" when another holds a live claim. Zero is an ordinary
// outcome, not an error, so the exit status stays 0 either way — the caller
// branches on the printed value.
//
// The claim exists so the inline file-time path and the background sweep cannot
// both pay for the same task's model call; see internal/monitor/triage_claims.go.
func runTriageClaim(args []string) error {
	fs := flag.NewFlagSet("triage-claim", flag.ContinueOnError)
	id := fs.Int64("id", 0, "task id")
	owner := fs.String("owner", "", "claimant identity (default: this process)")
	ttl := fs.Int("ttl-seconds", 0, "claim lifetime; must exceed the worst-case model call")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	if *ttl <= 0 {
		return fmt.Errorf("--ttl-seconds must be positive")
	}
	who := *owner
	if who == "" {
		who = monitor.TriageClaimOwner()
	}
	claimed, err := monitor.ClaimTriage(*id, who, time.Duration(*ttl)*time.Second)
	if err != nil {
		return fmt.Errorf("claim triage for E-%d: %w", *id, err)
	}
	if claimed {
		fmt.Println("1")
		return nil
	}
	fmt.Println("0")
	return nil
}

// runTriageRelease drops this owner's triage claim (E-1859). Releasing a claim
// that already lapsed and was taken by someone else is a no-op, not a steal.
func runTriageRelease(args []string) error {
	fs := flag.NewFlagSet("triage-release", flag.ContinueOnError)
	id := fs.Int64("id", 0, "task id")
	owner := fs.String("owner", "", "claimant identity (default: this process)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	who := *owner
	if who == "" {
		who = monitor.TriageClaimOwner()
	}
	return monitor.ReleaseTriage(*id, who)
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
	limit := fs.Int("limit", 50, "max rows to return (negative returns every row)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	edges, err := monitor.ListNavTrail(*client, *limit)
	if err != nil {
		return fmt.Errorf("list nav trail: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(edges)
}

// runGateClear closes the session's open gate of the given kind, recording the
// cleared_by reason, and prints how many open rows were cleared (0 = nothing was
// pending). It backs the `endless task continue` verb so the Python side clears
// a gate without a Python DB write (E-1486 / E-1542).
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
// Path-based for the same reason worktree-anomalies is (E-1766): in a self-dev
// worktree a DB lookup routes to the per-worktree sandbox, which lacks the task
// row. The Python caller already resolves task → worktree path, and passes
// every path in ONE invocation so the list view costs a single subprocess
// rather than one per worktree.
//
// E-1940 added a best-effort landings lookup underneath, keyed off the `e-NNNN`
// directory name. It does not make the command DB-dependent: a miss credits no
// landing and the verdict falls back to pure git. To make the lookup HIT, pass
// the same `--config-dir` the caller's own database context resolved to — which
// is what endless.task_cmd._unsettled_probe threads through.
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
	// Undetermined and its reason are the E-1940 addition: a probe that could
	// not run is now its own answer, distinct from both settled and unsettled,
	// and Unsettled is true alongside it so the row still gets marked.
	Undetermined       bool   `json:"undetermined"`
	UndeterminedReason string `json:"undetermined_reason"`
	// Base is the resolved default branch the count was measured against, so
	// the renderer can name it instead of saying "main" on a repo where that is
	// not true. LandedShas are the recorded landings credited.
	Base       string   `json:"base"`
	LandedShas []string `json:"landed_shas"`
	StatusErr  string   `json:"status_error,omitempty"`
	RevListErr string   `json:"rev_list_error,omitempty"`
	BaseErr    string   `json:"base_error,omitempty"`
	LookupErr  string   `json:"lookup_error,omitempty"`
}

func newWorktreeUnsettledJSON(d monitor.UnsettledDetail) worktreeUnsettledJSON {
	// Nil slices marshal as null; the Python side wants lists it can iterate
	// unconditionally, so normalize to empty.
	mod, auto, log, landed := d.Modified, d.AutoManaged, d.UnlandedLog, d.LandedShas
	if mod == nil {
		mod = []string{}
	}
	if auto == nil {
		auto = []string{}
	}
	if log == nil {
		log = []string{}
	}
	if landed == nil {
		landed = []string{}
	}
	return worktreeUnsettledJSON{
		WorktreePath:       d.WorktreePath,
		HasWorktree:        d.HasWorktree,
		Unsettled:          d.Unsettled(),
		Modified:           d.IsModified(),
		Unlanded:           d.IsUnlanded(),
		Reason:             d.Reason(),
		Branch:             d.Branch,
		ModifiedFiles:      mod,
		AutoManaged:        auto,
		UnlandedCount:      d.UnlandedCount,
		UnlandedLog:        log,
		Undetermined:       d.IsUndetermined(),
		UndeterminedReason: d.UndeterminedReason(),
		Base:               d.Base,
		LandedShas:         landed,
		StatusErr:          d.StatusErr,
		RevListErr:         d.RevListErr,
		BaseErr:            d.BaseErr,
		LookupErr:          d.LookupErr,
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

