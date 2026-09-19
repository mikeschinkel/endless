// Package errorscmd implements `endless-go errors` — the operator surface over
// the machine-local fault record (E-698).
//
//	endless-go errors list [--all] [--detail]   list incidents
//	endless-go errors show <id> [--detail]      one incident, in full
//	endless-go errors clear [<id>...]           mark incidents cleared
//	endless-go errors codes                     print the error catalog
//
// # list lists, show shows one
//
// `show` used to be the listing verb, which made it the only `show` in the CLI
// that did not mean what `task show` and `decision show` mean: one item, in
// detail (E-2148). Worse, there was nowhere to go for the rest of a summary the
// listing had truncated — the detail view the truncation implies did not exist.
//
// So the listing is `list`, and `show <id>` is the detail view. `errors show`
// with no id is a usage error naming `list`, never a listing: silently doing
// something other than what the verb says is how the old spelling misled people
// in the first place.
//
// # Project scope
//
// One Endless database holds every project on the machine, so `show` and `clear`
// are scoped to ONE of them (E-1960): by default the project enclosing the
// working directory, `--project <name>` for another, `--all-projects` for the
// whole machine. Standing in a project and asking what went wrong should answer
// about that project — before E-1960 it answered about every project at once and
// gave no way to tell which rows were yours.
//
// Every scope includes the incidents no project could be attributed to. Those
// are machine-level failures — the background job runner unable to open the
// database, the tmux status bar unable to resolve a pane — and a scoped view
// that hid them would be a view on which they are never reported at all.
//
// Outside any registered project there is nothing to scope to, so both verbs
// fall back to the whole machine.
//
// # A listing says what it counted
//
// Every listing opens with a header naming the scope and the count, the empty
// case included (E-2148). `errors show` once printed a bare "no errors" from
// inside one project while the session-status fault row said `1 error, 1
// warning`, because both incidents belonged to another project — two surfaces
// flatly disagreeing about whether anything was wrong. Neither was lying: the
// listing meant "none HERE" and said "none".
//
// The answer is not to widen the listing. A listing stays scoped, because
// standing in a project and asking what went wrong should answer about that
// project. The answer is that a scoped listing must say it is scoped, and hand
// over the command that shows the rest:
//
//	no errors in endless — 2 elsewhere (endless errors list --all-projects)
//
// The PROJECT column still appears exactly when the listing is machine-wide,
// but it is no longer load-bearing: the header says which of the two situations
// produced it, which a column's presence never could.
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
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// Run dispatches the `errors` subcommand.
func Run(args []string) {
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "list":
		runList(args[1:])
	case "show":
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
	fmt.Fprintln(w, "  list [--all] [--detail]           list uncleared errors (--all includes cleared)")
	fmt.Fprintln(w, "  show <id> [--detail]              one error in full, with its remedy")
	fmt.Fprintln(w, "  clear [<id>...]                   mark errors cleared (all open ones when no id given)")
	fmt.Fprintln(w, "  codes                             print the documented error catalog")
	fmt.Fprintln(w, "  record --code ID --summary T [--source S] [--detail D]")
	fmt.Fprintln(w, "                                   record a real catalog fault (internal; used by `endless triage run`)")
	fmt.Fprintln(w, "  raise [--severity S] [--summary T] [--repeat N]")
	fmt.Fprintln(w, "                                    record a SYNTHETIC fault, to see this surface work")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "list and clear cover the project you are standing in, plus the faults")
	fmt.Fprintln(w, "attributed to no project. Both accept:")
	fmt.Fprintln(w, "  --project <name>                  that project instead of this one")
	fmt.Fprintln(w, "  --all-projects                    every project on the machine")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "`show <id>` ignores the scope: you named the row, so there is nothing")
	fmt.Fprintln(w, "left for a scope to decide.")
}

