package main

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// TestFindLiveWritersEmpty verifies the no-writers case returns an empty
// slice with no error (lsof exits 1 when nothing matches).
func TestFindLiveWritersEmpty(t *testing.T) {
	dir := t.TempDir()
	writers := findLiveWriters(dir)
	if len(writers) != 0 {
		t.Fatalf("expected no writers, got %d: %+v", len(writers), writers)
	}
}

// TestFindLiveWritersDetectsCwd spawns a sleep with cwd inside the sandbox
// dir and confirms it appears in findLiveWriters' result. The result-shape
// check (PID lookup) is the contract we depend on.
func TestFindLiveWritersDetectsCwd(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", "sleep 30")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_, _ = cmd.Process.Wait()
	})

	// Give the kernel a moment to register the cwd.
	waitFor(t, func() bool {
		return len(findLiveWriters(dir)) > 0
	})

	writers := findLiveWriters(dir)
	if len(writers) == 0 {
		t.Fatal("expected at least one writer, got none")
	}
	found := false
	for _, w := range writers {
		if w.PID == cmd.Process.Pid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("sleep PID %d not in writers list: %+v", cmd.Process.Pid, writers)
	}
}

// TestParseLsofPidCommandOutput exercises the parser against a hand-built
// fixture so we can verify behavior without spawning processes.
func TestParseLsofPidCommandOutput(t *testing.T) {
	fixture := []byte("p1234\ncsh\nfcwd\nn/tmp/x\np5678\nczsh\nf3\nn/tmp/x/foo\n")
	got := parseLsofPidCommandOutput(fixture)
	if len(got) != 2 {
		t.Fatalf("expected 2 writers, got %d: %+v", len(got), got)
	}
	if got[0].PID != 1234 || got[0].Name != "sh" {
		t.Errorf("writer 0: got %+v, want {1234, sh}", got[0])
	}
	if got[1].PID != 5678 || got[1].Name != "zsh" {
		t.Errorf("writer 1: got %+v, want {5678, zsh}", got[1])
	}
}

// TestParseLsofDeduplicatesPID confirms a PID appearing on multiple p-lines
// (rare but possible if lsof emits per-fd grouping) is reported once.
func TestParseLsofDeduplicatesPID(t *testing.T) {
	fixture := []byte("p1234\ncsh\nfcwd\nn/tmp/x\np1234\ncsh\nf3\nn/tmp/x/foo\n")
	got := parseLsofPidCommandOutput(fixture)
	if len(got) != 1 {
		t.Fatalf("expected 1 writer (deduped), got %d: %+v", len(got), got)
	}
}

// TestDestroyRefusesWithLiveWriter is an end-to-end test: build the binary,
// create a sandbox, spawn a process with cwd inside it, then run destroy
// and confirm it refuses with the offending PID in stderr.
func TestDestroyRefusesWithLiveWriter(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)

	sb, err := Provision("test-live-writer", modeKeep)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	// Spawn a process whose cwd is inside the sandbox.
	holder := exec.Command("sh", "-c", "sleep 30")
	holder.Dir = sb.Dir
	holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := holder.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-holder.Process.Pid, syscall.SIGTERM)
		_, _ = holder.Process.Wait()
	})

	// Wait for lsof to see the cwd.
	waitFor(t, func() bool {
		return len(findLiveWriters(sb.Dir)) > 0
	})

	// Build the binary into a temp path so we don't depend on bin/ being
	// up to date.
	bin := filepath.Join(t.TempDir(), "endless-sandbox")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	destroy := exec.Command(bin, "destroy", "test-live-writer")
	destroy.Env = append(destroy.Environ(),
		"XDG_CACHE_HOME="+tmp,
		"HOME="+tmp)
	out, err := destroy.CombinedOutput()
	if err == nil {
		t.Fatalf("expected destroy to fail with live writer, got success.\nout: %s", out)
	}
	if !contains(out, "refusing to destroy") {
		t.Errorf("stderr missing refusal: %s", out)
	}
	if !contains(out, "--force") {
		t.Errorf("stderr missing --force hint: %s", out)
	}
}

// TestDestroyForceOverridesLiveWriterCheck confirms --force bypasses the gate.
func TestDestroyForceOverridesLiveWriterCheck(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)

	sb, err := Provision("test-force-destroy", modeKeep)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	holder := exec.Command("sh", "-c", "sleep 30")
	holder.Dir = sb.Dir
	holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := holder.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-holder.Process.Pid, syscall.SIGTERM)
		_, _ = holder.Process.Wait()
	})

	waitFor(t, func() bool {
		return len(findLiveWriters(sb.Dir)) > 0
	})

	bin := filepath.Join(t.TempDir(), "endless-sandbox")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	destroy := exec.Command(bin, "destroy", "--force", "test-force-destroy")
	destroy.Env = append(destroy.Environ(),
		"XDG_CACHE_HOME="+tmp,
		"HOME="+tmp)
	out, err := destroy.CombinedOutput()
	if err != nil {
		t.Fatalf("--force destroy failed: %v\n%s", err, out)
	}
	if !contains(out, "Destroyed") {
		t.Errorf("stdout missing Destroyed confirmation: %s", out)
	}
}

func contains(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
