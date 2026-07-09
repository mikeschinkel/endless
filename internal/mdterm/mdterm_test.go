package mdterm

import (
	"strings"
	"testing"
)

// hasSGR reports whether s contains any ANSI SGR escape.
func hasSGR(s string) bool { return strings.Contains(s, "\x1b[") }

func TestHeadingIsColored(t *testing.T) {
	out := RenderString("# Title\n")
	if !hasSGR(out) {
		t.Fatalf("heading not colorized: %q", out)
	}
	if !strings.Contains(out, "Title") {
		t.Fatalf("heading text missing: %q", out)
	}
}

func TestInlineCodeColoredAndNotBroken(t *testing.T) {
	const tok = "an-extremely-long-inline-code-token-that-must-stay-whole"
	out := RenderString("prose `" + tok + "` more prose\n")
	if !strings.Contains(out, tok) {
		t.Fatalf("inline code token was altered/broken: %q", out)
	}
	if !strings.Contains(out, codeStyle+tok) {
		t.Fatalf("inline code not styled: %q", out)
	}
}

func TestProseIsOneLogicalLine(t *testing.T) {
	// Two source lines in one paragraph must collapse to one logical line so
	// the terminal soft-wraps instead of hard-breaking mid-content.
	out := RenderString("first line of the paragraph\nsecond line of the paragraph\n")
	body := strings.TrimRight(out, "\n")
	if strings.Contains(body, "\n") {
		t.Fatalf("paragraph was hard-broken into multiple lines: %q", out)
	}
	if !strings.Contains(body, "first line of the paragraph second line") {
		t.Fatalf("soft break not joined with a space: %q", out)
	}
}

func TestFencedCodeVerbatim(t *testing.T) {
	src := "```go\nfunc main() { println(\"x\") }\n```\n"
	out := RenderString(src)
	if !strings.Contains(out, `func main() { println("x") }`) {
		t.Fatalf("fenced code not emitted verbatim: %q", out)
	}
	if !hasSGR(out) {
		t.Fatalf("fenced code not colorized: %q", out)
	}
}

func TestEveryLineEndsWithReset(t *testing.T) {
	out := RenderString("# H\n\ntext with `code` and **bold**\n\n- a\n- b\n")
	for ln := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if ln == "" {
			continue
		}
		if !strings.HasSuffix(ln, reset) {
			t.Fatalf("line does not end with SGR reset (breaks less -R): %q", ln)
		}
	}
}

func TestOrderedListCounters(t *testing.T) {
	out := RenderString("1. one\n2. two\n3. three\n")
	for _, want := range []string{"1.", "2.", "3."} {
		if !strings.Contains(out, want) {
			t.Fatalf("ordered marker %q missing: %q", want, out)
		}
	}
}
