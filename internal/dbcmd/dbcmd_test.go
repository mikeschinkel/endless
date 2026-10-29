package dbcmd

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// dispatchHelperEnv makes this test binary run the db dispatcher itself, so a
// test can watch a verb refuse and exit without ending the test process.
const dispatchHelperEnv = "ENDLESS_TEST_DB_DISPATCH"

func TestMain(m *testing.M) {
	if os.Getenv(dispatchHelperEnv) == "1" {
		args := os.Args[1:]
		for i, a := range args {
			if a == "--" {
				args = args[i+1:]
				break
			}
		}
		Run(args)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runDispatch runs `endless-go db <args>` in a child test binary from dir,
// returning combined output and exit code.
func runDispatch(t *testing.T, dir string, args ...string) (out string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^$", "--"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), dispatchHelperEnv+"=1")
	b, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running dispatcher: %v", err)
	}
	return string(b), code
}

// TestUsageOffersSeedSandbox pins where `sandbox init --mode worktree` went
// (E-2481).
func TestUsageOffersSeedSandbox(t *testing.T) {
	if !strings.Contains(usageText(), "seed-sandbox [--force]") {
		t.Errorf("usage lacks seed-sandbox:\n%s", usageText())
	}
}

func TestUnknownCommandRefuses(t *testing.T) {
	out, code := runDispatch(t, t.TempDir(), "init")
	if code != 1 || !strings.Contains(out, `unknown command "init"`) {
		t.Errorf("exit = %d, output:\n%s\nwant exit 1 refusing init as unknown", code, out)
	}
}

// TestSeedSandboxRefusesOutsideAWorktree: there is no sandbox to seed outside
// a task worktree, and nothing is created.
func TestSeedSandboxRefusesOutsideAWorktree(t *testing.T) {
	dir := t.TempDir()
	out, code := runDispatch(t, dir, "seed-sandbox")
	if code != 1 || !strings.Contains(out, "is not inside a task worktree") {
		t.Errorf("exit = %d, output:\n%s\nwant exit 1 and a not-in-worktree refusal", code, out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("seed-sandbox wrote into %s: %v", dir, entries)
	}
}

func TestSeedSandboxTakesNoName(t *testing.T) {
	out, code := runDispatch(t, t.TempDir(), "seed-sandbox", "e-1")
	if code != 1 || !strings.Contains(out, `unexpected argument "e-1"`) {
		t.Errorf("exit = %d, output:\n%s\nwant exit 1 refusing the name", code, out)
	}
}
