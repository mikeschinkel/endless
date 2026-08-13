package hookcmd

import (
	"os"
	"strings"
	"testing"
)

// TestSupportedAgent_TracksTheHarness pins the hook-side wrapper against the
// real detector (E-1962). The detector's own table is covered in
// internal/agentenv; this only checks the wiring is live.
func TestSupportedAgent_TracksTheHarness(t *testing.T) {
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	if !supportedAgent() {
		t.Error("a terminal Claude Code session is not recognized as supported")
	}

	// The Desktop condition: the CLI's variables are simply absent.
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	os.Unsetenv("CLAUDE_CODE_ENTRYPOINT")
	t.Setenv("CLAUDE_AGENT_SDK_VERSION", "0.3.222")
	if supportedAgent() {
		t.Error("Claude Code Desktop is treated as supported; it is not until E-1505")
	}
}

// TestReportChannelOn_ChecksTheHarness pins that the harness test sits inside
// reportChannelOn rather than at one of its call sites (E-1962).
//
// Asserted against source because the live branch needs a DB and a project row.
// The placement is the whole point: reportChannelOn feeds the SessionStart rule,
// the PostToolUse reinforcement, and the Stop gate, and E-1953's invariant is
// that a session is never told to use a channel that will not gate it. Moving
// this check out to a single consumer would satisfy the letter of "Desktop
// doesn't report" while telling Desktop to report, or gating it silently.
func TestReportChannelOn_ChecksTheHarness(t *testing.T) {
	fn := funcBody(t, readSource(t, "claude.go"), "func reportChannelOn(")
	if !strings.Contains(fn, "supportedAgent()") {
		t.Errorf("reportChannelOn does not consult supportedAgent(); "+
			"a Desktop session would still be told to use the report channel:\n%s", fn)
	}
}

// TestReportChannelOn_AndsWithTheConfigKey pins that harness and `report_gate`
// are independent veto axes (E-1962) — the project's decision and the product's
// must BOTH say yes.
//
// The direction this protects: a terminal session on a project carrying
// `"report_gate": false` (Endless's own checkout, among others) must stay off.
// An early `return true` on a supported harness would override the config key
// and switch the gate back on in the one repo that deliberately opted out.
func TestReportChannelOn_AndsWithTheConfigKey(t *testing.T) {
	fn := funcBody(t, readSource(t, "claude.go"), "func reportChannelOn(")

	if !strings.Contains(fn, "ReportGateEnabledForCwd") {
		t.Error("reportChannelOn no longer consults the report_gate config key")
	}
	if strings.Contains(fn, "return true") {
		t.Errorf("reportChannelOn short-circuits to true; the harness must VETO, "+
			"never override the project's report_gate key:\n%s", fn)
	}
}

// TestClaimHandoff_ChecksTheHarness pins the claim handoff's copy (E-1962).
//
// It does not route through reportChannelOn — it has a worktree path and a
// project root, not a projectID/isRegistered pair — so the check has to be
// spelled out there. A Desktop session that claims a task would otherwise be
// handed the reporting instructions in its handoff, which is exactly the report
// path that opened this task.
func TestClaimHandoff_ChecksTheHarness(t *testing.T) {
	src := readSource(t, "claim_handoff.go")
	if !strings.Contains(src, `supportedAgent() && monitor.ReportGateEnabledForCwd(`) {
		t.Error("the claim handoff's report_gate var is not harness-gated; " +
			"a Desktop session claiming a task would be handed the reporting contract")
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// funcBody returns the source from the given func signature up to the next
// top-level `\n}`, so an assertion about one function cannot be satisfied by
// text belonging to its neighbors.
func funcBody(t *testing.T, src, signature string) string {
	t.Helper()
	i := strings.Index(src, signature)
	if i < 0 {
		t.Fatalf("signature %q not found — was it renamed?", signature)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}
