// Package sessionquerycmd implements the `endless-go session-query`
// subcommand: an internal helper that exposes monitor.* read operations
// as JSON for the Python CLI. It exists so the Python side can avoid
// extending the legacy `db.query` pattern (E-894). Subcommands are
// intentionally narrow — one verb per Python need.
package sessionquerycmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/docmirror"
	"github.com/mikeschinkel/endless/internal/gatekind"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/taskcontent"
	_ "modernc.org/sqlite"
)

func Run(args []string) {
	if len(args) < 1 {
		// Today this prints the usage block with nothing above it, so the human
		// rendering is pinned verbatim and the summary exists only to give the
		// agent's verdict a line to carry. Every Python caller builds the argv
		// itself and always names a subcommand, so only a direct endless-go run
		// reaches this — which makes it the caller's to correct.
		refusal.NoReport(
			"endless-go session-query: no subcommand given",
			"Re-run with a subcommand from the printed list",
		).Command("session-query").Text(usageText()).Exit(2)
	}
	switch args[0] {
	case "list-live":
		if err := runListLive(args[1:]); err != nil {
			fail("list-live", err)
		}
	// `reap-dead-panes` was removed by E-1898. It existed so the Python
	// spawn/claim guard could WRITE a ghost owner to 'ended' before reading
	// ownership. `list-live` now excludes observably-dead sessions at read
	// time, so the ghost is absent without anything having been written.
	case "task-plan":
		if err := runTaskPlan(args[1:]); err != nil {
			fail("task-plan", err)
		}
	case "task-field":
		if err := runTaskField(args[1:]); err != nil {
			fail("task-field", err)
		}
	case "doc-content":
		if err := runDocContent(args[1:]); err != nil {
			fail("doc-content", err)
		}
	case "ensure-claude-id":
		if err := runEnsureClaudeID(args[1:]); err != nil {
			fail("ensure-claude-id", err)
		}
	case "gate-clear":
		if err := runGateClear(args[1:]); err != nil {
			fail("gate-clear", err)
		}
	case "worktree-anomalies":
		os.Exit(runWorktreeAnomalies(args[1:]))
	case "worktree-unsettled":
		if err := runWorktreeUnsettled(args[1:]); err != nil {
			fail("worktree-unsettled", err)
		}
	case "task-landedness":
		if err := runTaskLandedness(args[1:]); err != nil {
			fail("task-landedness", err)
		}
	case "relay-checkpoint":
		if err := runRelayCheckpoint(args[1:]); err != nil {
			fail("relay-checkpoint", err)
		}
	case "report-draft":
		if err := runReportDraft(args[1:]); err != nil {
			fail("report-draft", err)
		}
	case "report-prompt":
		if err := runReportPrompt(args[1:]); err != nil {
			fail("report-prompt", err)
		}
	case "report-runs":
		if err := runReportRuns(args[1:]); err != nil {
			fail("report-runs", err)
		}
	case "task-report":
		if err := runTaskReport(args[1:]); err != nil {
			fail("task-report", err)
		}
	case "task-questions":
		if err := runTaskQuestions(args[1:]); err != nil {
			fail("task-questions", err)
		}
	case "question-target":
		if err := runQuestionTarget(args[1:]); err != nil {
			fail("question-target", err)
		}
	case "unrated-tasks":
		if err := runUnratedTasks(args[1:]); err != nil {
			fail("unrated-tasks", err)
		}
	case "rater-context":
		if err := runRaterContext(args[1:]); err != nil {
			fail("rater-context", err)
		}
	case "rater-claim":
		if err := runRaterClaim(args[1:]); err != nil {
			fail("rater-claim", err)
		}
	case "rater-release":
		if err := runRaterRelease(args[1:]); err != nil {
			fail("rater-release", err)
		}
	case "resume-target":
		if err := runResumeTarget(args[1:]); err != nil {
			fail("resume-target", err)
		}
	case "-h", "--help", "help":
		// Help stays on STDERR, where this command has always put it. Every verb
		// below writes its answer — a JSON document, a raw column value, an
		// integer — to stdout for a Python caller to parse, so the usage block
		// is diagnostic text here rather than the result of a question, and
		// moving it to stdout would put prose in a stream somebody unmarshals.
		refusal.Info(usageText()).Print()
	default:
		// Two readings, and this binary cannot tell them apart. Typed directly,
		// it is a subcommand the caller misspelled and can simply retype.
		// Relayed from an `endless` command, it means the installed endless-go
		// does not know a verb the Python CLI calling it expects — a mismatched
		// pair only the user can reinstall. `task_cmd._landedness_binaries`
		// anticipates exactly that skew and falls through to the next binary,
		// which is why the condition is worth naming rather than guessing.
		refusal.ReportIf(
			fmt.Sprintf("unknown subcommand: %s", args[0]),
			"this came from an `endless` command rather than from a subcommand you typed",
			"retry with a subcommand from the printed list",
			"the installed endless-go is older or newer than the endless CLI calling it, and only the user can reinstall a matching pair",
		).Command("session-query").Detail(usageText()).Exit(2)
	}
}

