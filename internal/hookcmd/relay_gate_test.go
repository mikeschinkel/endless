package hookcmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// relay_gate_test.go pins the E-1901 verbatim-relay comparison. Everything here
// is pure — no DB, no payload plumbing — because the comparison is the part that
// has to be exactly right: a false negative lets appended prose through (the
// whole point of the gate), and a false positive bounces a compliant agent,
// which is worse, because an agent that learns the gate is noise stops treating
// any of it as real.

const sampleSanctioned = "Verify: `esu && ./tests/tasks/e-1901-verify.sh`\n" +
	"Follow-ups you filed: E-1906 [untriaged]"

func TestRelayVerdict_Compliant(t *testing.T) {
	cases := map[string]string{
		"byte-identical": sampleSanctioned,

		// The agent said nothing. Not what the report asked for, but the offense
		// this gate exists to catch is APPENDING; bouncing silence would punish
		// an agent for under-speaking while trying to obey.
		"empty message": "",
		"whitespace only": "   \n\n  \t\n",

		// Cosmetic-only differences. Each of these is an agent relaying the
		// report correctly and formatting it for a terminal; bouncing any of
		// them would be a false positive.
		"trailing whitespace per line": "Verify: `esu && ./tests/tasks/e-1901-verify.sh`   \n" +
			"Follow-ups you filed: E-1906 [untriaged]\t",
		"leading and trailing blank lines": "\n\n" + sampleSanctioned + "\n\n\n",
		"extra blank line between": "Verify: `esu && ./tests/tasks/e-1901-verify.sh`\n\n\n" +
			"Follow-ups you filed: E-1906 [untriaged]",
		"wrapped in a code fence": "```\n" + sampleSanctioned + "\n```",
		"wrapped in a tagged fence": "```text\n" + sampleSanctioned + "\n```",
		"markers copied along": "----- BEGIN REPORT -----\n" + sampleSanctioned +
			"\n----- END REPORT -----",
		"CRLF line endings": strings.ReplaceAll(sampleSanctioned, "\n", "\r\n"),
	}
	for name, actual := range cases {
		t.Run(name, func(t *testing.T) {
			extra, ok := relayVerdict(sampleSanctioned, actual)
			if !ok {
				t.Errorf("compliant message was blocked (extra=%d):\n%s", extra, actual)
			}
			if extra != 0 {
				t.Errorf("extra = %d, want 0 for a compliant message", extra)
			}
		})
	}
}

func TestRelayVerdict_BlocksAppendedProse(t *testing.T) {
	cases := []struct {
		name      string
		actual    string
		wantExtra int
	}{
		{
			// The canonical violation: the report, then a recap. This is the
			// E-1818 failure the task was filed for.
			name: "report plus a trailing recap",
			actual: sampleSanctioned + "\n\nI reproduced the bug, fixed the " +
				"off-by-one in the parser, and added a regression test.",
			wantExtra: 1,
		},
		{
			name:      "preamble before the report",
			actual:    "All done! Here's the summary:\n\n" + sampleSanctioned,
			wantExtra: 1,
		},
		{
			name:      "sign-off after the report",
			actual:    sampleSanctioned + "\nLet me know if you need anything else!",
			wantExtra: 1,
		},
		{
			name: "multi-line recap counts every line",
			actual: sampleSanctioned + "\nWhat changed:\n- the parser\n" +
				"- the tests\n- the docs",
			wantExtra: 4,
		},
		{
			// Confirming the absence of a problem is the exact ceremony the
			// report's own note-check DROPs; it must not survive here either.
			name:      "negative confirmation",
			actual:    sampleSanctioned + "\nNo stray files, nothing else to report.",
			wantExtra: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extra, ok := relayVerdict(sampleSanctioned, tc.actual)
			if ok {
				t.Fatalf("appended prose was NOT blocked:\n%s", tc.actual)
			}
			if extra != tc.wantExtra {
				t.Errorf("extra = %d, want %d", extra, tc.wantExtra)
			}
		})
	}
}

// A message that drops or rewrites sanctioned lines is still a violation — the
// report was not relayed — but it adds no unaccounted line. The verdict must not
// report a self-contradictory "blocked, 0 lines appended".
func TestRelayVerdict_DivergenceWithoutAppendReportsNonZero(t *testing.T) {
	extra, ok := relayVerdict(sampleSanctioned, "Verify: something else entirely")
	if ok {
		t.Fatal("a rewritten report was treated as compliant")
	}
	if extra < 1 {
		t.Errorf("extra = %d, want >= 1 so the reason is not self-contradictory", extra)
	}
}

