package sessionstatecmd_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessionstate"
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

// run invokes `endless-go session-state <args...>` and returns stdout, stderr
// and the exit code.
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if endlessGoBinPath == "" {
		t.Fatal("endless-go binary not built — TestMain did not run")
	}
	cmd := exec.Command(endlessGoBinPath, append([]string{"session-state"}, args...)...)
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

// TestGetEmitsOneStatePerLineInGroupOrder pins the documented shape of `get`:
// the client splits on newlines and does nothing else.
func TestGetEmitsOneStatePerLineInGroupOrder(t *testing.T) {
	stdout, stderr, code := run(t, "get", "display-order")
	if code != 0 {
		t.Fatalf("get display-order exited %d: %s", code, stderr)
	}
	got := lines(stdout)
	want := sessionstate.Get(sessionstate.DisplayOrder)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("get display-order = %v, want %v", got, want)
	}
}

// TestGetMatchesRegistryForEveryGroup walks every group so a new one cannot
// ship with a broken verb.
func TestGetMatchesRegistryForEveryGroup(t *testing.T) {
	for _, g := range sessionstate.AllGroups() {
		slug := sessionstate.GroupSlug(g)
		stdout, stderr, code := run(t, "get", slug)
		if code != 0 {
			t.Errorf("get %s exited %d: %s", slug, code, stderr)
			continue
		}
		got := strings.Join(lines(stdout), ",")
		want := strings.Join(sessionstate.Get(g), ",")
		if got != want {
			t.Errorf("get %s = %q, want %q", slug, got, want)
		}
	}
}

