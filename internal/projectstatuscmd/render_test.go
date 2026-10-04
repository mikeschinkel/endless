package projectstatuscmd

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/taskrow"
)

// now anchors fixture timestamps. Nothing renders against it — the frame
// carries no clock — it only makes "older" and "newer" fixed.
var now = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func ts(d time.Duration) string {
	return now.Add(-d).Format("2006-01-02T15:04:05")
}

// row is a `now` todo in the given status, last updated age ago.
func row(id int64, status string, age time.Duration) monitor.ProjectStatusRow {
	return monitor.ProjectStatusRow{
		ProjectID: 1, TaskID: id, Title: "task " + status, Status: status,
		Phase: "now", TypeSlug: "todo", TaskUpdated: ts(age),
	}
}

func with(r monitor.ProjectStatusRow, edit func(*monitor.ProjectStatusRow)) monitor.ProjectStatusRow {
	edit(&r)
	return r
}

func phase(p string) func(*monitor.ProjectStatusRow) {
	return func(r *monitor.ProjectStatusRow) { r.Phase = p }
}

func epic(r *monitor.ProjectStatusRow) { r.TypeSlug = "epic" }

func frame(rows []monitor.ProjectStatusRow, o frameOpts) string {
	if o.cols == 0 {
		o.cols = 120
	}
	if o.sort == "" {
		o.sort = sortUpdated
	}
	var b strings.Builder
	render(&b, "demo", rows, o, faults.ProjectScope(1))
	return b.String()
}

// ids is the task ids in the order a frame drew them.
func ids(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, "E-") {
				got = append(got, f)
				break
			}
		}
	}
	return got
}

// mixed is one task of each list, plus an urgent epic, out of order.
func mixed() []monitor.ProjectStatusRow {
	return []monitor.ProjectStatusRow{
		row(1, "unverified", time.Hour),
		with(row(2, "ready", 2*time.Hour), epic),                            // now epic
		with(row(3, "underway", 3*time.Hour), phase("urgent")),              // urgent task
		with(with(row(4, "submitted", 4*time.Hour), epic), phase("next")),   // next epic
		with(with(row(5, "unplanned", 5*time.Hour), epic), phase("urgent")), // urgent epic
		with(row(6, "submitted", 6*time.Hour), phase("next")),
	}
}

// TestThreeListsInOrder: urgent, then now/next epics, then everything else —
// and every task exactly once, an urgent epic in the urgent list only.
func TestThreeListsInOrder(t *testing.T) {
	got := strings.Join(ids(frame(mixed(), frameOpts{})), " ")
	want := "E-3 E-5 E-2 E-4 E-1 E-6"
	if got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}

func TestListOf(t *testing.T) {
	cases := []struct {
		r    monitor.ProjectStatusRow
		want list
	}{
		{with(row(1, "ready", 0), phase("urgent")), listUrgent},
		{with(with(row(1, "ready", 0), epic), phase("urgent")), listUrgent},
		{with(row(1, "ready", 0), epic), listEpics},
		{row(1, "ready", 0), listOther},
	}
	for _, c := range cases {
		if got := listOf(c.r); got != c.want {
			t.Errorf("listOf(%+v) = %d, want %d", c.r, got, c.want)
		}
	}
}

// many is n rows built by mk, with ids from base.
func many(n int, base int64, mk func(int64) monitor.ProjectStatusRow) []monitor.ProjectStatusRow {
	var out []monitor.ProjectStatusRow
	for i := 0; i < n; i++ {
		out = append(out, mk(base+int64(i)))
	}
	return out
}

func overflow() []monitor.ProjectStatusRow {
	rows := many(8, 100, func(id int64) monitor.ProjectStatusRow {
		return with(row(id, "underway", time.Duration(id)*time.Minute), phase("urgent"))
	})
	rows = append(rows, many(8, 200, func(id int64) monitor.ProjectStatusRow {
		return with(row(id, "ready", time.Duration(id)*time.Minute), epic)
	})...)
	return append(rows, many(30, 300, func(id int64) monitor.ProjectStatusRow {
		return row(id, "unplanned", time.Duration(id)*time.Minute)
	})...)
}

func countPrefix(out, prefix string) int {
	n := 0
	for _, id := range ids(out) {
		if strings.HasPrefix(id, prefix) {
			n++
		}
	}
	return n
}

