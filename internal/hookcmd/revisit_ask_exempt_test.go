package hookcmd

import (
	"strings"
	"testing"
)

// The revisit gate told the agent to ask the user with AskUserQuestion, and
// then blocked AskUserQuestion.
//
// Only a Bash `endless task continue` was exempt, so of the two options the
// refusal offered — continue, or stop and wait — the one that required asking
// could not be reached at all. The agent could put the question in reply text
// and hope, which is not what the instruction says to do.
//
// A refusal that names a remedy its own gate forbids is not a refusal an agent
// can obey, and E-2159's rule is that a refusal must say what to do. So asking
// is exempt: it is how this gate clears, not work done under the plan being
// reconsidered.
//
// The exemption is checked before anything touches the database, so this needs
// no fixture — which is also why it cannot regress silently behind a DB error.
func TestRevisitGate_ExemptsTheToolItsMessageNames(t *testing.T) {
	_, block := revisitGateDecision(claudePayload{
		SessionID: "s-1",
		EventName: "PreToolUse",
		ToolName:  askUserQuestionTool,
	})
	if block {
		t.Error("the revisit gate blocked AskUserQuestion, the tool its own instruction tells the agent to use")
	}
}

// TestRevisitPromptNamesTheExemptTool keeps the two halves together: if the
// instruction stops naming AskUserQuestion, or the exemption stops naming the
// same tool, the pair has drifted and one of them is lying.
func TestRevisitPromptNamesTheExemptTool(t *testing.T) {
	msg := revisitPromptInstruction(42, 7)
	if !strings.Contains(msg, askUserQuestionTool) {
		t.Errorf("the revisit instruction no longer names %q, but the gate still exempts it:\n%s",
			askUserQuestionTool, msg)
	}
}
