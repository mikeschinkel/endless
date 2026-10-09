package sandboxcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// The endless-go binary the destroy tests invoke is built at most once per
// `go test` run, into a directory TestMain removes afterwards. Building once
// matters less for time than for WHERE it builds: the build runs with the
// environment TestMain captured before any test ran, so it compiles against
// the developer's warm GOCACHE. A build under a test's redirected HOME gets
// an empty cache under that temp HOME instead and cold-compiles every
// dependency (E-1908) — which is also why the tests here redirect HOME only
// in the child's cmd.Env.
var (
	sandboxBinEnv  []string
	sandboxBinOnce sync.Once
	sandboxBinDir  string
	sandboxBinPath string
	sandboxBinErr  error
)

func TestMain(m *testing.M) {
	sandboxBinEnv = os.Environ()
	code := m.Run()
	if sandboxBinDir != "" {
		_ = os.RemoveAll(sandboxBinDir)
	}
	os.Exit(code)
}

// buildSandboxBinary returns the package's once-built endless-go so destroy
// tests can invoke it without depending on bin/ being up to date. Returns
// (binary path, sandbox-subcommand prefix args); callers prepend the
// prefix to argv before the subcommand (`destroy ...`).
func buildSandboxBinary(t *testing.T) (string, []string) {
	t.Helper()
	sandboxBinOnce.Do(func() {
		var out []byte
		sandboxBinDir, sandboxBinErr = os.MkdirTemp("", "endless-go-bin-")
		if sandboxBinErr != nil {
			return
		}
		bin := filepath.Join(sandboxBinDir, "endless-go")
		build := exec.Command("go", "build", "-o", bin, "../../cmd/endless-go")
		build.Env = sandboxBinEnv
		out, sandboxBinErr = build.CombinedOutput()
		if sandboxBinErr != nil {
			sandboxBinErr = fmt.Errorf("%w\n%s", sandboxBinErr, out)
			return
		}
		sandboxBinPath = bin
	})
	if sandboxBinErr != nil {
		t.Fatalf("go build: %v", sandboxBinErr)
	}
	return sandboxBinPath, []string{"sandbox"}
}

// TestDestroyMissingExitsNonZeroByDefault confirms the documented default:
// destroy on a missing sandbox exits non-zero with a stderr explanation.
func TestDestroyMissingExitsNonZeroByDefault(t *testing.T) {
	tmp := t.TempDir()
	bin, prefix := buildSandboxBinary(t)

	cmd := exec.Command(bin, append(prefix, "destroy", "does-not-exist")...)
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
	bin, prefix := buildSandboxBinary(t)

	cmd := exec.Command(bin, append(prefix, "destroy", "--if-exists", "does-not-exist")...)
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
	t.Setenv("XDG_CONFIG_HOME", "") // main follows XDG first (E-2186)

	sb, err := Provision("test-if-exists-existing", modeKeep)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	bin, prefix := buildSandboxBinary(t)
	// --force bypasses the live-writer check; this test process itself
	// holds the sandbox dir open via Provision's os.OpenRoot, which would
	// otherwise trigger the refusal. The check itself is exercised in
	// TestDestroyRefusesWithLiveWriter; here we only care that
	// --if-exists doesn't suppress the success message on an existing
	// sandbox.
	cmd := exec.Command(bin, append(prefix, "destroy", "--if-exists", "--force", "test-if-exists-existing")...)
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
