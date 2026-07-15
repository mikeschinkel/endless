package monitor_test

// End-to-end (CLI) verification of the worktree-anomaly surface using
// rogpeppe/go-internal's testscript, driven by the .txtar files under
// testdata/worktreecheck/. This is the reference exemplar for the "txtar" form
// of an Endless verification suite (E-1596/E-1605): a .txtar suite is just a Go
// test harness calling testscript.Run, so it runs under plain `go test` (safe in
// a bare clone with no Endless installed) and is captured by the `endless verify`
// runner as an ordinary `gotest` check via `go test -json`. There is no separate
// testscript binary or result format.
//
// The system under test is the Endless CLI. The harness builds `endless-go` from
// this repo once per run and puts it on the script PATH, so each .txtar invokes
// the real binary end-to-end. A downstream project's harness would build its own
// CLI the same way. .txtar files live beside this harness (testdata/worktreecheck)
// so the whole suite is discoverable and editable as one unit; the per-task
// verify.toml manifest merely points `gotest` at this test by name.
//
// Each script builds a throwaway git repo, mutates it into a known state, runs
// `endless-go session-query worktree-anomalies` (the Go subcommand that backs
// `endless worktree check`), and asserts the terse anomaly lines and exit code
// (0 clean, 1 anomalies present) that monitor.WorktreeAnomaliesAt produces.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestWorktreeCheckScripts runs every testdata/worktreecheck/*.txtar script
// against a freshly built endless-go binary.
func TestWorktreeCheckScripts(t *testing.T) {
	binDir := buildEndlessGo(t)

	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "worktreecheck"),
		Setup: func(env *testscript.Env) error {
			// Make the built endless-go the first thing on PATH so scripts can
			// `exec endless-go ...`; keep the host PATH so `git` stays reachable.
			env.Vars = append(env.Vars,
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				// Deterministic identity so `git commit` succeeds under the
				// script's isolated HOME (no ~/.gitconfig is present there).
				"GIT_AUTHOR_NAME=endless-test",
				"GIT_AUTHOR_EMAIL=endless-test@example.com",
				"GIT_COMMITTER_NAME=endless-test",
				"GIT_COMMITTER_EMAIL=endless-test@example.com",
			)
			return nil
		},
	})
}

var (
	buildOnce sync.Once
	builtDir  string
	buildErr  error
)

// buildEndlessGo builds the endless-go binary once per test process and returns
// the directory containing it. Building from source (rather than reusing a
// prebuilt bin/) keeps the harness hermetic and bare-clone-safe: `go test` alone,
// with no Endless installed, still exercises the real CLI. The Go toolchain is
// the only prerequisite — no standalone testscript tool.
func buildEndlessGo(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root := repoRoot(t)
		dir := t.TempDir()
		out := filepath.Join(dir, "endless-go")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/endless-go")
		cmd.Dir = root
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("building endless-go: %v\n%s", err, combined)
			return
		}
		builtDir = dir
	})
	if buildErr != nil {
		t.Fatalf("build endless-go: %v", buildErr)
	}
	// t.TempDir() from the first caller is cleaned up when THAT test ends; the
	// harness has a single caller (TestWorktreeCheckScripts), so the directory
	// outlives every subtest testscript spawns.
	return builtDir
}

// repoRoot walks up from the test's working directory (the package dir) to the
// module root — the first ancestor holding a go.mod — so `go build ./cmd/...`
// resolves regardless of where the package sits.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
