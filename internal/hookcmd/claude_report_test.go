package hookcmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTaskReportRe pins the Arm 1 detector: it fires only when `endless task
// report` is actually RUN — at a command position (start / after a separator),
// optionally path-prefixed — and never when the phrase appears as an argument
// (a git commit message, an echo, a grep pattern), which is common in this repo
// and would otherwise misfire the reinforcement. Detection lives in the binary,
// not a settings.json `if:` matcher, so this regex is the whole contract.
func TestTaskReportRe(t *testing.T) {
	cases := []struct {
		name  string
		cmd   string
		match bool
	}{
		{"plain", "endless task report E-1803 --db main", true},
		{"no id", "endless task report", true},
		{"redirect suffix", "endless task report E-1803 --db main 2>&1", true},
		{"absolute path prefix", "/usr/local/bin/endless task report E-9 --db main", true},
		{"extra whitespace", "endless   task    report  E-5", true},
		{"leading indent", "   endless task report E-5", true},
		{"chained after esu", "esu && endless task report E-1803 --db main", true},
		{"chained after semicolon", "cd /tmp; endless task report E-5", true},
		{"second line of a script", "esu\nendless task report E-5", true},

		// The phrase as an argument — must NOT fire the reinforcement.
		{"git commit -m mention", `git commit -m "E-1803: gate endless task report channel"`, false},
		{"echo mention", `echo "run endless task report next"`, false},
		{"grep pattern mention", `grep -n "endless task report" docs/guide/tasks.md`, false},
		{"substring inside word", "myendless task report E-5", false},

		{"unrelated task verb", "endless task show E-5", false},
		{"update not report", "endless task update E-5 --status unverified", false},
		{"not endless", "task report E-5", false},
		{"git word report", "git log --format=report", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := taskReportRe.MatchString(c.cmd); got != c.match {
				t.Errorf("MatchString(%q) = %v, want %v", c.cmd, got, c.match)
			}
		})
	}
}

// TestReportRelayResponse_Shape pins the structural contract the live-Claude
// reinforcement depends on: PostToolUse additionalContext must be nested under
// hookSpecificOutput with the event name, and the instruction must say relay
// verbatim / add nothing. This is what the verify script asserts fires.
func TestReportRelayResponse_Shape(t *testing.T) {
	b, err := json.Marshal(reportRelayResponse())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hso, ok := got["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hookSpecificOutput missing/wrong type: %v", got["hookSpecificOutput"])
	}
	if hso["hookEventName"] != "PostToolUse" {
		t.Errorf("hookEventName = %v, want PostToolUse", hso["hookEventName"])
	}
	ac, _ := hso["additionalContext"].(string)
	for _, want := range []string{"verbatim", "add nothing", "task report"} {
		if !strings.Contains(ac, want) {
			t.Errorf("additionalContext missing %q:\n%s", want, ac)
		}
	}
}

// TestComposeSessionStartContext pins the Arm 2 delivery: the coverage rule is
// always present at SessionStart — whether or not the one-shot task list has
// content — and the task list, when present, is preserved alongside it.
func TestComposeSessionStartContext(t *testing.T) {
	// Rule always delivered, even when the task-list context is empty (already
	// injected on an earlier start).
	if got := composeSessionStartContext(""); got != reportChannelRule {
		t.Errorf("empty task list = %q, want just the rule", got)
	}

	combined := composeSessionStartContext("Active tasks:\n  E-1 foo")
	if !strings.Contains(combined, reportChannelRule) {
		t.Errorf("combined dropped the rule:\n%s", combined)
	}
	if !strings.Contains(combined, "E-1 foo") {
		t.Errorf("combined dropped the task list:\n%s", combined)
	}

	// The rule is functional, not an enumerated checklist of situations.
	for _, want := range []string{"task report", "function", "cannot derive"} {
		if !strings.Contains(reportChannelRule, want) {
			t.Errorf("reportChannelRule missing %q:\n%s", want, reportChannelRule)
		}
	}
}
