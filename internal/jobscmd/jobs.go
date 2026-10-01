// Package jobscmd implements `endless-go jobs` — the operator surface over the
// fire-once background job runner (E-698).
//
//	endless-go jobs list           registry + schedule + last run + failures
//	endless-go jobs run [--job N]  fire the runner once, now
//	endless-go jobs retry <name>   clear a job's backoff and make it due now
//
// `run` exists independently of the session-monitor trigger so the runner can be
// fired by a future daemon (E-1848), by hand, and by the verify suite — the
// runner itself is trigger-agnostic by design.
package jobscmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// Run dispatches the `jobs` subcommand.
func Run(args []string) {
	if len(args) == 0 {
		refusal.NoReport(
			"endless-go jobs: no command given",
			"Re-run with a verb — list, run [--job N] or retry <name>",
		).Command("jobs").Text(usageText()).Exit(2)
	}

	switch args[0] {
	case "list":
		runList()
	case "run":
		runRun(args[1:])
	case "retry":
		runRetry(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		// Python's jobs_cmd._run_go always passes one of the three verbs, so a
		// name that misses was typed at this binary directly.
		refusal.NoReport(
			fmt.Sprintf("endless-go jobs: unknown command %q", args[0]),
			"Pick one of the listed verbs and retry",
		).Command("jobs").Detail(usageText()).Exit(2)
	}
}

// usageText is the verb list, printed for --help and carried as the body of
// the two usage refusals above.
func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go jobs <command>",
		"Commands:",
		"  list            show registered jobs and their schedule state",
		"  run [--job N]   run every due job once (or force one named job)",
		"  retry <name>    clear a job's backoff and make it due now",
	}, "\n") + "\n"
}

// runList renders the registry joined to its scheduling rows.
func runList() {
	statuses, err := jobs.Statuses()
	if err != nil {
		// The registry lives in this binary; the scheduling rows live in the
		// database. Failing here means the database could not be opened or
		// queried, which no rerun of `jobs list` changes.
		refusal.Report(
			fmt.Sprintf("endless-go jobs: list: %v", err),
			"how to repair an Endless installation whose jobs tables cannot be read",
		).Command("jobs list").Exit(1)
	}
	// Say so loudly when the runner cannot execute anything here: an operator
	// staring at "0 claimed" deserves to know the difference between "nothing was
	// due" and "this process will never run a job".
	if reason := jobs.SuppressionReason(); reason != "" {
		fmt.Printf("jobs suppressed: %s\n\n", reason)
	}
	if len(statuses) == 0 {
		// Since E-1859 registered the first job this is no longer expected —
		// an empty registry now means the blank imports in
		// cmd/endless-go/main.go were dropped. Say so explicitly either way, so
		// an operator can tell "no jobs registered" from "listing failed".
		fmt.Println("no jobs registered")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tINTERVAL\tNEXT DUE\tLAST RUN\tRUNS\tFAILS\tSTATE\tNOTE")
	for _, s := range statuses {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			s.Name,
			interval(s),
			nextDue(s),
			orDash(s.LastRunAt),
			s.RunCount,
			s.FailCount,
			state(s),
			orDash(s.LastNote),
		)
	}
	if err = tw.Flush(); err != nil {
		// Two failures wearing one message, and the errno tells them apart
		// rather than the agent: a reader that walked away — `endless-go jobs
		// list | head` — closed the pipe, and the rows it never read are nobody's
		// problem. Anything else is a write to a terminal or a file that failed,
		// which is the user's to look at.
		summary := fmt.Sprintf("endless-go jobs: list: %v", err)
		if errors.Is(err, syscall.EPIPE) {
			refusal.NoReport(summary,
				"Nothing is wrong: the reader closed the pipe before the listing finished").
				Command("jobs list").Exit(1)
		}
		refusal.Report(summary,
			"why writing the listing failed, given that the rows themselves were read").
			Command("jobs list").Exit(1)
	}
}

