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
		{"submitted is review not do", monitor.SessionStatusRow{Status: "submitted"}, actReview},
		{"needs_plan is plan", monitor.SessionStatusRow{Status: "needs_plan"}, actPlan},
		{"revisit folds into plan", monitor.SessionStatusRow{Status: "revisit"}, actPlan},
		{"verify", monitor.SessionStatusRow{Status: "verify"}, actVerify},
		{"unverified is verify", monitor.SessionStatusRow{Status: "unverified"}, actVerify},
		{"underway is orphan", monitor.SessionStatusRow{Status: "underway"}, actOrphan},
		{"unrecognized status is unknown", monitor.SessionStatusRow{Status: "blocked"}, actUnknown},
		// E-1693/E-1750: a landed task routes to actLanded regardless of its
		// non-terminal status, so merged work is never offered as a fresh verb.
		{"landed ready is landed", monitor.SessionStatusRow{Status: "ready", Landed: true}, actLanded},
		{"landed unverified is landed", monitor.SessionStatusRow{Status: "unverified", Landed: true}, actLanded},
		{"landed unplanned is landed", monitor.SessionStatusRow{Status: "unplanned", Landed: true}, actLanded},
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
// notably the E-1750 split of the old ⁇ catch-all into ⏚ landed and ⁇ unknown,
// the untouched ◷ orphan they must stay distinct from, and the E-1765 ⚑ review
// (submitted, awaiting approval — not spawnable).
func TestActionIcons(t *testing.T) {
	cases := map[action]string{
		actReview:  "⚑",
		actOrphan:  "◷",
		actLanded:  "⏚",
		actUnknown: "⁇",
	}
	for a, want := range cases {
		if got := a.icon(); got != want {
			t.Errorf("action(%d).icon() = %q, want %q", a, got, want)
		}
	}
	// ⚑ must measure display-width 1 so the fixed 13-col prefix and the width-aware
	// table stay aligned, the same guarantee ⏚/⁇/◆ carry (E-1765).
	if w := displayWidth("⚑"); w != 1 {
		t.Errorf("⚑ review glyph display width = %d, want 1", w)
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
	// The no-focal render shows ONLY the claim/bind hint (E-1698) — with no rows
	// to document there is no legend line (E-1750), so its glyphs must be absent.
	if !strings.Contains(out, "claim or bind") {
		t.Errorf("claim/bind hint missing:\n%s", out)
	}
	if strings.ContainsAny(out, "●▶✎") {
		t.Errorf("empty render should carry no legend glyphs:\n%s", out)
	}
	if len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 1 {
		t.Errorf("empty render should be a single hint line:\n%s", out)
	}
}

// TestBuildLegend covers the E-1750 dynamic legend: only glyphs for actions and
// decorations actually present in the row set, enum order then decorations, no
// `|` divider. This is the sole coverage of ◆ dirty (a real divergent worktree
// can't be seeded hermetically) and of ⁇ unknown (driven by a synthetic status).
func TestBuildLegend(t *testing.T) {
	cases := []struct {
		name        string
		rows        []monitor.SessionStatusRow
		want        string // exact when non-empty
		mustHave    []string
		mustNotHave []string
	}{
		{
			name: "do and plan only",
			rows: []monitor.SessionStatusRow{
				{Status: "ready"},     // do
				{Status: "unplanned"}, // plan
			},
			want:        "▶ do  ✎ plan",
			mustNotHave: []string{"orphan", "verify", "landed", "unknown", "done", "blocked", "blocks", "dirty", "|"},
		},
		{
			name:     "terminal row surfaces ✓ done",
			rows:     []monitor.SessionStatusRow{{IsFocal: true, Status: "confirmed"}},
			mustHave: []string{"✓ done"},
		},
		{
			name:        "no ✓ done when no terminal row",
			rows:        []monitor.SessionStatusRow{{Status: "ready"}},
			mustNotHave: []string{"done"},
		},
		{
			name:     "submitted row surfaces ⚑ review",
			rows:     []monitor.SessionStatusRow{{Status: "submitted"}},
			mustHave: []string{"⚑ review"},
		},
		{
			name: "do and review render in enum order",
			rows: []monitor.SessionStatusRow{
				{Status: "submitted"}, // review (later in enum)
				{Status: "ready"},     // do (earlier in enum)
			},
			want: "▶ do  ⚑ review",
		},
		{
			name:     "landed row surfaces ⏚ landed",
			rows:     []monitor.SessionStatusRow{{Status: "ready", Landed: true}},
			mustHave: []string{"⏚ landed"},
		},
		{
			name:     "unrecognized status surfaces ⁇ unknown",
			rows:     []monitor.SessionStatusRow{{Status: "blocked"}},
			mustHave: []string{"⁇ unknown"},
		},
		{
			name:     "blocked decoration",
			rows:     []monitor.SessionStatusRow{{Status: "ready", BlockedByN: 1}},
			mustHave: []string{"⊗ blocked"},
		},
		{
			name:     "blocks decoration",
			rows:     []monitor.SessionStatusRow{{Status: "ready", BlocksN: 1}},
			mustHave: []string{"⏸ blocks"},
		},
		{
			name:     "dirty decoration",
			rows:     []monitor.SessionStatusRow{{Status: "ready", Dirty: true}},
			mustHave: []string{"◆ dirty"},
		},
		{
			name: "actions in enum order then decorations",
			rows: []monitor.SessionStatusRow{
				{Status: "unplanned"},             // plan (later in enum)
				{IsFocal: true, Status: "ready"},  // this (first in enum)
				{Status: "ready", BlockedByN: 1},  // do + ⊗
				{Status: "underway", Dirty: true}, // orphan + ◆
			},
			want: "● this  ▶ do  ✎ plan  ◷ orphan  ⊗ blocked  ◆ dirty",
		},
		{
			name: "no rows yields empty legend",
			rows: nil,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildLegend(c.rows)
			if c.want != "" || c.name == "no rows yields empty legend" {
				if got != c.want {
					t.Errorf("buildLegend = %q, want %q", got, c.want)
				}
			}
			for _, s := range c.mustHave {
				if !strings.Contains(got, s) {
					t.Errorf("buildLegend = %q, must contain %q", got, s)
				}
			}
			for _, s := range c.mustNotHave {
				if strings.Contains(got, s) {
					t.Errorf("buildLegend = %q, must NOT contain %q", got, s)
				}
			}
			if strings.Contains(got, "|") {
				t.Errorf("buildLegend = %q, must contain no `|` divider", got)
			}
		})
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
