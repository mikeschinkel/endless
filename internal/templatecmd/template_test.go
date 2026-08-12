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
		"label_prefix": "E-8888/E-9999",
		"title": "Test task",
		"worktree_path": "/tmp/wt/e-9999",
		"branch": "task/9999-test",
		"child_count": 0
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
	out, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	wants := []string{
		"E-9999", "Test task",
		"/tmp/wt/e-9999", "task/9999-test",
		// E-1620: the hierarchical identity prefix renders on the opening line.
		"- E-8888/E-9999: Test task.",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestRender_HandoffClose_ExceptionRule verifies the shared handoff_close
// partial (E-1759): every handoff type invokes `endless worktree check`
// instead of enumerating report categories, forbids confirming the negative,
// drops the retired "dangling tags"/"landed-vs-worktree delta" phrasing, and
// (E-1770) never emits the tmux return line for either bg or non-bg. The
// type-specific deliverable prefix must survive the refactor. It also asserts
// the final-message verification discipline: the verify handoffs (todo, bugfix)
// carry the one-command contract, while the information-deliverable handoffs
// (epic, research, brainstorm) carry the anti-checklist prohibition. E-1773
// adds two more lines every partial-using type must carry: reporting routed
// through `endless task report` and the per-response bypass keyword — which
// E-1953 changed from `FULL STATUS` to the `$FULL` sigil, and whose meaning
// changed with it (it no longer licenses an unconstrained answer alongside a
// block; it bypasses the minimizer entirely for one turn).
func TestRender_HandoffClose_ExceptionRule(t *testing.T) {
	returnLine := "tmux move-window -t archive:"
	cases := []struct {
		typ      string
		prefix   string // deliverable pointer that must remain inline
		contract string // the final-message verification discipline for this type
	}{
		{"todo", "Hand me exactly ONE command to verify", "Do NOT enumerate a manual checklist"},
		{"bugfix", "Hand me exactly ONE command to verify", "Do NOT enumerate a manual checklist"},
		// E-1911: the epic's final-message line no longer leads with the state
		// of the children — that directive and `task report`'s `Children:` line
		// each justified the other while both duplicated `session status`, so
		// both are gone. The inline pointer this case pins is now the epic's
		// dispatch instruction, which is unaffected.
		{"epic", "these are the units of work to dispatch", "there is nothing to verify"},
		{"research", "say where the findings live", "there is nothing to verify"},
		{"brainstorm", "say where the synthesis lives", "there is nothing to verify"},
	}
	for _, c := range cases {
		for _, bg := range []bool{false, true} {
			name := fmt.Sprintf("%s/bg=%v", c.typ, bg)
			t.Run(name, func(t *testing.T) {
				root := projectFixture(t)
				vars := fmt.Sprintf(
					`{"spawned_id":1,"label_prefix":"E-1","title":"T",`+
						`"worktree_path":"/w",`+
						`"branch":"b","child_count":0,"children_state":"none","bg":%v}`, bg)
				out, errOut, err := runRenderInProject(t, root, "handoff/"+c.typ, vars)
				if err != nil {
					t.Fatalf("render: %v\nstderr: %s", err, errOut)
				}
				mustContain := []string{
					"endless worktree check",
					// E-1953 dropped `do NOT confirm the negative` from the
					// close. Telling the agent not to confirm the absence of a
					// problem is the self-judgment the minimizer replaced — the
					// instruction now lives in the minimize prompt, where a
					// second party applies it.
					c.prefix,
					c.contract,
					// E-1773: the shared close routes reporting through the
					// `endless task report` command. E-1953: it now hands over
					// the whole draft and names the `$FULL` bypass sigil.
					"endless task report",
					"--draft-file",
					"$FULL",
				}
				for _, w := range mustContain {
					if !strings.Contains(out, w) {
						t.Errorf("output missing %q\n--- output ---\n%s", w, out)
					}
				}
				mustNotContain := []string{
					"dangling tags", "landed-vs-worktree delta",
					// E-1911: the retired children directive, in the one place
					// it ever appeared.
					"lead with the state of the children",
				}
				for _, w := range mustNotContain {
					if strings.Contains(out, w) {
						t.Errorf("output still contains retired phrase %q\n--- output ---\n%s", w, out)
					}
				}
				// E-1770: the tmux return line is gone from every variant.
				if strings.Contains(out, returnLine) {
					t.Errorf("output should omit the tmux return line (E-1770):\n%s", out)
				}
				// bg output ends the message with the background-agent note.
				if bg && !strings.Contains(out, "You're a background agent") {
					t.Errorf("bg output missing background-agent note:\n%s", out)
				}
			})
		}
	}
}

// TestRender_Brainstorm_InterviewModeFraming pins the durable contract that a
// brainstorm handoff frames the work as interview-mode / requester-led (E-1657):
// the requester's own thinking is the primary source, not autonomous research.
// It asserts the stable framing tokens, not an exact sentence — the precise
// wording has drifted before (an earlier "interviewing the requester" phrasing
// was reworded) while the interview-mode framing itself is the contract worth
// protecting. This lives in the project suite because a per-task verify script
// is a one-shot land-time gate, not a standing regression guard (E-1806).
func TestRender_Brainstorm_InterviewModeFraming(t *testing.T) {
	root := projectFixture(t)
	vars := `{"spawned_id":1,"label_prefix":"E-1","title":"T",` +
		`"worktree_path":"/w","branch":"b","child_count":0,` +
		`"children_state":"none","bg":false}`
	out, errOut, err := runRenderInProject(t, root, "handoff/brainstorm", vars)
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	for _, w := range []string{"interview-mode", "requester-led"} {
		if !strings.Contains(out, w) {
			t.Errorf("brainstorm handoff missing interview-mode framing token %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestRender_MissingVar_PrintsNoValuePlaceholder confirms graceful
// degradation matching Python's string.Template.safe_substitute.
func TestRender_MissingVar_PrintsNoValuePlaceholder(t *testing.T) {
	root := projectFixture(t)
	vars := `{"spawned_id": 1, "title": "X"}`
	out, errOut, err := runRenderInProject(t, root, "handoff/todo", vars)
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
	dst := filepath.Join(root, ".endless", "templates", "handoff", "todo.md.tmpl")
	if _, err := os.Stat(dst); err == nil {
		t.Fatalf("precondition: %s should not exist yet", dst)
	}
	// Seed a .gitignore to assert non-modification.
	gi := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gi, []byte("# preserved\n"), 0644); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}

	_, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
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
	dir := filepath.Join(root, ".endless", "templates", "handoff")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	custom := "MODIFIED CONTENT {{.spawned_id}}"
	dst := filepath.Join(dir, "todo.md.tmpl")
	if err := os.WriteFile(dst, []byte(custom), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
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
	dir := filepath.Join(root, ".endless", "templates", "handoff")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dst := filepath.Join(dir, "todo.md.tmpl")
	if err := os.WriteFile(dst, []byte("CUSTOM"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Remove(dst); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
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
	dir := filepath.Join(root, ".endless", "templates", "handoff")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	committed := filepath.Join(dir, "todo.md.tmpl")
	local := filepath.Join(dir, "todo.md.local.tmpl")
	if err := os.WriteFile(committed, []byte("COMMITTED"), 0644); err != nil {
		t.Fatalf("seed committed: %v", err)
	}
	if err := os.WriteFile(local, []byte("LOCAL"), 0644); err != nil {
		t.Fatalf("seed local: %v", err)
	}
	out, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
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

	_, errOut, err := runRenderInProject(t, bareSub, "handoff/todo", fullHandoffVars())
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
		"handoff/todo",
	)
	cmd.Dir = cwdDir
	cmd.Stdin = strings.NewReader(fullHandoffVars())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("render --project: %v\nstderr: %s", err, stderr.String())
	}
	dst := filepath.Join(projRoot, ".endless", "templates", "handoff", "todo.md.tmpl")
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
		"handoff/todo",
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

// TestNormalizeName_AppliesDefaultExtension covers the bare-name shorthand:
// no `.` in the basename → append `.md`; already has a `.` → leave alone.
func TestNormalizeName_AppliesDefaultExtension(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"handoff", "handoff.md"},
		{"handoff.md", "handoff.md"},
		{"handoff.txt", "handoff.txt"},
		{"handoff/todo", "handoff/todo.md"},
		{"handoff/todo.md", "handoff/todo.md"},
		{"report.json", "report.json"},
	}
	for _, c := range cases {
		got := normalizeName(c.in)
		if got != c.want {
			t.Errorf("normalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRender_BareNameAndExplicitMd_AreEquivalent verifies the convenience
// shorthand: `handoff/todo` and `handoff/todo.md` both resolve to
// handoff/todo.md.tmpl. (E-1566: bare top-level `handoff` no longer
// resolves to a file — the template moved under handoff/ as a per-type
// variant; the bare-vs-explicit convenience still applies to the leaf.)
func TestRender_BareNameAndExplicitMd_AreEquivalent(t *testing.T) {
	root := projectFixture(t)
	out1, _, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	// Second invocation in the same project — file is now materialized.
	out2, _, err := runRenderInProject(t, root, "handoff/todo.md", fullHandoffVars())
	if err != nil {
		t.Fatalf("explicit: %v", err)
	}
	if out1 != out2 {
		t.Errorf("bare vs explicit .md produced different output:\nbare:\n%s\nexplicit:\n%s", out1, out2)
	}
}

// selfDevFixture creates a project root whose .endless/config.json marks
// it self_dev, so materialization is skipped (the embedded copy is the
// source of truth for endless itself).
func selfDevFixture(t *testing.T) string {
	t.Helper()
	root := projectFixture(t)
	cfg := filepath.Join(root, ".endless", "config.json")
	if err := os.WriteFile(cfg, []byte(`{"self_dev": true}`), 0644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return root
}

// gitFixture creates a consumer project root that is an initialized git
// work tree with a committer identity, so the auto-commit path runs.
func gitFixture(t *testing.T) string {
	t.Helper()
	root := projectFixture(t)
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// gitOut runs a read-only git command in root and returns trimmed stdout.
func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestRender_SelfDev_SkipsMaterialize confirms a self_dev project renders
// from embedded and writes no .endless/templates/ file (the fix for the
// `just land` block).
func TestRender_SelfDev_SkipsMaterialize(t *testing.T) {
	root := selfDevFixture(t)
	out, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "E-9999") {
		t.Errorf("expected rendered output; got:\n%s", out)
	}
	dst := filepath.Join(root, ".endless", "templates", "handoff", "todo.md.tmpl")
	if _, err := os.Stat(dst); err == nil {
		t.Errorf("self_dev project should not materialize %s", dst)
	}
	if _, err := os.Stat(filepath.Join(root, ".endless", "templates")); err == nil {
		t.Errorf("self_dev project should not create .endless/templates/")
	}
}

// TestRender_Consumer_AutoCommitsMaterialized confirms a non-self_dev git
// project materializes AND commits exactly the one file on first render,
// leaving a clean working tree, and no-ops on the second render.
func TestRender_Consumer_AutoCommitsMaterialized(t *testing.T) {
	root := gitFixture(t)
	relPath := ".endless/templates/handoff/todo.md.tmpl"

	before := gitOut(t, root, "rev-list", "--count", "HEAD")

	_, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}

	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Fatalf("expected materialized file: %v", err)
	}
	if status := gitOut(t, root, "status", "--porcelain"); status != "" {
		t.Errorf("expected clean working tree after auto-commit; got:\n%s", status)
	}
	after := gitOut(t, root, "rev-list", "--count", "HEAD")
	if before == after {
		t.Errorf("expected one new commit; rev count unchanged at %s", after)
	}
	files := gitOut(t, root, "show", "--name-only", "--format=", "HEAD")
	if files != relPath {
		t.Errorf("commit touched unexpected paths; got %q want %q", files, relPath)
	}

	// Second render is a no-op: no new commit, still clean.
	_, errOut, err = runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
	if err != nil {
		t.Fatalf("second render: %v\nstderr: %s", err, errOut)
	}
	if again := gitOut(t, root, "rev-list", "--count", "HEAD"); again != after {
		t.Errorf("second render created a commit; rev count %s != %s", again, after)
	}
	if status := gitOut(t, root, "status", "--porcelain"); status != "" {
		t.Errorf("second render dirtied the tree:\n%s", status)
	}
}

// TestRender_Consumer_NonGit_NoError confirms a non-self_dev, non-git
// project still renders and materializes (file left untracked, no error).
func TestRender_Consumer_NonGit_NoError(t *testing.T) {
	root := projectFixture(t) // no git, no self_dev
	out, errOut, err := runRenderInProject(t, root, "handoff/todo", fullHandoffVars())
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "E-9999") {
		t.Errorf("expected rendered output; got:\n%s", out)
	}
	dst := filepath.Join(root, ".endless", "templates", "handoff", "todo.md.tmpl")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("expected materialized file in non-git consumer: %v", err)
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
