package sessionstatuscmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestDuplicatesNote_TerminalGate: same display rule as the supersession note
// (E-1956), for the same reason — a row closed as `obsolete` BECAUSE it
// duplicated another task reads as abandoned rather than folded in. Gating on a
// terminal status is also what leaves the DEFAULT view untouched.
func TestDuplicatesNote_TerminalGate(t *testing.T) {
	terminal := []string{"confirmed", "assumed", "completed", "declined", "obsolete"}
	for _, s := range terminal {
		r := monitor.SessionStatusRow{Status: s, Duplicates: []int64{1086}}
		if got := duplicatesNote(r); !strings.Contains(got, "E-1086") {
			t.Errorf("status %q: duplicatesNote = %q, want the id", s, got)
		}
	}
	open := []string{"untriaged", "unplanned", "submitted", "ready", "underway",
		"unverified", "blocked", "revisit"}
	for _, s := range open {
		r := monitor.SessionStatusRow{Status: s, Duplicates: []int64{1086}}
		if got := duplicatesNote(r); got != "" {
			t.Errorf("status %q: duplicatesNote = %q, want empty", s, got)
		}
	}
}

func TestDuplicatesNote_Formatting(t *testing.T) {
	cases := []struct {
		name string
		row  monitor.SessionStatusRow
		want string
	}{
		{"none", monitor.SessionStatusRow{Status: "obsolete"}, ""},
		{"one", monitor.SessionStatusRow{Status: "obsolete", Duplicates: []int64{1086}},
			"  (duplicates E-1086)"},
		{"two", monitor.SessionStatusRow{Status: "obsolete", Duplicates: []int64{7, 9}},
			"  (duplicates E-7, E-9)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := duplicatesNote(c.row); got != c.want {
				t.Errorf("duplicatesNote = %q, want %q", got, c.want)
			}
		})
	}
}

// TestStatusNotesCompose: a task can be both superseded and a duplicate. Neither
// note wins — dropping one would lose a fact the row is the only place to see.
func TestStatusNotesCompose(t *testing.T) {
	r := monitor.SessionStatusRow{
		Status: "obsolete", ReplacedBy: []int64{1953}, Duplicates: []int64{1086},
	}
	got := statusNotes(r)
	if !strings.Contains(got, "E-1953") || !strings.Contains(got, "E-1086") {
		t.Errorf("statusNotes = %q, want both ids", got)
	}
	if strings.Index(got, "replaced by") > strings.Index(got, "duplicates") {
		t.Errorf("statusNotes = %q, want replaced-by first", got)
	}
}

// TestRenderDuplicatesNote: the note reaches the drawn row.
func TestRenderDuplicatesNote(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 986, Title: "Add post-worktree-create hook", Status: "obsolete",
			Phase: "now", TypeSlug: "todo", IsFocal: true, Duplicates: []int64{1086}},
		{ID: 1085, Title: "An unrelated closed task", Status: "obsolete",
			Phase: "now", TypeSlug: "todo"},
	}
	var b strings.Builder
	renderTo(&b, rows, 986, hintClaimBind, 120, false, hiddenOmit)
	out := b.String()
	if !strings.Contains(out, "(duplicates E-1086)") {
		t.Errorf("rendered frame lacks the note:\n%s", out)
	}
	// A closed row that duplicates nothing must not sprout one.
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "E-1085") && strings.Contains(ln, "duplicates") {
			t.Errorf("non-duplicate row carries a note: %q", ln)
		}
	}
}

// TestJSONDuplicatesUngated: --json is data, so it carries the relation even
// where the table's terminal gate suppresses it, and the key is always present
// so its absence never has to be read as "not a duplicate".
func TestJSONDuplicatesUngated(t *testing.T) {
	orig := gatherRows
	t.Cleanup(func() { gatherRows = orig })
	gatherRows = func(focal, parentSession, emittingSession int64, all bool) ([]monitor.SessionStatusRow, error) {
		return []monitor.SessionStatusRow{
			// `underway` is NOT terminal — the table would draw no note here.
			{ID: 986, Title: "t", Status: "underway", Phase: "now",
				TypeSlug: "todo", Duplicates: []int64{1086}},
			{ID: 1086, Title: "t", Status: "underway", Phase: "now", TypeSlug: "todo"},
		}, nil
	}
	origHidden := annotateHidden
	t.Cleanup(func() { annotateHidden = origHidden })
	annotateHidden = func(rows []monitor.SessionStatusRow, viewer int64) error { return nil }

	var b strings.Builder
	if err := renderJSON(&b, anchor{focal: 986}, true); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	var frame struct {
		Rows []struct {
			ID         int64    `json:"id"`
			Duplicates []string `json:"duplicates"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(b.String()), &frame); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b.String())
	}
	byID := map[int64][]string{}
	for _, r := range frame.Rows {
		byID[r.ID] = r.Duplicates
	}
	if got := byID[986]; len(got) != 1 || got[0] != "E-1086" {
		t.Errorf("row 986 duplicates = %v, want [E-1086]", got)
	}
	// Present-but-empty, never absent.
	if got, ok := byID[1086]; !ok || len(got) != 0 {
		t.Errorf("row 1086 duplicates = %v (present=%v), want an empty list", got, ok)
	}
	if !strings.Contains(b.String(), `"duplicates": []`) {
		t.Errorf("empty duplicates marshalled as null, not []:\n%s", b.String())
	}
}
