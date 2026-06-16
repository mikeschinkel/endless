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
	"path/filepath"
	"strings"
	"text/template"

	"github.com/mikeschinkel/endless/internal/monitor"
)

//go:embed templates
var embedded embed.FS

// Run dispatches `endless-go template <verb> [args]`.
func Run(args []string) {
	if len(args) < 1 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "render":
		if err := runRender(args[1:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-go template: unknown command %q\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: endless-go template <command> [flags] [args]")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  render [--project <name>] <name>   read JSON vars on stdin, render template to stdout")
}

func runRender(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: endless-go template render [--project <name>] <name>")
	}
	name := normalizeName(rest[0])

	projectRoot, err := resolveProjectRoot(*projectName)
	if err != nil {
		return err
	}

	if err := materializeIfMissing(projectRoot, name); err != nil {
		return err
	}

	content, err := loadTemplate(projectRoot, name)
	if err != nil {
		return err
	}

	vars, err := decodeVars(stdin)
	if err != nil {
		return err
	}

	out, err := render(name, content, vars)
	if err != nil {
		return err
	}

	_, err = io.WriteString(stdout, out)
	return err
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
		return "", fmt.Errorf("project not found: %s", name)
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("project %s has no registered path", name)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func projectRootFromCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if st, err := os.Stat(filepath.Join(dir, ".endless")); err == nil && st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("template render requires a project context — cd into a project or pass --project <name>")
}

// templatesSubdir returns the project-scoped templates directory.
func templatesSubdir(projectRoot string) string {
	return filepath.Join(projectRoot, ".endless", "templates")
}

// normalizeName applies the default-extension rule: when the basename of
// the user-supplied name has no `.`, append `.md`. Otherwise use as-is.
// So `handoff` → `handoff.md`, `handoff.md` → `handoff.md` (idempotent),
// `handoff.txt` → `handoff.txt`, `handoff/task` → `handoff/task.md`.
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
		return nil, fmt.Errorf("unknown template %q", name)
	}
	return data, nil
}

// materializeIfMissing writes the embedded template content to
// <project_root>/.endless/templates/<name>.tmpl when that file does not
// exist. Per-file: only the requested name is materialized; siblings are
// untouched.
func materializeIfMissing(projectRoot, name string) error {
	dst := filepath.Join(templatesSubdir(projectRoot), name+".tmpl")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	data, err := embeddedContent(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
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
// degradation of Python's string.Template.safe_substitute.
func render(name, content string, vars map[string]any) (string, error) {
	tmpl, err := template.New(name).Parse(content)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.String(), nil
}
