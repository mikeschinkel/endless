package projectstatuscmd

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// now is the fixed clock every test in this file measures ages against. A test
// that read the wall clock would age its own fixtures between runs.
var now = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func ts(d time.Duration) string {
	return now.Add(-d).Format("2006-01-02T15:04:05")
}

func taskRow(id int64, status string, age time.Duration) monitor.ProjectStatusRow {
	return monitor.ProjectStatusRow{
		ProjectID: 1, TaskID: id, Title: "task " + status, Status: status,
		Phase: "now", TypeSlug: "todo", TaskUpdated: ts(age),
	}
}

func sessionRow(sid int64, state string, age time.Duration, taskID int64) monitor.ProjectStatusRow {
	r := monitor.ProjectStatusRow{
		ProjectID: 1, SessionID: sid, SessionState: state, SessionActivity: ts(age),
	}
	if taskID != 0 {
		r.TaskID = taskID
		r.Title = "held task"
		r.Status = "underway"
		r.Phase = "now"
		r.TypeSlug = "todo"
		r.TaskUpdated = ts(age)
	}
	return r
}

// TestActionGlyphsAreSingleWidth pins the property the fixed-width prefix rests
// on. A two-column glyph shifts every following column on that row only, which
// reads as a corrupt table rather than as a bad glyph choice — so the check
// belongs here rather than in the eye of whoever adds the next rank.
func TestActionGlyphsAreSingleWidth(t *testing.T) {
	for _, a := range actions() {
		if w := runewidth.StringWidth(a.icon()); w != 1 {
			t.Errorf("action %q glyph %q measures %d columns, want 1",
				a.label(), a.icon(), w)
		}
	}
}

// TestActionMetaIsComplete guards the one failure mode a fixed-size array has:
// a rank added to the enum without a row in the table renders as an empty glyph
// and an empty label, silently.
func TestActionMetaIsComplete(t *testing.T) {
	for _, a := range actions() {
		if a.icon() == "" || a.label() == "" || a.noun() == "" {
			t.Errorf("action %d has an incomplete actionMeta row: icon=%q label=%q noun=%q",
				int(a), a.icon(), a.label(), a.noun())
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		row  monitor.ProjectStatusRow
		want action
	}{
		{"unverified task", taskRow(1, "unverified", time.Hour), actVerify},
		{"unreviewed task", taskRow(2, "unreviewed", time.Hour), actRead},
		{"submitted task", taskRow(3, "submitted", time.Hour), actReview},
		{"underway task with no session is an orphan", taskRow(4, "underway", time.Hour), actOrphan},
		{"ready task", taskRow(5, "ready", time.Hour), actReady},
		{"working session", sessionRow(10, "working", time.Minute, 0), actDoing},
		{"idle session", sessionRow(11, "idle", time.Minute, 0), actIdle},

		// The rank E-2091 will supply a producer for. classify() already routes
		// the state, so when the Notification hook starts writing it the board
		// needs no change here — only the SQL filter (monitor.boardSessionStates)
		// has to widen.
		{"needs_input session is the waiting rank", sessionRow(12, "needs_input", time.Minute, 0), actWaiting},

		// The session half wins over the task half. An `underway` task held by a
		// live session is that session working, NOT an orphan — the whole point
		// of the orphan rank is that nobody is holding the task.
		{"underway task held by a live session is not an orphan",
			sessionRow(13, "working", time.Minute, 4), actDoing},

		{"unknown status", taskRow(6, "no-such-status", time.Hour), actUnknown},
		{"unknown session state", sessionRow(14, "no-such-state", time.Minute, 0), actUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.row); got != tt.want {
				t.Fatalf("classify = %q, want %q", got.label(), tt.want.label())
			}
		})
	}
}

// TestRankOrderIsDeclarationOrder pins that the enum order IS the board order,
// which is the property that lets a new rank be added by declaring it in the
// right place and nothing else.
func TestRankOrderIsDeclarationOrder(t *testing.T) {
	want := []string{"waiting", "verify", "read", "review", "orphan", "idle", "doing", "ready", "unknown"}
	var got []string
	for _, a := range actions() {
		got = append(got, a.label())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rank order = %v, want %v", got, want)
	}
}

// TestGroupsRenderInRankOrder is the same property observed through the render,
// where it actually matters: unverified above idle above working, whatever order
// the query returned.
func TestGroupsRenderInRankOrder(t *testing.T) {
	rows := []monitor.ProjectStatusRow{
		sessionRow(10, "working", time.Minute, 0),
		taskRow(1, "unverified", time.Hour),
		sessionRow(11, "idle", time.Minute, 0),
		taskRow(2, "submitted", time.Hour),
	}
	var b strings.Builder
	render(&b, "demo", rows, 10, 0, 120, false, now, faults.AllProjects)

	order := []string{"☑", "⚑", "‖", "⟳"}
	at := 0
	for _, line := range strings.Split(b.String(), "\n") {
		if at < len(order) && strings.HasPrefix(line, order[at]) {
			at++
		}
	}
	if at != len(order) {
		t.Fatalf("groups did not render in rank order (matched %d of %d):\n%s",
			at, len(order), b.String())
	}
}

