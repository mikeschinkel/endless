package hookcmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// The events on which Endless hands Claude a string of context. Every one of
// them must frame it the same way; SessionStart and UserPromptSubmit are named
// explicitly because those two are what E-2001 fixed.
var contextEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUse"}

// TestContextInjection_ShapeMatchesContract pins the ONE shape the Claude Code
// hooks contract accepts for additionalContext, on every event that injects it.
//
// This is E-2001's regression test, and it pins the SHAPE rather than the text
// on purpose. Every test that came before asserted what the hook WRITES, and a
// bare top-level {"additionalContext": "…"} writes the right text — which is
// why SessionStart and UserPromptSubmit injections were discarded by the
// harness for as long as they existed while 20-odd checks stayed green. Nothing
// downstream of the hook can tell "emitted" from "delivered": the injection is
// dropped with no error and no transcript entry, and injected context is not
// written to the transcript either, so its absence there proves nothing.
//
// The three assertions below are each load-bearing:
//
//   - nested under hookSpecificOutput — the contract's only accepted location
//     ("Add context for Claude": "Return additionalContext inside
//     hookSpecificOutput alongside the event name").
//   - hookEventName present and equal to the event that fired.
//   - NO top-level additionalContext. This is the one that would have caught
//     the bug. It is not redundant with the first: an implementation that emits
//     both would look correct to a nesting-only check while re-establishing the
//     belief that the top-level field means something.
func TestContextInjection_ShapeMatchesContract(t *testing.T) {
	for _, event := range contextEvents {
		t.Run(event, func(t *testing.T) {
			b, err := json.Marshal(injectContext(event, "CTX"))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			hso, ok := got["hookSpecificOutput"].(map[string]any)
			if !ok {
				t.Fatalf("hookSpecificOutput missing/wrong type: %s", b)
			}
			if hso["hookEventName"] != event {
				t.Errorf("hookEventName = %v, want %q", hso["hookEventName"], event)
			}
			if hso["additionalContext"] != "CTX" {
				t.Errorf("additionalContext = %v, want %q", hso["additionalContext"], "CTX")
			}
			if _, bare := got["additionalContext"]; bare {
				t.Errorf("top-level additionalContext is discarded by the harness "+
					"(E-2001); it must appear only under hookSpecificOutput: %s", b)
			}
			if len(got) != 1 {
				t.Errorf("a context injection carries hookSpecificOutput and nothing "+
					"else, got %d top-level keys: %s", len(got), b)
			}
		})
	}
}

// TestWriteContextInjection_Stdout pins what actually reaches the harness: one
// JSON document on stdout, in the contract shape, with a leading '{'.
//
// The leading brace is not cosmetic. The harness chooses how to read a hook's
// stdout from its first character — '{' means "parse as JSON", anything else
// means "plain text". So on UserPromptSubmit and SessionStart, which accept
// plain-text stdout as context too, emitting JSON forfeits that channel: an
// unrecognised object is NOT re-read as text, it is dropped. Framing and
// nesting therefore have to be right together, which is why this asserts on
// bytes rather than on a struct.
func TestWriteContextInjection_Stdout(t *testing.T) {
	for _, event := range contextEvents {
		t.Run(event, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := writeContextInjection(event, "CTX"); err != nil {
					t.Fatalf("writeContextInjection: %v", err)
				}
			})

			if !strings.HasPrefix(out, "{") {
				t.Fatalf("stdout must start with '{' to be parsed as JSON, got %q", out)
			}
			if n := strings.Count(strings.TrimSpace(out), "\n"); n != 0 {
				t.Errorf("stdout must be exactly one JSON document, got %d newlines: %q", n, out)
			}

			want := `{"hookSpecificOutput":{"hookEventName":"` + event +
				`","additionalContext":"CTX"}}`
			if strings.TrimSpace(out) != want {
				t.Errorf("stdout = %s\nwant     %s", strings.TrimSpace(out), want)
			}
		})
	}
}

// TestPostToolUseConstructors_ShareTheContractShape guards the collapse of the
// former postToolUseResponse into the shared type: the two PostToolUse
// injections that were already arriving (E-1803's report reinforcement and
// E-1822's claim handoff) must keep emitting exactly what they did before.
func TestPostToolUseConstructors_ShareTheContractShape(t *testing.T) {
	cases := map[string]contextInjection{
		"reportRelay":  reportRelayResponse(),
		"claimHandoff": claimHandoffResponse("HANDOFF"),
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			if resp.HookSpecificOutput.HookEventName != "PostToolUse" {
				t.Errorf("hookEventName = %q, want PostToolUse",
					resp.HookSpecificOutput.HookEventName)
			}
			if resp.HookSpecificOutput.AdditionalContext == "" {
				t.Error("additionalContext is empty")
			}
		})
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// wrote. The hook writes to os.Stdout directly (its stdout IS the return
// channel), so there is no writer to inject.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
