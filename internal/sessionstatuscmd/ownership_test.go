package sessionstatuscmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// stubOwnership replaces the focus/ownership seam for one test. nil stubs it
// to a no-op, for tests that exercise other annotations without a database.
func stubOwnership(t *testing.T, fn func(rows []monitor.SessionStatusRow, viewer, focal int64) error) {
	t.Helper()
	prev := annotateOwnership
	t.Cleanup(func() { annotateOwnership = prev })
	if fn == nil {
		fn = func([]monitor.SessionStatusRow, int64, int64) error { return nil }
	}
	annotateOwnership = fn
}

// TestColumnFourMarkWidths: ◼︎ and ◫ take one column, like the unsettled marks
// they displace, so the id column never shifts (E-2188).
func TestColumnFourMarkWidths(t *testing.T) {
	for _, g := range []string{focusGlyph, duplicateGlyph} {
		if w := displayWidth(g); w != 1 {
			t.Errorf("display width of %q = %d, want 1", g, w)
		}
	}
}

// TestColumnFourMark: duplicate outranks focus, both displace the unsettled mark,
// and the board's own claimed task keeps its unsettled mark whatever it is.
func TestColumnFourMark(t *testing.T) {
	cases := []struct {
		name string
		row  monitor.SessionStatusRow
		want string
	}{
		{"plain row keeps unsettled", monitor.SessionStatusRow{Status: "ready", Unsettled: true, UnsettledKnown: true}, unsettledGlyph},
		{"focused row", monitor.SessionStatusRow{Status: "ready", UnsettledKnown: true, Focused: true}, focusGlyph},
		{"duplicate row", monitor.SessionStatusRow{Status: "ready", UnsettledKnown: true, DuplicateWork: true}, duplicateGlyph},
		{"duplicate outranks focus", monitor.SessionStatusRow{Status: "ready", UnsettledKnown: true, Focused: true, DuplicateWork: true}, duplicateGlyph},
		{"focused claimed task keeps unsettled", monitor.SessionStatusRow{Status: "underway", IsFocal: true, Unsettled: true, UnsettledKnown: true, Focused: true}, unsettledGlyph},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := columnFourMark(c.row); got != c.want {
				t.Errorf("columnFourMark = %q, want %q", got, c.want)
			}
		})
	}
}

// TestIDField: the highlight wraps the id only, the padding stays outside it,
// and colour off emits no escapes at all.
func TestIDField(t *testing.T) {
	focused := monitor.SessionStatusRow{ID: 42, Focused: true}
	dup := monitor.SessionStatusRow{ID: 42, Focused: true, DuplicateWork: true}

	if got := idField(focused, false); got != "E-42  " {
		t.Errorf("colour off: %q, want %q", got, "E-42  ")
	}
	if got, want := idField(focused, true), ansiInverse+"E-42"+ansiInverseOff+"  "; got != want {
		t.Errorf("focused: %q, want %q", got, want)
	}
	if got, want := idField(dup, true), ansiDuplicateID+"E-42"+ansiDuplicateOff+"  "; got != want {
		t.Errorf("duplicate: %q, want %q", got, want)
	}
	if got := idField(monitor.SessionStatusRow{ID: 1234567}, true); got != "E-1234567" {
		t.Errorf("long id: %q, want no padding and no escapes", got)
	}
}

// TestRender_FocusAndDuplicate renders a whole frame without colour: ◼︎ and ◫ in
// column 4 on the rows that carry them, not on the claimed task, a legend that
// documents both, and an id column that does not move.
func TestRender_FocusAndDuplicate(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 100, Title: "claimed", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true, UnsettledKnown: true, Focused: false},
		{ID: 150, Title: "being planned", Status: "unplanned", Phase: "now", TypeSlug: "todo", UnsettledKnown: true, Focused: true},
		{ID: 151, Title: "shared", Status: "ready", Phase: "now", TypeSlug: "todo", UnsettledKnown: true, DuplicateWork: true},
	}
	var b strings.Builder
	renderTo(&b, rows, 100, hintClaimBind, 200, false, hiddenOmit)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want legend + 3 rows, got:\n%s", b.String())
	}
	for _, e := range []string{focusGlyph + " focus", duplicateGlyph + " duplicate"} {
		if !strings.Contains(lines[0], e) {
			t.Errorf("legend %q must document %q", lines[0], e)
		}
	}
	wantCol4 := map[string]string{"E-100": notStartedGlyph, "E-150": focusGlyph, "E-151": duplicateGlyph}
	offset := -1
	for _, line := range lines[1:] {
		for id, glyph := range wantCol4 {
			i := strings.Index(line, id)
			if i < 0 {
				continue
			}
			if !strings.HasSuffix(line[:i], glyph) {
				t.Errorf("row %s: column 4 before %q is not %q: %q", id, id, glyph, line)
			}
			if w := displayWidth(line[:i]); offset < 0 {
				offset = w
			} else if w != offset {
				t.Errorf("row %s: id at column %d, others at %d", id, w, offset)
			}
		}
	}
}

