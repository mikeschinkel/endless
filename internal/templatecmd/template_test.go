package templatecmd

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// endlessGoBinPath is set by TestMain to the path of the prebuilt
// endless-go binary used by the binary-integration tests in this file.
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

func endlessGoBin(t *testing.T) string {
	t.Helper()
	if endlessGoBinPath == "" {
		t.Fatal("endless-go binary not built — TestMain did not run")
	}
	return endlessGoBinPath
}

// fullHandoffVars is the canonical complete var map the spawn flow supplies.
func fullHandoffVars() string {
	return `{
		"spawned_id": 9999,
		"title": "Test task",
		"spawner_task": 7777,
		"return_anchor": "%1",
		"worktree_path": "/tmp/wt/e-9999",
		"branch": "task/9999-test"
	}`
}

// projectFixture creates a tempdir that looks like a project root (has
// a .endless/ subdirectory) so cwd-based resolution succeeds when the
// binary cd's into it.
func projectFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".endless"), 0755); err != nil {
		t.Fatalf("create .endless: %v", err)
	}
	return root
}

// runRenderInProject invokes `endless-go template render <name>` with cwd
// set to projectRoot. Returns stdout, stderr, exit error. The binary's
// --config-dir gate is irrelevant here because the project is not self-dev.
func runRenderInProject(t *testing.T, projectRoot, name, stdin string, extraArgs ...string) (string, string, error) {
	t.Helper()
	bin := endlessGoBin(t)
	args := []string{"template", "render"}
	args = append(args, extraArgs...)
	args = append(args, name)
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// TestRender_FullVars_ContainsExpectedSubstitutions exercises the happy
// path: a complete var map plus the embedded handoff template renders
// every {{.var}} placeholder.
func TestRender_FullVars_ContainsExpectedSubstitutions(t *testing.T) {
	root := projectFixture(t)
	out, errOut, err := runRenderInProject(t, root, "handoff.md", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	wants := []string{
		"E-9999", "Test task", "E-7777", "%1",
		"/tmp/wt/e-9999", "task/9999-test",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestRender_MissingVar_PrintsNoValuePlaceholder confirms graceful
// degradation matching Python's string.Template.safe_substitute.
func TestRender_MissingVar_PrintsNoValuePlaceholder(t *testing.T) {
	root := projectFixture(t)
	vars := `{"spawned_id": 1, "title": "X"}`
	out, errOut, err := runRenderInProject(t, root, "handoff.md", vars)
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "<no value>") {
		t.Errorf("expected <no value> in output for missing vars; got:\n%s", out)
	}
}

// TestRender_UnknownTemplate_ExitsNonZero pins the unknown-name error.
func TestRender_UnknownTemplate_ExitsNonZero(t *testing.T) {
	root := projectFixture(t)
	_, errOut, err := runRenderInProject(t, root, "does-not-exist", `{}`)
	if err == nil {
		t.Fatalf("expected non-zero exit for unknown template; got success")
	}
	if !strings.Contains(errOut, "unknown template") {
		t.Errorf("stderr missing 'unknown template' marker: %s", errOut)
	}
}

// TestRender_MaterializesEmbedded confirms first-render materialization:
// the file appears under <root>/.endless/templates/ with the embedded
// content, and .gitignore is untouched.
func TestRender_MaterializesEmbedded(t *testing.T) {
	root := projectFixture(t)
	dst := filepath.Join(root, ".endless", "templates", "handoff.md.tmpl")
	if _, err := os.Stat(dst); err == nil {
		t.Fatalf("precondition: %s should not exist yet", dst)
	}
	// Seed a .gitignore to assert non-modification.
	gi := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gi, []byte("# preserved\n"), 0644); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}

	_, errOut, err := runRenderInProject(t, root, "handoff.md", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("expected %s to exist after render: %v", dst, err)
	}
	if st.IsDir() || st.Size() == 0 {
		t.Fatalf("materialized file is empty or a dir: %+v", st)
	}
	data, err := os.ReadFile(gi)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if string(data) != "# preserved\n" {
		t.Errorf(".gitignore was modified; got %q", string(data))
	}
}

// TestRender_UserEditPersists confirms an edited on-disk template wins
// over the embedded copy; render does not overwrite.
func TestRender_UserEditPersists(t *testing.T) {
	root := projectFixture(t)
	dir := filepath.Join(root, ".endless", "templates")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	custom := "MODIFIED CONTENT {{.spawned_id}}"
	dst := filepath.Join(dir, "handoff.md.tmpl")
	if err := os.WriteFile(dst, []byte(custom), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, errOut, err := runRenderInProject(t, root, "handoff.md", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "MODIFIED CONTENT") {
		t.Errorf("expected modified content to win; got:\n%s", out)
	}
	// On-disk file untouched.
	data, _ := os.ReadFile(dst)
	if string(data) != custom {
		t.Errorf("on-disk file overwritten: %q", string(data))
	}
}

// TestRender_DeleteToRestore confirms deleting the on-disk file restores
// the embedded copy on the next render.
func TestRender_DeleteToRestore(t *testing.T) {
	root := projectFixture(t)
	dir := filepath.Join(root, ".endless", "templates")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dst := filepath.Join(dir, "handoff.md.tmpl")
	if err := os.WriteFile(dst, []byte("CUSTOM"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Remove(dst); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out, errOut, err := runRenderInProject(t, root, "handoff.md", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if strings.Contains(out, "CUSTOM") {
		t.Errorf("restored output contains old content: %s", out)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("expected file to be re-materialized: %v", err)
	}
}

// TestRender_LocalTmplPrecedence verifies that .local.tmpl beats .tmpl.
func TestRender_LocalTmplPrecedence(t *testing.T) {
	root := projectFixture(t)
	dir := filepath.Join(root, ".endless", "templates")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	committed := filepath.Join(dir, "handoff.md.tmpl")
	local := filepath.Join(dir, "handoff.md.local.tmpl")
	if err := os.WriteFile(committed, []byte("COMMITTED"), 0644); err != nil {
		t.Fatalf("seed committed: %v", err)
	}
	if err := os.WriteFile(local, []byte("LOCAL"), 0644); err != nil {
		t.Fatalf("seed local: %v", err)
	}
	out, errOut, err := runRenderInProject(t, root, "handoff.md", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "LOCAL") {
		t.Errorf("expected LOCAL content; got: %s", out)
	}
	if strings.Contains(out, "COMMITTED") {
		t.Errorf("committed content leaked through: %s", out)
	}
}

// TestRender_NoProjectContext_ExitsNonZero verifies that a cwd outside any
// project errors with the documented message and writes nothing.
func TestRender_NoProjectContext_ExitsNonZero(t *testing.T) {
	// A fresh tempdir with no .endless/ ancestor anywhere in the chain.
	bareRoot := t.TempDir()
	// Walk to root to be safe — but t.TempDir() is inside /tmp/... which has
	// no .endless ancestors on the test machine. Use a path that's guaranteed
	// not to be inside a project: a bare subdir of t.TempDir.
	bareSub := filepath.Join(bareRoot, "outside")
	if err := os.MkdirAll(bareSub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Belt-and-suspenders: this test must not accidentally walk up to the
	// actual project root (the repo's main .endless). t.TempDir is under
	// /tmp on darwin and /tmp doesn't have .endless. Skip if our assumption
	// breaks.
	if hasEndlessAncestor(bareSub) {
		t.Skipf("test fixture path %s has a .endless ancestor; skip", bareSub)
	}

	_, errOut, err := runRenderInProject(t, bareSub, "handoff.md", fullHandoffVars())
	if err == nil {
		t.Fatalf("expected non-zero exit with no project context; got success")
	}
	if !strings.Contains(errOut, "requires a project context") {
		t.Errorf("stderr missing expected message; got: %s", errOut)
	}
}

// TestRender_ProjectFlagResolvesViaDB seeds a projects row, invokes with
// --project <name> --config-dir <db>, and verifies the named project's
// path is used as the project root (the materialized file lands there).
func TestRender_ProjectFlagResolvesViaDB(t *testing.T) {
	cfgDir := t.TempDir()
	projRoot := t.TempDir()
	// The project root needs to be writable; t.TempDir is.
	seedProject(t, cfgDir, "test-proj", projRoot)

	bin := endlessGoBin(t)
	// Run from a cwd that has no .endless ancestor so --project is the
	// only way to resolve. We deliberately use a different tempdir for cwd.
	cwdDir := t.TempDir()
	cmd := exec.Command(bin,
		"--config-dir", cfgDir,
		"template", "render",
		"--project", "test-proj",
		"handoff.md",
	)
	cmd.Dir = cwdDir
	cmd.Stdin = strings.NewReader(fullHandoffVars())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("render --project: %v\nstderr: %s", err, stderr.String())
	}
	dst := filepath.Join(projRoot, ".endless", "templates", "handoff.md.tmpl")
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected materialized file at %s: %v", dst, err)
	}
	if !strings.Contains(stdout.String(), "E-9999") {
		t.Errorf("expected E-9999 in rendered output; got:\n%s", stdout.String())
	}
}

// TestRender_UnknownProjectFlag_ExitsNonZero verifies --project <unknown>
// errors with a project-not-found message.
func TestRender_UnknownProjectFlag_ExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	// Seed schema but no projects.
	seedProject(t, cfgDir, "other-proj", t.TempDir())

	bin := endlessGoBin(t)
	cwdDir := t.TempDir()
	cmd := exec.Command(bin,
		"--config-dir", cfgDir,
		"template", "render",
		"--project", "no-such-project",
		"handoff.md",
	)
	cmd.Dir = cwdDir
	cmd.Stdin = strings.NewReader(`{}`)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit; got success\n%s", out)
	}
	if !strings.Contains(string(out), "project not found") {
		t.Errorf("stderr missing 'project not found' marker: %s", string(out))
	}
}

// seedProject writes an endless.db at $cfgDir/endless.db with schema
// applied and one projects row.
func seedProject(t *testing.T, cfgDir, name, path string) {
	t.Helper()
	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema to seed db: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (name, path, status, created_at, updated_at) "+
			"VALUES (?, ?, 'active', '2026-01-01T00:00:00', '2026-01-01T00:00:00')",
		name, path,
	); err != nil {
		t.Fatalf("seed projects row: %v", err)
	}
}

// hasEndlessAncestor walks up from dir looking for a `.endless`
// directory. Returns true if one exists at any ancestor.
func hasEndlessAncestor(dir string) bool {
	check := dir
	for {
		if st, err := os.Stat(filepath.Join(check, ".endless")); err == nil && st.IsDir() {
			return true
		}
		parent := filepath.Dir(check)
		if parent == check {
			return false
		}
		check = parent
	}
}
