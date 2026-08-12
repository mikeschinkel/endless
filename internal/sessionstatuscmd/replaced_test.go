package sessionstatuscmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestReplacedByNote_TerminalGate pins the display rule: the supersession rides
// with a TERMINAL status and nothing else. That is where ⇥ closed / ⏚ landed
// otherwise reads as "abandoned" rather than "handed on" — and gating there is
// also what leaves the DEFAULT view (which carries no terminal rows) untouched.
func TestReplacedByNote_TerminalGate(t *testing.T) {
	terminal := []string{"confirmed", "assumed", "completed", "declined", "obsolete"}
	for _, s := range terminal {
		r := monitor.SessionStatusRow{Status: s, ReplacedBy: []int64{1953}}
		if got := replacedByNote(r); !strings.Contains(got, "E-1953") {
			t.Errorf("status %q: replacedByNote = %q, want the id", s, got)
		}
	}
	open := []string{"untriaged", "unplanned", "submitted", "ready", "underway",
		"unverified", "blocked", "revisit"}
	for _, s := range open {
		r := monitor.SessionStatusRow{Status: s, ReplacedBy: []int64{1953}}
		if got := replacedByNote(r); got != "" {
			t.Errorf("status %q: replacedByNote = %q, want empty", s, got)
		}
	}
}

func TestReplacedByNote_Formatting(t *testing.T) {
	cases := []struct {
		name string
		row  monitor.SessionStatusRow
		want string
	}{
		{"none", monitor.SessionStatusRow{Status: "assumed"}, ""},
		{"one", monitor.SessionStatusRow{Status: "assumed", ReplacedBy: []int64{1953}},
			"  (replaced by E-1953)"},
		{"two", monitor.SessionStatusRow{Status: "obsolete", ReplacedBy: []int64{7, 9}},
			"  (replaced by E-7, E-9)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := replacedByNote(c.row); got != c.want {
				t.Errorf("replacedByNote = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRenderReplacedByNote: the note reaches the drawn row, appended after the
// title.
func TestRenderReplacedByNote(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1901, Title: "Enforce verbatim task report relay", Status: "assumed",
			Phase: "now", TypeSlug: "todo", IsFocal: true, ReplacedBy: []int64{1953}},
		{ID: 1952, Title: "Redesign task report around partial success",
			Status: "completed", Phase: "now", TypeSlug: "todo"},
	}
	var b strings.Builder
	renderTo(&b, rows, 1901, hintClaimBind, 120, false, hiddenOmit)
	out := b.String()
	if !strings.Contains(out, "(replaced by E-1953)") {
		t.Errorf("rendered frame lacks the note:\n%s", out)
	}
	// The unreplaced terminal row must not sprout one.
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "E-1952") && strings.Contains(ln, "replaced by") {
			t.Errorf("unreplaced row carries a note: %q", ln)
		}
	}
}

// TestRenderReplacedByNoteRespectsWidth is the point of charging the note to the
// title's budget rather than appending past it: a narrow terminal must still get
// rows that fit, with the note intact and the TITLE absorbing the loss.
func TestRenderReplacedByNoteRespectsWidth(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1901, Title: strings.Repeat("long title ", 12), Status: "assumed",
			Phase: "now", TypeSlug: "todo", IsFocal: true, ReplacedBy: []int64{1953}},
	}
	const cols = 60
	var b strings.Builder
	renderTo(&b, rows, 1901, hintClaimBind, cols, false, hiddenOmit)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	row := lines[len(lines)-1]
	if w := runewidth.StringWidth(row); w > cols {
		t.Errorf("row width %d exceeds cols %d: %q", w, cols, row)
	}
	if !strings.HasSuffix(row, "(replaced by E-1953)") {
		t.Errorf("note was truncated away: %q", row)
	}
	if !strings.Contains(row, "…") {
		t.Errorf("title should have absorbed the loss: %q", row)
	}
}

// TestJSONReplacedByUngated: --json is data, not a rendering, so it carries the
// relation even where the table's terminal gate would suppress it — and the key
// is always present so its absence never has to be read as "not replaced".
func TestJSONReplacedByUngated(t *testing.T) {
	orig := gatherRows
	t.Cleanup(func() { gatherRows = orig })
	gatherRows = func(focal, parentSession, emittingSession int64, all bool) ([]monitor.SessionStatusRow, error) {
		return []monitor.SessionStatusRow{
			// `underway` is NOT terminal — the table would draw no note here.
			{ID: 1901, Title: "t", Status: "underway", Phase: "now",
				TypeSlug: "todo", ReplacedBy: []int64{1953}},
			{ID: 1952, Title: "t", Status: "assumed", Phase: "now", TypeSlug: "todo"},
		}, nil
	}
	origHidden := annotateHidden
	t.Cleanup(func() { annotateHidden = origHidden })
	annotateHidden = func(rows []monitor.SessionStatusRow, viewer int64) error { return nil }

	var b strings.Builder
	if err := renderJSON(&b, anchor{focal: 1901}, true); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	var frame struct {
		Rows []struct {
			ID         int64    `json:"id"`
			ReplacedBy []string `json:"replaced_by"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(b.String()), &frame); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b.String())
	}
	byID := map[int64][]string{}
	for _, r := range frame.Rows {
		byID[r.ID] = r.ReplacedBy
	}
	if got := byID[1901]; len(got) != 1 || got[0] != "E-1953" {
		t.Errorf("row 1901 replaced_by = %v, want [E-1953]", got)
	}
	if got, ok := byID[1952]; !ok || got == nil || len(got) != 0 {
		t.Errorf("row 1952 replaced_by = %v, want an empty (non-null) list", got)
	}
	if !strings.Contains(b.String(), `"replaced_by": []`) {
		t.Errorf("empty case should marshal as [], not null:\n%s", b.String())
	}
}
