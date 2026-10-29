package dbcmd

import (
	"os/exec"
	"strings"
	"testing"
)

// gitOrFatal came along from sandboxcmd with the seeding tests (E-2481), which
// drive a real main checkout and worktree.
func gitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}
