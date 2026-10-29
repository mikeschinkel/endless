package verifycmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// readOnlyReport returns the one CTRF report a run of id wrote.
func readOnlyReport(t *testing.T, id string) (rpt verify.Report) {
	t.Helper()
	dir, err := reportDir(id)
	if err != nil {
		t.Fatalf("reportDir: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(string(dir), "*"+ReportFileSuffix))
	if len(files) != 1 {
		t.Fatalf("found %d reports in %s, want 1", len(files), dir)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err = json.Unmarshal(data, &rpt); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	return rpt
}

// Through the real runner: a shell check runs at the worktree root under the
// run's isolated HOME, a named check labels every test it produces, and a
// failing check's result carries its captured stdout and stderr.
func TestRun_ShellChecks(t *testing.T) {
	realHome := t.TempDir()
	root := writeSuite(t, "E-SH", `
schema = 1
task   = "E-SH"

[[check]]
name     = "runs at the worktree root"
sh       = "ls .endless/tasks"
contains = "E-SH"

[[check]]
name     = "HOME is the run's, not the caller's"
sh       = 'printf %s "$HOME"'
not_contains = "`+realHome+`"

[[check]]
sh = "echo unnamed"

[[check]]
name    = "a runner check"
runner  = "bats"
command = "printf '1..2\nok 1 a\nok 2 b\n'"

[[check]]
name = "fails with output"
sh   = "echo to-stdout; echo to-stderr >&2; exit 4"
`)
	t.Chdir(root)
	t.Setenv("HOME", realHome)
	t.Setenv("XDG_CONFIG_HOME", "")

	code, err := run("E-SH", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (one check fails)", code)
	}

	rpt := readOnlyReport(t, "E-SH")
	if rpt.ReportFormat != verify.ReportFormat || rpt.SpecVersion != verify.SpecVersion {
		t.Errorf("root = %q %q, want %q %q", rpt.ReportFormat, rpt.SpecVersion, verify.ReportFormat, verify.SpecVersion)
	}
	tests := rpt.Results.Tests
	if len(tests) != 6 {
		t.Fatalf("report has %d tests, want 6: %+v", len(tests), tests)
	}
	byName := map[string]verify.Test{}
	for _, tt := range tests {
		byName[tt.Name] = tt
	}

	for _, name := range []string{"runs at the worktree root", "HOME is the run's, not the caller's"} {
		got, ok := byName[name]
		switch {
		case !ok:
			t.Errorf("no test named %q", name)
		case got.Status != verify.StatusPassed:
			t.Errorf("%q: status %s, message:\n%s", name, got.Status, got.Message)
		case got.Labels[verify.LabelCheck] != name:
			t.Errorf("%q: labels = %v, want check=%q", name, got.Labels, name)
		}
	}

	if got, ok := byName["echo unnamed"]; !ok || got.Labels != nil {
		t.Errorf("unnamed sh check: present %v labels %v, want named by its command and unlabelled", ok, got.Labels)
	}

	for _, tt := range tests {
		if tt.Name == "a" || tt.Name == "b" {
			if tt.Labels[verify.LabelCheck] != "a runner check" {
				t.Errorf("runner check test %q: labels = %v, want check=%q", tt.Name, tt.Labels, "a runner check")
			}
		}
	}

	got := byName["fails with output"]
	if got.Status != verify.StatusFailed {
		t.Fatalf("failing check: status %s", got.Status)
	}
	if len(got.Stdout) != 1 || got.Stdout[0] != "to-stdout" || len(got.Stderr) != 1 || got.Stderr[0] != "to-stderr" {
		t.Errorf("failing check stdout/stderr = %q / %q", got.Stdout, got.Stderr)
	}
	for _, want := range []string{"exited 4", "to-stdout", "to-stderr"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("failing check message missing %q:\n%s", want, got.Message)
		}
	}
}

// An sh check honours as = "agent" exactly as a runner check does.
func TestRun_ShellCheckAsAgent(t *testing.T) {
	enterSuite(t, "E-SHA", `
schema = 1
task   = "E-SHA"

[[check]]
name   = "a person has no agent variable"
sh     = 'printf %s "${CLAUDECODE:-}"'
equals = ""

[[check]]
name   = "an agent does"
as     = "agent"
sh     = 'printf %s "${CLAUDECODE:-}"'
equals = "1"
`)
	code, err := run("E-SHA", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		rpt := readOnlyReport(t, "E-SHA")
		t.Errorf("exit code = %d, want 0: %+v", code, rpt.Results.Tests)
	}
}
