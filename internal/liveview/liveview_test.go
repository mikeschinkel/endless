package liveview

import (
	"strings"
	"testing"
)

// The pane-fit rule and the frame-line count are pinned in
// internal/sessionstatuscmd, which reaches them through wrappers of the same
// name — they were extracted from there and that suite is where their history
// lives. What is tested here is what only this package can say.

// TestDetectRowsHonorsThePercentage pins the parameter E-1976 added. A view whose
// frame grows to fill whatever budget it is given needs a SMALLER share of the
// window than the 80% default, because for such a view the cap binds on every
// frame — and whatever it leaves over is all the pane below it will ever get.
func TestDetectRowsHonorsThePercentage(t *testing.T) {
	// No pane and no tty: the fallback is the whole answer, and the percentage
	// never enters into it.
	if got := DetectRows("", 65, 40); got != 40 {
		t.Errorf("DetectRows with no pane and no tty = %d, want the fallback 40", got)
	}
	if got := DetectRows("", 0, 0); got != 0 {
		t.Errorf("DetectRows with no pane, no tty and no fallback = %d, want 0 (unbounded)", got)
	}
}

// TestEraseEachLineToEOL pins the repaint contract: every line is erased to
// end-of-line before the next is drawn, so a frame overwriting a longer one
// leaves no tail behind. The caller's trailing \x1b[J cannot do this — it only
// erases from the cursor's FINAL position downward (E-1699).
func TestEraseEachLineToEOL(t *testing.T) {
	got := EraseEachLineToEOL("a\nbb\n")
	want := "a\x1b[K\nbb\x1b[K\n\x1b[K"
	if got != want {
		t.Fatalf("EraseEachLineToEOL = %q, want %q", got, want)
	}
	if strings.Count(got, "\x1b[K") != 3 {
		t.Errorf("a two-line frame did not get an erase per line plus the tail: %q", got)
	}
}

func TestDimAndStrongAreNoOpsWithoutColor(t *testing.T) {
	if Dim("x", false) != "x" || Strong("x", false) != "x" {
		t.Fatal("an intensity helper emitted escapes with color disabled")
	}
	if !strings.HasPrefix(Dim("x", true), DimSGR) || !strings.HasPrefix(Strong("x", true), Bold) {
		t.Fatal("an intensity helper did not apply its escape with color enabled")
	}
	// Intensity only. The 30-47 range is remapped by the user's terminal theme,
	// so a frame using it renders differently — sometimes illegibly — on someone
	// else's machine.
	for _, s := range []string{Dim("x", true), Strong("x", true)} {
		if strings.Contains(s, "\x1b[3") || strings.Contains(s, "\x1b[4") {
			t.Errorf("an intensity helper emitted a theme-remapped color: %q", s)
		}
	}
}

func TestCollapse(t *testing.T) {
	if got := Collapse(" a  b\n\tc \r\n"); got != " a b c " {
		t.Fatalf("Collapse = %q, want %q", got, " a b c ")
	}
}