// runRun fires the runner once.
func runRun(args []string) {
	fs := refusal.NewFlags("run")
	job := fs.String("job", "", "run only this job, bypassing the due check (the lease still applies)")
	if err := fs.Parse(args); err != nil {
		// -h asked a question and got an answer; it is not a refusal.
		fs.ExitOnHelp(err)
		// flag.ExitOnError used to print and exit 2 from inside Parse, which is
		// why the os.Exit(2) that stood here was dead code. NewFlags is
		// ContinueOnError, so both are ours now; Text carries flag's own error
		// line and usage block, unchanged.
		refusal.NoReport(err.Error(),
			"Only --job <name> exists here: correct the flag and retry").
			Command("jobs run").Text(fs.Output()).Exit(2)
	}

	if *job != "" {
		outcome, err := jobs.RunNamed(context.Background(), *job)
		if err != nil {
			namedJobRefusal("run", err).Exit(1)
		}
		reportOutcome(outcome)
		return
	}

	result := jobs.RunDue(context.Background())
	for _, outcome := range result.Outcomes {
		reportOutcome(outcome)
	}
	fmt.Printf("%d registered, %d claimed, %d failed\n",
		len(result.Outcomes), result.Claimed(), result.Failed())
}

// runRetry clears a job's backoff.
func runRetry(args []string) {
	if len(args) != 1 {
		// Python's `endless jobs retry` always passes exactly one name, so a
		// count that misses came from somebody typing the binary directly.
		refusal.NoReport(
			"Usage: endless-go jobs retry <name>",
			"Pass exactly one job name and retry",
		).Command("jobs retry").Exit(2)
	}
	if err := jobs.Retry(args[0]); err != nil {
		namedJobRefusal("retry", err).Exit(1)
	}
	fmt.Printf("%s: backoff cleared, due now\n", args[0])
}

// namedJobRefusal classifies the single error `run --job` and `retry` can each
// raise, and the sentinel decides which reading applies rather than the agent:
// a name no job is registered under is one `endless jobs list` answers and the
// caller retypes, while anything else reached the database and failed there,
// which is the user's to investigate. verb names the verb for the verdict and
// reproduces the message prefix each site has always printed.
func namedJobRefusal(verb string, err error) *refusal.Error {
	summary := fmt.Sprintf("endless-go jobs: %s: %v", verb, err)
	if errors.Is(err, jobs.ErrUnknownJob) {
		return refusal.NoReport(summary,
			"Run `endless jobs list` and retry with a registered job name").
			Command("jobs " + verb)
	}
	return refusal.Report(summary,
		"how to repair an Endless database the job runner cannot read or write").
		Command("jobs " + verb)
}

// reportOutcome prints one job's result. A job that was not claimed is reported
// too: "not due" and "another invocation won the race" are both ordinary, and
// silence would leave an operator unsure whether the job exists.
func reportOutcome(outcome jobs.Outcome) {
	if !outcome.Claimed {
		fmt.Printf("%s: skipped (not due, or claimed elsewhere)\n", outcome.Name)
		return
	}
	if outcome.Err != nil {
		fmt.Printf("%s: FAILED in %s: %v\n", outcome.Name, outcome.Elapsed.Round(time.Millisecond), outcome.Err)
		return
	}
	if outcome.Note != "" {
		fmt.Printf("%s: ok in %s: %s\n", outcome.Name, outcome.Elapsed.Round(time.Millisecond), outcome.Note)
		return
	}
	fmt.Printf("%s: ok in %s\n", outcome.Name, outcome.Elapsed.Round(time.Millisecond))
}

// interval renders a job's declared cadence, marking backoff when configured.
func interval(s jobs.Status) (text string) {
	if !s.Registered {
		text = "-"
		goto end
	}
	text = s.Schedule.Interval.String()
	if s.Schedule.MaxBackoff > 0 {
		text += " (max " + s.Schedule.MaxBackoff.String() + ")"
	}

end:
	return text
}

// nextDue renders the relative time until a job is due, which is far easier to
// act on than a bare UTC timestamp — especially for a backed-off job whose next
// attempt is hours out.
func nextDue(s jobs.Status) (text string) {
	var d time.Duration

	if !s.Scheduled {
		text = "never run"
		goto end
	}
	d = s.DueIn()
	if d <= 0 {
		text = "due now"
		goto end
	}
	text = "in " + d.Round(time.Second).String()

end:
	return text
}

// state summarizes a job's health in one word.
func state(s jobs.Status) (text string) {
	switch {
	case !s.Registered:
		// A scheduling row whose job is gone from the registry. Inert, but worth
		// showing so a stale row is visible rather than silently ignored.
		text = "unregistered"
	case s.LeaseOwner != "":
		text = "running"
	case s.FailCount > 0:
		text = "failing"
	default:
		text = "ok"
	}
	return text
}

// orDash renders an empty timestamp as a dash.
func orDash(s string) (text string) {
	text = s
	if text == "" {
		text = "-"
	}
	return text
}