// runRaise records a synthetic fault so the fault row, the store and the detail log
// can be exercised on demand.
//
// Before this, the only way to look at the error surface was to wait for
// something to actually break (E-1950) — which made the one view whose whole job
// is reporting trouble the hardest view in the system to inspect, and left the
// session-status fault row with no end-to-end test that rendered a real incident.
//
// It goes through faults.Record, not a direct INSERT, so what it produces is
// indistinguishable in shape from a genuine fault: same upsert, same
// fingerprinting, same JSONL detail line. Only the CODE marks it synthetic, and
// the catalog titles say so out loud.
//
// Which database it writes to is the CALLER's decision, never this command's.
// Inside a self-dev worktree the Python CLI requires an explicit --db and
// refuses without one (E-1429/E-1950); `endless-go` takes --db. Pinning
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
	// Machine-wide read-back, deliberately: `raise` records through the same
	// ambient project resolution as any other fault, and asserting the row landed
	// must not depend on this process agreeing with itself about which project
	// that was. The match below is exact enough without the scope.
	incidents, err := faults.List(faults.AllProjects, false, 0)
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

// runList lists incidents within scope.
func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	all := fs.Bool("all", false, "include cleared errors")
	detail := fs.Bool("detail", false, "print each occurrence's full captured detail")
	project := fs.String("project", "", "scope to this project instead of the one you are in")
	allProjects := fs.Bool("all-projects", false, "cover every project on the machine")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	scoped := resolveScope("list", *project, *allProjects)

	incidents, err := faults.List(scoped.scope, *all, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "endless-go errors: list:", err)
		os.Exit(1)
	}

	// The header comes FIRST and is printed unconditionally, empty listing
	// included. A bare "no errors" from a scoped listing is the sentence that
	// contradicted the fault row; this one says which project it means and where
	// the rest are.
	fmt.Println(listingHeader(scoped, len(incidents), *all))
	if len(incidents) == 0 {
		return
	}
	fmt.Println()

	printTable(os.Stdout, incidents, scoped.wide(), *all, listingWidth())

	if *detail {
		for _, incident := range incidents {
			printDetails(incident.ID)
		}
	}

	printClearHint(incidents)
}

// listingWidth is the width the table is fitted to, or 0 for "do not fit".
//
// A real terminal gets its real width. Anything else — a pipe, a file, a capture
// in a test harness — gets 0 and therefore the whole summary, because a script
// reading this output wants the data, not a picture of a table. $COLUMNS is
// deliberately NOT consulted: it is commonly exported in a shell and would make
// a piped run truncate for no reason.
func listingWidth() (cols int) {
	if !liveview.IsTerminal(os.Stdout) {
		goto end
	}
	cols = liveview.DetectCols(0, 0)

end:
	return cols
}

// printTable renders the listing rows, fitted to width columns (0 = unbounded).
func printTable(w io.Writer, incidents []faults.Incident, wide, includeCleared bool, width int) {
	for _, line := range listingLines(incidents, wide, includeCleared, width) {
		fmt.Fprintln(w, line)
	}
}

// listingColumns builds the table, header row included, as a column-major
// grid — every cell already rendered, nothing measured yet.
//
// There is no SEVERITY column (E-2148). The code says it: WARN-0004 is a
// warning because it is spelled WARN-0004, checked by the catalog's own tests,
// so a column beside it would state the same fact on every row at a cost of ten
// columns — taken from the summary, which is the part that was being cut off.
//
// The PROJECT column earns its width only when the listing spans projects. On a
// scoped listing every row would carry the same value, which the header has
// already given once.
//
// STATUS appears only under --all, which is the only mode in which a row can be
// anything but open. It is what the severity column's "(cleared)" suffix became,
// and it says the same thing in its own column instead of inside another one's.
func listingColumns(incidents []faults.Incident, wide, includeCleared bool) (cols []listColumn) {
	add := func(head string, shed int, cell func(faults.Incident) string) {
		col := listColumn{head: head, shed: shed}
		col.cells = make([]string, 0, len(incidents))
		for _, incident := range incidents {
			col.cells = append(col.cells, cell(incident))
		}
		cols = append(cols, col)
	}

	add("ID", shedNever, func(i faults.Incident) string { return strconv.FormatInt(i.ID, 10) })
	add("CODE", shedNever, func(i faults.Incident) string { return i.Code })
	if includeCleared {
		add("STATUS", 3, statusText)
	}
	if wide {
		add("PROJECT", 4, projectText)
	}
	add("COUNT", 2, func(i faults.Incident) string { return strconv.FormatInt(i.Occurrences, 10) })
	add("LAST SEEN", 5, func(i faults.Incident) string { return i.LastSeenAt })
	add("SOURCE", 6, func(i faults.Incident) string { return i.Source })
	add("SUMMARY", shedNever, func(i faults.Incident) string { return i.Summary })

	return cols
}

