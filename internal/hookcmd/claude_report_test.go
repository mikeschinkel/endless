package hookcmd

import (
	"encoding/json"
	"os"
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
// hookSpecificOutput with the event name, and the instruction must state the
// VERBATIM contract (E-1953) plus the appeal that makes it resistible.
//
// The must-not-contain half is the load-bearing part. Every retired contract
// this surface has carried — "append the block", the separator, "Nothing to
// report." — would, if reintroduced, instruct the exact opposite of what the
// command now prints, and an agent obeying either one disobeys the other.
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
	for _, want := range []string{
		"task report", "verbatim", "entire final message", "ONE appeal", "--raw",
	} {
		if !strings.Contains(ac, want) {
			t.Errorf("additionalContext missing %q:\n%s", want, ac)
		}
	}
	for _, unwanted := range []string{
		"APPEND", "ENDLESS REPORT", "Nothing to report.", "in your own words",
	} {
		if strings.Contains(ac, unwanted) {
			t.Errorf("additionalContext still carries a retired contract %q:\n%s", unwanted, ac)
		}
	}
}

// TestReportChannelRule_StatesTheMechanicNotAStandard pins the SessionStart
// rule's shape (E-1953).
//
// Every previous version of this rule tried to teach the agent what deserves to
// be said, and every one failed the same way — the agent judged its own output
// while writing it, and judged generously. The minimizer is a second party, so
// the rule only has to get the whole draft to it. Re-introducing a quality
// standard here would put the agent back in the seat the minimizer took.
func TestReportChannelRule_StatesTheMechanicNotAStandard(t *testing.T) {
	for _, want := range []string{
		"--draft-file", "verbatim", "optional", "--raw",
	} {
		if !strings.Contains(reportChannelRule, want) {
			t.Errorf("reportChannelRule missing %q:\n%s", want, reportChannelRule)
		}
	}
	// The retired self-judgment standard, in the exact words it last used, plus
	// the "do not pre-summarize" criteria E-2030 retired. Those told the agent
	// to hand over bloat it had already recognized as bloat, on the reasoning
	// that the minimizer should be the one to cut it. Which agent cuts does not
	// matter — the outcome the user receives does — so the rule must not grow
	// them back.
	for _, unwanted := range []string{
		"cannot derive", "XOR", "append", "--json",
		"pre-summarize", "in full",
	} {
		if strings.Contains(reportChannelRule, unwanted) {
			t.Errorf("reportChannelRule still asks the agent to judge its own output (%q):\n%s",
				unwanted, reportChannelRule)
		}
	}
}

// TestComposeSessionStartContext pins SessionStart delivery (E-1953): the
// coverage rule rides along when the project runs the channel, and is withheld
// when it does not.
//
// The withheld-and-empty case is the load-bearing one. Returning "" (rather than
// a rule-shaped string) is what makes handleTaskContextInjection suppress the
// injection entirely, so a project that switched the channel off in
// .endless/config.json is not told to use it.
func TestComposeSessionStartContext(t *testing.T) {
	if got := composeSessionStartContext("", true); got != reportChannelRule {
		t.Errorf("empty task list = %q, want just the rule", got)
	}
	if got := composeSessionStartContext("", false); got != "" {
		t.Errorf("channel off + empty task list = %q, want %q", got, "")
	}

	combined := composeSessionStartContext("Active tasks:\n  E-1 foo", true)
	if !strings.Contains(combined, reportChannelRule) {
		t.Errorf("combined dropped the rule:\n%s", combined)
	}
	if !strings.Contains(combined, "E-1 foo") {
		t.Errorf("combined dropped the task list:\n%s", combined)
	}

	off := composeSessionStartContext("Active tasks:\n  E-1 foo", false)
	if strings.Contains(off, "task report") {
		t.Errorf("channel-off SessionStart still points at the command:\n%s", off)
	}
	if !strings.Contains(off, "E-1 foo") {
		t.Errorf("channel-off SessionStart dropped the task list:\n%s", off)
	}
}

// TestReportReinforcement_RespectsTheSwitch pins the fix for a defect E-1953
// shipped: the PostToolUse reinforcement fired regardless of `report_gate`, so
// in a project that had switched the gate OFF it still injected "a Stop hook
// compares your final message against it" — a statement that was simply false
// there. It was observed against Endless's own repo, which ships the gate off.
//
// The distinction this test protects is between an instruction and a claim. An
// instruction may outlive its enforcement harmlessly ("send it verbatim" is
// still reasonable advice with no gate behind it). A factual assertion about
// enforcement may not: a session told it is being checked when it is not learns
// that Endless's statements about its own behavior cannot be relied on.
//
// Asserted at the text level because the branch itself needs a DB and a project
// row. The end-to-end half — the hook staying silent in a gate-off project — is
// in .endless/tasks/e-1953/verify.sh.
func TestReportReinforcement_RespectsTheSwitch(t *testing.T) {
	ac := reportRelayInstruction

	// If this claim is ever removed from the text, the gating below stops being
	// load-bearing and this test should be revisited rather than left passing.
	if !strings.Contains(ac, "Stop hook") {
		t.Fatal("the reinforcement no longer claims a Stop hook is watching; " +
			"re-evaluate whether it still needs to be gated on report_gate")
	}

	// The call site must consult reportChannelOn before emitting it.
	src, err := os.ReadFile("claude.go")
	if err != nil {
		t.Fatalf("read claude.go: %v", err)
	}
	if !strings.Contains(string(src),
		`payload.ToolName == "Bash" && reportChannelOn(projectID, isRegistered, payload.CWD)`) {
		t.Error("the PostToolUse reinforcement is not gated on reportChannelOn; " +
			"a gate-off project would be told a Stop hook is checking it when none is")
	}
}
