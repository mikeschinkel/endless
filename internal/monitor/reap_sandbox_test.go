package monitor

import (
	"errors"
	"testing"
)

// withReapSandbox installs a recording seam and restores the previous one.
func withReapSandbox(t *testing.T, fn func(string) error) *[]string {
	t.Helper()
	var seen []string
	prev := ReapSandbox
	t.Cleanup(func() { ReapSandbox = prev })
	ReapSandbox = func(name string) error {
		seen = append(seen, name)
		return fn(name)
	}
	return &seen
}

// TestReapBoundSandboxInvokesSeam pins the E-1904 fix: reaping a worktree must
// also reap the sandbox bound to it. Before this, nothing in the drop/land/reap
// path called Destroy, so every reaped worktree leaked its sandbox forever.
func TestReapBoundSandboxInvokesSeam(t *testing.T) {
	seen := withReapSandbox(t, func(string) error { return nil })

	reapBoundSandbox("e-42")

	if len(*seen) != 1 {
		t.Fatalf("seam called %d times, want 1", len(*seen))
	}
	if (*seen)[0] != "e-42" {
		t.Fatalf("seam got %q, want %q", (*seen)[0], "e-42")
	}
}

// TestReapBoundSandboxNilSeamIsNoop guards the unwired case — a binary that
// never wires the seam (or a test binary) must not panic mid-sweep.
func TestReapBoundSandboxNilSeamIsNoop(t *testing.T) {
	prev := ReapSandbox
	t.Cleanup(func() { ReapSandbox = prev })
	ReapSandbox = nil

	reapBoundSandbox("e-42") // must not panic
}

// TestReapBoundSandboxSwallowsError asserts a sandbox failure cannot abort the
// sweep: the worktree is already gone by this point, and one stubborn sandbox
// must not strand the remaining candidates.
func TestReapBoundSandboxSwallowsError(t *testing.T) {
	seen := withReapSandbox(t, func(string) error { return errors.New("boom") })

	reapBoundSandbox("e-42") // must not panic or propagate

	if len(*seen) != 1 {
		t.Fatalf("seam called %d times, want 1", len(*seen))
	}
}
