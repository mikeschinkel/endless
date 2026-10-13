package liveview

import (
	"errors"
	"testing"
)

// fakeTmux replaces the pane fit's two tmux calls and counts them.
type fakeTmux struct {
	heightCalls int
	resizes     []int
	resizeErr   error
}

func (f *fakeTmux) install(t *testing.T) {
	t.Helper()
	prevH, prevR := paneWindowHeight, resizePaneHeight
	t.Cleanup(func() { paneWindowHeight, resizePaneHeight = prevH, prevR })
	paneWindowHeight = func(string) int { f.heightCalls++; return 100 }
	resizePaneHeight = func(_ string, rows int) error {
		f.resizes = append(f.resizes, rows)
		return f.resizeErr
	}
}

// E-2228: a frame whose text changes but whose height does not must not ask
// tmux anything.
func TestPaneFit_UnchangedHeightAsksTmuxNothing(t *testing.T) {
	tm := &fakeTmux{}
	tm.install(t)
	fit := &paneFit{pane: "%1"}

	fit.fit("legend\nrow one\n", 80, 1)
	fit.fit("legend\nrow two\n", 80, 1)
	fit.fit("legend\nrow 3!!\n", 80, 1)

	if tm.heightCalls != 1 {
		t.Errorf("window height asked %d times, want 1", tm.heightCalls)
	}
	if len(tm.resizes) != 1 || tm.resizes[0] != 3 {
		t.Errorf("resizes = %v, want [3]", tm.resizes)
	}
}

func TestPaneFit_ChangedHeightRefits(t *testing.T) {
	tm := &fakeTmux{}
	tm.install(t)
	fit := &paneFit{pane: "%1"}

	fit.fit("legend\nrow\n", 80, 1)
	fit.fit("legend\nrow\nrow\n", 80, 2)

	if tm.heightCalls != 2 {
		t.Errorf("window height asked %d times, want 2", tm.heightCalls)
	}
	if len(tm.resizes) != 2 || tm.resizes[1] != 4 {
		t.Errorf("resizes = %v, want [3 4]", tm.resizes)
	}
}

// A refused resize is not retried on every repaint of the same frame, and is
// retried once the terminal is resized.
func TestPaneFit_FailedResizeWaitsForAChange(t *testing.T) {
	tm := &fakeTmux{resizeErr: errors.New("single-pane window")}
	tm.install(t)
	fit := &paneFit{pane: "%1"}

	fit.fit("legend\nrow\n", 80, 1)
	fit.fit("legend\nrow\n", 80, 1)
	if len(tm.resizes) != 1 {
		t.Fatalf("resize attempted %d times for one unchanged frame, want 1", len(tm.resizes))
	}

	fit.resized()
	fit.fit("legend\nrow\n", 80, 1)
	if len(tm.resizes) != 2 || tm.heightCalls != 2 {
		t.Errorf("after a resize signal: resizes=%v heightCalls=%d, want 2 and 2", tm.resizes, tm.heightCalls)
	}
}

func TestPaneFit_NoPaneNoTmux(t *testing.T) {
	tm := &fakeTmux{}
	tm.install(t)
	(&paneFit{}).fit("legend\nrow\n", 80, 1)
	if tm.heightCalls != 0 || len(tm.resizes) != 0 {
		t.Errorf("outside tmux: heightCalls=%d resizes=%v, want none", tm.heightCalls, tm.resizes)
	}
}
