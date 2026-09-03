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
		// E-1845: `untriaged` is its own action. NOT actPlan (it carries no
		// plan judgment yet) and — load-bearing — NOT actUnknown, which is the
		// should-never-happen glyph and would otherwise land on the most common
		// row in the database, since every new task starts untriaged.
		{"untriaged is triage not plan", monitor.SessionStatusRow{Status: "untriaged"}, actTriage},
		{"untriaged with plan text is still triage", monitor.SessionStatusRow{Status: "untriaged", HasText: true}, actTriage},
		{"landed untriaged is landed", monitor.SessionStatusRow{Status: "untriaged", Landed: true}, actLanded},
		{"focal untriaged is this", monitor.SessionStatusRow{Status: "untriaged", IsFocal: true}, actThis},
		{"in-flight untriaged is doing", monitor.SessionStatusRow{Status: "untriaged", InFlight: true}, actDoing},
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
		// E-1871: every terminal status is a terminus (⇥ closed), NOT the ⁇
		// should-never-happen glyph it used to fall through to.
		{"confirmed is closed", monitor.SessionStatusRow{Status: "confirmed"}, actDone},
		{"assumed is closed", monitor.SessionStatusRow{Status: "assumed"}, actDone},
		{"declined is closed", monitor.SessionStatusRow{Status: "declined"}, actDone},
		{"obsolete is closed", monitor.SessionStatusRow{Status: "obsolete"}, actDone},
		{"completed is closed", monitor.SessionStatusRow{Status: "completed"}, actDone},
		// Precedence pin: ⏚ landed outranks ⇥ closed, so ⇥ marks only closed work
		// that never merged. A refactor that moves the isTerminal check above the
		// r.Landed check breaks these.
		{"landed confirmed is landed not closed", monitor.SessionStatusRow{Status: "confirmed", Landed: true}, actLanded},
		{"landed completed is landed not closed", monitor.SessionStatusRow{Status: "completed", Landed: true}, actLanded},
		// Decorations still outrank both.
		{"focal confirmed is this", monitor.SessionStatusRow{Status: "confirmed", IsFocal: true}, actThis},
		{"parent obsolete is parent", monitor.SessionStatusRow{Status: "obsolete", IsParent: true}, actParent},
		{"from declined is from", monitor.SessionStatusRow{Status: "declined", IsFrom: true}, actFrom},
		{"in-flight completed is doing", monitor.SessionStatusRow{Status: "completed", InFlight: true}, actDoing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.row); got != c.want {
				t.Errorf("classify(%s) = %d, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestTerminalStatusNeverUnknown is the E-1871 regression gate stated as the bug
// rather than as the fix: NO terminal status may classify as actUnknown, in any
// undecorated/unlanded combination. ⁇ is load-bearing as a diagnostic ("classify()
// met a status it does not know about"), so firing it on ordinary closed rows —
// which is what `endless session status --all` did — destroys that signal. The
// status list is the one isTerminal recognizes; if a sixth terminal status is ever
// added there, add it here too.
func TestTerminalStatusNeverUnknown(t *testing.T) {
	for _, status := range []string{"confirmed", "assumed", "declined", "obsolete", "completed"} {
		t.Run(status, func(t *testing.T) {
			if !isTerminal(status) {
				t.Fatalf("isTerminal(%q) = false — this test's status list has drifted from isTerminal", status)
			}
			if got := classify(monitor.SessionStatusRow{Status: status}); got == actUnknown {
				t.Errorf("classify(%q) = actUnknown (⁇) — a terminal status must never read as an unhandled one", status)
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
		actDone:    "⇥",
		actTriage:  "◌",
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
	// Same guarantee for ⇥ closed (E-1871) — it replaces ⁇ in column 1, so a
	// width-2 glyph there would shift the id column on every closed row.
	if w := displayWidth("⇥"); w != 1 {
		t.Errorf("⇥ closed glyph display width = %d, want 1", w)
	}
	// actDone is APPENDED after actUnknown, never inserted: enum order is both
	// legend order and sortRows' rank, so an insert would silently re-rank every
	// action below it.
	if actDone <= actUnknown {
		t.Errorf("actDone (%d) must rank after actUnknown (%d) — append, do not insert", actDone, actUnknown)
	}
	// Same guarantee for ◌ triage (E-1845): it shares column 1 with every other
	// glyph, and `untriaged` is the default status, so a width-2 glyph here would
	// shift the id column on the majority of rows.
	if w := displayWidth("◌"); w != 1 {
		t.Errorf("◌ triage glyph display width = %d, want 1", w)
	}
	// actTriage is likewise APPENDED, after actDone.
	if actTriage <= actDone {
		t.Errorf("actTriage (%d) must rank after actDone (%d) — append, do not insert", actTriage, actDone)
	}
	// Its label must not read as "needs a plan" — that distinction is the entire
	// reason the status exists.
	if got := actTriage.label(); got == actPlan.label() {
		t.Errorf("actTriage.label() = %q, must differ from actPlan's", got)
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
		// E-1871: a closed row sorts LAST — after every open row and after the ⁇
		// anomaly row, because an unhandled status deserves more prominence in the
		// list than a finished task. Its `urgent` phase is deliberate: rank is
		// decided by the action enum first, so phase must not pull it up.
		{ID: 4, Status: "blocked", Phase: "now"},      // unknown (⁇)
		{ID: 6, Status: "confirmed", Phase: "urgent"}, // closed (⇥)
	}
	sortRows(rows)
	gotOrder := make([]int64, len(rows))
	for i, r := range rows {
		gotOrder[i] = r.ID
	}
	// this(9) < parent(1) < from(2) < doing(3) < do(5) < plan/urgent(7) < plan/now(8)
	//   < unknown(4) < closed(6)
	want := []int64{9, 1, 2, 3, 5, 7, 8, 4, 6}
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

// TestUnsettledMark pins all three states of the column and the rule that picks
// between them (E-1701 for ◆, E-2107 for the ⊙/space split). The load-bearing
// case is the last pair: `underway` settled and `unverified` settled differ ONLY
// in status, and before E-2107 both rendered blank — the collapse the task
// exists to undo.
func TestUnsettledMark(t *testing.T) {
	cases := []struct {
		name   string
		row    monitor.SessionStatusRow
		want   string
		reason string
	}{
		{"unsettled beats everything", monitor.SessionStatusRow{Status: "underway", Unsettled: true}, "◆",
			"a diverged worktree has outstanding work product whatever the status says"},
		{"unsettled outranks a shipped status", monitor.SessionStatusRow{Status: "confirmed", Unsettled: true}, "◆",
			"◆ takes precedence over the ⊙/space split, unchanged from E-1701"},
		{"never spawned", monitor.SessionStatusRow{Status: "ready"}, "⊙",
			"nobody has picked this up, so there is no work product"},
		{"untriaged", monitor.SessionStatusRow{Status: "untriaged"}, "⊙", "same, earlier still"},
		{"claimed but empty", monitor.SessionStatusRow{Status: "underway"}, "⊙",
			"a session is sitting on it and has produced nothing — the same fact about the work"},
		{"revisit", monitor.SessionStatusRow{Status: "revisit"}, "⊙",
			"reopened work has not been restarted"},
		{"declined", monitor.SessionStatusRow{Status: "declined"}, "⊙",
			"abandoned without shipping — terminal, but never any work product"},
		{"obsolete", monitor.SessionStatusRow{Status: "obsolete"}, "⊙", "same"},
		{"unverified and settled", monitor.SessionStatusRow{Status: "unverified"}, " ",
			"reached the gate with a clean worktree: produced work, all of it landed"},
		{"unreviewed and settled", monitor.SessionStatusRow{Status: "unreviewed"}, " ", "the findings-lane gate"},
		{"confirmed and settled", monitor.SessionStatusRow{Status: "confirmed"}, " ", "past the gate"},
		{"assumed and settled", monitor.SessionStatusRow{Status: "assumed"}, " ", "past the gate"},
		{"completed and settled", monitor.SessionStatusRow{Status: "completed"}, " ", "past the gate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unsettledMark(c.row); got != c.want {
				t.Errorf("unsettledMark(status=%q, unsettled=%v) = %q, want %q — %s",
					c.row.Status, c.row.Unsettled, got, c.want, c.reason)
			}
		})
	}
}

// TestUnsettledMarkGlyphWidths pins every state of the column at one terminal
// column, the same invariant TestHiddenGlyphWidth and TestRelationGlyphWidths
// enforce for their own slots. This one is load-bearing for the WHOLE table:
// the column sits inside the fixed 13-col prefix (E-1765), so a double-width
// glyph here shoves the id, phase and title right on marked rows only.
//
// ⊙ (U+2299) is East Asian Ambiguous, the same class as the already-shipped ⊘ —
// asserted rather than assumed (E-2107).
func TestUnsettledMarkGlyphWidths(t *testing.T) {
	for name, g := range map[string]string{
		"unsettledGlyph":         unsettledGlyph,
		"notStartedGlyph":        notStartedGlyph,
		"the space they replace": " ",
	} {
		if w := displayWidth(g); w != 1 {
			t.Errorf("%s %q display width = %d, want 1", name, g, w)
		}
	}
}

// TestNotStartedDoesNotVetoDim is the deliberate asymmetry between the two
// marked states (E-2107). ◆ vetoes dim because it means "there is still
// something to do here" (E-1707); ⊙ means the opposite, so a never-started
// `later` or terminal row must still read dim. Getting this wrong would
// un-dim most of the table at once, since ⊙ is the common state.
func TestNotStartedDoesNotVetoDim(t *testing.T) {
	for _, r := range []monitor.SessionStatusRow{
		{Status: "ready", Phase: "later"},
		{Status: "ready", Phase: "maybe"},
		{Status: "declined", Phase: "now"},
	} {
		if got := unsettledMark(r); got != "⊙" {
			t.Fatalf("fixture no longer bears ⊙ (got %q) — the test proves nothing", got)
		}
		if got := colorize("row", r, true); !strings.HasPrefix(got, ansiDim) {
			t.Errorf("status=%q phase=%q: ⊙ row = %q, want dim", r.Status, r.Phase, got)
		}
	}
	// The contrast case: same row, unsettled, stays at full intensity.
	unsettled := monitor.SessionStatusRow{Status: "ready", Phase: "later", Unsettled: true}
	if got := colorize("row", unsettled, true); got != "row" {
		t.Errorf("◆ row = %q, want undimmed", got)
	}
}

// TestRenderUnsettledIndicator proves the flat view renders ◆ between the type
// letter and id for an unsettled row and a plain space for a settled one, and
// that the substitution does not shift the id column (both glyphs are width 1) —
// E-1701.
func TestRenderUnsettledIndicator(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1701, Title: "unsettled one", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true, Unsettled: true},
		{ID: 1702, Title: "settled one", Status: "ready", Phase: "now", TypeSlug: "todo", Unsettled: false},
	}
	var b strings.Builder
	renderTo(&b, rows, 1701, hintClaimBind, 90, false, hiddenOmit)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (legend + 2 rows), got %d:\n%s", len(lines), b.String())
	}
	if !strings.HasPrefix(lines[1], "● T◆E-1701 1 ") {
		t.Errorf("unsettled row prefix wrong: %q", lines[1])
	}
	// `ready` and settled is the never-spawned case: ⊙, not a blank (E-2107).
	if !strings.HasPrefix(lines[2], "▶ T⊙E-1702 1 ") {
		t.Errorf("clean row prefix wrong: %q", lines[2])
	}
	// The id column must start at the same DISPLAY offset in both rows — the ◆/space
	// swap is width-neutral (byte offsets differ: ◆ is 3 bytes, space is 1).
	dw := displayWidth(lines[1][:strings.Index(lines[1], "E-1701")])
	cw := displayWidth(lines[2][:strings.Index(lines[2], "E-1702")])
	if dw != cw {
		t.Errorf("id column shifted by unsettled marker: unsettled width=%d settled width=%d", dw, cw)
	}
}

// TestRenderThreeStateColumn is E-2107's whole-render proof: one frame holding
// all three states of the column at once, showing that they are DISTINGUISHABLE
// and that swapping between them never shifts the id column.
//
// The alignment half matters more than it looks. The column sits inside the
// fixed 13-col prefix, so the E-1765 guarantee — every row's id, phase and title
// start at the same display offset — holds only if all three states measure the
// same. Byte offsets differ (◆ and ⊙ are 3 bytes, a space is 1), which is
// exactly why the assertion measures display width instead.
func TestRenderThreeStateColumn(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		// ◆ — a session's worktree still diverges from main.
		{ID: 2101, Title: "outstanding", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true, Unsettled: true},
		// ⊙ — claimed, settled worktree: nothing produced yet.
		{ID: 2102, Title: "claimed and empty", Status: "underway", Phase: "now", TypeSlug: "todo", InFlight: true},
		// ⊙ — never spawned at all. Same column state as E-2102 on purpose: this
		// column does not distinguish them, the action icon does.
		{ID: 2103, Title: "never spawned", Status: "ready", Phase: "now", TypeSlug: "todo"},
		// (blank) — shipped and settled: produced work, all of it landed.
		{ID: 2104, Title: "landed and done", Status: "unverified", Phase: "now", TypeSlug: "todo"},
	}
	var b strings.Builder
	renderTo(&b, rows, 2101, hintClaimBind, 90, false, hiddenOmit)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 lines (legend + 4 rows), got %d:\n%s", len(lines), b.String())
	}
	if !strings.Contains(lines[0], "◆ unsettled") || !strings.Contains(lines[0], "⊙ not started") {
		t.Errorf("legend must document both marked states, got %q", lines[0])
	}

	want := map[string]string{"E-2101": "◆", "E-2102": "⊙", "E-2103": "⊙", "E-2104": " "}
	offsets := map[string]int{}
	for _, ln := range lines[1:] {
		for id, glyph := range want {
			i := strings.Index(ln, id)
			if i < 0 {
				continue
			}
			// The column is the single glyph immediately before the id.
			prefix := ln[:i]
			if !strings.HasSuffix(prefix, glyph) {
				t.Errorf("%s column = %q, want %q (row %q)", id, lastGlyph(prefix), glyph, ln)
			}
			offsets[id] = displayWidth(prefix)
		}
	}
	if len(offsets) != len(want) {
		t.Fatalf("not every row rendered: got offsets for %v", offsets)
	}
	for id, off := range offsets {
		if off != offsets["E-2101"] {
			t.Errorf("id column shifted on %s: display offset %d, want %d (E-1765 alignment)",
				id, off, offsets["E-2101"])
		}
	}
}

