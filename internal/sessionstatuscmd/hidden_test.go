package sessionstatuscmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// hiddenRows is the fixture for the three modes: one ordinary row and one the
// viewing session has hidden. Both are plain `ready` tasks so the only thing
// separating them in the output is the hide.
func hiddenRows() []monitor.SessionStatusRow {
	return []monitor.SessionStatusRow{
		{ID: 1914, Title: "Rework session list output", Status: "ready",
			Phase: "now", TypeSlug: "todo"},
		{ID: 1912, Title: "Design the session list rework", Status: "ready",
			Phase: "now", TypeSlug: "todo", Hidden: true, HiddenAt: "2026-08-07T04:00:00"},
	}
}

func renderHidden(t *testing.T, hm hiddenMode) string {
	t.Helper()
	var b strings.Builder
	renderTo(&b, hiddenRows(), 1914, hintClaimBind, 90, false, hm)
	return b.String()
}

// TestHiddenGlyphWidth pins ⊘ at a single terminal column, the same invariant
// TestActionIcons enforces for the action glyphs. The hidden column is a
// fixed-width slot in the row prefix; a double-width glyph would shove every
// following column right on hidden rows only — exactly the ragged-output bug
// E-1914 exists to fix on the other surface.
func TestHiddenGlyphWidth(t *testing.T) {
	if w := runewidth.StringWidth(hiddenGlyph); w != 1 {
		t.Errorf("hiddenGlyph %q measures %d columns, want 1", hiddenGlyph, w)
	}
}

// TestRenderHidden_DefaultOmitsWithFooter is the load-bearing case: by default a
// hidden row is gone from the table, but the footer states the count and names
// the flag that reveals it. A hidden task must never disappear without a trace.
func TestRenderHidden_DefaultOmitsWithFooter(t *testing.T) {
	out := renderHidden(t, hiddenOmit)

	if strings.Contains(out, "E-1912") {
		t.Errorf("default mode must omit the hidden row:\n%s", out)
	}
	if !strings.Contains(out, "E-1914") {
		t.Errorf("default mode must keep the visible row:\n%s", out)
	}
	if !strings.Contains(out, "… 1 hidden (--show-hidden)") {
		t.Errorf("footer missing; a hidden row must leave a trace:\n%s", out)
	}
	// The ⊘ column costs nothing when no rendered row wears it — the default
	// view is byte-identical to the pre-E-1914 render.
	if strings.Contains(out, hiddenGlyph+" ") && !strings.Contains(out, hiddenGlyph+" hidden") {
		t.Errorf("default mode must not draw the ⊘ column:\n%s", out)
	}
}

// TestRenderHidden_NoFooterWhenNothingHidden pins the negative: an ordinary
// session pays no footer, no column, and no legend entry.
func TestRenderHidden_NoFooterWhenNothingHidden(t *testing.T) {
	rows := hiddenRows()
	rows[1].Hidden = false
	rows[1].HiddenAt = ""
	var b strings.Builder
	renderTo(&b, rows, 1914, hintClaimBind, 90, false, hiddenOmit)
	out := b.String()

	if strings.Contains(out, "hidden") {
		t.Errorf("nothing hidden → no footer and no legend entry:\n%s", out)
	}
	if !strings.Contains(out, "E-1912") || !strings.Contains(out, "E-1914") {
		t.Errorf("both rows must render:\n%s", out)
	}
}

// TestRenderHidden_ShowHiddenMarksRows: --show-hidden renders everything with the
// hidden row marked, and prints NO footer — nothing is missing, so there is
// nothing to account for.
func TestRenderHidden_ShowHiddenMarksRows(t *testing.T) {
	out := renderHidden(t, hiddenShow)

	if !strings.Contains(out, "E-1912") || !strings.Contains(out, "E-1914") {
		t.Errorf("--show-hidden must render both rows:\n%s", out)
	}
	if strings.Contains(out, "(--show-hidden)") {
		t.Errorf("--show-hidden must not print the omission footer:\n%s", out)
	}
	if !strings.Contains(out, hiddenGlyph+" hidden") {
		t.Errorf("legend must document ⊘ when a hidden row renders:\n%s", out)
	}

	// The marker lands on the hidden row and only on it.
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "E-1912"):
			if !strings.Contains(line, hiddenGlyph) {
				t.Errorf("hidden row is unmarked: %q", line)
			}
		case strings.Contains(line, "E-1914"):
			if strings.Contains(line, hiddenGlyph) {
				t.Errorf("visible row wrongly marked hidden: %q", line)
			}
		}
	}
}

// TestRenderHidden_OnlyHidden: --only-hidden is the discovery path for unhiding
// without knowing ids, so it renders the hidden set ALONE.
func TestRenderHidden_OnlyHidden(t *testing.T) {
	out := renderHidden(t, hiddenOnly)

	if !strings.Contains(out, "E-1912") {
		t.Errorf("--only-hidden must render the hidden row:\n%s", out)
	}
	if strings.Contains(out, "E-1914") {
		t.Errorf("--only-hidden must omit the visible row:\n%s", out)
	}
	if strings.Contains(out, "(--show-hidden)") {
		t.Errorf("--only-hidden must not print the omission footer:\n%s", out)
	}
}

// TestRenderHidden_OnlyHiddenEmpty: `--only-hidden` with nothing hidden says so,
// rather than falling through to the claim/bind hint — which would be wrong
// advice (the session has work; none of it is suppressed).
func TestRenderHidden_OnlyHiddenEmpty(t *testing.T) {
	rows := hiddenRows()
	rows[1].Hidden = false
	var b strings.Builder
	renderTo(&b, rows, 1914, hintClaimBind, 90, false, hiddenOnly)
	out := b.String()

	if !strings.Contains(out, "nothing hidden") {
		t.Errorf("--only-hidden with an empty hidden set must say so:\n%s", out)
	}
	if strings.Contains(out, hintClaimBind) {
		t.Errorf("--only-hidden must not fall through to the claim/bind hint:\n%s", out)
	}
}

