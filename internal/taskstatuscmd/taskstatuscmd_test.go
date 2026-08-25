package taskstatuscmd_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// endlessGoBinPath holds the binary TestMain builds once for the whole sweep.
// The verbs are exercised through the real dispatcher rather than by calling
// Run directly, because their contract IS the process contract: what lands on
// stdout and what the exit code is. Run calls os.Exit, so it cannot be tested
// in-process at all.
var endlessGoBinPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "endless-go-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: mkdirtemp: %v\n", err)
		os.Exit(2)
	}
	defer os.RemoveAll(dir)

	bin := filepath.Join(dir, "endless-go")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/endless-go")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build endless-go: %v\n%s\n", err, out)
		os.Exit(2)
	}
	endlessGoBinPath = bin

	os.Exit(m.Run())
}

// run invokes `endless-go task-status <args...>` and returns stdout, stderr and
// the exit code.
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if endlessGoBinPath == "" {
		t.Fatal("endless-go binary not built — TestMain did not run")
	}
	cmd := exec.Command(endlessGoBinPath, append([]string{"task-status"}, args...)...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return outBuf.String(), errBuf.String(), code
}

// lines splits command output into its non-empty lines.
func lines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// TestGetEmitsOneStatusPerLineInGroupOrder pins the documented shape of `get`:
// the client splits on newlines and does nothing else.
func TestGetEmitsOneStatusPerLineInGroupOrder(t *testing.T) {
	stdout, stderr, code := run(t, "get", "derivation-precedence")
	if code != 0 {
		t.Fatalf("get derivation-precedence exited %d: %s", code, stderr)
	}
	got := lines(stdout)
	want := taskstatus.Get(taskstatus.DerivationPrecedence)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("get derivation-precedence = %v, want %v", got, want)
	}
}

// TestGetMatchesRegistryForEveryGroup walks every group so a new one cannot
// ship with a broken verb.
func TestGetMatchesRegistryForEveryGroup(t *testing.T) {
	for _, g := range taskstatus.AllGroups() {
		slug := taskstatus.GroupSlug(g)
		stdout, stderr, code := run(t, "get", slug)
		if code != 0 {
			t.Errorf("get %s exited %d: %s", slug, code, stderr)
			continue
		}
		got := strings.Join(lines(stdout), ",")
		want := strings.Join(taskstatus.Get(g), ",")
		if got != want {
			t.Errorf("get %s = %q, want %q", slug, got, want)
		}
	}
}

// TestHasAnswersByExitCode pins the 0/1 contract — `has` writes nothing.
func TestHasAnswersByExitCode(t *testing.T) {
	stdout, _, code := run(t, "has", "terminal", "confirmed")
	if code != 0 {
		t.Errorf("has terminal confirmed exited %d, want 0", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("has wrote %q to stdout, want nothing", stdout)
	}
	if _, _, code := run(t, "has", "terminal", "ready"); code != 1 {
		t.Errorf("has terminal ready exited %d, want 1", code)
	}
}

func TestSQLListEmitsQuotedCommaList(t *testing.T) {
	stdout, stderr, code := run(t, "sql-list", "pre-judgment")
	if code != 0 {
		t.Fatalf("sql-list pre-judgment exited %d: %s", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "'untriaged','unplanned'"; got != want {
		t.Errorf("sql-list pre-judgment = %q, want %q", got, want)
	}
}

func TestRankEmitsIndexAndSentinel(t *testing.T) {
	stdout, _, code := run(t, "rank", "derivation-precedence", "untriaged")
	if code != 0 || strings.TrimSpace(stdout) != "4" {
		t.Errorf("rank derivation-precedence untriaged = %q (exit %d), want \"4\"", stdout, code)
	}
	stdout, _, code = run(t, "rank", "derivation-precedence", "confirmed")
	if code != 0 || strings.TrimSpace(stdout) != "-1" {
		t.Errorf("rank derivation-precedence confirmed = %q (exit %d), want \"-1\"", stdout, code)
	}
}

func TestLabelAndGlyph(t *testing.T) {
	for _, s := range taskstatus.Get(taskstatus.All) {
		stdout, _, code := run(t, "label", s)
		if code != 0 || strings.TrimSpace(stdout) != taskstatus.Label(s) {
			t.Errorf("label %s = %q (exit %d), want %q", s, stdout, code, taskstatus.Label(s))
		}
		stdout, _, code = run(t, "glyph", s)
		if code != 0 || strings.TrimSpace(stdout) != taskstatus.Glyph(s) {
			t.Errorf("glyph %s = %q (exit %d), want %q", s, stdout, code, taskstatus.Glyph(s))
		}
	}
}

func TestGroupsListsEveryGroup(t *testing.T) {
	stdout, _, code := run(t, "groups")
	if code != 0 {
		t.Fatalf("groups exited %d", code)
	}
	got := strings.Join(lines(stdout), ",")
	want := strings.Join(taskstatus.AllGroupSlugs(), ",")
	if got != want {
		t.Errorf("groups = %q, want %q", got, want)
	}
}

// TestUsageErrorsExitTwo pins that every malformed call is distinguishable from
// `has` answering false. The client relies on this: exit 1 is an answer, exit 2
// is a bug worth raising.
func TestUsageErrorsExitTwo(t *testing.T) {
	cases := [][]string{
		{"get", "nonesuch-group"},
		{"get"},
		{"get", "terminal", "extra"},
		{"has", "terminal", "nonesuch-status"},
		{"has", "terminal"},
		{"sql-list", "nonesuch-group"},
		{"rank", "terminal", "nonesuch-status"},
		{"label", "nonesuch-status"},
		{"glyph", "nonesuch-status"},
		{"nonesuch-verb"},
		{},
	}
	for _, args := range cases {
		stdout, stderr, code := run(t, args...)
		if code != 2 {
			t.Errorf("task-status %v exited %d, want 2", args, code)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Errorf("task-status %v exited 2 with no message on stderr", args)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("task-status %v wrote %q to stdout on failure", args, stdout)
		}
	}
}

// TestHelpExitsZeroAndListsTheRegistry pins that discovery works from the
// command line, and that the usage text is derived rather than typed.
func TestHelpExitsZeroAndListsTheRegistry(t *testing.T) {
	stdout, _, code := run(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0", code)
	}
	for _, slug := range taskstatus.AllGroupSlugs() {
		if !strings.Contains(stdout, slug) {
			t.Errorf("--help does not mention group %q", slug)
		}
	}
	for _, s := range taskstatus.Get(taskstatus.All) {
		if !strings.Contains(stdout, s) {
			t.Errorf("--help does not mention status %q", s)
		}
	}
}
