// Package templatecmd implements the `endless-go template` subcommand:
// renders embedded templates with stdin-supplied JSON variables, and
// materializes the embedded copy to a project's <root>/.endless/templates/
// directory on first render so users can customize the on-disk file
// (E-1565).
//
// Lookup order at render time, per template name:
//
//  1. <project_root>/.endless/templates/<name>.local.tmpl  (per-developer)
//  2. <project_root>/.endless/templates/<name>.tmpl        (committed)
//  3. embedded                                              (fallback)
//
// The committed `.tmpl` is materialized from embed on first render of that
// template; `.local.tmpl` is purely user-created and the renderer never
// writes there.
package templatecmd

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// The `all:` prefix is required so leading-underscore partials (e.g.
// handoff/_close.tmpl) are embedded — a bare `//go:embed templates` excludes
// files whose names begin with `_` or `.`.
//
//go:embed all:templates
var embedded embed.FS

// closePartialName is the shared handoff tail parsed into every handoff
// template's set so `{{template "handoff_close" .}}` resolves. Its embedded
// path is templates/handoff/_close.tmpl; it is loaded via loadTemplate (no
// name normalization) so the leading-underscore basename stays intact.
const closePartialName = "handoff/_close"

// mechanicsPartialName is the shared type-mechanics partial (E-1822) parsed
// into every handoff template's set alongside _close, so `handoff_worktree`,
// `handoff_focus`, `handoff_deliverable`, `handoff_terminal`, and the
// `handoff_mechanics` composite resolve. It carries the lines whose drift
// between the spawn rendering and the claim-into-a-live-session rendering
// would be a bug. Same loading rules as closePartialName.
const mechanicsPartialName = "handoff/_mechanics"

// handoffPartialNames are the shared partials parsed into the set for any
// template under `handoff/`. Order is irrelevant — each file holds only
// `{{define}}` blocks.
var handoffPartialNames = []string{closePartialName, mechanicsPartialName}

// Run dispatches `endless-go template <verb> [args]`.
func Run(args []string) {
	if len(args) < 1 {
		refusal.NoReport(
			"endless-go template: no command given",
			"Pass `render` and retry",
		).Command("template").Text(usageText()).Exit(2)
	}
	switch args[0] {
	case "render":
		if err := runRender(args[1:], os.Stdin, os.Stdout); err != nil {
			// Every way render can fail arrives here, so the class travels with
			// the error rather than being guessed at this one site: the flag and
			// argument refusals below chose theirs, the E-1429 DB-context refusal
			// chose NO-REPORT back in monitor, and an I/O, JSON or database
			// failure nobody classified is a fault — which is what From makes of
			// anything that reaches it unclassified.
			refusal.From(err).Command("template render").Exit(1)
		}
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		// Every endless-go that has `template` at all knows `render`, so this is
		// somebody invoking the binary directly with a name that misses.
		refusal.NoReport(
			fmt.Sprintf("endless-go template: unknown command %q", args[0]),
			"Use `render` and retry",
		).Command("template").Detail(usageText()).Exit(2)
	}
}

// usageText is the command list, printed for --help and carried as the body of
// the two usage refusals above.
func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go template <command> [flags] [args]",
		"Commands:",
		"  render [--project <name>] <name>   read JSON vars on stdin, render template to stdout",
		"  render --file <path>               render an arbitrary file as a template instead",
	}, "\n") + "\n"
}

func runRender(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := refusal.NewFlags("render")
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	filePath := fs.String("file", "", "render this file as a template instead of a named template")
	if err := fs.Parse(args); err != nil {
		// This used to print twice: flag wrote its error line and usage block to
		// stderr itself, and then Run printed the returned error underneath. Now
		// flag's text is captured, and Text prints it once.
		//
		// The TSV's other reading — `endless guide` relaying an endless-go too
		// old to know --file — is unreachable from this source: the binary that
		// prints this is the binary that defines --file. What is left is a flag
		// somebody typed wrong, which the typist corrects.
		text := fs.Output()
		if errors.Is(err, flag.ErrHelp) {
			// `-h` is the one parse failure flag does not name in its own output;
			// that line was Run's print of the returned error, kept here so a
			// person reads what they always read.
			text += err.Error()
		}
		return refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("template render").Text(text)
	}
	rest := fs.Args()

	if *filePath != "" {
		if len(rest) != 0 {
			return refusal.NoReport(
				"--file renders one file: pass no template name with it",
				"Drop the positional template name and retry")
		}
		if *projectName != "" {
			return refusal.NoReport(
				"--file and --project are exclusive: --file names an exact file, so there is no override chain for --project to root",
				"Drop one of the two flags and retry")
		}
		return renderFile(*filePath, stdin, stdout)
	}

	if len(rest) != 1 {
		return refusal.NoReport(
			"usage: endless-go template render [--project <name>] <name> | --file <path>",
			"Pass exactly one template name, or --file <path>, and retry")
	}
	name := normalizeName(rest[0])

	projectRoot, err := resolveProjectRoot(*projectName)
	if err != nil {
		return err
	}

	// self_dev projects (endless itself) render from the embedded source —
	// materializing would write an untracked on-disk copy into the main
	// checkout that shadows the embedded template and blocks `just land`.
	// Consumer projects keep the write-on-read convenience and additionally
	// auto-commit the materialized file so it is tracked and discoverable.
	if !monitor.ProjectIsSelfDev(projectRoot) {
		wrote, err := materializeIfMissing(projectRoot, name)
		if err != nil {
			return err
		}
		if wrote {
			commitMaterialized(projectRoot, name)
		}
	}

	vars, err := decodeVars(stdin)
	if err != nil {
		return err
	}

	out, err := renderNamed(projectRoot, name, vars)
	if err != nil {
		return err
	}

	_, err = io.WriteString(stdout, out)
	return err
}