// listColumn is one column of the listing: its heading, its rendered cells, and
// how readily it gives up its width.
type listColumn struct {
	head  string
	cells []string
	shed  int // higher sheds first; shedNever stays at every width
	width int
}

// shedNever marks the three columns a listing is not a listing without: the id
// you type into the next command, the code that says what and how bad, and the
// summary that says which one.
const shedNever = 0

// minSummary is the narrowest SUMMARY worth keeping a column for. Below it the
// cell is an ellipsis and a syllable, which is not a summary.
const minSummary = 12

// gap is the run of spaces between columns.
const gap = "  "

// listingLines renders the table — heading first — fitted into width columns.
// A width of 0 means unbounded: every cell renders whole.
//
// This exists because the listing WRAPPED (E-2148 item 4). It rendered through
// text/tabwriter, which pads to the widest cell and knows nothing about the
// terminal, so a long summary ran past the margin and the row folded — on the
// one surface whose job is to be scannable when something is wrong.
//
// Two things follow from fitting it properly, and both are why tabwriter had to
// go rather than be configured. It measures cells in RUNES, so a CJK summary
// (two columns per rune) shifted every column after it; everything here measures
// with runewidth, which is the same yardstick the terminal uses. And a column
// that no longer fits must GO rather than be squeezed, which tabwriter has no
// concept of.
//
// Columns shed in a fixed order — SOURCE, LAST SEEN, PROJECT, STATUS, COUNT —
// each one whole, so what remains is always a true table rather than a smeared
// one. ID, CODE and SUMMARY never shed: they are the listing.
func listingLines(incidents []faults.Incident, wide, includeCleared bool, width int) (lines []string) {
	var cols []listColumn
	var row []string

	if len(incidents) == 0 {
		goto end
	}

	cols = listingColumns(incidents, wide, includeCleared)
	for i := range cols {
		cols[i].width = cols[i].natural()
	}
	cols = fitColumns(cols, width)

	lines = make([]string, 0, len(incidents)+1)
	row = make([]string, len(cols))

	for i := range cols {
		row[i] = pad(cols[i].head, cols[i].width)
	}
	lines = append(lines, clamp(strings.Join(row, gap), width))

	for r := range incidents {
		for i := range cols {
			row[i] = pad(cols[i].cells[r], cols[i].width)
		}
		lines = append(lines, clamp(strings.Join(row, gap), width))
	}

end:
	return lines
}

// clamp trims a row's trailing padding and, as a last resort, its content.
//
// The content cut is the backstop for widths no table can survive: ID, CODE and
// SUMMARY never shed, so below roughly twenty columns their headings alone
// overrun the line. Wrapping there would be worse than cutting — a wrapped row
// on a diagnostics listing is the defect this whole pass removes, while a cut
// one is at least still one row per incident.
//
// width 0 means unbounded and only the trailing padding goes.
func clamp(line string, width int) (out string) {
	out = strings.TrimRight(line, " ")
	if width > 0 {
		out = runewidth.Truncate(out, width-1, "")
	}
	return out
}

// natural is the width a column needs to render every cell whole.
func (c listColumn) natural() (w int) {
	w = runewidth.StringWidth(c.head)
	for _, cell := range c.cells {
		if cw := runewidth.StringWidth(cell); cw > w {
			w = cw
		}
	}
	return w
}