func usageText() string {
	return strings.Join([]string{
		"usage: endless-go session-query <subcommand>",
		"subcommands:",
		"  list-live --project-root <path>   JSON array of live sessions for the project",
		"                                    end non-ended sessions whose tmux pane is gone (silent; DB error → exit 1)",
		"  task-plan --id <task-id>          raw plan for the task (empty if none)",
		"  task-field --id <task-id> --name <" + strings.Join(taskcontent.Slugs(), "|") + ">",
		"                                    raw value of one multiline doc column (empty if none)",
		"  doc-content --path <rel-path>     authoritative content behind a document mirror path",
		"                                    (.endless/tasks/e-N/{plan,outcome,analysis}.md, a legacy",
		"                                    .endless/{plans,outcomes,analyses}/E-N.md, or",
		"                                    .endless/decisions/ED-N.md); unrecognized path → exit 1",
		"  ensure-claude-id --session-id <uuid> --project-root <path> [--process <pane>]",
		"                                    look up (or lazy-create) sessions.id; prints integer id",
		"  gate-clear --session-id <id> --kind <slug> --cleared-by <reason>",
		"                                    clear the session's open gate of the kind; prints rows cleared",
		"  worktree-anomalies --worktree-path <path> [--project-root <path>]",
		"                                    terse line per genuine handoff anomaly; nothing when clean",
		"                                    exit 0 clean, 1 anomalies present, 2 on error",
		"  worktree-unsettled <worktree-path>...",
		"                                    JSON array of per-worktree unsettled breakdowns (E-1865):",
		"                                    {unsettled, modified, unlanded, reason, modified_files, unlanded_log, …}",
		"  task-landedness --project-root <path> <branch>...",
		"                                    JSON array of per-branch landedness verdicts (E-2095):",
		"                                    {branch, branch_exists, base, unlanded_count, unlanded_log, …}",
		"  resume-target --ref <ES-session-id|task-id|session-id|uuid>",
		"                                    JSON {endless_id, session_id, task_id, worktree_path, state,",
		"                                    project_id, project_path, task_type, task_status, task_title, landed_sha}",
		"                                    to relaunch (or recover) a lost session; ES-<n> is session-explicit",
		"  task-report --id <task-id>        JSON {task_id, status, type, landed, successors[]} of a task's computed report facts (E-1771)",
		"  task-questions [--id <task-id>] [--project <name>] [--all]   JSON array of open (or --all) task questions",
		"  question-target (--task <id> | --question <id>)   JSON {task_id, project[, status]} a question command acts on",
		"  unrated-tasks [--project <name>] [--limit N]",
		"                                    JSON array [{id, project, title}] of submitted tasks missing a rating, oldest first (E-2203)",
		"  rater-context --id <task-id>      JSON {task_id, project, project_root, title, description, context, plan, type, phase,",
		"                                    status, complexity, risk, parent, siblings[], decisions[]} — what the rater may judge (E-2203)",
		"  rater-claim --id <task-id> --ttl-seconds N [--owner <id>]",
		"                                    take the per-task rater claim; prints 1 if won, 0 if another holds it (E-2203)",
		"  rater-release --id <task-id> [--owner <id>]",
		"                                    drop this owner's rater claim (E-2203)",
		"  relay-checkpoint --session-id <id> [--draft-file <path>] [--task-id <id>]",
		"                                    record the minimized report text (read from STDIN) the session",
		"                                    owes as its final message; the Stop gate enforces it (E-1901/E-1953).",
		"                                    --draft-file completes the eval-corpus triple; the prompting user",
		"                                    message is read from the session row, not passed in",
		"  report-draft --session-id <id>    print the raw draft of the session's most recent report (E-1953);",
		"                                    exit 1 when none exists",
		"  report-prompt --session-id <id>   print the message that prompted the turn in flight (E-1953)",
		"  report-runs --session-id <id>     print how many times `task report` produced output this turn (E-1953)",
	}, "\n") + "\n"
}

