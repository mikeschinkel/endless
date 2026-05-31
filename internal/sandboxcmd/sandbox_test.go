package sandboxcmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestProvisionRootBlocksEscape exercises the real Provision code path and
// asserts that os.Root prevents file operations from escaping the sandbox dir.
func TestProvisionRootBlocksEscape(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)

	sb, err := Provision("test-escape", modeEphemeral)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	root := sb.Root()
	cases := []struct {
		name string
		path string
	}{
		{"parent_relative", "../escape.txt"},
		{"absolute", "/etc/passwd-fake"},
		{"deep_relative", "../../escape.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := root.Create(tc.path)
			if err == nil {
				_ = f.Close()
				t.Fatalf("expected Create(%q) via Root to fail, got nil", tc.path)
			}
		})
	}

	// Sanity: legitimate writes succeed.
	f, err := root.Create("legit.txt")
	if err != nil {
		t.Fatalf("legit Create failed: %v", err)
	}
	_ = f.Close()
	if _, err := os.Stat(filepath.Join(sb.Dir, "legit.txt")); err != nil {
		t.Fatalf("legit file not visible: %v", err)
	}

	// Meta file written via Root should exist.
	if _, err := os.Stat(filepath.Join(sb.Dir, metaFilename)); err != nil {
		t.Fatalf("meta file missing: %v", err)
	}
}

// TestProvisionCollision verifies named-sandbox collisions error cleanly.
func TestProvisionCollision(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)

	sb1, err := Provision("dup", modeKeep)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb1.Destroy() })

	if _, err := Provision("dup", modeKeep); err == nil {
		t.Fatal("expected collision error, got nil")
	}
}

// TestClassify verifies the live/in-use/orphaned label semantics.
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		meta SandboxMeta
		want sandboxState
	}{
		{
			name: "keep_alive",
			meta: SandboxMeta{Mode: modeKeep, CreatorPID: os.Getpid()},
			want: stateInUse,
		},
		{
			name: "keep_dead_pid",
			meta: SandboxMeta{Mode: modeKeep, CreatorPID: 1},
			want: stateInUse,
		},
		{
			name: "ephemeral_alive",
			meta: SandboxMeta{Mode: modeEphemeral, CreatorPID: os.Getpid()},
			want: stateLive,
		},
		{
			name: "ephemeral_dead",
			// PID 0 is invalid; isAlive returns false for it.
			meta: SandboxMeta{Mode: modeEphemeral, CreatorPID: 0},
			want: stateOrphaned,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.meta); got != tc.want {
				t.Fatalf("classify: got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestSupervisorSetsProcessGroup asserts the child becomes its own pgroup
// leader, distinct from the test parent's pgroup.
func TestSupervisorSetsProcessGroup(t *testing.T) {
	sup := NewSupervisor("sh", "-c", "echo $$; sleep 30")
	var stdout bytes.Buffer
	sup.Stdout = &stdout
	sigCh := make(chan os.Signal, 1)
	sup.Signals = sigCh

	done := make(chan struct{})
	go func() {
		_ = sup.Run()
		close(done)
	}()

	waitFor(t, func() bool { return strings.TrimSpace(stdout.String()) != "" })
	childPID := mustAtoi(t, strings.TrimSpace(stdout.String()))

	pgid, err := syscall.Getpgid(childPID)
	if err != nil {
		t.Fatalf("Getpgid(%d): %v", childPID, err)
	}
	if pgid != childPID {
		t.Fatalf("expected pgid==%d (own group), got %d", childPID, pgid)
	}
	if pgid == os.Getpid() {
		t.Fatalf("child shares pgroup with test parent (%d)", os.Getpid())
	}

	sigCh <- syscall.SIGTERM
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Supervisor.Run did not return within 2s of signal")
	}
}

// TestSupervisorKillsDescendants asserts a backgrounded grandchild is reaped
// after the foreground child exits — the original E-1114 leak shape.
//
// The grandchild's stdout/stderr are redirected to /dev/null so it doesn't
// hold the test-side pipe open. In production the supervisor's Stdout is
// os.Stdout (a real file), so Go's exec package does not create an internal
// pipe and cmd.Wait returns as soon as the immediate child exits — we
// reproduce that here by detaching the descendant from the buffer-backed
// pipe.
func TestSupervisorKillsDescendants(t *testing.T) {
	sup := NewSupervisor("sh", "-c", "sleep 60 >/dev/null 2>&1 & echo $!; exit 0")
	var stdout bytes.Buffer
	sup.Stdout = &stdout

	if err := sup.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	grandchild := mustAtoi(t, strings.TrimSpace(stdout.String()))
	err := syscall.Kill(grandchild, 0)
	if err == nil {
		// Best-effort cleanup so we don't leave a stray sleep behind.
		_ = syscall.Kill(grandchild, syscall.SIGKILL)
		t.Fatalf("grandchild PID %d still alive after Run returned", grandchild)
	}
	if !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill(%d,0): want ESRCH, got %v", grandchild, err)
	}
}

// TestSupervisorSignalForwarding asserts a signal on Signals reaches the
// whole pgroup (not just the foreground child) within a bounded wait. The
// grandchild's stdout/stderr are detached for the same pipe-buffering
// reason as TestSupervisorKillsDescendants.
func TestSupervisorSignalForwarding(t *testing.T) {
	sup := NewSupervisor("sh", "-c", "sleep 60 >/dev/null 2>&1 & echo $!; sleep 60")
	var stdout bytes.Buffer
	sup.Stdout = &stdout
	sigCh := make(chan os.Signal, 1)
	sup.Signals = sigCh

	done := make(chan struct{})
	go func() {
		_ = sup.Run()
		close(done)
	}()

	waitFor(t, func() bool { return strings.TrimSpace(stdout.String()) != "" })
	grandchild := mustAtoi(t, strings.TrimSpace(stdout.String()))

	sigCh <- syscall.SIGTERM
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Supervisor.Run did not return within 2s of signal")
	}

	if err := syscall.Kill(grandchild, 0); !errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(grandchild, syscall.SIGKILL)
		t.Fatalf("grandchild not reaped: kill(%d,0)=%v", grandchild, err)
	}
}

// waitFor polls cond every 20ms up to 10s, fatal if it never becomes
// true. The 10s ceiling absorbs scheduler latency when `just test-go`
// fans packages out in parallel (E-1506); the success path early-returns
// in milliseconds so the bumped ceiling does not slow normal runs.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within 10s")
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("atoi(%q): %v", s, err)
	}
	return n
}
