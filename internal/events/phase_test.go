package events_test

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

func TestValidatePhase_AcceptsKnownValues(t *testing.T) {
	for _, phase := range []string{"now", "next", "later", "maybe"} {
		if err := events.ValidatePhase(phase); err != nil {
			t.Errorf("ValidatePhase(%q) returned error: %v", phase, err)
		}
	}
}

func TestValidatePhase_RejectsUnknown(t *testing.T) {
	for _, phase := range []string{"", "someday", "blocked", "now ", "NOW", "Now"} {
		err := events.ValidatePhase(phase)
		if err == nil {
			t.Errorf("ValidatePhase(%q) accepted invalid value", phase)
			continue
		}
		if !strings.Contains(err.Error(), "invalid phase") {
			t.Errorf("ValidatePhase(%q) error did not mention 'invalid phase': %v", phase, err)
		}
	}
}

func TestValidPhases_HasExactlyExpectedKeys(t *testing.T) {
	want := map[string]bool{"now": true, "next": true, "later": true, "maybe": true}
	if len(events.ValidPhases) != len(want) {
		t.Errorf("ValidPhases has %d entries, want %d", len(events.ValidPhases), len(want))
	}
	for k := range want {
		if !events.ValidPhases[k] {
			t.Errorf("ValidPhases missing %q", k)
		}
	}
}