// TestUrgentAndEpicsNeverTruncate: a budget far smaller than lists 1 and 2
// still renders every row of both.
func TestUrgentAndEpicsNeverTruncate(t *testing.T) {
	out := frame(overflow(), frameOpts{budget: 5, truncate: true})
	if n := countPrefix(out, "E-1"); n != 8 {
		t.Errorf("urgent rows drawn = %d, want all 8:\n%s", n, out)
	}
	if n := countPrefix(out, "E-2"); n != 8 {
		t.Errorf("epic rows drawn = %d, want all 8:\n%s", n, out)
	}
	if n := countPrefix(out, "E-3"); n != 0 {
		t.Errorf("list 3 drew %d rows with no room left", n)
	}
	if !strings.Contains(out, footerFor(30)) {
		t.Errorf("list 3 was cut without a trace:\n%s", out)
	}
}

// TestOnlyListThreeGrowsWithHeight: more height buys list-3 rows and nothing
// else, and the frame fits the budget it was given.
func TestOnlyListThreeGrowsWithHeight(t *testing.T) {
	small := frame(overflow(), frameOpts{budget: 25, truncate: true})
	large := frame(overflow(), frameOpts{budget: 35, truncate: true})
	if countPrefix(large, "E-3") <= countPrefix(small, "E-3") {
		t.Errorf("list 3 did not grow: %d at 25 rows, %d at 35",
			countPrefix(small, "E-3"), countPrefix(large, "E-3"))
	}
	for _, p := range []string{"E-1", "E-2"} {
		if countPrefix(small, p) != countPrefix(large, p) {
			t.Errorf("%sxx rows changed with height", p)
		}
	}
	for _, c := range []struct {
		out    string
		budget int
	}{{small, 25}, {large, 35}} {
		// With no fault open nothing is reserved for one, so the frame fills
		// the budget exactly.
		if lines := strings.Count(c.out, "\n"); lines != c.budget {
			t.Errorf("frame is %d lines, budget %d:\n%s", lines, c.budget, c.out)
		}
	}
}

// TestStatusTruncatesNothing: without truncate (`project status`) no budget cuts
// anything.
func TestStatusTruncatesNothing(t *testing.T) {
	out := frame(overflow(), frameOpts{budget: 5, truncate: false})
	if n := len(ids(out)); n != 46 {
		t.Errorf("rows drawn = %d, want all 46", n)
	}
	if strings.Contains(out, "more (project status)") {
		t.Errorf("`project status` printed a truncation footer:\n%s", out)
	}
}

// TestSortKeys: an old task updated recently leads under `updated` and trails
// under `id`.
func TestSortKeys(t *testing.T) {
	rows := []monitor.ProjectStatusRow{
		row(10, "ready", time.Minute), // filed first, touched last
		row(20, "ready", time.Hour),
		row(30, "ready", 2*time.Hour),
	}
	if got := strings.Join(ids(frame(rows, frameOpts{sort: sortUpdated})), " "); got != "E-10 E-20 E-30" {
		t.Errorf("--sort updated = %s", got)
	}
	if got := strings.Join(ids(frame(rows, frameOpts{sort: sortID})), " "); got != "E-30 E-20 E-10" {
		t.Errorf("--sort id = %s", got)
	}
}

func TestSortSinksUnreadableClocks(t *testing.T) {
	rows := []monitor.ProjectStatusRow{
		with(row(1, "ready", 0), func(r *monitor.ProjectStatusRow) { r.TaskUpdated = "" }),
		row(2, "ready", 48*time.Hour),
	}
	sortRows(rows, sortUpdated)
	if rows[0].TaskID != 2 {
		t.Errorf("a row with no readable clock sorted first")
	}
}

func TestParseTSAcceptsBothStoredSpellings(t *testing.T) {
	a, aok := parseTS("2026-08-30T10:00:00")
	b, bok := parseTS("2026-08-30 10:00:00")
	if !aok || !bok || !a.Equal(b) {
		t.Errorf("the two spellings disagree: %v/%v %v/%v", a, aok, b, bok)
	}
}

// TestClassifyLiveSession: a live session changes an underway row only.
func TestClassifyLiveSession(t *testing.T) {
	live := func(r *monitor.ProjectStatusRow) { r.LiveSession = true }
	prompted := func(r *monitor.ProjectStatusRow) { r.LiveSession, r.Prompted = true, true }
	cases := []struct {
		r    monitor.ProjectStatusRow
		want taskrow.Action
	}{
		{row(1, "underway", 0), taskrow.Orphan},
		{with(row(1, "underway", 0), live), taskrow.Doing},
		{with(row(1, "underway", 0), prompted), taskrow.Waiting},
		{with(row(1, "unverified", 0), live), taskrow.Verify},
		{with(row(1, "unreviewed", 0), prompted), taskrow.Read},
		{row(1, "submitted", 0), taskrow.Review},
		{row(1, "ready", 0), taskrow.Do},
		{row(1, "unplanned", 0), taskrow.Plan},
	}
	for _, c := range cases {
		if got := classify(c.r); got != c.want {
			t.Errorf("%s live=%v prompted=%v → %s, want %s",
				c.r.Status, c.r.LiveSession, c.r.Prompted, got.Label(), c.want.Label())
		}
	}
}

