package verify_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-dt"
)

// runShell runs one shell check through the driver DriverFor resolves for it,
// at a temp root with a minimal env, and returns its single test.
func runShell(t *testing.T, chk verify.Check) (test verify.Test, result verify.RunResult) {
	t.Helper()
	d, err := verify.DriverFor(chk)
	if err != nil {
		t.Fatalf("DriverFor: %v", err)
	}
	result, err = d.Run(chk, dt.DirPath(t.TempDir()), []string{"PATH=/usr/bin:/bin"}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(result.Report.Results.Tests); n != 1 {
		t.Fatalf("a shell check reports %d tests, want 1", n)
	}
	return result.Report.Results.Tests[0], result
}

func strptr(s string) *string { return &s }

// A shell check passes on exit 0 and fails on anything else, carrying the
// exit code in its message and the command's captured output.
func TestShellDriver_ExitCode(t *testing.T) {
	test, res := runShell(t, verify.Check{Sh: "echo hello"})
	if test.Status != verify.StatusPassed || res.Exit != 0 {
		t.Errorf("exit 0: status = %s exit = %d, want passed 0", test.Status, res.Exit)
	}

	test, res = runShell(t, verify.Check{Sh: "echo out; echo err >&2; exit 3"})
	if test.Status != verify.StatusFailed || res.Exit != 3 {
		t.Fatalf("exit 3: status = %s exit = %d, want failed 3", test.Status, res.Exit)
	}
	for _, want := range []string{"exited 3", "--- stdout ---\nout", "--- stderr ---\nerr"} {
		if !strings.Contains(test.Message, want) {
			t.Errorf("message missing %q:\n%s", want, test.Message)
		}
	}
	if len(test.Stdout) != 1 || test.Stdout[0] != "out" || len(test.Stderr) != 1 || test.Stderr[0] != "err" {
		t.Errorf("stdout/stderr lines = %q / %q, want [out] / [err]", test.Stdout, test.Stderr)
	}
	if res.Report.Results.Summary.Failed != 1 {
		t.Errorf("summary failed = %d, want 1", res.Report.Results.Summary.Failed)
	}
}

// The test is named by the check's name, or by its command when unnamed.
func TestShellDriver_Name(t *testing.T) {
	test, _ := runShell(t, verify.Check{Name: "says hello", Sh: "true"})
	if test.Name != "says hello" {
		t.Errorf("named: Name = %q, want %q", test.Name, "says hello")
	}
	test, _ = runShell(t, verify.Check{Sh: "\n  true\n"})
	if test.Name != "true" {
		t.Errorf("unnamed: Name = %q, want the trimmed command", test.Name)
	}
}

// Each matcher passes when its stdout holds and fails stating the expected
// value and the actual output.
func TestShellDriver_Matchers(t *testing.T) {
	cases := []struct {
		name       string
		chk        verify.Check
		wantPass   bool
		wantInMsgs []string
	}{
		{"contains pass", verify.Check{Sh: "echo alpha beta", Contains: "beta"}, true, nil},
		{"contains fail", verify.Check{Sh: "echo alpha", Contains: "beta"}, false,
			[]string{`stdout does not contain "beta"`, "--- stdout ---\nalpha"}},
		{"not_contains pass", verify.Check{Sh: "echo alpha", NotContains: "beta"}, true, nil},
		{"not_contains fail", verify.Check{Sh: "echo alpha beta", NotContains: "beta"}, false,
			[]string{`stdout contains "beta"`, "--- stdout ---\nalpha beta"}},
		{"equals pass", verify.Check{Sh: "printf 'x\\n\\n'", Equals: strptr("x")}, true, nil},
		{"equals fail", verify.Check{Sh: "echo y", Equals: strptr("x")}, false,
			[]string{`stdout does not equal "x"`, "--- stdout ---\ny"}},
		{"equals empty pass", verify.Check{Sh: "true", Equals: strptr("")}, true, nil},
		{"equals empty fail", verify.Check{Sh: "echo z", Equals: strptr("")}, false,
			[]string{`stdout does not equal ""`, "--- stdout ---\nz"}},
		{"stderr is not matched", verify.Check{Sh: "echo beta >&2", Contains: "beta"}, false,
			[]string{`stdout does not contain "beta"`, "--- stderr ---\nbeta"}},
		{"a failing exit fails before matchers", verify.Check{Sh: "echo beta; exit 1", Contains: "beta"}, false,
			[]string{"exited 1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			test, _ := runShell(t, tc.chk)
			if (test.Status == verify.StatusPassed) != tc.wantPass {
				t.Fatalf("status = %s, wantPass %v; message:\n%s", test.Status, tc.wantPass, test.Message)
			}
			for _, want := range tc.wantInMsgs {
				if !strings.Contains(test.Message, want) {
					t.Errorf("message missing %q:\n%s", want, test.Message)
				}
			}
		})
	}
}

// A shell check is refused, naming the key, when it carries a runner key, and
// a matcher is refused on a check without sh.
func TestValidate_ShellCheck(t *testing.T) {
	cases := []struct {
		name    string
		check   string
		wantErr error
		wantKey string
	}{
		{"sh alone", `sh = "true"`, nil, ""},
		{"sh with matchers", "sh = \"true\"\ncontains = \"a\"\nnot_contains = \"b\"\nequals = \"\"", nil, ""},
		{"sh with as and tmux", "sh = \"true\"\nas = \"agent\"\ntmux = true\nname = \"n\"", nil, ""},
		{"sh + runner", "sh = \"true\"\nrunner = \"bats\"", verify.ErrShConflict, "runner"},
		{"sh + command", "sh = \"true\"\ncommand = \"x\"", verify.ErrShConflict, "command"},
		{"sh + tests", "sh = \"true\"\ntests = [\"T\"]", verify.ErrShConflict, "tests"},
		{"sh + paths", "sh = \"true\"\npaths = [\"p\"]", verify.ErrShConflict, "paths"},
		{"sh + format", "sh = \"true\"\nformat = \"tap\"", verify.ErrShConflict, "format"},
		{"contains without sh", "runner = \"gotest\"\ntests = [\"T\"]\ncontains = \"a\"", verify.ErrMatcherRequiresSh, "contains"},
		{"not_contains without sh", "runner = \"gotest\"\ntests = [\"T\"]\nnot_contains = \"a\"", verify.ErrMatcherRequiresSh, "not_contains"},
		{"equals without sh", "runner = \"gotest\"\ntests = [\"T\"]\nequals = \"\"", verify.ErrMatcherRequiresSh, "equals"},
		{"neither runner nor sh", `name = "n"`, verify.ErrCheckMissingRunner, ""},
		{"name on a runner check", "name = \"n\"\nrunner = \"gotest\"\ntests = [\"T\"]", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verify.ParseManifest([]byte("schema = 1\ntask = \"E-1\"\n[[check]]\n" + tc.check + "\n"))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) || !errors.Is(err, verify.ErrInvalidManifest) {
				t.Fatalf("err = %v, want %v wrapping ErrInvalidManifest", err, tc.wantErr)
			}
			if tc.wantKey != "" && !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("err %q does not name the key %q", err, tc.wantKey)
			}
		})
	}
}
