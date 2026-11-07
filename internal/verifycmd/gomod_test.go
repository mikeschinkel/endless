package verifycmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// shSuite is a one-check suite whose own row always passes, so the built-in
// go.mod row's presence and verdict decide the run.
const shSuite = `
schema = 1
task   = "%s"
[[check]]
name = "the suite's own check"
sh   = "true"
`

// writeGoModule writes under dir a module that requires local modules a and b,
// with b listed at bVersion: a needs b at v0.0.1, so v0.0.0 understates it and
// v0.0.1 is tidy.
func writeGoModule(t *testing.T, dir, bVersion string) {
	t.Helper()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n\nrequire example.com/a v0.0.0\n\nrequire example.com/b "+bVersion+" // indirect\n\n"+
		"replace (\n\texample.com/a => ./a\n\texample.com/b => ./b\n)\n")
	write("m.go", "package m\n\nimport _ \"example.com/a\"\n")
	write("a/go.mod", "module example.com/a\n\ngo 1.21\n\nrequire example.com/b v0.0.1\n\nreplace example.com/b => ../b\n")
	write("a/a.go", "package a\n\nimport _ \"example.com/b\"\n")
	write("b/go.mod", "module example.com/b\n\ngo 1.21\n")
	write("b/b.go", "package b\n")
}

// enterGoSuite builds a project root holding one sh suite, chdirs into it with
// HOME redirected, and closes the network for go so a fixture can never hang.
func enterGoSuite(t *testing.T, id string) (root string) {
	t.Helper()
	root = writeSuite(t, id, strings.TrimSpace(strings.ReplaceAll(shSuite, "%s", id))+"\n")
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "")
	return root
}

// A worktree holding Go modules gets one built-in row per module, before the
// suite's own checks: the root module's with the bare label, a nested module's
// with its directory appended. Here every module is tidy and the run passes.
func TestRun_GoModRowsComeFirst(t *testing.T) {
	root := enterGoSuite(t, "E-GOMOD")
	writeGoModule(t, root, "v0.0.1")

	code, err := run("E-GOMOD", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	tests := readOnlyReport(t, "E-GOMOD").Results.Tests
	names := make([]string, len(tests))
	for i, tt := range tests {
		names[i] = tt.Name
		if tt.Status != verify.StatusPassed {
			t.Errorf("%q: status %s, message:\n%s", tt.Name, tt.Status, tt.Message)
		}
	}
	want := []string{goModCheckLabel, goModCheckLabel + " (a)", goModCheckLabel + " (b)", "the suite's own check"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("tests = %v, want %v", names, want)
	}
	if got := tests[0].Labels[verify.LabelCheck]; got != goModCheckLabel {
		t.Errorf("labels.check = %q, want %q", got, goModCheckLabel)
	}
}

// An untidy root module fails the run through its row, which carries the diff
// and the remedy, and the suite's own check still runs and is reported after.
func TestRun_UntidyGoModFailsRunWithDiffAndRemedy(t *testing.T) {
	root := enterGoSuite(t, "E-UNTIDY")
	writeGoModule(t, root, "v0.0.0")

	code, err := run("E-UNTIDY", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	tests := readOnlyReport(t, "E-UNTIDY").Results.Tests
	if len(tests) != 4 || tests[0].Name != goModCheckLabel {
		t.Fatalf("tests = %+v, want the root go.mod row first of four", tests)
	}
	row := tests[0]
	if row.Status != verify.StatusFailed {
		t.Fatalf("root row status = %s, want failed", row.Status)
	}
	for _, want := range []string{
		"GOWORK=off",
		"-require example.com/b v0.0.0",
		"+require example.com/b v0.0.1",
		"run `go mod tidy` in the worktree root, then commit go.mod and go.sum",
		"relative replace paths (./a, ./b)",
	} {
		if !strings.Contains(row.Message, want) {
			t.Errorf("row message lacks %q:\n%s", want, row.Message)
		}
	}
	if last := tests[len(tests)-1]; last.Name != "the suite's own check" || last.Status != verify.StatusPassed {
		t.Errorf("the suite's own check = %+v, want it run and passed after the rows", last)
	}
}

// A worktree with no Go module gets no row: a non-Go project's run is unchanged.
func TestRun_NoGoModRowForNonGoWorktree(t *testing.T) {
	enterGoSuite(t, "E-NOGO")

	code, err := run("E-NOGO", false)
	if err != nil || code != 0 {
		t.Fatalf("run = %d, %v; want 0, nil", code, err)
	}
	tests := readOnlyReport(t, "E-NOGO").Results.Tests
	if len(tests) != 1 || tests[0].Name != "the suite's own check" {
		t.Errorf("tests = %+v, want only the suite's own check", tests)
	}
}

// When the main checkout root holds a go.work, main builds in workspace mode
// too, so the check is skipped with no row even though the modules are untidy.
func TestRun_GoModRowSkippedWhenMainHasGoWork(t *testing.T) {
	root := enterGoSuite(t, "E-GOWORK")
	writeGoModule(t, root, "v0.0.0")
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.21\n\nuse .\n"), 0o644); err != nil {
		t.Fatalf("WriteFile go.work: %v", err)
	}

	code, err := run("E-GOWORK", false)
	if err != nil || code != 0 {
		t.Fatalf("run = %d, %v; want 0, nil", code, err)
	}
	tests := readOnlyReport(t, "E-GOWORK").Results.Tests
	if len(tests) != 1 || tests[0].Name != "the suite's own check" {
		t.Errorf("tests = %+v, want only the suite's own check", tests)
	}
}

// A toolchain too old for -diff fails the row, naming both versions, rather
// than skipping it or aborting the run.
func TestRun_OldGoFailsGoModRow(t *testing.T) {
	root := enterGoSuite(t, "E-OLDGO")
	writeGoModule(t, root, "v0.0.1")
	fake := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = env ]; then echo go1.22.5; exit 0; fi\necho \"fake go must not run $*\" >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(fake, "go"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake go: %v", err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))

	code, err := run("E-OLDGO", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	tests := readOnlyReport(t, "E-OLDGO").Results.Tests
	row := tests[0]
	if row.Name != goModCheckLabel || row.Status != verify.StatusFailed {
		t.Fatalf("first test = %+v, want the failed root go.mod row", row)
	}
	for _, want := range []string{"go1.22.5", "go1.23"} {
		if !strings.Contains(row.Message, want) {
			t.Errorf("row message lacks %q:\n%s", want, row.Message)
		}
	}
	_ = root
}
