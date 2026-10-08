// Package worktreecmd implements the `endless-go worktree` subcommand: the
// worktree-lifecycle questions the Python CLI must ask but cannot answer
// itself.
//
// It exists because the answers are already implemented in Go, on the reaper's
// path, and `endless worktree drop` is Python (E-1947). Porting a check across
// the language boundary would mean two implementations of a predicate that
// gates directory removal — the failure class that has already cost weeks. So
// the Python side shells out here instead.
//
// DB context: `in-use` READS the sessions table, so it must see the database
// the caller resolved. It is deliberately NOT in cmd/endless-go's PinMainDB
// group — it takes the `--db`/`--db-dir` that ConsumeDBFlags strips, exactly
// as `event` and `session-query` do, and the Python caller threads
// `config.go_db_context_args()`. Pinning main instead would answer a self-dev
// worktree's question from the real ledger and report no live session.
// `ledger-orphans` opens no database at all: it is pure git, so its caller
// threads nothing and the resolved context is simply unused. `land-gate` opens
// no database either: it is git, the project config and the project's hook.
// `verify-state` READS tasks, so like `in-use` it takes the caller's context.
package worktreecmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/landgate"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// Exit codes. `in-use` reports its verdict through the status code so a shell
// caller needs no parsing, with the in-use case distinct from a plain failure.
const (
	exitNotInUse     = 0
	exitUndetermined = 1
	exitUsage        = 2
	exitInUse        = 3
)

func Run(args []string) {
	if len(args) < 1 {
		refusal.NoReport(
			"endless-go worktree: no verb given",
			"Pass a verb — in-use or ledger-orphans — and retry",
		).Command("worktree").Text(usageText()).Exit(exitUsage)
	}
	switch args[0] {
	case "in-use":
		os.Exit(runInUse(args[1:]))
	case "ledger-orphans":
		os.Exit(runLedgerOrphans(args[1:]))
	case "land-gate":
		os.Exit(runLandGate(args[1:]))
	case "verify-state":
		os.Exit(runVerifyState(args[1:]))
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		// Two readings, and this binary cannot tell them apart. Typed directly,
		// it is a typo the agent fixes and forgets. Relayed by `endless worktree
		// drop`, it means the installed endless-go is older than the Python CLI
		// that called it — a mismatched pair the user has to reinstall, and one
		// that silently disables the guard standing between a live session and
		// its own working directory.
		refusal.ReportIf(
			fmt.Sprintf("endless-go worktree: unknown verb %q", args[0]),
			"this came from `endless worktree drop` or `endless worktree land` rather than from a verb you typed",
			"retry with in-use, ledger-orphans, land-gate or verify-state",
			"the installed endless-go is older than the endless CLI calling it, and only the user can reinstall a matching pair",
		).Command("worktree").Detail(usageText()).Exit(exitUsage)
	}
}

