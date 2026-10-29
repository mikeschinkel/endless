package sandboxcmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
// worktree sandbox's verbs and nothing that names or initializes a sandbox.
func TestUsageNamesOnlyWorktreeVerbs(t *testing.T) {
	usage := usageText()
	for _, want := range []string{"seed    [--force]", "reset", "claude-settings-repair"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage lacks %q:\n%s", want, usage)
		}
	}
	for _, gone := range []string{"run ", "enter", "init", "list", "prune", "destroy", "<name>", "--mode"} {
		if strings.Contains(usage, gone) {
			t.Errorf("usage still names %q:\n%s", gone, usage)
		}
	}
}

// TestRemovedVerbsAreUnknownCommands: each named-sandbox verb, and every init
// form but the bridged `init --mode worktree`, is refused by the dispatcher as a
// command it does not know, and writes nothing.
func TestRemovedVerbsAreUnknownCommands(t *testing.T) {
	for _, args := range [][]string{
		{"run", "e-1"},
		{"enter", "e-1"},
		{"list"},
		{"prune"},
		{"destroy", "e-1"},
		{"init", "repro-x"},
		{"init", "--mode", "empty", "repro-x"},
		{"init", "--mode", "worktree", "repro-x"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, code, cache := runDispatch(t, args...)
			if code != 1 {
				t.Errorf("exit = %d, want 1; output:\n%s", code, out)
			}
			if !strings.Contains(out, `unknown command "`+args[0]+`"`) {
				t.Errorf("output does not refuse %q as unknown:\n%s", args[0], out)
			}
			assertNoCacheSandboxes(t, cache)
		})
	}
}

// TestSeedRefusesOutsideAWorktree: there is no sandbox to seed outside a task
// worktree, and nothing is created.
func TestSeedRefusesOutsideAWorktree(t *testing.T) {
	out, code, cache := runDispatch(t, "seed")
	if code != 1 || !strings.Contains(out, "is not inside a task worktree") {
		t.Errorf("exit = %d, output:\n%s\nwant exit 1 and a not-in-worktree refusal", code, out)
	}
	assertNoCacheSandboxes(t, cache)
}

func TestSeedTakesNoName(t *testing.T) {
	out, code, _ := runDispatch(t, "seed", "e-1")
	if code != 1 || !strings.Contains(out, `unexpected argument "e-1"`) {
		t.Errorf("exit = %d, output:\n%s\nwant exit 1 refusing the name", code, out)
	}
}

// TestLegacyInitReachesSeed: until E-2484, the old `init --mode worktree`
// spelling runs seed rather than being refused (E-2481's bridge).
func TestLegacyInitReachesSeed(t *testing.T) {
	out, code, _ := runDispatch(t, "init", "--mode", "worktree")
	if code != 1 || !strings.Contains(out, "is not inside a task worktree") ||
		strings.Contains(out, "unknown command") {
		t.Errorf("exit = %d, output:\n%s\nwant seed's own not-in-worktree refusal", code, out)
	}
}

// TestLegacyInitBridgesOnlyTheWorktreeForm pins which argv the bridge accepts.
func TestLegacyInitBridgesOnlyTheWorktreeForm(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{[]string{"init", "--mode", "worktree"}, []string{}},
		{[]string{"init", "--mode=worktree"}, []string{}},
		{[]string{"init", "--force", "--mode", "worktree"}, []string{"--force"}},
		{[]string{"init", "-mode", "worktree", "-force"}, []string{"--force"}},
		{[]string{"init"}, nil},
		{[]string{"init", "repro-x"}, nil},
		{[]string{"init", "--mode", "empty", "repro-x"}, nil},
		{[]string{"init", "--mode", "worktree", "repro-x"}, nil},
		{[]string{"seed"}, nil},
		{nil, nil},
	} {
		got, ok := legacyInit(tt.args)
		if ok != (tt.want != nil) || !slices.Equal(got, tt.want) {
			t.Errorf("legacyInit(%q) = %q, %v; want %q", tt.args, got, ok, tt.want)
		}
	}
}