// The empty-report case is the common one post-E-1880, so its sanctioned text
// has to gate exactly like any other.
func TestRelayVerdict_NothingToReport(t *testing.T) {
	const sanctioned = "Nothing to report."
	if extra, ok := relayVerdict(sanctioned, "Nothing to report."); !ok || extra != 0 {
		t.Errorf("verbatim relay of the empty-case text was blocked (extra=%d)", extra)
	}
	extra, ok := relayVerdict(sanctioned, "Nothing to report.\n\nThe build is green and all 42 tests pass.")
	if ok {
		t.Fatal("prose appended to the empty-case report was not blocked")
	}
	if extra != 1 {
		t.Errorf("extra = %d, want 1", extra)
	}
}

// Both audiences must be named. A violation only Claude sees is one it can
// privately reinterpret — which is why E-1803's compose-time nudge was not
// enough, and why omitting systemMessage would quietly regress this task to it.
func TestRelayBlockResponse_NamesBothAudiences(t *testing.T) {
	block := stopBlock{
		Decision:      "block",
		Reason:        relayBlockReason(3, sampleSanctioned),
		SystemMessage: relaySystemMessage(3),
	}
	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshaling block response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshaling block response: %v", err)
	}
	if decoded["decision"] != "block" {
		t.Errorf("decision = %v, want \"block\" (anything else lets the turn end)", decoded["decision"])
	}
	reason, _ := decoded["reason"].(string)
	if !strings.Contains(reason, "3 lines") {
		t.Errorf("reason does not quantify the violation: %q", reason)
	}
	if !strings.Contains(reason, sampleSanctioned) {
		t.Error("reason does not include the sanctioned text to resend")
	}
	if !strings.Contains(reason, "--json") {
		t.Error("reason does not name the legitimate channel for the extra content")
	}
	sysMsg, _ := decoded["systemMessage"].(string)
	if !strings.Contains(sysMsg, "3 lines") {
		t.Errorf("systemMessage does not name the violation to the user: %q", sysMsg)
	}
}

// Singular/plural in a message the user reads.
func TestPluralHelpers(t *testing.T) {
	if got := pluralLines(1); got != "1 line" {
		t.Errorf("pluralLines(1) = %q", got)
	}
	if got := pluralLines(4); got != "4 lines" {
		t.Errorf("pluralLines(4) = %q", got)
	}
	if got := pluralTimes(1); got != "once" {
		t.Errorf("pluralTimes(1) = %q", got)
	}
	if got := pluralTimes(2); got != "2 times" {
		t.Errorf("pluralTimes(2) = %q", got)
	}
}

// Giving up must be audible. The analysis is explicit that this gate cannot
// guarantee compliance; a silent surrender would render a violation as a clean
// handoff, which is exactly the misreport the task exists to prevent.
func TestRelayExhaustedMessage_IsNotSilent(t *testing.T) {
	msg := relayExhaustedMessage(2)
	if msg == "" {
		t.Fatal("exhausted message is empty — the user would see a violation as a clean handoff")
	}
	if !strings.Contains(msg, "NOT the sanctioned report") {
		t.Errorf("exhausted message does not warn that the final message is unsanctioned: %q", msg)
	}
}

// A subagent's final message is a return value to the parent agent, not a user
// handoff. Gating it would block on a contract that does not apply and strand
// the parent. This runs before any DB access, so it is safe without fixtures.
func TestRelayGate_SkipsSubagents(t *testing.T) {
	handled, err := enforceRelayGate(claudePayload{
		AgentID:              "agent-123",
		SessionID:            "some-session",
		LastAssistantMessage: "Here is my analysis, at length, with plenty of prose.",
	})
	if err != nil {
		t.Fatalf("subagent path returned an error: %v", err)
	}
	if handled {
		t.Error("subagent turn was gated; its final message goes to the parent, not the user")
	}
}

func TestNormalizeRelayText_KeepsProseVisible(t *testing.T) {
	// Normalization must never erase a line that carries words — that is the
	// property the whole gate rests on.
	got := normalizeRelayText("```\n\n  Some appended sentence.  \n\n```")
	if got != "Some appended sentence." {
		t.Errorf("normalizeRelayText = %q, want the prose line preserved", got)
	}
}