// renderFile renders one file, by path, with stdin-supplied vars.
//
// Deliberately outside the .local.tmpl -> .tmpl -> embedded chain, and outside
// materialization: the caller named an exact file, so there is nothing to look
// up and nothing to fall back to. Overriding a path the caller already chose
// would silently render something other than what they asked for; materializing
// it would copy a file that already lives on disk.
//
// This is what lets content Endless does NOT embed be conditional on project
// config. `endless guide` renders docs/guide/*.md through here so the report
// channel's instructions appear only where `report_gate` will actually enforce
// them (E-2030) -- a session must never be told to use a channel that will not
// gate it. Those files stay in docs/ because humans read them there.
//
// No handoff partials are parsed into the set: `{{template "handoff_close" .}}`
// is a handoff concern, and a file rendered by path is not a handoff.
func renderFile(path string, stdin io.Reader, stdout io.Writer) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read template file %s: %w", path, err)
	}
	vars, err := decodeVars(stdin)
	if err != nil {
		return err
	}
	out, err := render(filepath.Base(path), string(content), nil, vars)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, out)
	return err
}

// Render renders the named template for projectRoot with vars and returns the
// result, honoring the same `.local.tmpl` → `.tmpl` → embedded lookup and the
// same shared `handoff/` partials as the CLI `template render` path. It is the
// in-process entry point for callers already running inside the endless-go
// binary — the Claude hook renders the claim handoff through it (E-1822) rather
// than shelling out to itself.
//
// Unlike the CLI path it never materializes the embedded copy to disk: a hook
// firing mid-session must not write files into the user's repo as a side effect.
// An already-materialized on-disk template is still honored.
func Render(projectRoot, name string, vars map[string]any) (string, error) {
	return renderNamed(projectRoot, normalizeName(name), vars)
}

// renderNamed loads name (already normalized) plus, for `handoff/` templates,
// the shared partials, then renders. Shared by the CLI and in-process paths so
// the two cannot diverge on which partials are in the set.
func renderNamed(projectRoot, name string, vars map[string]any) (string, error) {
	content, err := loadTemplate(projectRoot, name)
	if err != nil {
		return "", err
	}

	// Handoff templates share `{{template "handoff_close" .}}` (handoff/
	// _close.tmpl) and the type-mechanics defines (handoff/_mechanics.tmpl).
	// Parse those partials into the set so the references resolve. They honor
	// the same .local→.tmpl→embedded precedence and are never materialized on
	// their own (only the requested top-level name is).
	var partials []string
	if strings.HasPrefix(name, "handoff/") {
		for _, p := range handoffPartialNames {
			text, err := loadTemplate(projectRoot, p)
			if err != nil {
				return "", err
			}
			partials = append(partials, text)
		}
	}

	return render(name, content, partials, vars)
}

// resolveProjectRoot returns the absolute path of the project root. When
// projectName is non-empty it is looked up in projects.path against the
// active DB. Otherwise the cwd is walked up looking for a `.endless`
// directory; missing → error with the documented message.
func resolveProjectRoot(projectName string) (string, error) {
	if projectName != "" {
		return projectRootByName(projectName)
	}
	return projectRootFromCwd()
}