// lastGlyph returns the final rune of s as a string, for readable failure
// messages when the column carries the wrong mark.
func lastGlyph(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[len(r)-1])
}

func TestRenderEmptyFocal(t *testing.T) {
	var b strings.Builder
	renderTo(&b, nil, 0, hintClaimBind, 90, false, hiddenOmit)
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

// TestRenderNoGoalSurfacesRows pins the E-1802 no-goal render: with focal == 0
// but a non-empty row set (the emitting session's surfaced/revisited tasks), the
// table renders normally — legend + rows — instead of short-circuiting to the
// claim/bind hint. The old gate was `focal == 0 || len(rows) == 0`; the new gate
// is `len(rows) == 0`, so focal == 0 with work no longer hides it.
func TestRenderNoGoalSurfacesRows(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1801, Title: "filed this session", Status: "ready", Phase: "now", TypeSlug: "todo"},
		{ID: 1776, Title: "touched this session", Status: "unplanned", Phase: "next", TypeSlug: "todo"},
	}
	var b strings.Builder
	renderTo(&b, rows, 0, hintClaimBind, 90, false, hiddenOmit)
	out := b.String()
	if strings.Contains(out, "claim or bind") {
		t.Errorf("no-goal view with rows must NOT show the claim/bind hint:\n%s", out)
	}
	for _, id := range []string{"E-1801", "E-1776"} {
		if !strings.Contains(out, id) {
			t.Errorf("row %s missing from no-goal render:\n%s", id, out)
		}
	}
	// legend + 2 rows.
	if n := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); n != 3 {
		t.Errorf("want 3 lines (legend + 2 rows), got %d:\n%s", n, out)
	}
}