// inUseJSON is the wire shape of the verdict under --json. Reason is the
// human-readable string the Python refusal prints verbatim, so the two sides
// never word the same guard differently.
type inUseJSON struct {
	Dir    string `json:"dir"`
	TaskID int64  `json:"task_id"`
	InUse  bool   `json:"in_use"`
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

// runInUse answers monitor.WorktreeInUse for one directory.
//
//	endless-go worktree in-use --dir <path> [--task <id>] [--json]
//
// --task is optional: 0 (absent) means the directory belongs to no task, so
// only the live-process probe applies. Every endless-managed e-NNN worktree
// has one and the caller passes it.
func runInUse(args []string) int {
	fs := refusal.NewFlags("in-use")
	dir := fs.String("dir", "", "worktree directory to inspect (required)")
	taskID := fs.Int64("task", 0, "id of the task the directory belongs to; 0 skips the session probe")
	asJSON := fs.Bool("json", false, "emit the verdict as JSON")
	if err := fs.Parse(args); err != nil {
		// A fault, not a usage error, and that is not a formality: the only
		// caller is `endless worktree drop`, which builds this argv itself. A
		// flag it got wrong means Endless is broken — and the guard standing
		// between a removal and a live session's working directory just failed
		// open-looking, which the user has to hear about.
		refusal.Faultf("endless-go worktree in-use: %s", err).
			Command("worktree in-use").Text(fs.Output()).Print()
		return exitUsage
	}
	if *dir == "" {
		refusal.Faultf("endless-go worktree in-use: --dir is required").
			Command("worktree in-use").Print()
		return exitUsage
	}

	db, err := monitor.DB()
	if err != nil {
		// Fail closed, as WorktreeInUse itself does: a caller about to remove a
		// directory must read "could not tell" as "do not remove".
		return report(*asJSON, inUseJSON{
			Dir: *dir, TaskID: *taskID, InUse: true,
			Reason: string(monitor.ReasonUndetermined), Error: err.Error(),
		})
	}

	inUse, reason, err := monitor.WorktreeInUse(db, *dir, *taskID)
	out := inUseJSON{
		Dir: *dir, TaskID: *taskID, InUse: inUse, Reason: string(reason),
	}
	if err != nil {
		out.Error = err.Error()
	}
	return report(*asJSON, out)
}

// report writes the verdict and maps it to the exit code. An error is always
// accompanied by InUse=true (WorktreeInUse fails closed), but the exit code
// still distinguishes the two so a caller can tell "in use" from "broken".
func report(asJSON bool, out inUseJSON) int {
	if asJSON {
		_ = dbprovenance.EncodeIndent(os.Stdout, out, "  ")
	} else if out.Reason != "" {
		fmt.Fprintln(os.Stdout, out.Reason)
	}
	if out.Error != "" {
		// Undetermined, and WorktreeInUse fails closed, so the caller is about
		// to refuse a removal it cannot justify. Only the user can decide to
		// proceed past a guard that could not answer.
		refusal.Report(
			fmt.Sprintf("endless-go worktree in-use: %s", out.Error),
			"whether to remove a worktree Endless could not prove is idle",
		).Command("worktree in-use").Print()
		return exitUndetermined
	}
	if out.InUse {
		return exitInUse
	}
	return exitNotInUse
}

// runLedgerOrphans classifies the commits in base..branch by whether the base
// branch already holds their ledger content.
//
//	endless-go worktree ledger-orphans --repo <path> --base <rev> --branch <rev>
//
// Always JSON on stdout — the only caller is `endless worktree diagnose`, which
// needs the per-commit evidence, not a verdict. Exit 0 on a successful
// classification whatever it found; 1 when git could not answer.
//
// This is pure git: no database, no config. It is here rather than in Python
// because the rule it applies (ED-1553: a fork point is identified by ledger
// content, never by the SHA that held it) already has one implementation, in
// internal/events, and the behind-base bug is what a second copy of a git
// predicate costs.
func runLedgerOrphans(args []string) int {
	fs := refusal.NewFlags("ledger-orphans")
	repo := fs.String("repo", "", "repository or worktree directory (required)")
	base := fs.String("base", "", "base revision the branch would land on (required)")
	branch := fs.String("branch", "", "branch revision to classify (required)")
	if err := fs.Parse(args); err != nil {
		ledgerOrphansFault("%s", err).Text(fs.Output()).Print()
		return exitUsage
	}
	for name, val := range map[string]string{
		"--repo": *repo, "--base": *base, "--branch": *branch,
	} {
		if val == "" {
			ledgerOrphansFault("%s is required", name).Print()
			return exitUsage
		}
	}

	rpt, err := events.LedgerOrphans(*repo, *base, *branch)
	if err != nil {
		ledgerOrphansFault("%s", err).Print()
		return exitUndetermined
	}
	if err = dbprovenance.EncodeIndent(os.Stdout, rpt, "  "); err != nil {
		ledgerOrphansFault("%s", err).Print()
		return exitUndetermined
	}
	return exitNotInUse
}

// ledgerOrphansFault classifies every way this verb can fail, and they are all
// the same way: its sole caller — `endless worktree diagnose`, through
// land_conflict.ledger_orphans — builds the argv, captures the output and
// treats any non-zero exit as "no answer". So nothing here is a usage error
// somebody can retype, and nothing here is a decision anybody makes. A git
// failure or a bad flag is Endless unable to say which of a branch's commits
// the base already holds, which is a fault whether or not this particular text
// ever reaches a reader.
func ledgerOrphansFault(format string, args ...any) *refusal.Error {
	return refusal.Faultf("endless-go worktree ledger-orphans: "+format, args...).
		Command("worktree ledger-orphans")
}

// runLandGate asks whether a land may proceed (E-2184).
//
//	endless-go worktree land-gate --project <main root> --worktree <path>
//	    --base <branch> --task <E-NNN>
//
// Always JSON on stdout (landgate.Verdict); the only caller is `endless
// worktree land`, which renders a refusal. Exit 0 whenever the gate could
// answer — refused or not, the verdict is in the JSON — and 1 when it could
// not, which the caller must treat as a refusal: a gate that cannot run has not
// said yes.
func runLandGate(args []string) int {
	fs := refusal.NewFlags("land-gate")
	var a landgate.Args
	fs.StringVar(&a.ProjectRoot, "project", "", "main checkout: config and hook are read here (required)")
	fs.StringVar(&a.Worktree, "worktree", "", "task worktree being landed (required)")
	fs.StringVar(&a.Base, "base", "", "branch the land merges into (required)")
	fs.StringVar(&a.Task, "task", "", "task id, E-NNN (required)")
	if err := fs.Parse(args); err != nil {
		landGateFault("%s", err).Text(fs.Output()).Print()
		return exitUsage
	}
	for name, val := range map[string]string{
		"--project": a.ProjectRoot, "--worktree": a.Worktree,
		"--base": a.Base, "--task": a.Task,
	} {
		if val == "" {
			landGateFault("%s is required", name).Print()
			return exitUsage
		}
	}

	v, err := landgate.Check(a)
	if err != nil {
		landGateFault("%s", err).Print()
		return exitUndetermined
	}
	if err = dbprovenance.EncodeIndent(os.Stdout, v, "  "); err != nil {
		landGateFault("%s", err).Print()
		return exitUndetermined
	}
	return exitNotInUse
}

// landGateFault classifies every way land-gate can fail, for ledgerOrphansFault's
// reason: its sole caller, `endless worktree land`, builds the argv itself and
// treats any non-zero exit as a refusal, so a failure here is Endless unable to
// judge a land — a fault, never a usage error somebody can retype.
func landGateFault(format string, args ...any) *refusal.Error {
	return refusal.Faultf("endless-go worktree land-gate: "+format, args...).
		Command("worktree land-gate")
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go worktree <verb> [flags]",
		"Verbs:",
		"  in-use --dir <path> [--task <id>] [--json]",
		"         exit 0 not in use, 3 in use (reason on stdout), 1 undetermined",
		"  ledger-orphans --repo <path> --base <rev> --branch <rev>",
		"         JSON on stdout: which commits in base..branch hold ledger",
		"         content the base branch provably already has",
		"  land-gate --project <path> --worktree <path> --base <branch> --task <E-NNN>",
		"         JSON verdict on stdout: may this land proceed (migration",
		"         collision check, then .endless/hooks/pre-land.sh)",
		"  verify-state [--task <id>]",
		"         JSON on stdout: land properties, status and passed-verify",
		"         staleness for one task, or for every unlanded task",
	}, "\n") + "\n"
}