// parseFlags parses one verb's argv and names a class for what the flag package
// used to decide on its own.
//
// Every set here was already flag.ContinueOnError, so the error did come back
// to the verb — but flag had ALREADY written its own error line and usage block
// to os.Stderr by then, unclassified, invisible to a reviewer reading the call
// site and to TestNoStderrOutsideThisPackage, and Run then printed the same
// error a second time underneath it. refusal.NewFlags captures that text so it
// is printed once, from one place, with a class on it.
//
// -h gets back the exit 0 it deserves. Under plain ContinueOnError it fell
// through to the error branch, so asking this command for help produced the
// help text, the line "flag: help requested", and a failing status.
func parseFlags(fs *refusal.Flags, verb string, args []string) error {
	err := fs.Parse(args)
	if err == nil {
		return nil
	}
	fs.ExitOnHelp(err)
	// fs.Output() is flag's own error line plus its usage block — exactly the
	// bytes a person read before — so it is the human rendering, and the
	// summary carries the one-line verdict.
	return refusal.NoReport(err.Error(), "Correct the flag and retry").
		Command("session-query " + verb).
		Text(fs.Output())
}

// relayed prepares an error raised by one of this command's verbs for printing.
//
// Run has always printed that error bare — no "endless-go session-query <verb>:"
// in front of it, unlike every other Go command here — so the text passes
// through untouched and only the agent's verdict names the verb.
//
// The class travels with the error rather than being re-decided here: each verb
// classifies its own argument checks where it builds them, so a flag the caller
// can simply correct stays NO-REPORT all the way to this print site.
// refusal.From faults everything else, which is the right reading of what is
// left — monitor's DB reads, a stdin that would not read, a JSON encode that
// failed. Those mean Endless is broken, not that the command was misused.
//
// Two groups inside that remainder are owed a class by the package that builds
// them. resume-target's "no session matches <ref>" (internal/monitor/resume.go)
// and question-target's "no task E-<n>" (internal/monitor/task_questions.go)
// are not-found answers rather than breakage, and they arrive here
// unclassified; when internal/monitor converts, the class it chooses travels
// through From and this funnel needs no change.
func relayed(verb string, err error) *refusal.Error {
	return refusal.From(err).Command("session-query " + verb)
}

// fail is the single exit path for a verb that returned an error: print it once,
// with the exit 1 Run has always used.
func fail(verb string, err error) {
	relayed(verb, err).Exit(1)
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
	fs := refusal.NewFlags("relay-checkpoint")
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK) recording the checkpoint")
	draftFile := fs.String("draft-file", "", "path to the raw draft this output was minimized from")
	taskID := fs.Int64("task-id", 0, "task the report is attributed to (0 = none)")
	asJSON := fs.Bool("json", false, "stdin is a checkpoint JSON document, not the sanctioned text")
	if err := parseFlags(fs, "relay-checkpoint", args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return refusal.NoReport("--session-id is required", "Pass --session-id and retry")
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
			// Nothing normally reads this: the only caller, report_cmd._record,
			// discards the result entirely. If anything did read it, it would
			// mean Endless's own Python half had built a checkpoint document
			// with nothing in it to gate against — a broken caller, not an
			// argument a different invocation could get right — so it is a
			// fault rather than the usage error its shape suggests.
			return refusal.Faultf("checkpoint json carries no variants")
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
	fs := refusal.NewFlags("report-draft")
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK) whose draft to print")
	if err := parseFlags(fs, "report-draft", args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return refusal.NoReport("--session-id is required", "Pass --session-id and retry")
	}
	draft, found, err := monitor.LatestReportDraft(*sessionID)
	if err != nil {
		return err
	}
	if !found {
		// Nothing normally reads this either: report_cmd replaces the Go text
		// with its own ClickException naming the command to run first. Read
		// directly it is still not breakage — the session simply has not
		// reported yet — and recording a draft is something the caller does for
		// itself, so NO-REPORT with that same instruction as the remedy.
		return refusal.NoReport(
			fmt.Sprintf("no persisted draft for session %d", *sessionID),
			"Run `endless task report --draft-file <path>` for the session first, then retry",
		)
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
	fs := refusal.NewFlags("report-prompt")
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK)")
	if err := parseFlags(fs, "report-prompt", args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return refusal.NoReport("--session-id is required", "Pass --session-id and retry")
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
	fs := refusal.NewFlags("report-runs")
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK)")
	if err := parseFlags(fs, "report-runs", args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return refusal.NoReport("--session-id is required", "Pass --session-id and retry")
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
	fs := refusal.NewFlags("task-report")
	id := fs.Int64("id", 0, "task id")
	if err := parseFlags(fs, "task-report", args); err != nil {
		return err
	}
	if *id == 0 {
		return refusal.NoReport("--id is required", "Pass --id and retry")
	}
	facts, err := monitor.BuildTaskReportFacts(*id)
	if err != nil {
		return fmt.Errorf("build report facts for E-%d: %w", *id, err)
	}
	return dbprovenance.Encode(os.Stdout, facts)
}

