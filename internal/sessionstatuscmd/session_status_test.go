package sessionstatuscmd

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		row  monitor.SessionStatusRow
		want action
	}{
		{"focal wins over status", monitor.SessionStatusRow{IsFocal: true, Status: "ready"}, actThis},
		{"parent wins over in_flight", monitor.SessionStatusRow{IsParent: true, InFlight: true, Status: "underway"}, actParent},
		{"parent wins over from", monitor.SessionStatusRow{IsParent: true, IsFrom: true, Status: "underway"}, actParent},
		{"from wins over in_flight", monitor.SessionStatusRow{IsFrom: true, InFlight: true, Status: "underway"}, actFrom},
		{"in_flight wins over status", monitor.SessionStatusRow{InFlight: true, Status: "ready"}, actDoing},
		{"ready with no plan still do", monitor.SessionStatusRow{Status: "ready", HasText: false}, actDo},
		{"unplanned is plan", monitor.SessionStatusRow{Status: "unplanned"}, actPlan},
		{"submitted is do not plan", monitor.SessionStatusRow{Status: "submitted"}, actDo},
		{"needs_plan is plan", monitor.SessionStatusRow{Status: "needs_plan"}, actPlan},
		{"revisit folds into plan", monitor.SessionStatusRow{Status: "revisit"}, actPlan},
		{"verify", monitor.SessionStatusRow{Status: "verify"}, actVerify},
		{"unverified is verify", monitor.SessionStatusRow{Status: "unverified"}, actVerify},
		{"underway is orphan", monitor.SessionStatusRow{Status: "underway"}, actOrphan},
		{"unknown is other", monitor.SessionStatusRow{Status: "blocked"}, actOther},
		// E-1693: a landed task routes to actOther regardless of its non-terminal
		// status, so merged work is never offered as a fresh actionable verb.
		{"landed ready is other", monitor.SessionStatusRow{Status: "ready", Landed: true}, actOther},
		{"landed unverified is other", monitor.SessionStatusRow{Status: "unverified", Landed: true}, actOther},
		{"landed unplanned is other", monitor.SessionStatusRow{Status: "unplanned", Landed: true}, actOther},
		{"non-landed ready still do", monitor.SessionStatusRow{Status: "ready"}, actDo},
		{"non-landed underway still orphan", monitor.SessionStatusRow{Status: "underway"}, actOrphan},
		// Decoration still wins: a landed task a live session is on reads ⟳ doing.
		{"landed in-flight still doing", monitor.SessionStatusRow{Status: "ready", Landed: true, InFlight: true}, actDoing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.row); got != c.want {
				t.Errorf("classify(%s) = %d, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestActionIcons pins the glyphs that other surfaces (and the legend) depend on,
// notably the E-1693 ⁇ catch-all that replaced the silent · and the untouched
// ◷ orphan it must stay distinct from.
func TestActionIcons(t *testing.T) {
	cases := map[action]string{
		actOrphan: "◷",
		actOther:  "⁇",
	}
	for a, want := range cases {
		if got := a.icon(); got != want {
			t.Errorf("action(%d).icon() = %q, want %q", a, got, want)
		}
	}
}

func TestSortRows(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 5, Status: "ready", Phase: "now"},        // do
		{ID: 1, IsParent: true, Phase: "later"},       // parent
		{ID: 2, IsFrom: true, Phase: "now"},           // from (spawner)
		{ID: 9, IsFocal: true, Phase: "maybe"},        // this
		{ID: 7, Status: "unplanned", Phase: "urgent"}, // plan, urgent
		{ID: 8, Status: "unplanned", Phase: "now"},    // plan, now
		{ID: 3, InFlight: true, Status: "ready"},      // doing
	}
	sortRows(rows)
	gotOrder := make([]int64, len(rows))
	for i, r := range rows {
		gotOrder[i] = r.ID
	}
	// this(9) < parent(1) < from(2) < doing(3) < do(5) < plan/urgent(7) < plan/now(8)
	want := []int64{9, 1, 2, 3, 5, 7, 8}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("sort order = %v, want %v", gotOrder, want)
		}
	}
}

