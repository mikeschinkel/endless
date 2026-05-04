package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// buildSandboxBinary builds endless-sandbox into a temp path so destroy tests
// can invoke it without depending on bin/ being up to date.
func buildSandboxBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "endless-sandbox")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// TestDestroyMissingExitsNonZeroByDefault confirms the documented default:
// destroy on a missing sandbox exits non-zero with a stderr explanation.
func TestDestroyMissingExitsNonZeroByDefault(t *testing.T) {
	tmp := t.TempDir()
	bin := buildSandboxBinary(t)

	cmd := exec.Command(bin, "destroy", "does-not-exist")
	cmd.Env = append(cmd.Environ(),
		"XDG_CACHE_HOME="+tmp,
		"HOME="+tmp)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success.\nout: %s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
		}
	}
	if !contains(out, "does not exist") {
		t.Errorf("stderr missing 'does not exist': %s", out)
	}
}

// TestDestroyMissingWithIfExistsExitsZeroSilently is the script-friendly
// path: --if-exists turns missing into a no-op.
func TestDestroyMissingWithIfExistsExitsZeroSilently(t *testing.T) {
	tmp := t.TempDir()
	bin := buildSandboxBinary(t)

	cmd := exec.Command(bin, "destroy", "--if-exists", "does-not-exist")
	cmd.Env = append(cmd.Environ(),
		"XDG_CACHE_HOME="+tmp,
		"HOME="+tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 with --if-exists, got %v.\nout: %s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("expected silent success, got output: %s", out)
	}
}

// TestDestroyExistingWithIfExistsStillDestroysAndReports confirms --if-exists
// only changes the missing-sandbox case; an existing sandbox is destroyed
// normally with the usual confirmation message.
func TestDestroyExistingWithIfExistsStillDestroysAndReports(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)

	sb, err := Provision("test-if-exists-existing", modeKeep)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	bin := buildSandboxBinary(t)
	// --force bypasses the live-writer check; this test process itself
	// holds the sandbox dir open via Provision's os.OpenRoot, which would
	// otherwise trigger the refusal. The check itself is exercised in
	// TestDestroyRefusesWithLiveWriter; here we only care that
	// --if-exists doesn't suppress the success message on an existing
	// sandbox.
	cmd := exec.Command(bin, "destroy", "--if-exists", "--force", "test-if-exists-existing")
	cmd.Env = append(cmd.Environ(),
		"XDG_CACHE_HOME="+tmp,
		"HOME="+tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("destroy failed: %v\n%s", err, out)
	}
	if !contains(out, "Destroyed") {
		t.Errorf("expected 'Destroyed' in output, got: %s", out)
	}
}
