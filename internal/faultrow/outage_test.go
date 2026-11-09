package faultrow

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/outages"
)

func stubOutages(t *testing.T, open []outages.Outage) {
	t.Helper()
	prev := openOutages
	openOutages = func() ([]outages.Outage, error) { return open, nil }
	t.Cleanup(func() { openOutages = prev })
}

// P4: each open outage is its own line, with no fault row, no warning colour
// and no action hint — and with no fault open there is no fault row above it.
func TestRender_OpenOutagesAreTheirOwnCalmLines(t *testing.T) {
	stubOutages(t, []outages.Outage{
		{Provider: "GitHub", Status: "partial_outage", OpenedAt: time.Now().Add(-70 * time.Minute)},
		{Provider: "Anthropic", Status: "degraded_performance", OpenedAt: time.Now().Add(-5 * time.Minute)},
	})

	var buf bytes.Buffer
	Render(&buf, 120, true, faults.AllProjects)
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered %d lines, want 2:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "GitHub") || !strings.Contains(lines[1], "Anthropic") {
		t.Errorf("lines = %q", lines)
	}
	for _, bad := range []string{Hint, rowWarning, rowError, chipWarning} {
		if strings.Contains(out, bad) {
			t.Errorf("outage lines carry %q, which belongs to the fault row", bad)
		}
	}
}

func TestRender_NoOutagesNoLines(t *testing.T) {
	stubOutages(t, nil)
	var buf bytes.Buffer
	Render(&buf, 120, false, faults.AllProjects)
	if buf.Len() != 0 {
		t.Errorf("rendered %q with nothing open", buf.String())
	}
}