func projectRootByName(name string) (string, error) {
	db, err := monitor.DB()
	if err != nil {
		return "", err
	}
	var path string
	err = db.QueryRow("SELECT path FROM projects WHERE name = ?", name).Scan(&path)
	if err != nil {
		// Two callers pass --project, and only one of them reads what is printed
		// here: the triage job passes a name it read from this same database,
		// but it captures this output and fails open, leaving the task
		// untriaged. The reader is therefore always somebody who typed the name
		// at `endless internal template render`, and a name is the one thing
		// they can correct.
		return "", refusal.NoReport(
			fmt.Sprintf("project not found: %s", name),
			"Check `endless project list` and retry with a registered project name")
	}
	if strings.TrimSpace(path) == "" {
		// The row exists and its path column is empty, which nothing the caller
		// typed produced and no other spelling of the name avoids.
		return "", refusal.Report(
			fmt.Sprintf("project %s has no registered path", name),
			"how to repair a projects row whose path is empty")
	}
	// The column is STORED form — `~/...` for a project under $HOME — and
	// filepath.Abs would turn that into `<cwd>/~/...` (E-2011).
	return monitor.ResolvedProjectPath(path)
}

// projectRootFromCwd delegates to the shared resolver and re-words the
// no-project-context case in this command's own terms (E-1919). The search
// itself lives in monitor because more than one command needs it.
func projectRootFromCwd() (string, error) {
	root, err := monitor.ProjectRootFromCwd()
	if errors.Is(err, monitor.ErrNoProjectContext) {
		return "", refusal.NoReport(
			"template render requires a project context — cd into a project or pass --project <name>",
			"cd into the project, or pass --project <name>, and retry")
	}
	return root, err
}

// templatesSubdir returns the project-scoped templates directory.
func templatesSubdir(projectRoot string) string {
	return filepath.Join(projectRoot, ".endless", "templates")
}

// normalizeName applies the default-extension rule: when the basename of
// the user-supplied name has no `.`, append `.md`. Otherwise use as-is.
// So `handoff` → `handoff.md`, `handoff.md` → `handoff.md` (idempotent),
// `handoff.txt` → `handoff.txt`, `handoff/todo` → `handoff/todo.md`.
func normalizeName(raw string) string {
	base := filepath.Base(raw)
	if strings.Contains(base, ".") {
		return raw
	}
	return raw + ".md"
}

// embeddedContent reads the embedded template content for name. Returns
// the file content and a not-found error when no embedded match exists.
func embeddedContent(name string) ([]byte, error) {
	data, err := embedded.ReadFile("templates/" + name + ".tmpl")
	if err != nil {
		return nil, unknownTemplateRefusal(name)
	}
	return data, nil
}

// computedNamePrefixes are the template namespaces Endless fills in itself:
// `handoff/<type>` from a task's type, `triage/<name>` from the triager's own
// constant. A name under neither was supplied by whoever ran the command.
var computedNamePrefixes = []string{"handoff/", "triage/"}

// unknownTemplateRefusal classifies a name with no embedded template, and the
// name answers which reading applies without asking the agent. A miss under a
// prefix Endless computes means this binary does not ship a template its own
// code asks for — a fault no caller can retype their way out of, however the
// name reached here. Any other name was typed, and a name is exactly what the
// typist can correct.
func unknownTemplateRefusal(name string) *refusal.Error {
	for _, prefix := range computedNamePrefixes {
		if strings.HasPrefix(name, prefix) {
			return refusal.Faultf("unknown template %q", name).
				Command("template render")
		}
	}
	return refusal.NoReport(
		fmt.Sprintf("unknown template %q", name),
		"Pass a template name Endless ships and retry").
		Command("template render")
}

// materializeIfMissing writes the embedded template content to
// <project_root>/.endless/templates/<name>.tmpl when that file does not
// exist. Per-file: only the requested name is materialized; siblings are
// untouched. Returns wrote=true only when it actually created the file, so
// callers can auto-commit on first render and no-op on subsequent ones.
func materializeIfMissing(projectRoot, name string) (wrote bool, err error) {
	dst := filepath.Join(templatesSubdir(projectRoot), name+".tmpl")
	if _, err := os.Stat(dst); err == nil {
		return false, nil
	}
	data, err := embeddedContent(name)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return false, err
	}
	return true, nil
}

// gitRedirectVars lists the env vars that override git's repo resolution.
// They are stripped from the auto-commit subprocess env so `git -C
// <projectRoot>` is authoritative and an inherited GIT_DIR (e.g. from a
// caller running inside a worktree) cannot misroute the commit.
var gitRedirectVars = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_COMMON_DIR",
	"GIT_NAMESPACE",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