// runTaskQuestions prints task_questions rows (E-2176) as JSON — open ones only
// unless --all. With neither --id nor --project it is every open question in
// the database, which is the feed an attention surface reads.
func runTaskQuestions(args []string) error {
	fs := refusal.NewFlags("task-questions")
	id := fs.Int64("id", 0, "task id (default: every task)")
	project := fs.String("project", "", "registered project name (default: every project)")
	all := fs.Bool("all", false, "include answered, withdrawn, invalid and superseded questions")
	if err := parseFlags(fs, "task-questions", args); err != nil {
		return err
	}
	// Every flag is optional — no argument check to classify. The only error
	// left is monitor's, and a query that will not run means the database is
	// unreachable, which relayed() reads as a fault.
	qs, err := monitor.TaskQuestions(monitor.TaskQuestionFilter{
		TaskID: *id, Project: *project, All: *all,
	})
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, qs)
}

// runQuestionTarget prints {task_id, project[, status]} for the task a question
// command acts on (E-2176), named by --task or by --question.
func runQuestionTarget(args []string) error {
	fs := refusal.NewFlags("question-target")
	task := fs.Int64("task", 0, "task id")
	question := fs.Int64("question", 0, "question id")
	if err := parseFlags(fs, "question-target", args); err != nil {
		return err
	}
	// "name exactly one of a task or a question", "no task E-<n>" and "no
	// question EQ-<n>" are all built in internal/monitor, which has no classes
	// yet; see relayed(). They are not reclassified here, because guessing from
	// the message text is how a class gets attached to a sentence somebody
	// later rewords.
	tgt, err := monitor.ResolveQuestionTarget(*task, *question)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, tgt)
}

// defaultUnratedLimit caps a rater sweep that names no limit, so a caller that
// forgets --limit cannot walk an unbounded backlog: every task selected here
// becomes a model call downstream.
const defaultUnratedLimit = 10

// runUnratedTasks prints the rater queue (E-2203) as JSON — `submitted` tasks
// with either rating unset, oldest first, capped by --limit. No --project means
// every project: the background job has a database but no cwd.
func runUnratedTasks(args []string) error {
	fs := refusal.NewFlags("unrated-tasks")
	project := fs.String("project", "", "registered project name (default: every project)")
	limit := fs.Int("limit", defaultUnratedLimit, "max tasks to return")
	if err := parseFlags(fs, "unrated-tasks", args); err != nil {
		return err
	}
	if *limit <= 0 {
		return refusal.NoReport("--limit must be positive", "Pass a positive --limit and retry")
	}
	tasks, err := monitor.UnratedSubmittedTasks(*project, *limit)
	if err != nil {
		return fmt.Errorf("read rater queue: %w", err)
	}
	return dbprovenance.Encode(os.Stdout, tasks)
}

// runRaterContext prints one task's rater context (E-2203) as JSON: the
// persisted artifacts the rating prompt may judge. No transcript — the rater
// judges what is written down, as a future implementer will have to.
func runRaterContext(args []string) error {
	fs := refusal.NewFlags("rater-context")
	id := fs.Int64("id", 0, "task id")
	if err := parseFlags(fs, "rater-context", args); err != nil {
		return err
	}
	if *id == 0 {
		return refusal.NoReport("--id is required", "Pass --id and retry")
	}
	ctx, err := monitor.BuildRaterContext(*id)
	if err != nil {
		return fmt.Errorf("build rater context for E-%d: %w", *id, err)
	}
	return dbprovenance.Encode(os.Stdout, ctx)
}