// fitColumns sheds columns and narrows the summary until the row fits width.
// A width of 0 leaves everything at its natural size.
//
// The summary absorbs the slack in both directions: it is the column with no
// natural size worth respecting (a summary is as long as it is) and the only one
// a reader can lose the tail of and still use the row — `errors show <id>` is
// where the whole one lives, which is exactly why pass 3 had to come first.
func fitColumns(cols []listColumn, width int) []listColumn {
	if width <= 0 {
		return cols
	}

	// cols-1, not cols: a line ending exactly at the right margin sits on the
	// terminal's deferred-wrap boundary, where some emulators emit a phantom
	// second row. One unused column at the edge is invisible; a wrapped table
	// is not.
	budget := width - 1

	for {
		fixed := 0
		for _, c := range cols[:len(cols)-1] {
			fixed += c.width
		}
		left := budget - fixed - len(gap)*(len(cols)-1)

		if left >= minSummary || !shedOne(&cols) {
			cols[len(cols)-1].width = left
			if cols[len(cols)-1].width < 1 {
				cols[len(cols)-1].width = 1
			}
			truncateCells(&cols[len(cols)-1])
			return cols
		}
	}
}

// shedOne drops the most readily shed column, and reports whether it found one.
func shedOne(cols *[]listColumn) (shed bool) {
	victim := -1
	for i, c := range *cols {
		if c.shed == shedNever {
			continue
		}
		if victim < 0 || c.shed > (*cols)[victim].shed {
			victim = i
		}
	}
	if victim < 0 {
		goto end
	}
	*cols = append((*cols)[:victim], (*cols)[victim+1:]...)
	shed = true

end:
	return shed
}

// truncateCells cuts a column's cells — and its heading — down to its width.
func truncateCells(c *listColumn) {
	c.head = runewidth.Truncate(c.head, c.width, "")
	for i, cell := range c.cells {
		c.cells[i] = runewidth.Truncate(cell, c.width, "…")
	}
}

// pad right-pads s to w display columns, measured the way a terminal measures.
func pad(s string, w int) (padded string) {
	padded = s
	if n := w - runewidth.StringWidth(s); n > 0 {
		padded += strings.Repeat(" ", n)
	}
	return padded
}

// statusText says whether a row is still open. Only rendered under --all, which
// is the only mode in which it can be anything but "open".
func statusText(incident faults.Incident) (text string) {
	text = "open"
	if incident.ClearedAt != "" {
		text = "cleared"
	}
	return text
}

// runShow prints ONE incident in full.
//
// The id is positional — `errors show 7`, the spelling every other `show` in
// the CLI uses. `--id 7` stays accepted but undocumented, because it is what
// this surface took for two years and a scripted caller should not break on a
// rename; it is deliberately absent from the usage text so nobody learns it new.
//
// No id at all is a usage error naming `list`. It used to print the listing,
// which is exactly the confusion E-2148 removed — a verb that means "one item,
// in detail" everywhere else must not quietly mean "all of them" here.
//
// The scope is IGNORED, as it is for `clear <id>` and as `--id` always was: an
// id is an exact selector the user typed, and refusing to show a row because it
// belongs to another project would make `show 7` fail immediately after a
// listing displayed row 7.
func runShow(args []string) {
	var id int64

	fs := flag.NewFlagSet("show", flag.ExitOnError)
	detail := fs.Bool("detail", false, "print each occurrence's full captured detail")
	idFlag := fs.Int64("id", 0, "the error to show (positional `<id>` is the documented spelling)")
	fs.Usage = func() { showUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	id = *idFlag
	if fs.NArg() > 0 {
		parsed, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "endless-go errors: show: %q is not an error id\n", fs.Arg(0))
			os.Exit(2)
		}
		id = parsed
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr,
			"endless-go errors: show: one id at a time; `errors list` shows them together")
		os.Exit(2)
	}
	if id <= 0 {
		showUsage(os.Stderr)
		os.Exit(2)
	}

	showOne(id, *detail)
}