func TestPhaseChar(t *testing.T) {
	cases := map[string]struct {
		row  monitor.SessionStatusRow
		want string
	}{
		"terminal":     {monitor.SessionStatusRow{Status: "confirmed", Phase: "now"}, "✓"},
		"urgent":       {monitor.SessionStatusRow{Status: "ready", Phase: "urgent"}, "!"},
		"now":          {monitor.SessionStatusRow{Status: "ready", Phase: "now"}, "1"},
		"next":         {monitor.SessionStatusRow{Status: "ready", Phase: "next"}, "2"},
		"later":        {monitor.SessionStatusRow{Status: "ready", Phase: "later"}, "3"},
		"maybe":        {monitor.SessionStatusRow{Status: "ready", Phase: "maybe"}, "?"},
		"unknownphase": {monitor.SessionStatusRow{Status: "ready", Phase: "xyz"}, " "},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := phaseChar(c.row); got != c.want {
				t.Errorf("phaseChar = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBlockField(t *testing.T) {
	blockedOnly := monitor.SessionStatusRow{BlockedByN: 1}
	blocksOnly := monitor.SessionStatusRow{BlocksN: 1}
	both := monitor.SessionStatusRow{BlockedByN: 1, BlocksN: 1}
	neither := monitor.SessionStatusRow{}

	if got := blockField(neither, 0); got != "" {
		t.Errorf("bw=0 should be empty, got %q", got)
	}
	if got := blockField(blockedOnly, 1); got != "⊗ " {
		t.Errorf("bw=1 blocked = %q, want %q", got, "⊗ ")
	}
	if got := blockField(blocksOnly, 1); got != "⏸ " {
		t.Errorf("bw=1 blocks = %q, want %q", got, "⏸ ")
	}
	if got := blockField(neither, 1); got != "  " {
		t.Errorf("bw=1 neither = %q, want two spaces", got)
	}
	if got := blockField(both, 2); got != "⊗⏸ " {
		t.Errorf("bw=2 both = %q, want %q", got, "⊗⏸ ")
	}
	if got := blockField(blockedOnly, 2); got != "⊗  " {
		t.Errorf("bw=2 blocked-only = %q, want %q", got, "⊗  ")
	}
}

func TestDirtyMark(t *testing.T) {
	if got := dirtyMark(monitor.SessionStatusRow{Dirty: true}); got != "◆" {
		t.Errorf("dirty row = %q, want ◆", got)
	}
	if got := dirtyMark(monitor.SessionStatusRow{Dirty: false}); got != " " {
		t.Errorf("clean row = %q, want a single space", got)
	}
	// ◆ and the space it replaces must both be display-width 1 so the fixed 13-col
	// prefix and its alignment hold regardless of the dirty state (E-1701).
	if w := displayWidth("◆"); w != 1 {
		t.Errorf("◆ display width = %d, want 1", w)
	}
}

// TestRenderDirtyIndicator proves the flat view renders ◆ between the type
// letter and id for a dirty row and a plain space for a clean one, and that the
// substitution does not shift the id column (both glyphs are width 1) — E-1701.
func TestRenderDirtyIndicator(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1701, Title: "dirty one", Status: "underway", Phase: "now", TypeSlug: "task", IsFocal: true, Dirty: true},
		{ID: 1702, Title: "clean one", Status: "ready", Phase: "now", TypeSlug: "task", Dirty: false},
	}
	var b strings.Builder
	renderTo(&b, rows, 1701, hintClaimBind, 90, false)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (legend + 2 rows), got %d:\n%s", len(lines), b.String())
	}
	if !strings.HasPrefix(lines[1], "● T◆E-1701 1 ") {
		t.Errorf("dirty row prefix wrong: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "▶ T E-1702 1 ") {
		t.Errorf("clean row prefix wrong: %q", lines[2])
	}
	// The id column must start at the same DISPLAY offset in both rows — the ◆/space
	// swap is width-neutral (byte offsets differ: ◆ is 3 bytes, space is 1).
	dw := displayWidth(lines[1][:strings.Index(lines[1], "E-1701")])
	cw := displayWidth(lines[2][:strings.Index(lines[2], "E-1702")])
	if dw != cw {
		t.Errorf("id column shifted by dirty marker: dirty width=%d clean width=%d", dw, cw)
	}
}

func TestRenderEmptyFocal(t *testing.T) {
	var b strings.Builder
	renderTo(&b, nil, 0, hintClaimBind, 90, false)
	out := b.String()
	if !strings.Contains(out, legend) {
		t.Errorf("legend missing from empty render:\n%s", out)
	}
	// The no-focal render shows the claim/bind hint, never an unrelated task list
	// (E-1698): the caller passes the pane's resolved hint verbatim.
	if !strings.Contains(out, "claim or bind") {
		t.Errorf("claim/bind hint missing:\n%s", out)
	}
}

func TestRenderColumnsAndTruncation(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1465, Title: "Implement endless session next briefing read command", Status: "underway", Phase: "now", TypeSlug: "task", IsFocal: true},
		{ID: 1461, Title: "Add endless session next prospective remaining-work briefing", Status: "ready", Phase: "now", TypeSlug: "epic", IsParent: true},
		{ID: 1684, Title: "Add session next --tree showing task IDs in implementation order", Status: "confirmed", Phase: "now", TypeSlug: "task", IsFrom: true},
	}
	var b strings.Builder
	renderTo(&b, rows, 1465, hintClaimBind, 40, false)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	// legend + 3 rows
	if len(lines) != 4 {
		t.Fatalf("want 4 lines (legend + 3 rows), got %d:\n%s", len(lines), b.String())
	}
	if !strings.HasPrefix(lines[1], "● T E-1465 1 ") {
		t.Errorf("focal row prefix wrong: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "↑ E E-1461 1 ") {
		t.Errorf("parent row prefix wrong: %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "↩ T E-1684 ") {
		t.Errorf("from row prefix wrong: %q", lines[3])
	}
	// Truncated to terminal width (40): no row should exceed it in display width.
	for _, ln := range lines[1:] {
		if w := displayWidth(ln); w > 40 {
			t.Errorf("row exceeds width 40 (got %d): %q", w, ln)
		}
	}
}

// TestEraseEachLineToEOL guards the E-1699 fix: the live `session monitor`
// repaint must erase each line to end-of-line so that when a row's new title is
// shorter than the prior frame's, no stale tail survives. It asserts the exact
// byte transform the production repaint applies before the caller's \x1b[H … \x1b[J.
func TestEraseEachLineToEOL(t *testing.T) {
	const K = "\x1b[K"
	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{"two trailing-newline rows", "row A\nrow B\n", "row A" + K + "\nrow B" + K + "\n" + K},
		{"no trailing newline", "row A\nrow B", "row A" + K + "\nrow B" + K},
		{"single line", "only\n", "only" + K + "\n" + K},
		{"empty frame", "", K},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eraseEachLineToEOL(c.frame); got != c.want {
				t.Errorf("eraseEachLineToEOL(%q)\n  got:  %q\n  want: %q", c.frame, got, c.want)
			}
		})
	}

	// The whole point: repainting a SHORTER frame over a LONGER one must leave
	// no leftover characters. Simulate a terminal's line buffer cell-by-cell:
	// start from the long frame's line, overwrite from column 0 with the new
	// (erased) line, and confirm the erase truncates the old tail.
	long := "Row A: E-1698 leftover long title here"
	short := "Row A: E-1461"
	wrapped := eraseEachLineToEOL(short + "\n")
	newLine := strings.SplitN(wrapped, "\n", 2)[0] // "Row A: E-1461\x1b[K"
	if overwriteLine(long, newLine) != short {
		t.Errorf("stale tail survived: %q over %q -> %q, want %q",
			newLine, long, overwriteLine(long, newLine), short)
	}
}