// runRaterClaim takes the per-task rater claim (E-2203) and prints "1" when
// this process won it, "0" when another holds a live claim. Zero is an ordinary
// outcome, so the exit status stays 0 either way; the caller branches on the
// printed value. See internal/monitor/rater_claims.go.
func runRaterClaim(args []string) error {
	fs := refusal.NewFlags("rater-claim")
	id := fs.Int64("id", 0, "task id")
	owner := fs.String("owner", "", "claimant identity (default: this process)")
	ttl := fs.Int("ttl-seconds", 0, "claim lifetime; must exceed the worst-case model call")
	if err := parseFlags(fs, "rater-claim", args); err != nil {
		return err
	}
	if *id == 0 {
		return refusal.NoReport("--id is required", "Pass --id and retry")
	}
	if *ttl <= 0 {
		return refusal.NoReport("--ttl-seconds must be positive", "Pass a positive --ttl-seconds and retry")
	}
	who := *owner
	if who == "" {
		who = monitor.RaterClaimOwner()
	}
	claimed, err := monitor.ClaimRater(*id, who, time.Duration(*ttl)*time.Second)
	if err != nil {
		return fmt.Errorf("claim rating of E-%d: %w", *id, err)
	}
	if claimed {
		fmt.Println("1")
		return nil
	}
	fmt.Println("0")
	return nil
}

// runRaterRelease drops this owner's rater claim (E-2203). Releasing a claim
// that already lapsed and was taken by someone else is a no-op, not a steal.
func runRaterRelease(args []string) error {
	fs := refusal.NewFlags("rater-release")
	id := fs.Int64("id", 0, "task id")
	owner := fs.String("owner", "", "claimant identity (default: this process)")
	if err := parseFlags(fs, "rater-release", args); err != nil {
		return err
	}
	if *id == 0 {
		return refusal.NoReport("--id is required", "Pass --id and retry")
	}
	who := *owner
	if who == "" {
		who = monitor.RaterClaimOwner()
	}
	return monitor.ReleaseRater(*id, who)
}

// runResumeTarget prints the JSON a `session resume` needs to relaunch a lost
// Claude session: the harness UUID and the task worktree to cd into. The ref
// is resolved task-first (the tmux-tab task id is the primary handle) but also
// accepts a session id or UUID prefix. The DB read stays Go-side (E-1486); the
// Python caller cd's to worktree_path and execs `claude --resume <session_id>`.
func runResumeTarget(args []string) error {
	fs := refusal.NewFlags("resume-target")
	ref := fs.String("ref", "", "task id (E-NNNN/NNNN), session id, or Claude UUID prefix")
	if err := parseFlags(fs, "resume-target", args); err != nil {
		return err
	}
	if *ref == "" {
		return refusal.NoReport("--ref is required", "Pass a task or session ref and retry")
	}
	// A ref that resolves to nothing is the one refusal here whose class turns
	// on where the ref came from — the user's own words or the agent's guess —
	// and the audit settled it as report_if for that reason. The sentence is
	// built in internal/monitor/resume.go, so the class belongs there too; see
	// relayed().
	target, err := monitor.ResolveResumeTarget(*ref)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, target)
}

// runGateClear closes the session's open gate of the given kind, recording the
// cleared_by reason, and prints how many open rows were cleared (0 = nothing was
// pending). It backs the `endless task continue` verb so the Python side clears
// a gate without a Python DB write (E-1486 / E-1542).
// --session-id is the integer sessions.id PK (the Python resolver supplies it).
func runGateClear(args []string) error {
	fs := refusal.NewFlags("gate-clear")
	sessionID := fs.Int64("session-id", 0, "sessions.id (integer PK) to clear")
	kind := fs.String("kind", "", "gate kind slug (e.g. revisit)")
	clearedBy := fs.String("cleared-by", "", "reason recorded in cleared_by")
	if err := parseFlags(fs, "gate-clear", args); err != nil {
		return err
	}
	if *sessionID == 0 {
		return refusal.NoReport("--session-id is required", "Pass --session-id and retry")
	}
	gk, err := gatekind.Parse(*kind)
	if err != nil {
		// gatekind.Parse's "invalid gate kind" is built in internal/gatekind,
		// which carries no class yet. It is named one here rather than left to
		// relayed(), because a slug the caller can retype is the one reading
		// that sentence has — and reaching the funnel unclassified would make
		// it a fault the agent reports.
		return refusal.NoReport(err.Error(), "Pass --kind revisit and retry").Cause(err)
	}
	// The verb-driven clear reasons are the only ones valid on this CLI surface;
	// revisit_resolved / superseded are set by the hook directly via the monitor
	// helper, never through here.
	if *clearedBy != "revisit_continue" && *clearedBy != "revisit_pause" {
		return refusal.NoReport(
			"--cleared-by must be revisit_continue or revisit_pause",
			"Pass one of those two values and retry",
		)
	}
	switch gk {
	case gatekind.GateKindRevisit:
		n, err := monitor.ClearRevisitGate(*sessionID, *clearedBy)
		if err != nil {
			// The one refusal in this command the user has to hear about. Every
			// other failure here either leaves a read unanswered or is a flag
			// the caller can fix; this one means a WRITE did not happen, so the
			// `task continue` or `task pause` the user just ran looks like it
			// took effect and the gate is still standing. Nothing downstream
			// re-checks, so an agent that handled it quietly would leave the
			// session wedged against a gate the user believes is gone.
			return refusal.Report(
				err.Error(),
				"what to do about a revisit gate Endless could not clear, which left `task continue` or `task pause` without effect",
			).Cause(err)
		}
		fmt.Println(n)
		return nil
	default:
		return refusal.NoReport(
			fmt.Sprintf("gate-clear: unsupported kind %q", gk),
			"Pass --kind revisit and retry",
		)
	}
}

