package sessionprimecmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestRefusesOutsideAClaudeSession: priming is a session's claim about itself,
// so with no CLAUDE_CODE_SESSION_ID there is nobody to prime.
func TestRefusesOutsideAClaudeSession(t *testing.T) {
	var out bytes.Buffer
	ref, code := run(nil, "", &out)
	if ref == nil || code != 1 {
		t.Fatalf("refusal = %v, exit = %d; want a refusal, exit 1", ref, code)
	}
	if !strings.Contains(ref.Render(), "CLAUDE_CODE_SESSION_ID") {
		t.Errorf("refusal does not name the variable: %q", ref.Render())
	}
}

func TestRejectsArguments(t *testing.T) {
	var out bytes.Buffer
	if ref, code := run([]string{"ES-1"}, "sess", &out); ref == nil || code != 2 {
		t.Errorf("refusal = %v, exit = %d; want a refusal, exit 2", ref, code)
	}
}