// commitMaterialized commits exactly the just-materialized template on
// projectRoot's git repo (consumer projects only). Best-effort: when
// projectRoot is not a git work tree, or any git step fails, it logs to
// stderr and returns — a failed auto-commit must never fail the render.
// It stages only the single pathspec (never `git add -A`) so unrelated
// working-tree changes are untouched.
func commitMaterialized(projectRoot, name string) {
	relPath := filepath.Join(".endless", "templates", name+".tmpl")
	if !isGitWorkTree(projectRoot) {
		return
	}
	if err := runGit(projectRoot, "add", "--", relPath); err != nil {
		autoCommitSkipped(err).Print()
		return
	}
	msg := "Endless: materialize handoff template " + name
	if err := runGit(projectRoot, "commit", "-m", msg, "--", relPath); err != nil {
		autoCommitSkipped(err).Print()
	}
}

// autoCommitSkipped classifies a failed auto-commit of a just-materialized
// template. The render already succeeded and its output is on stdout; all that
// is left behind is an untracked (or staged-but-uncommitted) file in the user's
// working tree, which nothing downstream waits on — so the agent carries on.
// The guide, handoff and triage callers read stdout only on exit 0 and discard
// this entirely; `endless internal template render` passes it through.
func autoCommitSkipped(err error) *refusal.Error {
	return refusal.NoReport(
		fmt.Sprintf("endless-go template: auto-commit skipped: %v", err),
		"Nothing is blocked: the template rendered, and the file can be committed by hand").
		Command("template render")
}

// isGitWorkTree reports whether projectRoot is inside a git work tree.
func isGitWorkTree(projectRoot string) bool {
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--is-inside-work-tree")
	cmd.Env = sanitizedGitEnv()
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// runGit runs `git -C projectRoot <args>` with a sanitized env and returns
// an error with stderr folded in on non-zero exit.
func runGit(projectRoot string, args ...string) error {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = sanitizedGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sanitizedGitEnv returns os.Environ() with every variable in
// gitRedirectVars stripped, so `git -C <projectRoot>` cannot be overridden
// by an inherited GIT_DIR or sibling var pointing at another repo's gitdir.
func sanitizedGitEnv() []string {
	skip := make(map[string]struct{}, len(gitRedirectVars))
	for _, k := range gitRedirectVars {
		skip[k] = struct{}{}
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// loadTemplate returns the template content honoring the lookup order:
// .local.tmpl → .tmpl → embedded. When projectRoot is empty, only the
// embedded copy is consulted (currently unused by the CLI path, retained
// for in-process callers).
func loadTemplate(projectRoot, name string) (string, error) {
	if projectRoot != "" {
		dir := templatesSubdir(projectRoot)
		localPath := filepath.Join(dir, name+".local.tmpl")
		if data, err := os.ReadFile(localPath); err == nil {
			return string(data), nil
		}
		committedPath := filepath.Join(dir, name+".tmpl")
		if data, err := os.ReadFile(committedPath); err == nil {
			return string(data), nil
		}
	}
	data, err := embeddedContent(name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeVars(r io.Reader) (map[string]any, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var vars map[string]any
	if err := json.Unmarshal(data, &vars); err != nil {
		return nil, fmt.Errorf("decode stdin JSON: %w", err)
	}
	if vars == nil {
		vars = map[string]any{}
	}
	return vars, nil
}

// render parses content as a Go text/template and applies vars. Missing
// keys produce `<no value>` (Go's default), matching the graceful
// degradation of Python's string.Template.safe_substitute. Each partial is
// parsed into the same set first, so any `{{define}}` it carries (e.g.
// "handoff_close", "handoff_mechanics") resolves from content's
// `{{template}}` references. The partials hold only defines, leaving the
// named template empty until content is parsed into it.
func render(name, content string, partials []string, vars map[string]any) (string, error) {
	tmpl := template.New(name)
	for _, partial := range partials {
		if partial == "" {
			continue
		}
		if _, err := tmpl.Parse(partial); err != nil {
			return "", templateRefusal(fmt.Sprintf("parse partial for %s: %v", name, err), err)
		}
	}
	if _, err := tmpl.Parse(content); err != nil {
		return "", templateRefusal(fmt.Sprintf("parse template %s: %v", name, err), err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", templateRefusal(fmt.Sprintf("execute template %s: %v", name, err), err)
	}
	return buf.String(), nil
}

// templateRefusal classifies template text that will not parse or execute. The
// failing file may be the embedded copy or the project's own .local.tmpl /
// .tmpl override, and nothing here can tell which — but either way it is
// content somebody has to fix or remove, and no rerun renders it. Cause keeps
// text/template's own error reachable through errors.Is/As, where the %w wrap
// this replaced used to put it.
func templateRefusal(summary string, cause error) *refusal.Error {
	return refusal.Report(summary,
		"how to fix or remove the template that will not render").
		Command("template render").Cause(cause)
}