// showUsage is `show`'s own usage, which names `list` because reaching it
// almost always means the caller wanted the listing.
//
// It deliberately does not mention --id: that alias exists for callers who
// already type it, not for anyone learning the command today.
func showUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go errors show <id> [--detail]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Prints ONE error in full — the whole summary, its remedy, and where it")
	fmt.Fprintln(w, "came from. --detail adds every logged occurrence's full capture.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "To see which errors exist, list them:")
	fmt.Fprintln(w, "  endless errors list")
}

// scoping is a resolved scope together with what a surface must SAY about it.
//
// The scope alone cannot be described honestly, because faults.AllProjects is
// two different situations wearing one value: "cover the machine, I asked for
// it" and "cover the machine, because nothing here narrows it". A listing that
// cannot tell those apart cannot explain itself, and explaining itself is the
// whole of E-2148's item 3.
type scoping struct {
	scope   faults.ProjectScope
	project string // the project's name when scoped; "" machine-wide
	asked   bool   // machine-wide because --all-projects, not because of a fallback
}

// wide reports whether this covers every project — the condition under which a
// PROJECT column earns its width.
func (s scoping) wide() bool {
	return s.scope == faults.AllProjects
}

// resolveScope settles which project a `list` or `clear` covers.
//
// The precedence is explicit-beats-ambient and the two explicit flags are
// mutually exclusive: naming a project and asking for all of them are opposite
// instructions, and picking one silently would dismiss or hide rows the user did
// not mean. An unknown --project name is a usage error for the same reason — a
// typo must not degrade into "the whole machine", which for `clear` would
// dismiss every open incident on it.
//
// The ambient case fails OPEN, to the whole machine: run outside any registered
// project there is no project to scope to, and refusing would make the fault
// record unreadable from exactly the directories where something unexplained is
// most likely happening. The listing's header says so in as many words, rather
// than leaving the PROJECT column's appearance to be decoded.
func resolveScope(verb, project string, allProjects bool) (s scoping) {
	if project != "" && allProjects {
		fmt.Fprintf(os.Stderr,
			"endless-go errors: %s: --project and --all-projects are opposites; pass one\n", verb)
		os.Exit(2)
	}
	if allProjects {
		return scoping{scope: faults.AllProjects, asked: true}
	}
	if project != "" {
		id, name, err := monitor.ProjectByName(project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "endless-go errors: %s: %v\n", verb, err)
			os.Exit(2)
		}
		return scoping{scope: faults.ProjectScope(id), project: name}
	}
	id, name, err := monitor.ProjectForCwd()
	if err != nil {
		return scoping{scope: faults.AllProjects}
	}
	return scoping{scope: faults.ProjectScope(id), project: name}
}

// listingHeader is the line above the table (or instead of it) saying WHAT was
// counted and WHERE — and, on a scoped listing, how many incidents are open
// outside it.
//
// This exists because of a flat contradiction (E-2148 item 3). `errors show`
// printed "no errors" from inside the endless project while the session-status
// fault row simultaneously said `1 error, 1 warning`, because both incidents
// belonged to a different project. Neither surface was lying; the listing meant
// "none here" and said "none", and two surfaces disagreeing about whether
// anything is wrong is worse than either answer alone.
//
// The fix is NOT to widen the listing — a listing stays scoped to the project
// you are standing in, because "generally when I am in a project I don't want
// other project's concerns leaking in". The fix is to stop a scoped listing
// saying "no errors" when it means "none here", and to hand over the command
// that shows the rest.
//
// elsewhere is counted with a second read rather than inferred, and it is the
// machine-wide total minus this scope's: exactly the incidents belonging to
// OTHER projects, since the unattributed ones are already inside every scope.
// A read that fails contributes nothing and is not reported — a header is an
// annotation, and a diagnostics surface must not fail over its own annotation.
func listingHeader(s scoping, shown int, includeCleared bool) (header string) {
	var elsewhere int

	noun := "errors"
	if shown == 1 {
		noun = "error"
	}
	count := strconv.Itoa(shown)
	if shown == 0 {
		count, noun = "no", "errors"
	}

	if s.wide() {
		header = count + " " + noun + " across every project"
		if !s.asked {
			// The fallback, named rather than implied. Before E-2148 the only
			// hint that a listing had widened was the PROJECT column appearing,
			// which says nothing at all to a reader who has not seen the other
			// shape.
			header += " (no project encloses this directory)"
		}
		goto end
	}

	header = count + " " + noun + " in " + s.project

	elsewhere = countElsewhere(s, shown, includeCleared)
	if elsewhere > 0 {
		header += " — " + strconv.Itoa(elsewhere) +
			" elsewhere (endless errors list --all-projects)"
	}

end:
	if includeCleared {
		header += " [including cleared]"
	}
	return header
}

