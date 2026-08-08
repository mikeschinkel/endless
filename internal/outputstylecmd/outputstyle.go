// Package outputstylecmd implements the `endless-go outputstyle` subcommand:
// installs the embedded Endless Claude Code output style into a project's
// <root>/.claude/output-styles/ directory, and optionally activates it by
// writing the `outputStyle` key into <root>/.claude/settings.json (E-1919).
//
// Scope is deliberately project-only. A user-global install (~/.claude/) would
// reshape every Claude session on the machine, including sessions for projects
// that never opted in; installing a project tool must not do that.
//
// Placement and activation are separate steps. `install` writes the file and
// leaves the style inert; `--activate` additionally selects it. A bare install
// warns on stderr that the style is present but doing nothing, and prints the
// one command that turns it on — a silently-inert file is worse than no file,
// because it reads as "done" while changing nothing.
//
// Unlike templatecmd this package has no read-back precedence chain: it only
// ever writes. There is therefore no on-disk copy that can shadow the embedded
// original, which is the failure mode that makes templatecmd skip
// materialization for self_dev projects.
package outputstylecmd

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mikeschinkel/endless/internal/monitor"
)

//go:embed templates
var embedded embed.FS

// StyleName is the output style's identifier as Claude Code resolves it. The
// embedded file's basename, its YAML `name:` frontmatter, and the value written
// to settings.json are all deliberately this same string, so no lookup can
// disagree about what the style is called.
const StyleName = "Endless"

// embeddedPath is the style's path inside the embedded FS.
const embeddedPath = "templates/" + StyleName + ".md.tmpl"

// settingsKey is the settings.json key naming the active output style. Claude
// Code exposes the same setting as the `/config` row `outputStyle` and the
// `/config output-style=<name>` key; there is no `/output-style` command.
const settingsKey = "outputStyle"

// activateHint is the command a user runs to select the style by hand. Kept in
// one place so the warning, the help text, and the remove notice cannot drift.
const activateHint = "/config output-style=" + StyleName

// Run dispatches `endless-go outputstyle <command> [flags]`.
func Run(args []string) {
	if len(args) < 1 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch args[0] {
	case "install":
		err = runInstall(args[1:], os.Stdout, os.Stderr)
	case "remove":
		err = runRemove(args[1:], os.Stdout, os.Stderr)
	case "path":
		err = runPath(args[1:], os.Stdout)
	case "-h", "--help", "help":
		usage(os.Stdout)
		return
	default:
		fmt.Fprintf(os.Stderr, "endless-go outputstyle: unknown command %q\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: endless-go outputstyle <command> [flags]")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  install [--project <name>] [--activate] [--force]")
	fmt.Fprintln(w, "        write the style to <project>/.claude/output-styles/"+StyleName+".md")
	fmt.Fprintln(w, "  remove [--project <name>]")
	fmt.Fprintln(w, "        delete the style file and deactivate it if selected")
	fmt.Fprintln(w, "  path [--project <name>]")
	fmt.Fprintln(w, "        print the style's install path without writing anything")
}

func runInstall(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	activate := fs.Bool("activate", false, "also select the style in .claude/settings.json")
	force := fs.Bool("force", false, "overwrite an existing style file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := resolveProjectRoot(*projectName)
	if err != nil {
		return err
	}
	dst := stylePath(root)

	data, err := embedded.ReadFile(embeddedPath)
	if err != nil {
		return fmt.Errorf("embedded output style missing: %w", err)
	}

	_, statErr := os.Stat(dst)
	exists := statErr == nil
	switch {
	case exists && !*force:
		fmt.Fprintf(stdout, "• output style already present: %s\n", rel(root, dst))
	default:
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return err
		}
		verb := "installed"
		if exists {
			verb = "overwrote"
		}
		fmt.Fprintf(stdout, "✓ %s output style: %s\n", verb, rel(root, dst))
	}

	if !*activate {
		active, err := activeStyle(root)
		if err != nil {
			return err
		}
		if active == StyleName {
			fmt.Fprintf(stdout, "• already active (settings.json outputStyle=%s)\n", StyleName)
			return nil
		}
		warnInert(stderr)
		return nil
	}

	changed, err := setActiveStyle(root, StyleName)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(stdout, "✓ activated: settings.json outputStyle=%s\n", StyleName)
	} else {
		fmt.Fprintf(stdout, "• already active (settings.json outputStyle=%s)\n", StyleName)
	}
	return nil
}