// TestSortDirectionPerGroup pins the split the actionMeta table encodes: every
// group reads newest-first, because the cap makes the top of a group the only
// part most people see — EXCEPT the waiting queue, where longest-blocked-first
// is the fair discipline.
func TestSortDirectionPerGroup(t *testing.T) {
	verify := []monitor.ProjectStatusRow{
		taskRow(1, "unverified", 80*24*time.Hour),
		taskRow(2, "unverified", time.Minute),
	}
	sortGroup(actVerify, verify, now)
	if verify[0].TaskID != 2 {
		t.Errorf("verify sorted oldest-first; the 80-day sediment took the top row")
	}

	idle := []monitor.ProjectStatusRow{
		sessionRow(1, "idle", 15*24*time.Hour, 0),
		sessionRow(2, "idle", time.Minute, 0),
	}
	sortGroup(actIdle, idle, now)
	if idle[0].SessionID != 2 {
		t.Errorf("idle sorted oldest-first; the session that just handed work back was buried")
	}

	waiting := []monitor.ProjectStatusRow{
		sessionRow(1, "needs_input", time.Minute, 0),
		sessionRow(2, "needs_input", time.Hour, 0),
	}
	sortGroup(actWaiting, waiting, now)
	if waiting[0].SessionID != 2 {
		t.Errorf("waiting did not put the longest-blocked session first")
	}
}

