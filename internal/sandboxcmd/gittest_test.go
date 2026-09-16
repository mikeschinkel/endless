package sandboxcmd

import (
	"os/exec"
	"strings"
	"testing"
)

// Shared git helpers for this package's tests. They lived in bind_test.go until
// E-1964 deleted `sandbox bind` along with the XDG_CONFIG_HOME injection it
// wrote; the helpers outlived their file because other suites here drive real
// git repositories too.

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitOrFatal(t, dir, "init", "-q", "-b", "main")
	gitOrFatal(t, dir, "config", "user.email", "test@example.com")
	gitOrFatal(t, dir, "config", "user.name", "test")
	gitOrFatal(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func gitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}