// TestBuildLegend covers the E-1750 dynamic legend: only glyphs for actions and
// decorations actually present in the row set, enum order then decorations, no
// `|` divider. This is the sole coverage of ◆ unsettled (a real divergent worktree
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
			want:        "▶ do  ✎ plan  ⊙ not started",
			mustNotHave: []string{"orphan", "verify", "landed", "unknown", "closed", "done", "blocked", "blocks", "unsettled", "|"},
		},
		// E-1871: an undecorated, unlanded terminal row is the case that used to
		// advertise "⁇ unknown" for perfectly ordinary done work. It now carries BOTH
		// ⇥ closed (the column-1 action) and ✓ done (the phase-column marker) — not
		// redundant: ✓ is what a DECORATED terminal row shows instead, where column 1
		// is ●/↑/⏚.
		{
			name: "undecorated unlanded terminal row surfaces ⇥ closed and ✓ done, never ⁇",
			rows: []monitor.SessionStatusRow{{Status: "confirmed"}},
			want: "⇥ closed  ✓ done",
			// No ⊙: `confirmed` is Shipped, so the row's column is a blank —
			// "produced work, all of it landed". Contrast the declined/obsolete
			// case below, which is terminal but never shipped anything (E-2107).
			mustNotHave: []string{"⁇ unknown", "not started"},
		},
		{
			name: "declined and obsolete — which never land — also read ⇥ closed",
			rows: []monitor.SessionStatusRow{{Status: "declined"}, {Status: "obsolete"}},
			// ⊙ too: abandoned work is terminal but never shipped, so the column
			// correctly reports no work product (E-2107).
			want:        "⇥ closed  ✓ done  ⊙ not started",
			mustNotHave: []string{"⁇ unknown"},
		},
		// ⏚ wins over ⇥: a terminal task whose work merged is landed, so ⇥ is the
		// glyph for closed work that never merged — the informative case.
		{
			name:        "landed terminal row surfaces ⏚ landed and ✓ done, not ⇥ closed",
			rows:        []monitor.SessionStatusRow{{Status: "confirmed", Landed: true}},
			want:        "⏚ landed  ✓ done",
			mustNotHave: []string{"⇥ closed", "⁇ unknown"},
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
			want: "▶ do  ⚑ review  ⊙ not started",
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
			name: "⇥ closed follows ⁇ unknown in legend order",
			rows: []monitor.SessionStatusRow{
				{Status: "confirmed"}, // closed (last in enum)
				{Status: "blocked"},   // unknown
				{Status: "ready"},     // do
			},
			want: "▶ do  ⁇ unknown  ⇥ closed  ✓ done  ⊙ not started",
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
			name:        "unsettled decoration",
			rows:        []monitor.SessionStatusRow{{Status: "ready", Unsettled: true}},
			mustHave:    []string{"◆ unsettled"},
			mustNotHave: []string{"not started"},
		},
		{
			name:        "not-started decoration",
			rows:        []monitor.SessionStatusRow{{Status: "underway"}},
			mustHave:    []string{"⊙ not started"},
			mustNotHave: []string{"unsettled"},
		},
		{
			// The conditional half of the width-on-demand rule: a frame in which
			// every row has shipped and settled bears no ⊙, so the legend must not
			// advertise one (E-2107).
			name:        "no ⊙ when every row has shipped and settled",
			rows:        []monitor.SessionStatusRow{{Status: "unverified"}, {Status: "confirmed"}},
			mustNotHave: []string{"not started"},
		},
		{
			name: "actions in enum order then decorations",
			rows: []monitor.SessionStatusRow{
				{Status: "unplanned"},                 // plan (later in enum)
				{IsFocal: true, Status: "ready"},      // this (first in enum)
				{Status: "ready", BlockedByN: 1},      // do + ⊗
				{Status: "underway", Unsettled: true}, // orphan + ◆
			},
			want: "● this  ▶ do  ✎ plan  ◷ orphan  ⊗ blocked  ◆ unsettled  ⊙ not started",
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
		{ID: 1465, Title: "Implement endless session next briefing read command", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true},
		{ID: 1461, Title: "Add endless session next prospective remaining-work briefing", Status: "ready", Phase: "now", TypeSlug: "epic", IsParent: true},
		{ID: 1684, Title: "Add session next --tree showing task IDs in implementation order", Status: "confirmed", Phase: "now", TypeSlug: "todo", IsFrom: true},
	}
	var b strings.Builder
	renderTo(&b, rows, 1465, hintClaimBind, 40, false, hiddenOmit)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	// legend + 3 rows
	if len(lines) != 4 {
		t.Fatalf("want 4 lines (legend + 3 rows), got %d:\n%s", len(lines), b.String())
	}
	if !strings.HasPrefix(lines[1], "● T⊙E-1465 1 ") {
		t.Errorf("focal row prefix wrong: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "↑ E⊙E-1461 1 ") {
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

// TestColorize pins the E-1707 rule on top of the pre-existing intensity matrix:
// the unsettled flag (◆) VETOES every dim case — terminal, later and maybe — so a
// diverged worktree never renders grey and reads as done. Vetoing dim yields
// NORMAL weight (bold stays reserved for urgent), and terminal still outranks
// urgent when the row is settled.
func TestColorize(t *testing.T) {
	const line = "⁇ T◆E-1687 ✓ completed but unlanded"
	cases := []struct {
		name                string
		phase               string
		terminal, unsettled bool
		want                string
	}{
		// Pre-E-1707 behavior, unchanged for settled rows.
		{"terminal settled dims", "now", true, false, ansiDim + line + ansiReset},
		{"later settled dims", "later", false, false, ansiDim + line + ansiReset},
		{"maybe settled dims", "maybe", false, false, ansiDim + line + ansiReset},
		{"urgent settled bolds", "urgent", false, false, ansiBold + line + ansiReset},
		{"terminal outranks urgent", "urgent", true, false, ansiDim + line + ansiReset},
		{"now settled is normal", "now", false, false, line},
		// E-1707: ◆ vetoes dim, at normal weight — never bold.
		{"terminal unsettled is NOT dim", "now", true, true, line},
		{"later unsettled is NOT dim", "later", false, true, line},
		{"maybe unsettled is NOT dim", "maybe", false, true, line},
		{"now unsettled stays normal", "now", false, true, line},
		// urgent keeps its bold: ◆ only removes dim, it does not add emphasis.
		{"urgent unsettled stays bold", "urgent", false, true, ansiBold + line + ansiReset},
		// The veto is a COLOR decision only — with color off nothing is ever wrapped.
		{"color disabled is untouched", "now", true, true, line},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enabled := c.name != "color disabled is untouched"
			status := "underway"
			if c.terminal {
				status = "confirmed"
			}
			row := monitor.SessionStatusRow{
				Phase:     c.phase,
				Status:    status,
				Unsettled: c.unsettled,
			}
			got := colorize(line, row, enabled)
			if got != c.want {
				t.Errorf("colorize(phase=%q, terminal=%v, unsettled=%v) = %q, want %q",
					c.phase, c.terminal, c.unsettled, got, c.want)
			}
		})
	}
}

