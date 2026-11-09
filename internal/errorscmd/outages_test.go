package errorscmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/outages"
)

func stubListOutages(t *testing.T, list []outages.Outage) *bool {
	t.Helper()
	var askedClosed bool
	prev := listOutages
	listOutages = func(includeClosed bool) ([]outages.Outage, error) {
		askedClosed = includeClosed
		if includeClosed {
			return list, nil
		}
		var open []outages.Outage
		for _, o := range list {
			if o.Open() {
				open = append(open, o)
			}
		}
		return open, nil
	}
	t.Cleanup(func() { listOutages = prev })
	return &askedClosed
}

// P20: open outages under their own heading; --all adds the closed ones.
func TestPrintOutages(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	stubListOutages(t, []outages.Outage{
		{Provider: "GitHub", Component: "Git Operations", Status: "partial_outage",
			OpenedBy: "main-sync: fetch origin", OpenedAt: now.Add(-2 * time.Hour)},
		{Provider: "Anthropic", Component: "Claude API (api.anthropic.com)", Status: "operational",
			OpenedBy: "a turn ended on an API error: overloaded",
			OpenedAt: now.Add(-30 * time.Hour), ClosedAt: now.Add(-29 * time.Hour)},
	})

	var open bytes.Buffer
	printOutages(&open, false, now)
	if !strings.HasPrefix(open.String(), "Outages\n") {
		t.Errorf("no heading:\n%s", open.String())
	}
	if !strings.Contains(open.String(), "GitHub Git Operations: Partial outage for 2 hrs") {
		t.Errorf("open outage line missing:\n%s", open.String())
	}
	if strings.Contains(open.String(), "Anthropic") {
		t.Errorf("a closed outage listed without --all:\n%s", open.String())
	}

	var all bytes.Buffer
	printOutages(&all, true, now)
	if !strings.Contains(all.String(), "Anthropic Claude API (api.anthropic.com): closed after 1 hr") {
		t.Errorf("--all lacks the closed outage:\n%s", all.String())
	}
}

func TestPrintOutages_NoneIsSilent(t *testing.T) {
	stubListOutages(t, nil)
	var buf bytes.Buffer
	printOutages(&buf, true, time.Now())
	if buf.Len() != 0 {
		t.Errorf("printed %q with no outages", buf.String())
	}
}
