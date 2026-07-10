package verifycmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-dt"
)

// writeSuite creates <root>/.endless/tasks/<id>/verify.toml with the given body
// and returns root.
func writeSuite(t *testing.T, id, body string) (root string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, ".endless", "tasks", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return root
}

// enterSuite chdirs into a fresh project holding one suite and redirects HOME so
// the CTRF artifact lands in a temp dir, not the developer's real cache.
func enterSuite(t *testing.T, id, body string) {
	t.Helper()
	root := writeSuite(t, id, body)
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
}

func TestRun_PassingSuite(t *testing.T) {
	enterSuite(t, "E-PASS", `
schema = 1
task   = "E-PASS"
[[check]]
runner  = "bats"
command = "printf '1..2\nok 1 a\nok 2 b\n'"
format  = "tap"
`)
	code, err := run("E-PASS", false)
	if err != nil {
		t.Fatalf("run: unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestRun_FailingSuite(t *testing.T) {
	enterSuite(t, "E-FAIL", `
schema = 1
task   = "E-FAIL"
[[check]]
runner  = "bats"
command = "printf '1..2\nok 1 a\nnot ok 2 b\n'; exit 1"
format  = "tap"
`)
	code, err := run("E-FAIL", false)
	if err != nil {
		t.Fatalf("run: unexpected error: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

// A non-zero exit not explained by a parsed test failure is a runner/build
// error, which must surface as a loud error rather than pass.
func TestRun_RunnerErrorFailsLoudly(t *testing.T) {
	enterSuite(t, "E-ERR", `
schema = 1
task   = "E-ERR"
[[check]]
runner  = "bats"
command = "printf '1..1\nok 1 a\n'; exit 3"
format  = "tap"
`)
	_, err := run("E-ERR", false)
	if err == nil {
		t.Fatal("expected a loud error for non-zero exit with no failures, got nil")
	}
}

func TestRun_NeedsGuard(t *testing.T) {
	enterSuite(t, "E-NEEDS", `
schema = 1
task   = "E-NEEDS"
needs  = ["postgres"]
[[check]]
runner  = "bats"
command = "printf '1..1\nok 1 a\n'"
format  = "tap"
`)
	_, err := run("E-NEEDS", false)
	if err == nil || !strings.Contains(err.Error(), "Tier 0") {
		t.Fatalf("expected a Tier-0 needs guard error, got %v", err)
	}
}

func TestRun_SeedGuard(t *testing.T) {
	enterSuite(t, "E-SEED", `
schema = 1
task   = "E-SEED"
seed   = ["fixtures/x.json"]
[[check]]
runner  = "bats"
command = "printf '1..1\nok 1 a\n'"
format  = "tap"
`)
	_, err := run("E-SEED", false)
	if err == nil || !strings.Contains(err.Error(), "seed") {
		t.Fatalf("expected a seed guard error, got %v", err)
	}
}

func TestRun_UnknownTask(t *testing.T) {
	enterSuite(t, "E-REAL", `
schema = 1
task   = "E-REAL"
[[check]]
runner  = "bats"
command = "printf '1..1\nok 1 a\n'"
format  = "tap"
`)
	_, err := run("E-NOPE", false)
	if err == nil || !strings.Contains(err.Error(), "no verification suite") {
		t.Fatalf("expected a no-suite error naming the missing task, got %v", err)
	}
}

// The isolated env must replace HOME/XDG_CONFIG_HOME (not merely append) so a
// suite can never read the developer's real values.
func TestIsolatedEnvReplacesHomeAndXDG(t *testing.T) {
	t.Setenv("HOME", "/real/home")
	t.Setenv("XDG_CONFIG_HOME", "/real/xdg")
	runDir := dt.DirPath(t.TempDir())

	env, err := isolatedEnv(runDir)
	if err != nil {
		t.Fatalf("isolatedEnv: %v", err)
	}
	homes := envValues(env, "HOME=")
	xdgs := envValues(env, "XDG_CONFIG_HOME=")
	if len(homes) != 1 || len(xdgs) != 1 {
		t.Fatalf("want exactly one HOME and one XDG_CONFIG_HOME, got HOME=%v XDG=%v", homes, xdgs)
	}
	if homes[0] == "/real/home" || xdgs[0] == "/real/xdg" {
		t.Errorf("isolated env did not replace real values: HOME=%q XDG=%q", homes[0], xdgs[0])
	}
	if !strings.HasPrefix(homes[0], string(runDir)) {
		t.Errorf("isolated HOME %q is not under the per-run dir %q", homes[0], runDir)
	}
}

func TestCountsLine(t *testing.T) {
	cases := []struct {
		name string
		s    verify.Summary
		want string
	}{
		{"all pass", verify.Summary{Tests: 3, Passed: 3}, "3 passed (3 tests)"},
		{"with failures leads with failed", verify.Summary{Tests: 3, Passed: 1, Failed: 2}, "2 failed, 1 passed (3 tests)"},
		{"with skips", verify.Summary{Tests: 2, Passed: 1, Skipped: 1}, "1 passed, 1 skipped (2 tests)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countsLine(tc.s); got != tc.want {
				t.Errorf("countsLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func envValues(env []string, prefix string) (vals []string) {
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			vals = append(vals, strings.TrimPrefix(kv, prefix))
		}
	}
	return vals
}
