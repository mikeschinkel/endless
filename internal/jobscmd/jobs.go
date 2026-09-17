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
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// Run dispatches the `jobs` subcommand.
func Run(args []string) {
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "list":
		runList()
	case "run":
		runRun(args[1:])
	case "retry":
		runRetry(args[1:])
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-go jobs: unknown command %q\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go jobs <command>")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list            show registered jobs and their schedule state")
	fmt.Fprintln(w, "  run [--job N]   run every due job once (or force one named job)")
	fmt.Fprintln(w, "  retry <name>    clear a job's backoff and make it due now")
}

// runList renders the registry joined to its scheduling rows.
func runList() {
	statuses, err := jobs.Statuses()
	if err != nil {
		fmt.Fprintln(os.Stderr, "endless-go jobs: list:", err)
		os.Exit(1)
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
	fmt.Fprintln(tw, "NAME\tINTERVAL\tNEXT DUE\tLAST RUN\tRUNS\tFAILS\tSTATE")
	for _, s := range statuses {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			s.Name,
			interval(s),
			nextDue(s),
			orDash(s.LastRunAt),
			s.RunCount,
			s.FailCount,
			state(s),
		)
	}
	if err = tw.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "endless-go jobs: list:", err)
		os.Exit(1)
	}
}

// runRun fires the runner once.
func runRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	job := fs.String("job", "", "run only this job, bypassing the due check (the lease still applies)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if *job != "" {
		outcome, err := jobs.RunNamed(context.Background(), *job)
		if err != nil {
			fmt.Fprintln(os.Stderr, "endless-go jobs: run:", err)
			os.Exit(1)
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
		fmt.Fprintln(os.Stderr, "Usage: endless-go jobs retry <name>")
		os.Exit(2)
	}
	if err := jobs.Retry(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "endless-go jobs: retry:", err)
		os.Exit(1)
	}
	fmt.Printf("%s: backoff cleared, due now\n", args[0])
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
