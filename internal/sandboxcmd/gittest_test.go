package sandboxcmd

import (
	"os/exec"
	"strings"
	"testing"
)

// Shared git helper for this package's tests. It lived in bind_test.go until
// E-1964 deleted `sandbox bind`, and outlived its file because other suites
// here drive real git repositories too.

func gitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}
