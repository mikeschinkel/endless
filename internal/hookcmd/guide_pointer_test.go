package hookcmd

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestGuidePointer_Wording pins the exact sentence (E-1854). The wording is
// fixed by the task, not by this package: it has to read as an instruction to
// an agent that has never heard of Endless, and it names the literal command
// so there is nothing to guess. A rewrite here is a behavior change to every
// downstream project's first prompt, so it must be a deliberate edit that
// breaks this test.
func TestGuidePointer_Wording(t *testing.T) {
	const want = "New to this project? Run `endless guide` to learn the Endless workflow."
	if guidePointer != want {
		t.Errorf("guidePointer = %q, want %q", guidePointer, want)
	}
}

// TestWithGuidePointer_LeadsTheInjection confirms the pointer is the FIRST
// line of the injected context and the task list follows it, separated by a
// blank line. Position is the whole point: an agent skimming an injection
// reads the top, and the pointer is what makes the rest actionable.
func TestWithGuidePointer_LeadsTheInjection(t *testing.T) {
	tasks := "Endless has active tasks for proj.\nIN PROGRESS:\n  - E-1 do the thing\n"
	got := withGuidePointer(tasks)

	lines := strings.Split(got, "\n")
	if lines[0] != guidePointer {
		t.Errorf("first line = %q, want the guide pointer %q", lines[0], guidePointer)
	}
	if len(lines) < 2 || lines[1] != "" {
		t.Errorf("second line = %q, want a blank separator line", strings.Join(lines[1:2], ""))
	}
	if !strings.HasSuffix(got, tasks) {
		t.Errorf("task context was not preserved verbatim after the pointer:\n%s", got)
	}
	if strings.Index(got, guidePointer) > strings.Index(got, "IN PROGRESS:") {
		t.Error("guide pointer appears after the task list; it must lead")
	}
}

// TestWithGuidePointer_EmptyTaskContext covers the freshly-set-up downstream
// project this feature exists for: no tasks, nothing else to say — the agent
// still gets told how to learn the workflow, and with no stray blank lines
// padding an otherwise one-line injection.
func TestWithGuidePointer_EmptyTaskContext(t *testing.T) {
	if got := withGuidePointer(""); got != guidePointer {
		t.Errorf("withGuidePointer(\"\") = %q, want %q", got, guidePointer)
	}
}

// TestWithGuidePointer_NoTasksYet is the realistic empty-project shape: a
// downstream project that installed the hook and registered but has filed no
// tasks. monitor.FormatTasks still returns its "No tasks yet" blurb, so the
// pointer must lead that too — this is the exact context a first-run product
// user's agent receives.
func TestWithGuidePointer_NoTasksYet(t *testing.T) {
	got := withGuidePointer(monitor.FormatTasks("downstream-proj", nil))

	if !strings.HasPrefix(got, guidePointer+"\n\n") {
		t.Errorf("no-tasks injection does not lead with the pointer:\n%s", got)
	}
	if !strings.Contains(got, "No tasks yet") {
		t.Errorf("no-tasks injection lost the task-list body:\n%s", got)
	}
}

// TestWithGuidePointer_NotRepeated guards the one-shot property at the level
// this package controls: the pointer is composed in exactly one place, so it
// cannot leak into the per-prompt "Active task: E-XXX" reminder that
// handleUserPromptSubmit emits on every later prompt. Composing it a second
// time would double the line.
func TestWithGuidePointer_NotRepeated(t *testing.T) {
	got := withGuidePointer("Endless has active tasks for proj.\n")
	if n := strings.Count(got, guidePointer); n != 1 {
		t.Errorf("guide pointer appears %d times, want exactly 1:\n%s", n, got)
	}
}