// TestRenderHidden_AllRowsHiddenStillFooters is the edge the footer contract most
// depends on: when EVERY row is suppressed the footer is the entire frame, so an
// "empty result" shortcut must not swallow it.
func TestRenderHidden_AllRowsHiddenStillFooters(t *testing.T) {
	rows := hiddenRows()
	rows[0].Hidden = true
	var b strings.Builder
	renderTo(&b, rows, 1914, hintClaimBind, 90, false, hiddenOmit)
	out := b.String()

	if !strings.Contains(out, "… 2 hidden (--show-hidden)") {
		t.Errorf("an all-hidden frame must still report the count:\n%s", out)
	}
	if strings.Contains(out, hintClaimBind) {
		t.Errorf("all-hidden is not the same as no-work; the claim/bind hint is wrong here:\n%s", out)
	}
}

// TestRenderHidden_ColumnAlignment proves the ⊘ column is a fixed-width slot: the
// title starts at the same offset on a hidden row as on a visible one. This is
// the same alignment invariant the session-list icon rework enforces.
func TestRenderHidden_ColumnAlignment(t *testing.T) {
	out := renderHidden(t, hiddenShow)

	offsets := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		for id, title := range map[string]string{
			"E-1912": "Design the session list rework",
			"E-1914": "Rework session list output",
		} {
			if strings.Contains(line, id) {
				offsets[id] = runewidth.StringWidth(line[:strings.Index(line, title)])
			}
		}
	}
	if len(offsets) != 2 {
		t.Fatalf("expected both rows to render, got offsets %v\n%s", offsets, out)
	}
	if offsets["E-1912"] != offsets["E-1914"] {
		t.Errorf("title column is ragged: hidden row starts at %d, visible at %d\n%s",
			offsets["E-1912"], offsets["E-1914"], out)
	}
}

// TestApplyHiddenMode covers the partition rule directly, including that it never
// mutates its input — --json renders the same annotated rows unfiltered, so a
// destructive filter would silently drop rows from the data surface.
func TestApplyHiddenMode(t *testing.T) {
	cases := []struct {
		mode           hiddenMode
		wantIDs        []int64
		wantSuppressed int
	}{
		{hiddenOmit, []int64{1914}, 1},
		{hiddenShow, []int64{1914, 1912}, 0},
		{hiddenOnly, []int64{1912}, 0},
	}
	for _, c := range cases {
		in := hiddenRows()
		got, suppressed := applyHiddenMode(in, c.mode)
		if len(in) != 2 {
			t.Errorf("mode %d mutated the input slice", c.mode)
		}
		if suppressed != c.wantSuppressed {
			t.Errorf("mode %d suppressed %d, want %d", c.mode, suppressed, c.wantSuppressed)
		}
		if len(got) != len(c.wantIDs) {
			t.Fatalf("mode %d returned %d rows, want %d", c.mode, len(got), len(c.wantIDs))
		}
		for i, want := range c.wantIDs {
			if got[i].ID != want {
				t.Errorf("mode %d row %d = E-%d, want E-%d", c.mode, i, got[i].ID, want)
			}
		}
	}
}

// TestRenderJSON_CarriesHiddenState pins the --json contract: EVERY row is
// emitted with its hidden state attached (no display filtering), and the frame
// names the viewer the hidden flags belong to — without which `hidden: true`
// would be an unattributed claim.
func TestRenderJSON_CarriesHiddenState(t *testing.T) {
	prevGather, prevAnnotate, prevRelation := gatherRows, annotateHidden, annotateRelation
	t.Cleanup(func() {
		gatherRows, annotateHidden, annotateRelation = prevGather, prevAnnotate, prevRelation
	})
	gatherRows = func(focal, parentSession, emittingSession int64, all bool) ([]monitor.SessionStatusRow, error) {
		return hiddenRows(), nil
	}
	annotateHidden = func(rows []monitor.SessionStatusRow, viewer int64) error { return nil }
	annotateRelation = func(rows []monitor.SessionStatusRow, viewer int64) error { return nil }

	var b strings.Builder
	if err := renderJSON(&b, anchor{focal: 1914, emittingSession: 42}, false); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}

	var frame struct {
		Focal         int64 `json:"focal"`
		ViewerSession int64 `json:"viewer_session"`
		Rows          []struct {
			ID       int64  `json:"id"`
			Hidden   bool   `json:"hidden"`
			HiddenAt string `json:"hidden_at"`
			Action   string `json:"action"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(b.String()), &frame); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b.String())
	}
	if frame.Focal != 1914 || frame.ViewerSession != 42 {
		t.Errorf("frame = focal %d viewer %d, want 1914 / 42", frame.Focal, frame.ViewerSession)
	}
	if len(frame.Rows) != 2 {
		t.Fatalf("--json must emit every row unfiltered, got %d:\n%s", len(frame.Rows), b.String())
	}
	byID := map[int64]bool{}
	for _, r := range frame.Rows {
		byID[r.ID] = r.Hidden
		if r.Action == "" {
			t.Errorf("row E-%d has no action slug", r.ID)
		}
		if r.Hidden && r.HiddenAt == "" {
			t.Errorf("hidden row E-%d carries no hidden_at", r.ID)
		}
	}
	if byID[1914] || !byID[1912] {
		t.Errorf("hidden flags wrong: %v", byID)
	}
}