// runTaskPlan prints the raw plan for a task id to stdout — a Python-side
// read of a document column without a Python DB read (E-894 / E-1445). Output is
// the raw plan, not JSON; empty output means "no plan".
//
// It backed birth-time plan materialization until E-2137 retired that, and no
// caller in this repository uses it now. Kept because it is the narrow read verb
// for one column and costs nothing; `doc-content` below is the one callers
// reach for, because it takes the FILE and works out the column itself.
func runTaskPlan(args []string) error {
	fs := refusal.NewFlags("task-plan")
	id := fs.Int64("id", 0, "task id")
	if err := parseFlags(fs, "task-plan", args); err != nil {
		return err
	}
	if *id == 0 {
		return refusal.NoReport("--id is required", "Pass --id and retry")
	}
	plan, err := monitor.TaskPlan(*id)
	if err != nil {
		return fmt.Errorf("read task plan for E-%d: %w", *id, err)
	}
	_, err = os.Stdout.WriteString(plan)
	return err
}

// runTaskField prints the raw value of one content row (any taskcontent name)
// for a task, without a forbidden Python DB read
// (E-894/E-1486). It backed E-1747's birth-time mirror seeding until E-2137
// retired that; see runTaskPlan for why the verb stays.
func runTaskField(args []string) error {
	fs := refusal.NewFlags("task-field")
	id := fs.Int64("id", 0, "task id")
	name := fs.String("name", "", "content name: "+strings.Join(taskcontent.Slugs(), "|"))
	if err := parseFlags(fs, "task-field", args); err != nil {
		return err
	}
	if *id == 0 {
		return refusal.NoReport("--id is required", "Pass --id and retry")
	}
	if *name == "" {
		return refusal.NoReport(
			"--name is required",
			"Pass --name "+strings.Join(taskcontent.Slugs(), "|")+" and retry",
		)
	}
	content, err := taskcontent.Parse(*name)
	if err != nil {
		// Same reasoning as gate-clear's gatekind.Parse: the sentence is built
		// in internal/taskcontent and is a name the caller can correct, so the
		// class is named at the site that prints it rather than inferred by the
		// funnel.
		return refusal.NoReport(
			err.Error(),
			"Pass --name "+strings.Join(taskcontent.Slugs(), "|")+" and retry",
		).Cause(err)
	}
	value, err := monitor.TaskContent(*id, content)
	if err != nil {
		// The audit classed this NO-REPORT because worktree_cmd.py relayed it as
		// "warning: could not read <label> for E-<n>" and carried on without
		// that mirror. E-2137 retired that caller, so there is no longer a
		// degraded path for the agent to continue down: whoever runs this verb
		// asked a direct question and a database that cannot answer it is
		// Endless broken. Left unclassified, which relayed() reads as a fault.
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
	fs := refusal.NewFlags("worktree-anomalies")
	worktreePath := fs.String("worktree-path", "", "absolute path of the worktree to inspect")
	projectRoot := fs.String("project-root", "", "absolute path of the repo main checkout (enables the prunable probe)")
	// This verb owns its exit code — `endless worktree check` propagates it so
	// the agent can script on 0/1/2 — so it prints through relayed() and
	// returns rather than handing the error to fail(), which would exit 1 and
	// make a usage mistake indistinguishable from "anomalies found".
	if err := parseFlags(fs, "worktree-anomalies", args); err != nil {
		relayed("worktree-anomalies", err).Print()
		return 2
	}
	if *worktreePath == "" {
		refusal.NoReport("--worktree-path is required", "Pass --worktree-path and retry").
			Command("session-query worktree-anomalies").Print()
		return 2
	}
	anomalies := monitor.WorktreeAnomaliesAt(context.Background(), *projectRoot, *worktreePath)
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
// The probe underneath reads no database at all (E-2087): "has this branch's
// work reached the base?" is answered by comparing content in the repository,
// so nothing here depends on a task row being reachable.
//
// Always exits 0 when it ran: "settled" is a legitimate answer, not a failure,
// and the caller reads the verdict from the JSON rather than the exit code.
func runWorktreeUnsettled(args []string) error {
	fs := refusal.NewFlags("worktree-unsettled")
	if err := parseFlags(fs, "worktree-unsettled", args); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return refusal.NoReport(
			"at least one worktree path argument is required",
			"Pass at least one worktree path and retry",
		)
	}
	// context.Background(): a one-shot CLI invocation has no ambient context to
	// inherit, and every git probe beneath this now takes one (E-2128). This is
	// where the chain terminates.
	ctx := context.Background()
	out := make([]worktreeUnsettledJSON, 0, len(paths))
	for _, p := range paths {
		out = append(out, newWorktreeUnsettledJSON(monitor.WorktreeUnsettledDetailAt(ctx, p)))
	}
	return dbprovenance.EncodeIndent(os.Stdout, out, "  ")
}

// runTaskLandedness emits the landedness verdict for each branch given as a
// positional argument, as a JSON array in the same order (E-2095).
//
// BRANCH-based, where its neighbour above is path-based, and the difference is
// the question. `worktree-unsettled` asks about a directory that exists; this
// asks whether a TASK's work reached the base branch, and the most interesting
// answers come from tasks whose worktree is long gone. ED-1587 made the branch
// name a pure function of the task id, so the Python caller constructs
// `task/<id>` and never looks one up.
//
// Reads no database, like every probe in this family (E-1766/E-2087): the answer
// is in the repository, so it is the same inside a self-dev worktree whose
// sandbox has no task row.
//
// Always exits 0 when it ran. "Never landed" is an answer, not a failure, and a
// probe that could not run is reported in the row rather than as an exit code —
// the caller must render it as unknown, never as clean.
func runTaskLandedness(args []string) error {
	fs := refusal.NewFlags("task-landedness")
	root := fs.String("project-root", "", "repository root to probe (required)")
	if err := parseFlags(fs, "task-landedness", args); err != nil {
		return err
	}
	if *root == "" {
		return refusal.NoReport("--project-root is required", "Pass --project-root and retry")
	}
	branches := fs.Args()
	if len(branches) == 0 {
		return refusal.NoReport(
			"at least one branch argument is required",
			"Pass at least one branch and retry",
		)
	}
	rows := monitor.TaskLandedness(context.Background(), *root, branches)
	out := make([]taskLandednessJSON, 0, len(rows))
	for _, l := range rows {
		// Nil slices marshal as null; the Python side wants a list it can
		// iterate unconditionally, so normalize to empty.
		log := l.UnlandedLog
		if log == nil {
			log = []string{}
		}
		out = append(out, taskLandednessJSON{
			Branch:        l.Branch,
			BranchExists:  l.BranchExists,
			Base:          l.Base,
			UnlandedCount: l.UnlandedCount,
			UnlandedLog:   log,
			Undetermined:  l.Undetermined(),
			Interrupted:   l.Interrupted,
			BaseErr:       l.BaseErr,
			ProbeErr:      l.ProbeErr,
		})
	}
	return dbprovenance.EncodeIndent(os.Stdout, out, "  ")
}

// taskLandednessJSON is the wire shape of one task's landedness. Declared
// explicitly for the same reason worktreeUnsettledJSON is: the Go/Python
// contract stays readable in one place, and `undetermined` is derived here so
// the renderer never re-derives the predicate.
type taskLandednessJSON struct {
	Branch        string   `json:"branch"`
	BranchExists  bool     `json:"branch_exists"`
	Base          string   `json:"base"`
	UnlandedCount int      `json:"unlanded_count"`
	UnlandedLog   []string `json:"unlanded_log"`
	Undetermined  bool     `json:"undetermined"`
	// Interrupted rides alongside `undetermined` rather than replacing it: the
	// verdict is the same (the probe established nothing), but a surface must
	// not print "failed" about a repository somebody just pressed Ctrl-C in
	// (E-2113).
	Interrupted bool   `json:"interrupted"`
	BaseErr     string `json:"base_error,omitempty"`
	ProbeErr    string `json:"probe_error,omitempty"`
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
	// UnlandedKnown is E-2128's: FALSE means nothing has computed whether this
	// branch's commits reached the base, which is a third state beside settled and
	// unsettled and a different fact from Undetermined's "the probe failed".
	//
	// It is effectively always true through THIS command, which computes on a miss
	// — a direct question deserves a real answer. It is carried anyway so the
	// Python renderer reads one contract whatever fills it, rather than inferring
	// the state from the absence of a key.
	UnlandedKnown bool `json:"unlanded_known"`
	// Base is the resolved default branch the count was measured against, so
	// the renderer can name it instead of saying "main" on a repo where that is
	// not true.
	Base        string `json:"base"`
	StatusErr   string `json:"status_error,omitempty"`
	UnlandedErr string `json:"unlanded_error,omitempty"`
	BaseErr     string `json:"base_error,omitempty"`
	LookupErr   string `json:"lookup_error,omitempty"`
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
		UnlandedKnown:      d.UnlandedKnown,
		Base:               d.Base,
		StatusErr:          d.StatusErr,
		UnlandedErr:        d.UnlandedErr,
		BaseErr:            d.BaseErr,
		LookupErr:          d.LookupErr,
	}
}

