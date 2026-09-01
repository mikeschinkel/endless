package hookcmd

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// E-2093. Two sessions were stranded by the same defect: `Stop` marks a
// session idle at the end of every turn, nothing marked it working again, and
// the PreToolUse gate admitted `working` alone. One clean turn was enough to
// lock a session out of Write/Edit for the rest of its life — and the refusal
// it met announced "No active work session", which was false for a session
// that had claimed a task, and pointed at `task claim`, which refuses on
// status before it reaches the question of who holds the task.
//
// These pin both halves: what the gate admits, and that each refusal describes
// the state that produced it.

func ptrInt64(v int64) *int64 { return &v }

// TestSessionMayWrite pins the admission rule. `idle` is the case that
// stranded the two sessions; it is admitted BECAUSE a write from an idle
// session is mid-turn by construction — writes only happen inside turns — so
// the state is stale, not the agent.
func TestSessionMayWrite(t *testing.T) {
	cases := []struct {
		name string
		s    *monitor.SessionInfo
		want bool
	}{
		{"working and holding a task", &monitor.SessionInfo{State: "working", TaskID: ptrInt64(42)}, true},
		{"idle and holding a task", &monitor.SessionInfo{State: "idle", TaskID: ptrInt64(42)}, true},
		{"needs_input holding a task", &monitor.SessionInfo{State: "needs_input", TaskID: ptrInt64(42)}, false},
		{"ended holding a task", &monitor.SessionInfo{State: "ended", TaskID: ptrInt64(42)}, false},
		{"working but holding no task", &monitor.SessionInfo{State: "working"}, false},
		{"idle and holding no task", &monitor.SessionInfo{State: "idle"}, false},
		{"no session row at all", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sessionMayWrite(c.s); got != c.want {
				t.Errorf("sessionMayWrite(%+v) = %v, want %v", c.s, got, c.want)
			}
		})
	}
}

// TestDeclarationRefusal_UndeclaredSession pins the refusal the gate exists
// for: a session that never said what it is working on. This is the ONE case
// the old single message was accurate about, and it keeps naming `task claim`,
// which does work from that state.
func TestDeclarationRefusal_UndeclaredSession(t *testing.T) {
	db := newSchemaDB(t)
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'proj', '/tmp/proj')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	for _, name := range []string{"nil session", "row holding no task"} {
		t.Run(name, func(t *testing.T) {
			var s *monitor.SessionInfo
			if name == "row holding no task" {
				s = &monitor.SessionInfo{State: "working"}
			}
			msg := declarationRefusal(1, s)
			for _, want := range []string{
				"has not declared a task in project 'proj'",
				"endless task claim <id>",
				"endless task chat",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal missing %q\n--- message ---\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "--force") {
				t.Errorf("refusal offers --force\n--- message ---\n%s", msg)
			}
		})
	}
}

// TestDeclarationRefusal_DeclaredButNotActing pins the refusal that was
// previously wrong. A `needs_input` session HAS declared its task, so the
// message must say which task it holds, must not claim there is no work
// session, and must not name a command — only the human answering clears
// `needs_input`.
func TestDeclarationRefusal_DeclaredButNotActing(t *testing.T) {
	msg := declarationRefusal(1, &monitor.SessionInfo{
		State:  "needs_input",
		TaskID: ptrInt64(2093),
	})

	for _, want := range []string{
		"holds E-2093",
		"state 'needs_input'",
		"The task IS declared",
		"There is no command for you to run.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q\n--- message ---\n%s", want, msg)
		}
	}
	for _, unwanted := range []string{
		"No active work session",
		"endless task claim",
		"--force",
	} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("refusal still carries %q\n--- message ---\n%s", unwanted, msg)
		}
	}
}

// TestDeclarationRefusal_DescribesTheStateItChecked guards against the shape
// of the original bug rather than its instance: a refusal that asserts a state
// it never looked at. Any live-but-not-acting state other than `needs_input`
// must not be explained AS `needs_input`.
func TestDeclarationRefusal_DescribesTheStateItChecked(t *testing.T) {
	msg := declarationRefusal(1, &monitor.SessionInfo{
		State:  "ended",
		TaskID: ptrInt64(2093),
	})
	if !strings.Contains(msg, "state 'ended'") {
		t.Errorf("refusal does not name the state it refused\n--- message ---\n%s", msg)
	}
	if strings.Contains(msg, "`needs_input` means") {
		t.Errorf("refusal explains a state it did not check\n--- message ---\n%s", msg)
	}
}
