package verify_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-dt"
)

// LookupDriver dispatches a runner string to the driver that serves its family,
// resolving unregistered families to the generic driver and rejecting malformed
// names / unimplemented variants.
func TestLookupDriver(t *testing.T) {
	cases := []struct {
		runner        string
		wantErr       error
		wantSelection verify.SelectionKind
		wantFormat    verify.Format
	}{
		{runner: "gotest", wantSelection: verify.SelectionStructured, wantFormat: verify.FormatGotestJSON},
		{runner: "pytest", wantSelection: verify.SelectionStructured, wantFormat: verify.FormatPytestJSON},
		{runner: "pytest/uv", wantSelection: verify.SelectionStructured, wantFormat: verify.FormatPytestJSON},
		{runner: "bats", wantSelection: verify.SelectionCommandOnly, wantFormat: ""},
		{runner: "vnd.newclarity.foo/bar", wantSelection: verify.SelectionCommandOnly, wantFormat: ""},
		{runner: "pytest/poetry", wantErr: verify.ErrUnknownVariant},
		{runner: "gotest/uv", wantErr: verify.ErrUnknownVariant},
		{runner: "a/b/c", wantErr: verify.ErrMalformedRunner},
		{runner: "pytest/", wantErr: verify.ErrMalformedRunner},
		{runner: "/uv", wantErr: verify.ErrMalformedRunner},
	}
	for _, tc := range cases {
		t.Run(tc.runner, func(t *testing.T) {
			d, err := verify.LookupDriver(tc.runner)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("LookupDriver(%q) err = %v, want %v", tc.runner, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LookupDriver(%q) unexpected err: %v", tc.runner, err)
			}
			if d.Selection() != tc.wantSelection {
				t.Errorf("Selection() = %v, want %v", d.Selection(), tc.wantSelection)
			}
			if d.Format() != tc.wantFormat {
				t.Errorf("Format() = %q, want %q", d.Format(), tc.wantFormat)
			}
		})
	}
}

// The generic driver execs a literal command and normalizes its declared-format
// (default tap) stdout — the low-fidelity admission path the committed shell/TAP
// suites depend on.
func TestGenericDriver_RunTAP(t *testing.T) {
	d, err := verify.LookupDriver("bats")
	if err != nil {
		t.Fatalf("LookupDriver: %v", err)
	}
	root := dt.DirPath(t.TempDir())
	report := dt.FilepathJoin(root, "unused.json")
	check := verify.Check{
		Runner:  "bats",
		Command: `printf '1..2\nok 1 a\nok 2 b\n'`,
		Format:  verify.FormatTAP,
	}
	res, err := d.Run(check, root, os.Environ(), report)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Exit != 0 {
		t.Errorf("Exit = %d, want 0", res.Exit)
	}
	if res.Report.Results.Summary.Tests != 2 || res.Report.Results.Summary.Passed != 2 {
		t.Errorf("summary = %+v, want 2 tests / 2 passed", res.Report.Results.Summary)
	}
}

// A failing TAP stream with a non-zero exit is a genuine test failure (Failed >
// 0), not an infrastructure error: Run returns it as a normal RunResult and lets
// the caller apply the exit-semantics guard.
func TestGenericDriver_RunReportsFailures(t *testing.T) {
	d, _ := verify.LookupDriver("bats")
	root := dt.DirPath(t.TempDir())
	report := dt.FilepathJoin(root, "unused.json")
	check := verify.Check{
		Runner:  "bats",
		Command: `printf '1..2\nok 1 a\nnot ok 2 b\n'; exit 1`,
		Format:  verify.FormatTAP,
	}
	res, err := d.Run(check, root, os.Environ(), report)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Exit != 1 {
		t.Errorf("Exit = %d, want 1", res.Exit)
	}
	if res.Report.Results.Summary.Failed != 1 {
		t.Errorf("Failed = %d, want 1", res.Report.Results.Summary.Failed)
	}
}

// The gotest driver executes a real `go test -json` and normalizes the test2json
// stream to CTRF end to end (structured selection → native filter → execute →
// normalize).
func TestGotestDriver_Run(t *testing.T) {
	root := dt.DirPath(writeGoModule(t))
	d, err := verify.LookupDriver("gotest")
	if err != nil {
		t.Fatalf("LookupDriver: %v", err)
	}
	report := dt.FilepathJoin(root, "report.json")
	check := verify.Check{Runner: "gotest", Paths: []string{"./..."}}
	res, err := d.Run(check, root, os.Environ(), report)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Exit != 0 {
		t.Fatalf("Exit = %d, want 0 (stderr: %s)", res.Exit, res.Stderr)
	}
	if res.Report.Results.Tool.Name != "go test" {
		t.Errorf("tool = %q, want %q", res.Report.Results.Tool.Name, "go test")
	}
	if res.Report.Results.Summary.Passed != 1 || res.Report.Results.Summary.Failed != 0 {
		t.Errorf("summary = %+v, want 1 passed / 0 failed", res.Report.Results.Summary)
	}
}

// writeGoModule writes a throwaway module with one passing test and returns its
// root, for exercising the gotest driver against the real go toolchain offline.
func writeGoModule(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	write("go.mod", "module verifytest\n\ngo 1.21\n")
	write("ok_test.go", "package verifytest\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
	return root
}