// TestSortSinksUnreadableClocks pins that a row whose timestamp cannot be parsed
// does not take the top of an oldest-first group by virtue of reading as the
// zero time.
func TestSortSinksUnreadableClocks(t *testing.T) {
	rows := []monitor.ProjectStatusRow{
		{SessionID: 1, SessionState: "needs_input", SessionActivity: "not a timestamp"},
		{SessionID: 2, SessionState: "needs_input", SessionActivity: ts(time.Hour)},
	}
	sortGroup(actWaiting, rows, now)
	if rows[0].SessionID != 2 {
		t.Fatalf("an unreadable clock sorted above a real one")
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{-time.Hour, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m"},
		{59 * time.Minute, "59m"},
		{3 * time.Hour, "3h"},
		{47 * time.Hour, "47h"},
		{50 * time.Hour, "2d"},
		{80 * 24 * time.Hour, "80d"},
		{5000 * 24 * time.Hour, "999d"},
	}
	for _, tt := range tests {
		got := formatAge(tt.d)
		if got != tt.want {
			t.Errorf("formatAge(%v) = %q, want %q", tt.d, got, tt.want)
		}
		if w := runewidth.StringWidth(got); w > ageWidth {
			t.Errorf("formatAge(%v) = %q, %d columns — wider than the %d-column age field",
				tt.d, got, w, ageWidth)
		}
	}
}

func TestParseTSAcceptsBothStoredSpellings(t *testing.T) {
	iso := parseTS("2026-08-30T11:00:00")
	sqlite := parseTS("2026-08-30 11:00:00")
	if iso.IsZero() || !iso.Equal(sqlite) {
		t.Fatalf("the two stored timestamp spellings did not parse alike: %v vs %v", iso, sqlite)
	}
	if !parseTS("nonsense").IsZero() {
		t.Errorf("an unparseable timestamp did not yield the zero time")
	}
}

// TestCapIsPerGroup is the defect the per-group cap exists to prevent: a
// board-wide cap with 40 unverified tasks ranked near the top spends every row
// on them and the sessions — what the board was built to triage — never render.
func TestCapIsPerGroup(t *testing.T) {
	var rows []monitor.ProjectStatusRow
	for i := int64(1); i <= 40; i++ {
		rows = append(rows, taskRow(i, "unverified", time.Duration(i)*time.Hour))
	}
	rows = append(rows, sessionRow(99, "working", time.Minute, 0))

	var b strings.Builder
	render(&b, "demo", rows, 3, 0, 120, false, now, faults.AllProjects)
	out := b.String()

	if n := strings.Count(out, "☑ "); n != 4 { // 3 rows + the legend entry
		t.Errorf("verify rendered %d lines under --limit 3, want 3 rows + 1 legend entry", n)
	}
	if !strings.Contains(out, "⟳ T E-") && !strings.Contains(out, "⟳  ") {
		t.Errorf("the working session was crowded off the board by the unverified backlog:\n%s", out)
	}
	if !strings.Contains(out, "… 37 more unverified (--no-limit)") {
		t.Errorf("the truncated group did not name what it dropped:\n%s", out)
	}
}

// TestBudgetKeepsTheFrameInsideThePane is the property a ranked board cannot do
// without. An overrun frame does not wrap, it SCROLLS, and a scrolled frame
// loses its top — which on this board is the loudest rows. Sweeping the budget
// rather than testing one value: the failure is a boundary, and boundaries are
// where an off-by-one lives.
func TestBudgetKeepsTheFrameInsideThePane(t *testing.T) {
	var rows []monitor.ProjectStatusRow
	for i := int64(1); i <= 40; i++ {
		rows = append(rows, taskRow(i, "unverified", time.Duration(i)*time.Hour))
		rows = append(rows, taskRow(1000+i, "submitted", time.Duration(i)*time.Hour))
		rows = append(rows, sessionRow(2000+i, "idle", time.Duration(i)*time.Minute, 0))
	}

	for budget := 5; budget <= 60; budget++ {
		var b strings.Builder
		render(&b, "demo", rows, 10, budget, 120, false, now, faults.AllProjects)
		if lines := strings.Count(b.String(), "\n"); lines > budget {
			t.Fatalf("budget %d produced a %d-line frame:\n%s", budget, lines, b.String())
		}
	}
}

// TestBudgetKeepsEveryGroupPresent is the other half: the frame must fit, but it
// must not fit by dropping whole ranks. A board that shows only unverified rows
// has stopped answering the question it was built for.
func TestBudgetKeepsEveryGroupPresent(t *testing.T) {
	var rows []monitor.ProjectStatusRow
	for i := int64(1); i <= 40; i++ {
		rows = append(rows, taskRow(i, "unverified", time.Duration(i)*time.Hour))
		rows = append(rows, taskRow(1000+i, "submitted", time.Duration(i)*time.Hour))
		rows = append(rows, sessionRow(2000+i, "idle", time.Duration(i)*time.Minute, 0))
		rows = append(rows, sessionRow(3000+i, "working", time.Duration(i)*time.Minute, 0))
	}

	var b strings.Builder
	render(&b, "demo", rows, 10, 20, 120, false, now, faults.AllProjects)
	out := b.String()
	for _, glyph := range []string{"☑", "⚑", "‖", "⟳"} {
		if !strings.Contains(out, glyph) {
			t.Errorf("group %q vanished from a 20-row board:\n%s", glyph, out)
		}
	}
}

// TestFairShareGivesSmallGroupsWhole pins the allocation's shape: a group that
// wants less than its share renders whole and releases the rest, so the large
// groups split what is left rather than everyone being trimmed alike.
func TestFairShareGivesSmallGroupsWhole(t *testing.T) {
	give := fairShare([]int{1, 2, 30, 30}, []int{1, 2, 30, 30}, 20)
	if give[0] != 1 || give[1] != 2 {
		t.Errorf("small groups were trimmed: got %v, want the first two whole", give)
	}
	total := 0
	for i, n := range give {
		total += n
		if n < []int{1, 2, 30, 30}[i] {
			total++ // the footer a truncated group pays for
		}
	}
	if total > 20 {
		t.Errorf("fairShare over-allocated: %v costs %d lines of a 20-line budget", give, total)
	}
}

// TestFairShareSurvivesAnImpossibleBudget: a budget too small for even one row
// per group must not panic or produce negative counts. Every group then renders
// as its footer alone, which still tells the truth.
func TestFairShareSurvivesAnImpossibleBudget(t *testing.T) {
	for _, budget := range []int{-5, 0, 1, 2} {
		give := fairShare([]int{10, 10, 10, 10, 10}, []int{40, 40, 40, 40, 40}, budget)
		for i, n := range give {
			if n < 0 {
				t.Fatalf("budget %d gave group %d a negative row count: %v", budget, i, give)
			}
		}
	}
}

// TestEmptyGroupStillFooters: a group squeezed to zero rows by the budget must
// still say it exists. A rank that vanishes silently is the defect the whole
// footer idiom exists to prevent.
func TestEmptyGroupStillFooters(t *testing.T) {
	var rows []monitor.ProjectStatusRow
	for i := int64(1); i <= 20; i++ {
		rows = append(rows, taskRow(i, "unverified", time.Duration(i)*time.Hour))
		rows = append(rows, sessionRow(1000+i, "idle", time.Duration(i)*time.Minute, 0))
	}
	var b strings.Builder
	render(&b, "demo", rows, 10, 4, 120, false, now, faults.AllProjects)
	out := b.String()
	if !strings.Contains(out, "more unverified") || !strings.Contains(out, "more idle") {
		t.Fatalf("a group squeezed to nothing did not footer:\n%s", out)
	}
}

func TestLegendCarriesOnlyPresentGlyphs(t *testing.T) {
	rows := []monitor.ProjectStatusRow{taskRow(1, "unverified", time.Hour)}
	var b strings.Builder
	render(&b, "demo", rows, 10, 0, 120, false, now, faults.AllProjects)
	legendLine := strings.SplitN(b.String(), "\n", 2)[0]

	if !strings.HasPrefix(legendLine, "demo · ") {
		t.Errorf("legend does not name the project: %q", legendLine)
	}
	if !strings.Contains(legendLine, "☑ verify") {
		t.Errorf("legend omits a present glyph: %q", legendLine)
	}
	for _, absent := range []string{"⚑", "‖", "⟳", "◷", "▶", "⚠", "☰"} {
		if strings.Contains(legendLine, absent) {
			t.Errorf("legend names %q, which no row carries: %q", absent, legendLine)
		}
	}
}

func TestEmptyBoard(t *testing.T) {
	var b strings.Builder
	n := render(&b, "demo", nil, 10, 0, 120, false, now, faults.AllProjects)
	if n != 0 {
		t.Errorf("empty board reported %d rows, want 0 (the pane fit treats 0 specially)", n)
	}
	if !strings.Contains(b.String(), "nothing needs attention in demo") {
		t.Errorf("empty board did not name the project it found nothing in:\n%s", b.String())
	}
}

// TestSessionColumnIsWidthOnDemand: a board of pure task rows must not be padded
// with an empty session gutter, and one with sessions must align every row to
// the same column.
func TestSessionColumnIsWidthOnDemand(t *testing.T) {
	var tasksOnly strings.Builder
	render(&tasksOnly, "demo", []monitor.ProjectStatusRow{
		taskRow(1, "unverified", time.Hour),
	}, 10, 0, 120, false, now, faults.AllProjects)
	row := strings.Split(tasksOnly.String(), "\n")[1]
	if strings.Contains(row, "  3d") && strings.Contains(row, "     3d") {
		t.Errorf("task-only board padded a session column: %q", row)
	}

	var mixed strings.Builder
	render(&mixed, "demo", []monitor.ProjectStatusRow{
		taskRow(1, "unverified", time.Hour),
		sessionRow(1234, "idle", time.Minute, 0),
	}, 10, 0, 120, false, now, faults.AllProjects)
	if !strings.Contains(mixed.String(), "ES-1234") {
		t.Errorf("session column did not appear when a row had a session:\n%s", mixed.String())
	}
}

// TestRowsNeverExceedTheWidth: every rendered ROW must fit `cols`, at every
// width. A wrapped row costs the whole table's alignment, not just its own.
//
// The legend is exempt, deliberately and on the same rule sessionstatuscmd
// states: its glyphs are never truncated, because a legend missing a glyph that
// IS on screen is worse than a legend that soft-wraps. Its wrap is not a silent
// overrun either — nonRowLines measures it and charges the budget for it.
func TestRowsNeverExceedTheWidth(t *testing.T) {
	rows := []monitor.ProjectStatusRow{
		taskRow(1, "unverified", time.Hour),
		sessionRow(1234, "idle", time.Minute, 4321),
		sessionRow(5, "working", time.Minute, 0),
	}
	rows[0].Title = strings.Repeat("a very long title ", 20)

	for cols := 20; cols <= 200; cols += 7 {
		var b strings.Builder
		render(&b, "demo", rows, 10, 0, cols, false, now, faults.AllProjects)
		for i, line := range strings.Split(b.String(), "\n") {
			if line == "" || i == 0 { // i == 0 is the legend; see above
				continue
			}
			if w := runewidth.StringWidth(line); w > cols {
				t.Fatalf("at cols=%d a line measured %d columns: %q", cols, w, line)
			}
		}
	}
}

// TestColorizeIsIntensityOnly pins the theme-independence rule: the board emits
// bold and dim, never a color from the 30-47 range a terminal theme remaps.
func TestColorizeIsIntensityOnly(t *testing.T) {
	for _, a := range actions() {
		got := colorize("row", a, true)
		if strings.Contains(got, "\x1b[3") || strings.Contains(got, "\x1b[4") {
			t.Errorf("action %q emitted a theme-remapped color: %q", a.label(), got)
		}
		if colorize("row", a, false) != "row" {
			t.Errorf("action %q emitted escapes with color disabled", a.label())
		}
	}
	if !strings.Contains(colorize("row", actWaiting, true), "\x1b[1m") {
		t.Errorf("the waiting rank is not bold — the one rank E-1815 calls load-bearing")
	}
}