// TestLiveSessionGlyphs: the same task renders ⟳ held, ◷ stalled.
func TestLiveSessionGlyphs(t *testing.T) {
	held := frame([]monitor.ProjectStatusRow{with(row(7, "underway", 0),
		func(r *monitor.ProjectStatusRow) { r.LiveSession = true })}, frameOpts{})
	stalled := frame([]monitor.ProjectStatusRow{row(7, "underway", 0)}, frameOpts{})
	if !strings.Contains(held, "⟳ T E-7") {
		t.Errorf("held task is not ⟳:\n%s", held)
	}
	if !strings.Contains(stalled, "◷ T E-7") {
		t.Errorf("stalled task is not ◷:\n%s", stalled)
	}
}

// TestNoSessionInAnyRow: no session id, and none of the session-relative
// glyphs, on any frame.
func TestNoSessionInAnyRow(t *testing.T) {
	rows := append(mixed(), with(row(9, "underway", 0), func(r *monitor.ProjectStatusRow) {
		r.LiveSession, r.Prompted = true, true
	}))
	out := frame(rows, frameOpts{})
	for _, bad := range []string{"ES-", "●", "↑", "↩"} {
		if strings.Contains(out, bad) {
			t.Errorf("frame carries %q:\n%s", bad, out)
		}
	}
}

// TestFrameHasNoClock: the frame is a pure function of the row set, so a
// render a second later is byte-identical.
func TestFrameHasNoClock(t *testing.T) {
	a := frame(mixed(), frameOpts{budget: 6, truncate: true})
	time.Sleep(1100 * time.Millisecond)
	b := frame(mixed(), frameOpts{budget: 6, truncate: true})
	if a != b {
		t.Errorf("two renders of one row set differ:\n%s\n---\n%s", a, b)
	}
}

func TestRowShape(t *testing.T) {
	out := frame([]monitor.ProjectStatusRow{with(row(2156, "unverified", 0), phase("urgent"))}, frameOpts{})
	if !strings.Contains(out, "☑ T E-2156 ! task unverified") {
		t.Errorf("row is not glyph, type, id, phase, title:\n%s", out)
	}
}

func TestLegendCarriesOnlyPresentGlyphs(t *testing.T) {
	out := frame([]monitor.ProjectStatusRow{row(1, "unverified", 0)}, frameOpts{})
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(first, "demo · ") || !strings.Contains(first, "☑ verify") {
		t.Errorf("legend = %q", first)
	}
	if strings.Contains(first, "orphan") {
		t.Errorf("legend names a glyph no row wears: %q", first)
	}
}

func TestEmptyFrame(t *testing.T) {
	var b strings.Builder
	n := render(&b, "demo", nil, frameOpts{cols: 80, emptyPhrase: "later"}, faults.ProjectScope(1))
	if n != 0 || !strings.Contains(b.String(), "no open tasks in later in demo") {
		t.Errorf("empty frame = %q (n=%d)", b.String(), n)
	}
}

func TestRowsNeverExceedTheWidth(t *testing.T) {
	long := with(row(1, "ready", 0), func(r *monitor.ProjectStatusRow) {
		r.Title = strings.Repeat("a very long title ", 20)
	})
	for _, cols := range []int{12, 40, 80} {
		out := frame([]monitor.ProjectStatusRow{long}, frameOpts{cols: cols})
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n")[1:] {
			if w := runewidth.StringWidth(line); w > cols {
				t.Errorf("cols %d: line is %d wide: %q", cols, w, line)
			}
		}
	}
}

// TestListBackgrounds: each list wears its own background, padded to the full
// width, and color off leaves the line untouched.
func TestListBackgrounds(t *testing.T) {
	r := row(1, "ready", 0)
	for l, want := range map[list]string{listUrgent: "48;5;1m", listEpics: "48;5;2m", listOther: "48;5;3m"} {
		got := (*listStyles)(nil).colorize("x", r, l, 10, true)
		if !strings.Contains(got, want) || !strings.Contains(got, "38;5;232m") {
			t.Errorf("list %d: %q lacks %s on 232", l, got, want)
		}
		if !strings.Contains(got, "x"+strings.Repeat(" ", 9)) {
			t.Errorf("list %d: %q is not padded to the width", l, got)
		}
	}
	if got := (*listStyles)(nil).colorize("x", r, listOther, 10, false); got != "x" {
		t.Errorf("color off changed the line: %q", got)
	}
	if got := (*listStyles)(nil).colorize("x", with(r, phase("urgent")), listUrgent, 10, true); !strings.HasPrefix(got, "\x1b[1m") {
		t.Errorf("urgent row is not bold: %q", got)
	}
}
