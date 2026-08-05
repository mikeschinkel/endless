// Package errorscmd implements `endless-go errors` — the operator surface over
// the machine-local fault record (E-698).
//
//	endless-go errors show [--all] [--detail] [--id N]   list incidents
//	endless-go errors clear [<id>...]                    mark incidents cleared
//	endless-go errors codes                              print the error catalog
//
// Clearing NEVER deletes: the row stays as history, and a recurrence of the same
// fingerprint opens a NEW incident beside it, so a fault that came back is
// visibly distinct from one that never left.
//
// Clearing is also NOT a retry. It means "I have seen this"; making a
// backed-off job due again is `endless-go jobs retry`, deliberately a separate
// verb so that tidying an error list cannot silently re-arm a job that is still
// broken.
package errorscmd

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/mikeschinkel/endless/internal/faults"
)

// Run dispatches the `errors` subcommand.
func Run(args []string) {
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "show", "list":
		runShow(args[1:])
	case "clear":
		runClear(args[1:])
	case "codes":
		runCodes()
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-go errors: unknown command %q\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go errors <command>")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  show [--all] [--detail] [--id N]  list uncleared errors (--all includes cleared)")
	fmt.Fprintln(w, "  clear [<id>...]                   mark errors cleared (all open ones when no id given)")
	fmt.Fprintln(w, "  codes                             print the documented error catalog")
}

// runShow lists incidents.
func runShow(args []string) {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	all := fs.Bool("all", false, "include cleared errors")
	detail := fs.Bool("detail", false, "print each occurrence's full captured detail")
	id := fs.Int64("id", 0, "show only this error id")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if *id > 0 {
		showOne(*id, *detail)
		return
	}

	incidents, err := faults.List(*all, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: show:", err)
		os.Exit(1)
	}
	if len(incidents) == 0 {
		fmt.Println("no errors")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSEVERITY\tCODE\tCOUNT\tLAST SEEN\tSOURCE\tSUMMARY")
	for _, incident := range incidents {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\t%s\t%s\n",
			incident.ID, severityText(incident), incident.Code, incident.Occurrences,
			incident.LastSeenAt, incident.Source, incident.Summary)
	}
	if err = tw.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: show:", err)
		os.Exit(1)
	}

	if *detail {
		for _, incident := range incidents {
			printDetails(incident.ID)
		}
	}
}

// showOne prints a single incident in long form.
func showOne(id int64, detail bool) {
	incident, ok, err := faults.Get(id)
	if err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: show:", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "endless-go errors: no error with id %d\n", id)
		os.Exit(1)
	}

	fmt.Printf("Error:       %d\n", incident.ID)
	fmt.Printf("Code:        %s (%s)\n", incident.Code, incident.Title())
	fmt.Printf("Severity:    %s\n", severityText(incident))
	fmt.Printf("Source:      %s\n", incident.Source)
	fmt.Printf("Occurrences: %d\n", incident.Occurrences)
	fmt.Printf("First seen:  %s\n", incident.FirstSeenAt)
	fmt.Printf("Last seen:   %s\n", incident.LastSeenAt)
	if incident.ClearedAt != "" {
		fmt.Printf("Cleared:     %s", incident.ClearedAt)
		if incident.ClearedBy != "" {
			fmt.Printf(" by %s", incident.ClearedBy)
		}
		fmt.Println()
	}
	fmt.Printf("Summary:     %s\n", incident.Summary)

	if detail {
		printDetails(incident.ID)
	}
}

// printDetails prints every logged occurrence for one incident. The detail lives
// in the JSONL log rather than the DB, so the table stays bounded by distinct
// fingerprint count while diagnosis loses nothing.
func printDetails(id int64) {
	details, err := faults.Details(id)
	if err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: detail:", err)
		return
	}
	if len(details) == 0 {
		return
	}
	fmt.Printf("\n--- error %d: %d logged occurrence(s) ---\n", id, len(details))
	for _, d := range details {
		fmt.Printf("\n[%s] occurrence %d\n", d.TS, d.Occurrence)
		if d.Detail != "" {
			fmt.Println(d.Detail)
		}
		for key, value := range d.Fields {
			fmt.Printf("  %s: %v\n", key, value)
		}
	}
}

// runClear marks incidents cleared.
func runClear(args []string) {
	var ids []int64

	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "endless-go errors: clear: %q is not an error id\n", arg)
			os.Exit(2)
		}
		ids = append(ids, id)
	}

	cleared, err := faults.Clear(ids, clearedBy())
	if err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: clear:", err)
		os.Exit(1)
	}
	fmt.Printf("cleared %d error(s)\n", cleared)
}

// runCodes prints the catalog, which is also what docs/errors.md documents.
func runCodes() {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CODE\tSEVERITY\tSLUG\tTITLE")
	for _, code := range faults.Codes() {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", code.ID, code.Severity, code.Slug, code.Title)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: codes:", err)
		os.Exit(1)
	}
}

// severityText renders an incident's severity, marking cleared rows so `--all`
// output distinguishes history from live problems at a glance.
func severityText(incident faults.Incident) (text string) {
	text = string(incident.Severity)
	if incident.ClearedAt != "" {
		text += " (cleared)"
	}
	return text
}

// clearedBy records who cleared an incident. $USER is best-effort attribution
// for a machine-local record; an empty value is stored as-is rather than
// invented.
func clearedBy() string {
	return os.Getenv("USER")
}