// TestRenderUnsettledRowNotDimmed is the end-to-end form of E-1707 through the
// real render path: a terminal-status row whose worktree still diverges (◆) must
// carry NO dim escape, so the marker and the land it calls for stay visible; the
// same row once landed (◆ cleared) dims exactly as before. This is the regression
// that reproduced the bug (a `completed` + ◆ row rendered `\x1b[2m…◆…\x1b[0m`).
func TestRenderUnsettledRowNotDimmed(t *testing.T) {
	render := func(unsettled bool) string {
		rows := []monitor.SessionStatusRow{
			{ID: 1687, Title: "completed but unlanded", Status: "completed", Phase: "now",
				TypeSlug: "todo", IsFrom: true, Unsettled: unsettled},
		}
		var b strings.Builder
		renderTo(&b, rows, 1687, hintClaimBind, 90, true, hiddenOmit)
		return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")[1]
	}

	unlanded := render(true)
	if strings.Contains(unlanded, ansiDim) {
		t.Errorf("unsettled terminal row must not be dimmed (◆ would read as done): %q", unlanded)
	}
	if !strings.Contains(unlanded, "◆") {
		t.Errorf("unsettled row lost its ◆ marker: %q", unlanded)
	}
	if strings.Contains(unlanded, ansiBold) {
		t.Errorf("unsettled row must render at NORMAL weight, not bold: %q", unlanded)
	}

	landed := render(false)
	if !strings.HasPrefix(landed, ansiDim) || !strings.HasSuffix(landed, ansiReset) {
		t.Errorf("settled terminal row must still dim as before: %q", landed)
	}
	if strings.Contains(landed, "◆") {
		t.Errorf("settled row must carry no ◆: %q", landed)
	}
}