func runListLive(args []string) error {
	fs := refusal.NewFlags("list-live")
	projectRoot := fs.String("project-root", "", "absolute path of the project root")
	if err := parseFlags(fs, "list-live", args); err != nil {
		return err
	}
	if *projectRoot == "" {
		return refusal.NoReport("--project-root is required", "Pass --project-root and retry")
	}

	projectID, _, err := monitor.ProjectIDForPath(*projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project for %s: %w", *projectRoot, err)
	}
	if projectID == 0 {
		// Unregistered cwd: empty result rather than error so the Python
		// caller can treat "no project" and "no live sessions" uniformly.
		return dbprovenance.Encode(os.Stdout, []monitor.LiveSession{})
	}

	sessions, err := monitor.ListLiveSessions(projectID)
	if err != nil {
		return fmt.Errorf("list live sessions: %w", err)
	}
	return dbprovenance.Encode(os.Stdout, sessions)
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
	fs := refusal.NewFlags("ensure-claude-id")
	sessionID := fs.String("session-id", "", "Claude harness session UUID")
	projectRoot := fs.String("project-root", "", "absolute path of the project root")
	process := fs.String("process", "", "TMUX_PANE value (optional)")
	if err := parseFlags(fs, "ensure-claude-id", args); err != nil {
		return err
	}
	if *sessionID == "" {
		return refusal.NoReport("--session-id is required", "Pass --session-id and retry")
	}
	if *projectRoot == "" {
		return refusal.NoReport("--project-root is required", "Pass --project-root and retry")
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

// runDocContent prints the AUTHORITATIVE content behind a document mirror
// path — the database column the file is a projection of — so the Python side
// can compare a file against its source without a Python DB read (E-894).
//
// It takes the PATH rather than an id and a column because that is the question
// every caller actually has: git hands them a changed file and they need to
// know what it should have said. Resolving the path in one place (docmirror)
// keeps "which column owns this file" from being decided separately by the
// sweep, the orphan-branch check, and the branch-history cleanup.
//
// Exit 0 with empty output means "the row exists and the column is empty", which
// is a legitimate answer. An UNRECOGNIZED path is an error, not empty output:
// silently reporting "" for a typo'd path would read as "the database has
// nothing", and a caller comparing against it would conclude the file is
// unauthorized content and act on that.
func runDocContent(args []string) error {
	fs := refusal.NewFlags("doc-content")
	path := fs.String("path", "", "repo-relative path of a document mirror")
	if err := parseFlags(fs, "doc-content", args); err != nil {
		return err
	}
	if *path == "" {
		return refusal.NoReport("--path is required", "Pass --path and retry")
	}
	src, ok := docmirror.Resolve(*path)
	if !ok {
		// No row in the audit for either of this verb's refusals — doc-content
		// is not in the inventory at all. Both are classified by the same rule
		// as its neighbours: an argument naming something that is not a mirror
		// path is the caller's to correct, and the usage block already lists
		// the shapes that resolve.
		return refusal.NoReport(
			fmt.Sprintf("not a document mirror path: %s", *path),
			"Pass a path under .endless/tasks/e-N/, .endless/{plans,outcomes,analyses}/ or .endless/decisions/ and retry",
		)
	}

	var content string
	var err error
	switch {
	case src.DecisionID != 0:
		content, err = monitor.DecisionBody(src.DecisionID)
	default:
		content, err = monitor.TaskContent(src.TaskID, src.Name)
	}
	if err != nil {
		return fmt.Errorf("read content for %s: %w", *path, err)
	}
	_, err = os.Stdout.WriteString(content)
	return err
}