// countElsewhere returns how many incidents are open outside this scope: the
// machine-wide count less the count already shown. Zero on any read failure,
// and zero on a machine-wide scope, where there is no "elsewhere".
func countElsewhere(s scoping, shown int, includeCleared bool) (n int) {
	var all []faults.Incident
	var err error

	if s.wide() {
		goto end
	}
	all, err = faults.List(faults.AllProjects, includeCleared, 0)
	if err != nil {
		goto end
	}
	n = len(all) - shown
	if n < 0 {
		// Two reads, one racing writer. Reporting a negative count would be
		// worse than reporting none.
		n = 0
	}

end:
	return n
}

// projectText renders an incident's project for the wide listing. An
// unattributed incident prints an em dash rather than an empty cell, so a
// machine-level fault reads as "belongs to no project" instead of as a column
// the renderer forgot to fill.
func projectText(incident faults.Incident) (text string) {
	text = incident.Project
	if text == "" {
		text = "—"
	}
	return text
}

// printClearHint names the command that makes these rows go away.
//
// The fault row and this listing were the only surfaces a user ever saw, and neither
// mentioned `clear` — so the one action available on a fault that had already
// self-healed was undiscoverable (E-1950). Nothing ages off the fault row
// (E-2151), which makes this hint the whole exit: an incident stays on it until someone
// runs the command named here. Suppressed when nothing here is still open, since
// clearing a cleared incident does nothing.
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
	fmt.Println("`clear` covers the same project scope the listing above did; pass the same")
	fmt.Println("--project/--all-projects flag to widen or narrow both together.")
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
	fmt.Printf("Project:     %s\n", projectText(incident))
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
//
// The scope bounds the no-id form only — see faults.Clear. `clear` with no ids
// means "dismiss what you just showed me", so it must cover the same set `show`
// listed under the same flags, and nothing beyond it: a project-scoped listing
// followed by a machine-wide clear would silently acknowledge other projects'
// incidents on their owners' behalf.
func runClear(args []string) {
	var ids []int64

	fs := flag.NewFlagSet("clear", flag.ExitOnError)
	project := fs.String("project", "", "scope to this project instead of the one you are in")
	allProjects := fs.Bool("all-projects", false, "cover every project on the machine")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	for _, arg := range fs.Args() {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "endless-go errors: clear: %q is not an error id\n", arg)
			os.Exit(2)
		}
		ids = append(ids, id)
	}

	cleared, err := faults.Clear(resolveScope("clear", *project, *allProjects).scope, ids, clearedBy())
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
// `session monitor` fault row). Distinct from `raise`, which only ever emits the two
// synthetic test codes and says so in its detail text.
//
// --code must name a catalog entry, so this cannot invent classifications that
// have no docs/errors.md section; an unknown ID is a usage error.
func runRecord(args []string) {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	codeID := fs.String("code", "", "catalog code ID, e.g. ERR-0008 or WARN-0009")
	summary := fs.String("summary", "", "short summary shown in lists and the fault row")
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