// TestRender_ClaimedTaskFocusIsColourOnly: when the claimed task has focus, no
// row wears ◼︎ and the legend does not list it — the absence is the signal in
// plain text, and the claimed task's id is inverse in colour.
func TestRender_ClaimedTaskFocusIsColourOnly(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 100, Title: "claimed", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true, UnsettledKnown: true, Focused: true},
		{ID: 150, Title: "other", Status: "ready", Phase: "now", TypeSlug: "todo", UnsettledKnown: true},
	}
	var plain, colour strings.Builder
	renderTo(&plain, rows, 100, hintClaimBind, 200, false, hiddenOmit)
	if strings.Contains(plain.String(), focusGlyph) {
		t.Errorf("plain frame shows %q for a claimed-task focus:\n%s", focusGlyph, plain.String())
	}
	renderTo(&colour, rows, 100, hintClaimBind, 200, true, hiddenOmit)
	if !strings.Contains(colour.String(), ansiInverse+"E-100"+ansiInverseOff) {
		t.Errorf("colour frame does not highlight the focused claimed task:\n%q", colour.String())
	}
}

// TestApplyHiddenMode_FocusAndOwnership: focus overrides a manual hide (the row
// is shown and still wears ⊘); a row owned elsewhere is omitted in every mode
// and never counted in the hidden footer.
func TestApplyHiddenMode_FocusAndOwnership(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1},
		{ID: 2, Hidden: true},
		{ID: 3, Hidden: true, Focused: true},
		{ID: 4, OwnedElsewhere: true},
	}
	ids := func(rs []monitor.SessionStatusRow) []int64 {
		var out []int64
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	cases := []struct {
		hm         hiddenMode
		want       []int64
		suppressed int
	}{
		{hiddenOmit, []int64{1, 3}, 1},
		{hiddenShow, []int64{1, 2, 3}, 0},
		{hiddenOnly, []int64{2, 3}, 0},
	}
	for _, c := range cases {
		got, n := applyHiddenMode(rows, c.hm)
		if g := ids(got); len(g) != len(c.want) || !equalIDs(g, c.want) || n != c.suppressed {
			t.Errorf("mode %d: rows %v suppressed %d, want %v suppressed %d", c.hm, g, n, c.want, c.suppressed)
		}
	}
}

func equalIDs(a, b []int64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return len(a) == len(b)
}

// TestBuildLegend_Fit pins the three-way rule: normal when it fits, compact when
// only compact fits, and normal — wrapping — when neither does. Never truncated.
func TestBuildLegend_Fit(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{Status: "ready", UnsettledKnown: true, IsFocal: true},
		{Status: "ready", UnsettledKnown: true, Focused: true},
		{Status: "ready", UnsettledKnown: true, DuplicateWork: true},
	}
	entries := legendEntries(rows)
	normal := joinLegend(entries, " ", "  ")
	compact := joinLegend(entries, "", " ")
	nw, cw := displayWidth(normal), displayWidth(compact)
	if cw >= nw {
		t.Fatalf("compact (%d) is not narrower than normal (%d)", cw, nw)
	}

	cases := []struct {
		name string
		cols int
		want string
	}{
		{"normal fits exactly", nw, normal},
		{"one column short of normal", nw - 1, compact},
		{"compact fits exactly", cw, compact},
		{"neither fits: normal, wrapping", cw - 1, normal},
	}
	for _, c := range cases {
		if got := buildLegend(rows, c.cols); got != c.want {
			t.Errorf("%s (cols %d): %q, want %q", c.name, c.cols, got, c.want)
		}
	}
	if !strings.Contains(compact, "●this") || !strings.Contains(compact, focusGlyph+"focus") {
		t.Errorf("compact form %q is not <icon><label>", compact)
	}
}

// TestRenderJSON_CarriesFocus: --json states focus and ownership explicitly on
// every row, including the claimed task's focus the table leaves to colour, and
// keeps a row owned elsewhere that the table omits.
func TestRenderJSON_CarriesFocus(t *testing.T) {
	prevGather, prevHidden, prevRelation := gatherRows, annotateHidden, annotateRelation
	t.Cleanup(func() { gatherRows, annotateHidden, annotateRelation = prevGather, prevHidden, prevRelation })
	gatherRows = func(focal, parentSession, emittingSession int64, all bool) ([]monitor.SessionStatusRow, error) {
		return []monitor.SessionStatusRow{
			{ID: 100, Title: "claimed", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true},
			{ID: 150, Title: "elsewhere", Status: "ready", Phase: "now", TypeSlug: "todo"},
			{ID: 151, Title: "shared", Status: "ready", Phase: "now", TypeSlug: "todo"},
		}, nil
	}
	annotateHidden = func([]monitor.SessionStatusRow, int64) error { return nil }
	annotateRelation = func([]monitor.SessionStatusRow, int64) error { return nil }
	stubOwnership(t, func(rows []monitor.SessionStatusRow, viewer, focal int64) error {
		rows[0].Focused = true
		rows[1].OwnedElsewhere = true
		rows[2].DuplicateWork = true
		return nil
	})

	var b strings.Builder
	if err := renderJSON(&b, anchor{focal: 100, emittingSession: 7}, false); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	var frame jsonFrame
	if err := json.Unmarshal([]byte(b.String()), &frame); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b.String())
	}
	if frame.Focus != "E-100" {
		t.Errorf("focus = %q, want E-100", frame.Focus)
	}
	got := map[int64]jsonRow{}
	for _, r := range frame.Rows {
		got[r.ID] = r
	}
	if !got[100].Focused || !got[150].OwnedElsewhere || !got[151].DuplicateWork {
		t.Errorf("rows missing their verdicts: %+v", frame.Rows)
	}
	for _, key := range []string{`"focused"`, `"duplicate_work"`, `"owned_elsewhere"`} {
		if !strings.Contains(b.String(), key) {
			t.Errorf("JSON lacks %s", key)
		}
	}
}
