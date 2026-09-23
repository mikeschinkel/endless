package hookcmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	_ "modernc.org/sqlite"
)

// bashPayload builds a PreToolUse Bash payload for the gate tests.
func bashPayload(sessionID, cwd, command string) claudePayload {
	input, _ := json.Marshal(map[string]string{"command": command})
	return claudePayload{
		SessionID: sessionID,
		CWD:       cwd,
		ToolName:  "Bash",
		ToolInput: input,
	}
}

// gateFixture seeds a project with task 1983's worktree and an unbound session,
// returning the project root and the worktree path.
func gateFixture(t *testing.T, sessionID string) (projectRoot, worktree string) {
	t.Helper()
	db := newBindTestDB(t)
	projectRoot = t.TempDir()
	seedProjectAndTasks(t, db, projectRoot, 1983)
	worktree = seedWorktree(t, projectRoot, 1983)
	if err := monitor.TouchSession(sessionID, "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	t.Setenv("CLAUDE_JOB_DIR", "")
	return projectRoot, worktree
}

// TestUnboundWorktreeGate_BlocksUnboundSessionInWorktree is E-1983 Decision 3's
// trigger: cwd looks like a task worktree and the session holds no task. That is
// the one state the cwd-only bind rule leaves genuinely wrong, and before this
// gate it was silent.
func TestUnboundWorktreeGate_BlocksUnboundSessionInWorktree(t *testing.T) {
	_, worktree := gateFixture(t, "sess-gate")

	msg, blocked := unboundWorktreeDecision(1, bashPayload("sess-gate", worktree, "ls"))
	if !blocked {
		t.Fatal("gate did not fire on an unbound session inside a task worktree")
	}
	// The message must name the task, and BOTH escapes, or the block strands the
	// window it fired on.
	for _, want := range []string{"E-1983", "endless task claim E-1983", "endless task bind E-1983"} {
		if !strings.Contains(msg, want) {
			t.Errorf("block message does not mention %q:\n%s", want, msg)
		}
	}
}

// TestUnboundWorktreeGate_NamesTheFailedStep pins that the message diagnoses
// WHICH step of the cwd bind failed, because the fixes differ. Here cwd is
// worktree-SHAPED but carries no .endless/worktree.json, so the walk found
// nothing — the E-1983 Decision 2 failure mode.
func TestUnboundWorktreeGate_NamesTheFailedStep(t *testing.T) {
	db := newBindTestDB(t)
	projectRoot := t.TempDir()
	seedProjectAndTasks(t, db, projectRoot, 1983)
	// Worktree-shaped path, no companion file planted.
	bare := filepath.Join(projectRoot, ".endless", "worktrees", "e-1983")
	if err := monitor.TouchSession("sess-nostep", "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	t.Setenv("CLAUDE_JOB_DIR", "")

	msg, blocked := unboundWorktreeDecision(1, bashPayload("sess-nostep", bare, "ls"))
	if !blocked {
		t.Fatal("gate did not fire")
	}
	if !strings.Contains(msg, "worktree.json") {
		t.Errorf("message does not name the failed step (the companion-file walk):\n%s", msg)
	}
}

// TestUnboundWorktreeGate_DoesNotBlockSubagents is the false positive that would
// make the gate unusable: Agent-tool subagents share the parent's cwd but have
// their own session identity and are deliberately never bound (E-1300). Without
// this screen the gate blocks every subagent tool call in every worktree.
func TestUnboundWorktreeGate_DoesNotBlockSubagents(t *testing.T) {
	_, worktree := gateFixture(t, "sess-sub")

	payload := bashPayload("sess-sub", worktree, "ls")
	payload.AgentID = "agent-1"
	if _, blocked := unboundWorktreeDecision(1, payload); blocked {
		t.Fatal("gate blocked an Agent-tool subagent — the worst false positive available here")
	}
}

// TestUnboundWorktreeGate_DoesNotBlockBackgroundAgents: a background agent's
// dispatch row already carries its task and maybeCwdBind deliberately never
// binds it (E-1568), so "unbound" is its correct state, exactly as for a
// subagent.
func TestUnboundWorktreeGate_DoesNotBlockBackgroundAgents(t *testing.T) {
	_, worktree := gateFixture(t, "sess-bg")
	t.Setenv("CLAUDE_JOB_DIR", "/tmp/job")

	if _, blocked := unboundWorktreeDecision(1, bashPayload("sess-bg", worktree, "ls")); blocked {
		t.Fatal("gate blocked a background agent, which is deliberately never bound")
	}
}

// TestUnboundWorktreeGate_DoesNotBlockMainCheckout: an unbound session in main is
// the correct outcome of the cwd-only rule, not a breach. enforceWorktreeGate
// covers the different case of a session that HOLDS a task while sitting in main.
func TestUnboundWorktreeGate_DoesNotBlockMainCheckout(t *testing.T) {
	projectRoot, _ := gateFixture(t, "sess-main")

	if _, blocked := unboundWorktreeDecision(1, bashPayload("sess-main", projectRoot, "ls")); blocked {
		t.Fatal("gate blocked an unbound session in the main checkout")
	}
	sub := filepath.Join(projectRoot, "src", "internal")
	if _, blocked := unboundWorktreeDecision(1, bashPayload("sess-main", sub, "ls")); blocked {
		t.Fatal("gate blocked an unbound session in an ordinary subdirectory of main")
	}
}

// TestUnboundWorktreeGate_DoesNotBlockForeignTree: a worktree-shaped path outside
// the registered project is none of this gate's business, matching
// enforceWorktreeGate's own treatment of foreign trees.
func TestUnboundWorktreeGate_DoesNotBlockForeignTree(t *testing.T) {
	gateFixture(t, "sess-foreign")
	foreign := filepath.Join(t.TempDir(), "other", ".endless", "worktrees", "e-1983")

	if _, blocked := unboundWorktreeDecision(1, bashPayload("sess-foreign", foreign, "ls")); blocked {
		t.Fatal("gate blocked a worktree-shaped path outside the registered project")
	}
}

// TestUnboundWorktreeGate_DoesNotBlockBoundSession: a session that holds a task
// is enforceClaimedCwd's half of the invariant, not this one's.
func TestUnboundWorktreeGate_DoesNotBlockBoundSession(t *testing.T) {
	_, worktree := gateFixture(t, "sess-bound")
	if err := monitor.BindSessionToTask("sess-bound", 1, 1983); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if _, blocked := unboundWorktreeDecision(1, bashPayload("sess-bound", worktree, "ls")); blocked {
		t.Fatal("gate blocked a session that already holds a task")
	}
}

// TestUnboundWorktreeGate_DoesNotBlockItsOwnEscape: the gate must never block the
// command it tells you to run, or the first false positive strands the window
// permanently — the failure mode this task's analysis calls "the gate becomes
// the bug".
func TestUnboundWorktreeGate_DoesNotBlockItsOwnEscape(t *testing.T) {
	_, worktree := gateFixture(t, "sess-escape")

	for _, cmd := range []string{
		"endless task claim E-1983",
		"endless task bind E-1983",
		"uv run endless task claim E-1983",
		"/usr/local/bin/endless task bind E-1983",
	} {
		if _, blocked := unboundWorktreeDecision(1, bashPayload("sess-escape", worktree, cmd)); blocked {
			t.Errorf("gate blocked its own escape hatch: %q", cmd)
		}
	}
	// Anything else in the same worktree still blocks.
	if _, blocked := unboundWorktreeDecision(1, bashPayload("sess-escape", worktree, "endless task show E-1983")); !blocked {
		t.Error("gate let an unrelated `endless task` command through")
	}
}
