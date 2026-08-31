package hookcmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestHookExitCode_GradesByEvent pins the E-1661 grading table: which exit code
// a hook failure gets, and why.
//
// The two columns that matter are PreToolUse and PostToolUse — the only events
// whose stderr Claude Code feeds into the model's context, and therefore the
// only ones where blocking is what makes a failure visible to the agent at all.
// Stop is the deliberate exception: see stopEvents.
func TestHookExitCode_GradesByEvent(t *testing.T) {
	tests := []struct {
		event string
		want  int
		why   string
	}{
		{"PreToolUse", exitBlocking, "blocks the call and hands the model stderr"},
		{"PostToolUse", exitBlocking, "hands the model stderr after the tool ran"},
		{"UserPromptSubmit", exitBlocking, "blocks and erases the prompt"},
		{"SessionStart", exitBlocking, "refuses to start a session Endless cannot track"},
		{"SessionEnd", exitBlocking, "nothing left to trap; blocking costs nothing"},
		{"PreCompact", exitBlocking, "blocks the compaction"},
		{"Stop", exitNonBlocking, "exit 2 would loop the turn with no DB-backed guard"},
		{"SubagentStop", exitNonBlocking, "same loop, one level down"},
	}
	for _, tc := range tests {
		t.Run(tc.event, func(t *testing.T) {
			got := hookExitCode(taggedWithEvent(tc.event, errors.New("boom")))
			if got != tc.want {
				t.Errorf("%s exits %d, want %d — %s", tc.event, got, tc.want, tc.why)
			}
		})
	}
}

// TestHookExitCode_UntaggedStaysNonBlocking pins the safe default. An error
// with no event was raised before the payload could be parsed, or by a hook
// that is not a Claude event at all — and blocking an event you cannot name is
// the one mistake that can hang a session on Stop.
func TestHookExitCode_UntaggedStaysNonBlocking(t *testing.T) {
	if got := hookExitCode(errors.New("reading stdin: broken pipe")); got != exitNonBlocking {
		t.Errorf("an untagged error exits %d, want %d", got, exitNonBlocking)
	}
}

// TestTaggedWithEvent_PreservesTheChain proves the tag is transparent: it must
// not cost callers their errors.Is/errors.As, since the tag is applied at a
// choke point that has no idea what the error underneath is.
func TestTaggedWithEvent_PreservesTheChain(t *testing.T) {
	if err := taggedWithEvent("PostToolUse", nil); err != nil {
		t.Errorf("nil in produced %v, want nil out", err)
	}

	sentinel := errors.New("task_types integrity check")
	wrapped := fmt.Errorf("looking up project: %w", sentinel)
	tagged := taggedWithEvent("PostToolUse", wrapped)

	if !errors.Is(tagged, sentinel) {
		t.Error("tagging broke errors.Is against the wrapped cause")
	}
	if got, want := tagged.Error(), wrapped.Error(); got != want {
		t.Errorf("tagging changed the message:\n got: %s\nwant: %s", got, want)
	}
}

// TestRunClaude_TagsItsFailuresWithTheEvent is the wiring test, and the one
// that catches the regression this feature is most exposed to: runClaude tags
// its errors from a deferred call at the single exit, so a new `return err`
// added below it is covered automatically — but only for as long as that defer
// survives. Without it every code below grades as untagged and silently drops
// back to the old, invisible exit 1.
//
// Driven with no database in sight: an unset HOME makes a `~/`-spelled cwd
// unresolvable, which fails runClaude in the frame BEFORE its first DB call.
func TestRunClaude_TagsItsFailuresWithTheEvent(t *testing.T) {
	run := func(t *testing.T, payload string) error {
		t.Helper()
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
		t.Setenv("HOME", "")
		os.Unsetenv("HOME")

		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		if _, err := w.WriteString(payload); err != nil {
			t.Fatalf("write: %v", err)
		}
		w.Close()
		orig := os.Stdin
		os.Stdin = r
		t.Cleanup(func() { os.Stdin = orig; r.Close() })

		return runClaude(nil)
	}

	const cwd = `"cwd":"~/definitely-not-a-real-project"`

	t.Run("PostToolUse blocks", func(t *testing.T) {
		err := run(t, `{`+cwd+`,"hook_event_name":"PostToolUse"}`)
		if err == nil {
			t.Fatal("expected an unresolvable cwd to fail runClaude")
		}
		if got := hookExitCode(err); got != exitBlocking {
			t.Errorf("PostToolUse failure exits %d, want %d (%v)", got, exitBlocking, err)
		}
	})

	t.Run("Stop does not", func(t *testing.T) {
		err := run(t, `{`+cwd+`,"hook_event_name":"Stop"}`)
		if err == nil {
			t.Fatal("expected an unresolvable cwd to fail runClaude")
		}
		if got := hookExitCode(err); got != exitNonBlocking {
			t.Errorf("Stop failure exits %d, want %d (%v)", got, exitNonBlocking, err)
		}
	})

	t.Run("an unparseable payload names no event", func(t *testing.T) {
		err := run(t, `not json`)
		if err == nil {
			t.Fatal("expected malformed stdin to fail runClaude")
		}
		if got := hookExitCode(err); got != exitNonBlocking {
			t.Errorf("a pre-parse failure exits %d, want %d (%v)", got, exitNonBlocking, err)
		}
	})
}

// TestHaltNotice_IsActionable pins the contract from the E-1661 plan: the
// notice tells the agent to stop, and tells whoever reads it what to do. The
// error itself is logged separately, so the notice is checked for the parts
// only it carries.
func TestHaltNotice_IsActionable(t *testing.T) {
	notice := haltNotice()
	for _, want := range []string{
		"STOP",               // the instruction to the agent
		"do not work around", // and the shape of compliance
		"database:",          // which database disagreed
		"stale binary",       // the diagnosis
		"Reinstall or rebuild",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("halt notice omits %q:\n%s", want, notice)
		}
	}
	// Machine-specific remedies are wrong everywhere but one checkout.
	for _, never := range []string{"just install", "just build", "uv tool"} {
		if strings.Contains(notice, never) {
			t.Errorf("halt notice names %q — the remedy must not assume a build recipe:\n%s", never, notice)
		}
	}
}
