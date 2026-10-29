package verifycmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// pytestReportJSON is a one-test pytest-json-report document naming
// tests/test_x.py, as each of two subprojects would emit it.
const pytestReportJSON = `{"created": 1, "duration": 0.1, "tests": [` +
	`{"nodeid": "tests/test_x.py::test_a", "outcome": "passed"}]}`

// Every runner runs its process in the check's dir, paths resolve against it,
// setup still runs at the repo root, and a pytest check's suites carry the dir
// so two subprojects' tests/test_x.py stay distinct while gotest's package
// names do not change.
func TestRun_ChecksRunInDir(t *testing.T) {
	root := writeSuite(t, "E-DIR", `
schema = 1
task   = "E-DIR"
setup  = ["pwd > setup-cwd"]

[[check]]
name   = "gotest in a nested module"
runner = "gotest"
dir    = "nested"
paths  = ["./..."]

[[check]]
name     = "sh in dir"
sh       = "cat marker"
dir      = "nested"
equals   = "nested-marker"

[[check]]
name    = "generic in dir"
runner  = "bats"
dir     = "nested"
command = "test -f marker && printf '1..1\nok 1 generic-in-dir\n'"

[[check]]
name    = "pytest in py/a"
runner  = "pytest"
dir     = "py/a"
command = "cat report.json"

[[check]]
name    = "pytest in py/b"
runner  = "pytest"
dir     = "py/b"
command = "cat report.json"
`)
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}
	write("nested/go.mod", "module nested\n\ngo 1.21\n")
	write("nested/ok_test.go", "package nested\n\nimport \"testing\"\n\nfunc TestNested(t *testing.T) {}\n")
	write("nested/marker", "nested-marker\n")
	write("py/a/report.json", pytestReportJSON)
	write("py/b/report.json", pytestReportJSON)

	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	code, err := run("E-DIR", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	setupCwd, err := os.ReadFile(filepath.Join(root, "setup-cwd"))
	if err != nil {
		t.Fatalf("setup did not run at the root: %v", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(root)
	gotRoot, _ := filepath.EvalSymlinks(string(setupCwd[:len(setupCwd)-1]))
	if gotRoot != wantRoot {
		t.Errorf("setup ran in %q, want the root %q", gotRoot, wantRoot)
	}

	byCheck := map[string][]verify.Test{}
	for _, tt := range readOnlyReport(t, "E-DIR").Results.Tests {
		if tt.Status != verify.StatusPassed {
			t.Errorf("%q: status %s, message:\n%s", tt.Name, tt.Status, tt.Message)
		}
		byCheck[tt.Labels[verify.LabelCheck]] = append(byCheck[tt.Labels[verify.LabelCheck]], tt)
	}

	if got := byCheck["gotest in a nested module"]; len(got) != 1 || got[0].Name != "TestNested" ||
		len(got[0].Suite) == 0 || got[0].Suite[0] != "nested" {
		t.Errorf("gotest tests = %+v, want TestNested in package nested", got)
	}
	for _, name := range []string{"sh in dir", "generic in dir"} {
		if len(byCheck[name]) != 1 {
			t.Errorf("%q: %d tests, want 1", name, len(byCheck[name]))
		}
	}
	for name, wantSuite := range map[string]string{
		"pytest in py/a": "py/a/tests/test_x.py",
		"pytest in py/b": "py/b/tests/test_x.py",
	} {
		got := byCheck[name]
		if len(got) != 1 || len(got[0].Suite) != 1 || got[0].Suite[0] != wantSuite {
			t.Errorf("%q tests = %+v, want suite %q", name, got, wantSuite)
		}
	}
}