// overwriteLine models a VT100 line buffer: prev is the existing line content;
// next is written from column 0. A literal rune overwrites one cell; \x1b[K
// erases from the cursor to end-of-line (drops the remaining old cells). Returns
// the resulting visible line — used to prove the erase kills a shorter title's
// leftover tail.
func overwriteLine(prev, next string) string {
	cells := []rune(prev)
	col := 0
	for i := 0; i < len(next); i++ {
		if strings.HasPrefix(next[i:], "\x1b[K") {
			cells = cells[:min(col, len(cells))]
			i += len("\x1b[K") - 1
			continue
		}
		r := rune(next[i])
		if col < len(cells) {
			cells[col] = r
		} else {
			cells = append(cells, r)
		}
		col++
	}
	return string(cells)
}

func TestTypeLetter(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"epic", "E"},
		{"bug", "F"},
		{"research", "R"},
		{"brainstorm", "B"},
		{"task", "T"},
		{"todo", "T"},
		{"", "T"},
		{"anything-else", "T"},
	}
	for _, c := range cases {
		if got := typeLetter(c.slug); got != c.want {
			t.Errorf("typeLetter(%q) = %q, want %q", c.slug, got, c.want)
		}
		if typeLetter(c.slug) == "Z" {
			t.Errorf("typeLetter(%q) still returns freed letter %q", c.slug, "Z")
		}
	}
}
