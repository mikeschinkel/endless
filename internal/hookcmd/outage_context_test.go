package hookcmd

import (
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/outages"
	"github.com/mikeschinkel/endless/internal/sessionstate"
)

func stubOpenOutages(t *testing.T, open []outages.Outage) {
	t.Helper()
	prev := openOutages
	openOutages = func() ([]outages.Outage, error) { return open, nil }
	t.Cleanup(func() { openOutages = prev })
}

// P21: an agent's turn carries each open outage in its context.
func TestUserPromptSubmit_CarriesOpenOutages(t *testing.T) {
	_, projectPath := stopFailureEnv(t, sessionstate.Working)
	stubOpenOutages(t, []outages.Outage{{
		Provider: "GitHub", Component: "Git Operations", Status: "partial_outage",
		OpenedAt: time.Now().Add(-90 * time.Minute),
	}})

	out := captureStdout(t, func() {
		err := handleUserPromptSubmit(1, claudePayload{
			SessionID: "sess-A", CWD: projectPath, EventName: "UserPromptSubmit",
		}, "")
		if err != nil {
			t.Errorf("handleUserPromptSubmit: %v", err)
		}
	})
	for _, want := range []string{"Outage: GitHub", "Git Operations", "not a local problem"} {
		if !strings.Contains(out, want) {
			t.Errorf("context lacks %q:\n%s", want, out)
		}
	}
}

func TestOutageContext_NoneOpenIsSilent(t *testing.T) {
	stubOpenOutages(t, nil)
	if got := outageContext(); got != "" {
		t.Errorf("outageContext = %q, want empty", got)
	}
}
