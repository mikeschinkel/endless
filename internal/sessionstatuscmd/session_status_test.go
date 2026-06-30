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
		{"in_flight wins over status", monitor.SessionStatusRow{InFlight: true, Status: "ready"}, actDoing},
		{"ready with no plan still do", monitor.SessionStatusRow{Status: "ready", HasText: false}, actDo},
		{"unplanned is plan", monitor.SessionStatusRow{Status: "unplanned"}, actPlan},
		{"needs_plan is plan", monitor.SessionStatusRow{Status: "needs_plan"}, actPlan},
		{"revisit folds into plan", monitor.SessionStatusRow{Status: "revisit"}, actPlan},
		{"verify", monitor.SessionStatusRow{Status: "verify"}, actVerify},
		{"unverified is verify", monitor.SessionStatusRow{Status: "unverified"}, actVerify},
		{"underway is orphan", monitor.SessionStatusRow{Status: "underway"}, actOrphan},
		{"unknown is other", monitor.SessionStatusRow{Status: "blocked"}, actOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.row); got != c.want {
				t.Errorf("classify(%s) = %d, want %d", c.name, got, c.want)
			}
		})
	}
}

func TestSortRows(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 5, Status: "ready", Phase: "now"},        // do
		{ID: 1, IsParent: true, Phase: "later"},       // parent
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
	// this(9) < parent(1) < doing(3) < do(5) < plan/urgent(7) < plan/now(8)
	want := []int64{9, 1, 3, 5, 7, 8}
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

func TestRenderEmptyFocal(t *testing.T) {
	var b strings.Builder
	renderTo(&b, nil, 0, 90, false)
	out := b.String()
	if !strings.Contains(out, legend) {
		t.Errorf("legend missing from empty render:\n%s", out)
	}
	if !strings.Contains(out, "no active task") {
		t.Errorf("empty hint missing:\n%s", out)
	}
}

func TestRenderColumnsAndTruncation(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1465, Title: "Implement endless session next briefing read command", Status: "underway", Phase: "now", TypeSlug: "task", IsFocal: true},
		{ID: 1461, Title: "Add endless session next prospective remaining-work briefing", Status: "ready", Phase: "now", TypeSlug: "epic", IsParent: true},
	}
	var b strings.Builder
	renderTo(&b, rows, 1465, 40, false)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	// legend + 2 rows
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (legend + 2 rows), got %d:\n%s", len(lines), b.String())
	}
	if !strings.HasPrefix(lines[1], "● T E-1465 1 ") {
		t.Errorf("focal row prefix wrong: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "↑ E E-1461 1 ") {
		t.Errorf("parent row prefix wrong: %q", lines[2])
	}
	// Truncated to terminal width (40): no row should exceed it in display width.
	for _, ln := range lines[1:] {
		if w := displayWidth(ln); w > 40 {
			t.Errorf("row exceeds width 40 (got %d): %q", w, ln)
		}
	}
}