// warnInert prints the not-activated warning. Deliberately on stderr and
// deliberately loud: the file is present, so every surface reads as installed,
// while the style is in fact doing nothing at all.
func warnInert(stderr io.Writer) {
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "WARNING: the output style is installed but NOT ACTIVE.")
	fmt.Fprintln(stderr, "  The file is on disk and Claude Code can see it, but no session is using it")
	fmt.Fprintln(stderr, "  until the style is selected. Nothing about agent output changes yet.")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "  To activate, either:")
	fmt.Fprintln(stderr, "    - in a Claude Code session, run:  "+activateHint)
	fmt.Fprintln(stderr, "    - or re-run this command with --activate")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "  (There is no /output-style command; it is a /config setting.)")
}

func runRemove(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := resolveProjectRoot(*projectName)
	if err != nil {
		return err
	}
	dst := stylePath(root)

	switch err := os.Remove(dst); {
	case err == nil:
		fmt.Fprintf(stdout, "✓ removed output style: %s\n", rel(root, dst))
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(stdout, "• output style not present: %s\n", rel(root, dst))
	default:
		return err
	}

	// Deactivate only when this style is the selected one — never clobber a
	// different style the user chose themselves.
	active, err := activeStyle(root)
	if err != nil {
		return err
	}
	if active != StyleName {
		return nil
	}
	if _, err := setActiveStyle(root, ""); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "✓ deactivated: removed outputStyle from settings.json")
	return nil
}

func runPath(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("path", flag.ContinueOnError)
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := resolveProjectRoot(*projectName)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, stylePath(root))
	return nil
}

// stylePath is the style's install path for a project root.
func stylePath(root string) string {
	return filepath.Join(root, ".claude", "output-styles", StyleName+".md")
}

// settingsPath is the project-scoped Claude settings file.
func settingsPath(root string) string {
	return filepath.Join(root, ".claude", "settings.json")
}

// activeStyle returns the currently selected style name, or "" when none is
// set or the settings file does not exist.
func activeStyle(root string) (string, error) {
	settings, err := readSettings(root)
	if err != nil {
		return "", err
	}
	name, _ := settings[settingsKey].(string)
	return name, nil
}

// setActiveStyle writes (name != "") or clears (name == "") the outputStyle key
// in the project's settings.json, preserving every other key. Reports whether
// the file actually changed. Creates the file when absent.
func setActiveStyle(root, name string) (changed bool, err error) {
	settings, err := readSettings(root)
	if err != nil {
		return false, err
	}
	current, _ := settings[settingsKey].(string)
	if current == name {
		return false, nil
	}
	if name == "" {
		delete(settings, settingsKey)
	} else {
		settings[settingsKey] = name
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	path := settingsPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return false, err
	}
	return true, nil
}

// readSettings loads the project's settings.json into a map. A missing file is
// an empty map, not an error — the caller may be creating it. A malformed file
// IS an error: silently replacing a settings file we could not parse would
// discard whatever the user had in it.
func readSettings(root string) (map[string]any, error) {
	data, err := os.ReadFile(settingsPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]any{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (refusing to overwrite it): %w", settingsPath(root), err)
	}
	return settings, nil
}

func resolveProjectRoot(projectName string) (string, error) {
	if projectName != "" {
		return projectRootByName(projectName)
	}
	root, err := monitor.ProjectRootFromCwd()
	if errors.Is(err, monitor.ErrNoProjectContext) {
		return "", errors.New("outputstyle requires a project context — cd into a project or pass --project <name>")
	}
	return root, err
}

func projectRootByName(name string) (string, error) {
	db, err := monitor.DB()
	if err != nil {
		return "", err
	}
	var path string
	if err := db.QueryRow("SELECT path FROM projects WHERE name = ?", name).Scan(&path); err != nil {
		return "", fmt.Errorf("project not found: %s", name)
	}
	return filepath.Abs(path)
}

// rel renders a path relative to the project root for display, falling back to
// the absolute path when it is not under the root.
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}