func TestTypeLetter(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"epic", "E"},
		{"bugfix", "F"},
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

// TestFocalExpansionSuppressesUncommitted pins E-1768: the focal-row anomaly
// expansion drops AnomalyUncommitted (uncommitted user files are the expected
// work-in-progress state of the task you are actively on, not an anomaly) while
// still surfacing the genuine kinds — detached HEAD, branch mismatch, prunable.
// The handoff-time `worktree check` surface is unaffected (it uses the shared
// monitor core directly, not this render path).
func TestFocalExpansionSuppressesUncommitted(t *testing.T) {
	prev := worktreeAnomalies
	t.Cleanup(func() { worktreeAnomalies = prev })
	worktreeAnomalies = func(projectID, taskID int64) []monitor.WorktreeAnomaly {
		return []monitor.WorktreeAnomaly{
			{Kind: monitor.AnomalyUncommitted, Detail: "3 uncommitted/untracked user files"},
			{Kind: monitor.AnomalyDetachedHead, Detail: "HEAD is detached"},
			{Kind: monitor.AnomalyBranchMismatch, Detail: "HEAD on foo, companion on bar"},
			{Kind: monitor.AnomalyPrunable, Detail: "git marks the worktree prunable"},
		}
	}

	rows := []monitor.SessionStatusRow{
		{ID: 1768, Title: "focal", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true, Unsettled: true},
	}
	var b strings.Builder
	renderTo(&b, rows, 1768, hintClaimBind, 90, false, hiddenOmit)
	out := b.String()

	// The suppressed kind must not appear in the focal detail lines...
	if strings.Contains(out, "uncommitted") {
		t.Errorf("focal expansion must suppress the uncommitted anomaly line:\n%s", out)
	}
	// ...while every genuine kind still surfaces, each as a ◆ detail line.
	for _, want := range []string{"detached-head", "branch-mismatch", "prunable"} {
		if !strings.Contains(out, want) {
			t.Errorf("focal expansion dropped genuine anomaly %q:\n%s", want, out)
		}
	}
	// Exactly the three genuine kinds render as expanded ◆ detail lines (the row's
	// own ◆ unsettled marker is inline in the row prefix, not a "      ◆ " detail line).
	if n := strings.Count(out, "      ◆ "); n != 3 {
		t.Errorf("want 3 expanded anomaly detail lines, got %d:\n%s", n, out)
	}
}

// TestFocalExpansionUncommittedOnlyIsSilent pins the pure false-positive case:
// when the ONLY anomaly is uncommitted user files, the focal row emits no
// expansion detail lines at all (E-1768).
func TestFocalExpansionUncommittedOnlyIsSilent(t *testing.T) {
	prev := worktreeAnomalies
	t.Cleanup(func() { worktreeAnomalies = prev })
	worktreeAnomalies = func(projectID, taskID int64) []monitor.WorktreeAnomaly {
		return []monitor.WorktreeAnomaly{
			{Kind: monitor.AnomalyUncommitted, Detail: "3 uncommitted/untracked user files"},
		}
	}

	rows := []monitor.SessionStatusRow{
		{ID: 1768, Title: "focal", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true, Unsettled: true},
	}
	var b strings.Builder
	renderTo(&b, rows, 1768, hintClaimBind, 90, false, hiddenOmit)
	if n := strings.Count(b.String(), "      ◆ "); n != 0 {
		t.Errorf("uncommitted-only focal worktree must add no detail lines, got %d:\n%s", n, b.String())
	}
}
