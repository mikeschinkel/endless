package hookcmd

import (
	"os"
	"strings"
	"testing"
)

// TestTerminalSurface pins the allow-list (E-1962). The two rows that matter are
// the observed ones: terminal Claude Code sets CLAUDE_CODE_ENTRYPOINT=cli, and
// the Desktop app sets nothing at all because it hosts the agent through the
// Agent SDK rather than the CLI.
//
// The unknown-value rows are the design, not padding. A surface that has not
// been seen yet must land OUTSIDE the channel, so shipping a new Claude Code
// host cannot silently start enforcing a contract nobody chose for it.
func TestTerminalSurface(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"terminal cli", true, "cli", true},

		{"desktop app leaves it unset", false, "", false},
		{"empty value", true, "", false},
		{"ide extension", true, "vscode", false},
		{"python sdk", true, "sdk-py", false},
		{"unknown future surface", true, "holodeck", false},

		// Case- and whitespace-sensitive on purpose: the value is produced by
		// Claude Code, not typed by a human, so a near-miss is a different
		// surface (or a corrupted environment), not the terminal.
		{"wrong case", true, "CLI", false},
		{"padded", true, " cli ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("CLAUDE_CODE_ENTRYPOINT", c.value)
			} else {
				// t.Setenv restores the prior value at test end, including for
				// an unset var, so this is safe under `go test` in a terminal.
				t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
				os.Unsetenv("CLAUDE_CODE_ENTRYPOINT")
			}
			if got := terminalSurface(); got != c.want {
				t.Errorf("terminalSurface() with %s = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestReportChannelOn_ChecksSurface pins that the surface test sits inside
// reportChannelOn rather than at one of its call sites (E-1962).
//
// Asserted against source because the live branch needs a DB and a project row.
// The placement is the whole point: reportChannelOn feeds the SessionStart rule,
// the PostToolUse reinforcement, and the Stop gate, and E-1953's invariant is
// that a session is never told to use a channel that will not gate it. Moving
// this check out to a single consumer would satisfy the letter of "Desktop
// doesn't report" while telling Desktop to report, or gating it silently.
func TestReportChannelOn_ChecksSurface(t *testing.T) {
	src := readSource(t, "claude.go")

	fn := funcBody(t, src, "func reportChannelOn(")
	if !strings.Contains(fn, "terminalSurface()") {
		t.Errorf("reportChannelOn does not consult terminalSurface(); "+
			"a Desktop session would still be told to use the report channel:\n%s", fn)
	}
}

// TestReportChannelOn_AndsWithTheConfigKey pins that surface and `report_gate`
// are independent veto axes (E-1962) — the project's decision and the product's
// must BOTH say yes.
//
// The direction this protects: a terminal session on a project carrying
// `"report_gate": false` (Endless's own checkout, among others) must stay off.
// An early `return true` on terminal would override the config key and switch
// the gate back on in the one repo that deliberately opted out.
func TestReportChannelOn_AndsWithTheConfigKey(t *testing.T) {
	fn := funcBody(t, readSource(t, "claude.go"), "func reportChannelOn(")

	if !strings.Contains(fn, "ReportGateEnabledForCwd") {
		t.Error("reportChannelOn no longer consults the report_gate config key")
	}
	if strings.Contains(fn, "return true") {
		t.Errorf("reportChannelOn short-circuits to true; surface must VETO, "+
			"never override the project's report_gate key:\n%s", fn)
	}
}

// TestClaimHandoff_ChecksSurface pins the claim handoff's copy (E-1962).
//
// It does not route through reportChannelOn — it has a worktree path and a
// project root, not a projectID/isRegistered pair — so the surface check has to
// be spelled out there. A Desktop session that claims a task would otherwise be
// handed the reporting instructions in its handoff, which is exactly the report
// path that opened this task.
func TestClaimHandoff_ChecksSurface(t *testing.T) {
	src := readSource(t, "claim_handoff.go")
	if !strings.Contains(src, `terminalSurface() && monitor.ReportGateEnabledForCwd(`) {
		t.Error("the claim handoff's report_gate var is not surface-gated; " +
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