// TestHasAnswersByExitCode pins the 0/1 contract — `has` writes nothing.
func TestHasAnswersByExitCode(t *testing.T) {
	stdout, _, code := run(t, "has", "may-write", "idle")
	if code != 0 {
		t.Errorf("has may-write idle exited %d, want 0", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("has wrote %q to stdout, want nothing", stdout)
	}
	if _, _, code := run(t, "has", "may-write", "needs_input"); code != 1 {
		t.Errorf("has may-write needs_input exited %d, want 1", code)
	}
}

func TestSQLListEmitsQuotedCommaList(t *testing.T) {
	stdout, stderr, code := run(t, "sql-list", "live")
	if code != 0 {
		t.Fatalf("sql-list live exited %d: %s", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "'working','idle','needs_input'"; got != want {
		t.Errorf("sql-list live = %q, want %q", got, want)
	}
}

func TestRankEmitsIndexAndSentinel(t *testing.T) {
	stdout, _, code := run(t, "rank", "display-order", "ended")
	if code != 0 || strings.TrimSpace(stdout) != "3" {
		t.Errorf("rank display-order ended = %q (exit %d), want \"3\"", stdout, code)
	}
	stdout, _, code = run(t, "rank", "may-write", "ended")
	if code != 0 || strings.TrimSpace(stdout) != "-1" {
		t.Errorf("rank may-write ended = %q (exit %d), want \"-1\"", stdout, code)
	}
}

func TestLabelAndGlyph(t *testing.T) {
	for _, s := range sessionstate.Get(sessionstate.All) {
		stdout, _, code := run(t, "label", s)
		if code != 0 || strings.TrimSpace(stdout) != sessionstate.Label(s) {
			t.Errorf("label %s = %q (exit %d), want %q", s, stdout, code, sessionstate.Label(s))
		}
		stdout, _, code = run(t, "glyph", s)
		if code != 0 || strings.TrimSpace(stdout) != sessionstate.Glyph(s) {
			t.Errorf("glyph %s = %q (exit %d), want %q", s, stdout, code, sessionstate.Glyph(s))
		}
	}
}

// TestGlyphAnswersForANonState is the one deliberate asymmetry with
// `task-status`, and the Python client depends on it: `session_states.glyph("")`
// is how that side obtains the ⁇ marker without holding a copy of it. If this
// starts exiting 2, `session list` loses its fallback glyph.
func TestGlyphAnswersForANonState(t *testing.T) {
	for _, arg := range []string{"", "prompted", "nonsense"} {
		stdout, stderr, code := run(t, "glyph", arg)
		if code != 0 {
			t.Errorf("glyph %q exited %d: %s", arg, code, stderr)
			continue
		}
		if got := strings.TrimSpace(stdout); got != sessionstate.UnknownGlyph {
			t.Errorf("glyph %q = %q, want %q", arg, got, sessionstate.UnknownGlyph)
		}
	}
}

func TestGroupsListsEveryGroup(t *testing.T) {
	stdout, _, code := run(t, "groups")
	if code != 0 {
		t.Fatalf("groups exited %d", code)
	}
	got := strings.Join(lines(stdout), ",")
	want := strings.Join(sessionstate.AllGroupSlugs(), ",")
	if got != want {
		t.Errorf("groups = %q, want %q", got, want)
	}
}

// TestUsageErrorsExitTwo pins that every malformed call is distinguishable from
// `has` answering false. The client relies on this: exit 1 is an answer, exit 2
// is a bug worth raising.
//
// `glyph` is absent from this list on purpose — see TestGlyphAnswersForANonState.
func TestUsageErrorsExitTwo(t *testing.T) {
	cases := [][]string{
		{"get", "nonesuch-group"},
		{"get"},
		{"get", "live", "extra"},
		{"has", "live", "nonesuch-state"},
		{"has", "live"},
		{"sql-list", "nonesuch-group"},
		{"rank", "live", "nonesuch-state"},
		{"label", "nonesuch-state"},
		{"glyph"},
		{"glyph", "working", "extra"},
		{"transitions", "extra"},
		{"nonesuch-verb"},
		{},
	}
	for _, args := range cases {
		stdout, stderr, code := run(t, args...)
		if code != 2 {
			t.Errorf("session-state %v exited %d, want 2", args, code)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Errorf("session-state %v exited 2 with no message on stderr", args)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("session-state %v wrote %q to stdout on failure", args, stdout)
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
	for _, slug := range sessionstate.AllGroupSlugs() {
		if !strings.Contains(stdout, slug) {
			t.Errorf("--help does not mention group %q", slug)
		}
	}
	for _, s := range sessionstate.Get(sessionstate.All) {
		if !strings.Contains(stdout, s) {
			t.Errorf("--help does not mention state %q", s)
		}
	}
}

// TestTransitionsEmitsTheWholeTable pins the data verb's shape: one edge per
// line, three tab-separated columns, in table order. Compared against the
// package rather than a golden file so an edge added tomorrow needs no edit.
func TestTransitionsEmitsTheWholeTable(t *testing.T) {
	stdout, _, code := run(t, "transitions")
	if code != 0 {
		t.Fatalf("transitions exited %d, want 0", code)
	}
	got := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	table := sessionstate.Transitions()
	if len(got) != len(table) {
		t.Fatalf("transitions emitted %d lines, table has %d edges", len(got), len(table))
	}
	for i, line := range got {
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			t.Fatalf("line %d has %d columns, want 3: %q", i, len(cols), line)
		}
		tr := table[i]
		if cols[0] != tr.From || cols[1] != tr.To || cols[2] != tr.Trigger {
			t.Errorf("line %d = %q, want %q\t%q\t%q", i, line, tr.From, tr.To, tr.Trigger)
		}
	}
}

// TestTransitionsCarriesTheSentinelsVerbatim pins that a creating write emits
// an empty From and an unconditional one emits "*". A client reading this table
// distinguishes the three shapes on that column alone.
func TestTransitionsCarriesTheSentinelsVerbatim(t *testing.T) {
	stdout, _, _ := run(t, "transitions")
	if !strings.Contains(stdout, "\tneeds_input\t`SessionStart`") {
		t.Error("a creating transition does not leave the from column empty")
	}
	if !strings.Contains(stdout, "*\tended\t") {
		t.Error("an unconditional transition does not carry the * sentinel")
	}
}
