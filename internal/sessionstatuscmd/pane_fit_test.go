package sessionstatuscmd

import "testing"

// TestFrameLines pins the row count of a rendered frame. renderTo emits every
// line with Fprintln, so the newline count is the line count — counting split
// segments instead would over-count by one for the empty trailing segment.
func TestFrameLines(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  int
	}{
		{"empty frame", "", 0},
		{"hint only", "  no active task — claim or bind one\n", 1},
		{"legend + 3 rows", "legend\nrow1\nrow2\nrow3\n", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := frameLines(tt.frame); got != tt.want {
				t.Fatalf("frameLines(%q) = %d, want %d", tt.frame, got, tt.want)
			}
		})
	}
}

// TestPaneHeightForFrame pins the sizing rule the monitor applies to its own
// pane: frame rows + one slack row, floored at monitorPaneMinHeight and capped
// at monitorPanePctOfWindow percent of the window so the shell pane below always
// keeps a usable share.
func TestPaneHeightForFrame(t *testing.T) {
	tests := []struct {
		name         string
		lines        int
		windowHeight int
		want         int
	}{
		{"legend + 6 rows in a tall window", 7, 50, 8},
		{"hint-only frame floors at the minimum", 1, 50, 2},
		{"empty frame floors at the minimum", 0, 50, 2},
		{"unknown window height applies no cap", 40, 0, 41},
		{"long frame capped at 80% of the window", 40, 50, 40},
		{"cap binds before the frame is huge", 100, 20, 16},
		{"cap never falls below the minimum", 30, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paneHeightForFrame(tt.lines, tt.windowHeight)
			if got != tt.want {
				t.Fatalf("paneHeightForFrame(%d, %d) = %d, want %d",
					tt.lines, tt.windowHeight, got, tt.want)
			}
		})
	}
}

// TestFitPaneToFrame_NoTmux pins that outside tmux (no TMUX_PANE) the fit is a
// no-op returning the prior height — `session monitor` in a plain terminal must
// not shell out to tmux on every repaint.
func TestFitPaneToFrame_NoTmux(t *testing.T) {
	if got := fitPaneToFrame("", "legend\nrow\n", 7); got != 7 {
		t.Fatalf("fitPaneToFrame with no pane = %d, want 7 (unchanged)", got)
	}
}
