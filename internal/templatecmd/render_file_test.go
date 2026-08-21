package templatecmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `template render --file <path>` renders a file that is not one of the
// embedded templates (E-2030). It exists because `endless guide` needs the
// guide's report-channel instructions to depend on the reading project's
// `report_gate` — a session must never be told to use a channel that will not
// gate it — and the guide lives in docs/guide/ where humans read it, outside the
// tree `render <name>` resolves in.
//
// The alternative was a second conditional syntax, invented in Python, for
// exactly one condition. Endless already has one templating language.

// runRenderArgs invokes `template render` with exactly the given args and cwd.
// runRenderInProject cannot be reused here: it always appends a template name,
// and the whole point of --file is that there is not one.
func runRenderArgs(t *testing.T, cwd, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(endlessGoBin(t), append([]string{"template", "render"}, args...)...)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// runRenderFile invokes `template render --file <path>` from a neutral cwd.
// Deliberately NOT inside a project fixture: --file must not need one.
func runRenderFile(t *testing.T, path, stdin string, extraArgs ...string) (string, string, error) {
	t.Helper()
	return runRenderArgs(t, t.TempDir(), stdin, append([]string{"--file", path}, extraArgs...)...)
}

func writeTemplateFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write template file: %v", err)
	}
	return path
}

const branchDoc = "before\n{{if .report_gate}}ON\n{{else}}OFF\n{{end}}after\n"

// TestRenderFile_BothBranches is the behaviour `endless guide` depends on: one
// source file, two renderings, chosen by a var the caller supplies.
func TestRenderFile_BothBranches(t *testing.T) {
	path := writeTemplateFile(t, branchDoc)

	for _, c := range []struct{ vars, want string }{
		{`{"report_gate": true}`, "before\nON\nafter\n"},
		{`{"report_gate": false}`, "before\nOFF\nafter\n"},
	} {
		out, errOut, err := runRenderFile(t, path, c.vars)
		if err != nil {
			t.Fatalf("render %s: %v\n%s", c.vars, err, errOut)
		}
		if out != c.want {
			t.Errorf("render %s = %q, want %q", c.vars, out, c.want)
		}
	}
}

// TestRenderFile_NeedsNoProjectContext pins that --file works from a directory
// with no .endless/ above it. `endless guide` is often the first command a
// session runs, and requiring project context to read documentation would make
// the guide unavailable exactly when someone is trying to learn what to do.
func TestRenderFile_NeedsNoProjectContext(t *testing.T) {
	path := writeTemplateFile(t, "plain body\n")
	out, errOut, err := runRenderFile(t, path, `{}`)
	if err != nil {
		t.Fatalf("render outside a project: %v\n%s", err, errOut)
	}
	if out != "plain body\n" {
		t.Errorf("out = %q, want %q", out, "plain body\n")
	}
}

// TestRenderFile_MissingFileExitsNonZero — a typo'd path must fail loudly.
// Printing nothing and exiting zero would hand `endless guide` an empty guide.
func TestRenderFile_MissingFileExitsNonZero(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.md")
	_, errOut, err := runRenderFile(t, missing, `{}`)
	if err == nil {
		t.Fatal("render of a missing file exited zero")
	}
	if !strings.Contains(errOut, "read template file") {
		t.Errorf("stderr does not name the failure:\n%s", errOut)
	}
}

// TestRenderFile_RejectsATemplateName — `--file x <name>` is ambiguous about
// which of the two is being rendered, so it is refused rather than silently
// preferring one.
func TestRenderFile_RejectsATemplateName(t *testing.T) {
	path := writeTemplateFile(t, "body\n")
	_, errOut, err := runRenderArgs(t, t.TempDir(), `{}`, "--file", path, "handoff/todo")
	if err == nil {
		t.Fatal("--file with a template name exited zero")
	}
	if !strings.Contains(errOut, "pass no template name") {
		t.Errorf("stderr does not explain the conflict:\n%s", errOut)
	}
}

// TestRenderFile_RejectsProject pins that --file does not pretend to honour the
// .local.tmpl -> .tmpl -> embedded chain. The caller named an exact file; there
// is nothing to look up and nothing to fall back to, so accepting --project
// would suggest an override that will never be consulted.
func TestRenderFile_RejectsProject(t *testing.T) {
	path := writeTemplateFile(t, "body\n")
	_, errOut, err := runRenderFile(t, path, `{}`, "--project", "endless")
	if err == nil {
		t.Fatal("--file with --project exited zero")
	}
	if !strings.Contains(errOut, "exclusive") {
		t.Errorf("stderr does not explain the conflict:\n%s", errOut)
	}
}

// TestRenderFile_DoesNotMaterialize — the named-template path copies the
// embedded source into <root>/.endless/templates/ on first render so a user can
// edit it. --file must not: the file already exists on disk at the path the
// caller gave, and copying it elsewhere would fork the guide.
func TestRenderFile_DoesNotMaterialize(t *testing.T) {
	path := writeTemplateFile(t, branchDoc)
	root := projectFixture(t)

	if _, errOut, err := runRenderArgs(
		t, root, `{"report_gate": true}`, "--file", path,
	); err != nil {
		t.Fatalf("render: %v\n%s", err, errOut)
	}

	entries, err := os.ReadDir(filepath.Join(root, ".endless", "templates"))
	if err == nil && len(entries) > 0 {
		t.Errorf("--file materialized %d file(s) into the project", len(entries))
	}
}
