package sandboxcmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dispatchHelperEnv makes this test binary run the sandbox dispatcher itself,
// so a test can watch a verb refuse and exit without ending the test process.
const dispatchHelperEnv = "ENDLESS_TEST_SANDBOX_DISPATCH"

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

// runDispatch runs `endless-go sandbox <args>` in a child test binary whose
// cache directory is a fresh temp dir, returning combined output, exit code and
// that cache directory.
func runDispatch(t *testing.T, args ...string) (out string, code int, cache string) {
	t.Helper()
	cache = t.TempDir()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^$", "--"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		dispatchHelperEnv+"=1",
		"XDG_CACHE_HOME="+cache,
	)
	b, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running dispatcher: %v", err)
	}
	return string(b), code, cache
}

// assertNoCacheSandboxes fails if anything was written to the cache
// directory's sandboxes/ subdirectory, where named sandboxes used to live.
func assertNoCacheSandboxes(t *testing.T, cache string) {
	t.Helper()
	for _, dir := range []string{
		filepath.Join(cache, "endless", "sandboxes"),
		filepath.Join(cache, "sandboxes"),
	} {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%s exists; nothing may write named sandboxes any more (E-2481)", dir)
		}
	}
}

// TestUsageNamesOnlyWorktreeVerbs pins E-2481: the usage text offers the
// worktree sandbox's verbs and nothing that names a sandbox.
func TestUsageNamesOnlyWorktreeVerbs(t *testing.T) {
	usage := usageText()
	for _, want := range []string{"init    --mode worktree", "reset", "claude-settings-repair"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage lacks %q:\n%s", want, usage)
		}
	}
	for _, gone := range []string{"run ", "enter", "list", "prune", "destroy", "<name>", "empty", "--mode seed", "clone"} {
		if strings.Contains(usage, gone) {
			t.Errorf("usage still names %q:\n%s", gone, usage)
		}
	}
}

// TestRemovedVerbsAreUnknownCommands: each named-sandbox verb is refused by
// the dispatcher as a command it does not know, and writes nothing.
func TestRemovedVerbsAreUnknownCommands(t *testing.T) {
	for _, verb := range []string{"run", "enter", "list", "prune", "destroy"} {
		t.Run(verb, func(t *testing.T) {
			out, code, cache := runDispatch(t, verb, "e-1")
			if code != 1 {
				t.Errorf("exit = %d, want 1; output:\n%s", code, out)
			}
			if !strings.Contains(out, `unknown command "`+verb+`"`) {
				t.Errorf("output does not refuse %q as unknown:\n%s", verb, out)
			}
			assertNoCacheSandboxes(t, cache)
		})
	}
}

// TestInitRefusesEveryModeButWorktree: `init <name>` used to provision a named
// sandbox under the cache directory. Every form of it now refuses, naming the
// one mode left, and creates nothing.
func TestInitRefusesEveryModeButWorktree(t *testing.T) {
	for _, args := range [][]string{
		{"init", "repro-x"},
		{"init", "--mode", "empty", "repro-x"},
		{"init", "--mode", "seed", "repro-x"},
		{"init", "--mode", "clone", "repro-x"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, code, cache := runDispatch(t, args...)
			if code != 1 {
				t.Errorf("exit = %d, want 1; output:\n%s", code, out)
			}
			if !strings.Contains(out, "the only mode is --mode worktree") {
				t.Errorf("output does not name the worktree mode:\n%s", out)
			}
			assertNoCacheSandboxes(t, cache)
		})
	}
}
