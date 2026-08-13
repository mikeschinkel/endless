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
	case "record":
		runRecord(args[1:])
	case "raise":
		runRaise(args[1:])
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
	fmt.Fprintln(w, "  record --code ID --summary T [--source S] [--detail D]")
	fmt.Fprintln(w, "                                   record a real catalog fault (internal; used by `endless triage run`)")
	fmt.Fprintln(w, "  raise [--severity S] [--summary T] [--repeat N]")
	fmt.Fprintln(w, "                                    record a SYNTHETIC fault, to see this surface work")
}

// runRaise records a synthetic fault so the badge, the store and the detail log
// can be exercised on demand.
//
// Before this, the only way to look at the error surface was to wait for
// something to actually break (E-1950) — which made the one view whose whole job
// is reporting trouble the hardest view in the system to inspect, and left the
// session-status badge with no end-to-end test that rendered a real incident.
//
// It goes through faults.Record, not a direct INSERT, so what it produces is
// indistinguishable in shape from a genuine fault: same upsert, same
// fingerprinting, same JSONL detail line. Only the CODE marks it synthetic, and
// the catalog titles say so out loud.
//
// Which database it writes to is the CALLER's decision, never this command's.
// Inside a self-dev worktree the Python CLI requires an explicit --db and
// refuses without one (E-1429/E-1950); `endless-go` takes --config-dir. Pinning
// a database here on the user's behalf was tried and reverted: it let
// `errors clear` dismiss incidents in the real record from a worktree with no
// flag, which is the failure the gate exists to prevent.
func runRaise(args []string) {
	fs := flag.NewFlagSet("raise", flag.ExitOnError)
	severity := fs.String("severity", "warning", "severity to raise: warning or error")
	summary := fs.String("summary", "", "incident summary (defaults to the code's title)")
	source := fs.String("source", "manual:raise", "source subsystem to attribute it to")
	repeat := fs.Int("repeat", 1, "record this many occurrences (they collapse into one incident)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	var code faults.Code
	switch *severity {
	case "warning":
		code = faults.ErrCodeTestWarning
	case "error":
		code = faults.ErrCodeTestError
	default:
		fmt.Fprintf(os.Stderr, "endless-go errors: raise: unknown severity %q (want warning or error)\n", *severity)
		os.Exit(2)
	}

	if *repeat < 1 {
		fmt.Fprintln(os.Stderr, "endless-go errors: raise: --repeat must be at least 1")
		os.Exit(2)
	}

	if !faults.Bound() {
		fmt.Fprintln(os.Stderr, "endless-go errors: raise: the fault store is not bound")
		os.Exit(1)
	}

	for i := 0; i < *repeat; i++ {
		faults.Record(faults.Fault{
			Code:    code,
			Source:  *source,
			Summary: *summary,
			Detail:  "Raised deliberately by `endless errors raise`. Nothing is wrong.",
			Fields:  map[string]any{"synthetic": true, "occurrence": i + 1},
		})
	}

	// Record cannot report failure — by contract it swallows everything — so
	// confirm by reading the incident back rather than by assuming it landed.
	incidents, err := faults.List(false, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: raise: recorded, but could not read it back:", err)
		os.Exit(1)
	}
	for _, incident := range incidents {
		if incident.Code != code.ID || incident.Source != *source {
			continue
		}
		fmt.Printf("raised %s (%s) as error %d, %d occurrence(s)\n",
			code.ID, code.Severity, incident.ID, incident.Occurrences)
		fmt.Println("dismiss it with: endless errors clear", incident.ID)
		return
	}

	fmt.Fprintln(os.Stderr, "endless-go errors: raise: the fault did not land")
	os.Exit(1)
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

	printClearHint(incidents)
}

// printClearHint names the command that makes these rows go away.
//
// The badge and this listing were the only surfaces a user ever saw, and neither
// mentioned `clear` — so the one action available on a fault that had already
// self-healed was undiscoverable, and a stale warning read as permanent (E-1950).
// Suppressed when nothing here is still open, since clearing a cleared incident
// does nothing.
func printClearHint(incidents []faults.Incident) {
	open := 0
	for _, incident := range incidents {
		if incident.ClearedAt == "" {
			open++
		}
	}
	if open == 0 {
		return
	}

	fmt.Println()
	fmt.Println("Once you have read these, dismiss them:")
	fmt.Println("  endless errors clear            mark every open error above as seen")
	fmt.Println("  endless errors clear <id>       dismiss just one")
	fmt.Println()
	fmt.Println("Clearing is an acknowledgement, not a retry — it does not re-arm a failing job.")
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

// runRecord records a REAL catalog fault. It is the bridge the Python CLI needs:
// `endless triage run` executes detached, where a failure has nowhere to go, and
// the fault store is the surface a user actually watches (the `session status` /
// `session monitor` badge). Distinct from `raise`, which only ever emits the two
// synthetic test codes and says so in its detail text.
//
// --code must name a catalog entry, so this cannot invent classifications that
// have no docs/errors.md section; an unknown ID is a usage error.
func runRecord(args []string) {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	codeID := fs.String("code", "", "catalog code ID, e.g. ERR-0008")
	summary := fs.String("summary", "", "short summary shown in lists and the badge")
	source := fs.String("source", "", "subsystem raising it, e.g. triage:inline")
	detail := fs.String("detail", "", "long capture; goes to the detail log, never the DB")
	fingerprint := fs.String("fingerprint", "", "grouping key (defaults to the summary)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *codeID == "" || *summary == "" {
		fmt.Fprintln(os.Stderr, "endless-go errors: record: --code and --summary are required")
		os.Exit(2)
	}

	code, ok := faults.LookupCode(*codeID)
	if !ok {
		fmt.Fprintf(os.Stderr, "endless-go errors: record: unknown code %q\n", *codeID)
		os.Exit(2)
	}

	if !faults.Bound() {
		// Not an error: a caller with no fault store bound (a test DB, a
		// sandbox) still has a working triager. Say so and exit clean rather
		// than failing the caller for a diagnostic side effect.
		fmt.Fprintln(os.Stderr, "endless-go errors: record: the fault store is not bound; nothing recorded")
		return
	}

	faults.Record(faults.Fault{
		Code:        code,
		Source:      *source,
		Fingerprint: *fingerprint,
		Summary:     *summary,
		Detail:      *detail,
	})
}
