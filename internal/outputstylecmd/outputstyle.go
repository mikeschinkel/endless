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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
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
//
// The verbs take stdout but never a stderr writer: anything bound for stderr is
// a classified refusal, raised through internal/refusal by the site that knows
// what it means, rather than a line handed to a writer that knows nothing about
// it. Errors travel back here as values carrying their own class, and the one
// print site below renders whichever class each of them chose.
func Run(args []string) {
	if len(args) < 1 {
		// setup.py always names a subcommand, so a bare invocation is somebody
		// running the binary by hand — retype it and move on.
		refusal.NoReport(
			"endless-go outputstyle: no command given",
			"Re-run with install, remove or path",
		).Command("outputstyle").Text(usageText()).Exit(2)
	}
	var err error
	switch args[0] {
	case "install":
		err = runInstall(args[1:], os.Stdout)
	case "remove":
		err = runRemove(args[1:], os.Stdout)
	case "path":
		err = runPath(args[1:], os.Stdout)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
		return
	default:
		refusal.NoReport(
			fmt.Sprintf("endless-go outputstyle: unknown command %q", args[0]),
			"Re-run with install, remove or path",
		).Command("outputstyle").Detail(usageText()).Exit(2)
	}
	if err != nil {
		// The single print site for every verb. From keeps the class the error
		// chose at construction — the flag and project-context refusals are the
		// caller's to retry, the filesystem failures are the user's to clear —
		// and faults anything that never chose one, which is the right reading
		// for the invariant breaks left unclassified below.
		refusal.From(err).Command("outputstyle").Exit(1)
	}
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go outputstyle <command> [flags]",
		"Commands:",
		"  install [--project <name>] [--activate] [--force]",
		"        write the style to <project>/.claude/output-styles/" + StyleName + ".md",
		"  remove [--project <name>]",
		"        delete the style file and deactivate it if selected",
		"  path [--project <name>]",
		"        print the style's install path without writing anything",
	}, "\n") + "\n"
}

// flagRefusal classifies a flag-parsing failure for one of the verbs.
//
// Text reproduces stderr exactly as it stands today: flag's own output (its
// error line plus the usage block, or for -h the usage block alone) followed by
// the error line the print site in Run has always appended. That repetition is
// not an improvement anybody asked for here — this task adds a verdict for
// agents and changes nothing a person reads — so it is preserved rather than
// quietly tidied away.
func flagRefusal(verb string, fs *refusal.Flags, err error) error {
	return refusal.NoReport(err.Error(), "Correct the flags and retry").
		Command("outputstyle " + verb).
		Text(fs.Output() + err.Error()).
		Cause(err)
}

// fsFailure classifies a filesystem error none of these verbs can work around.
//
// EACCES on .claude/, a read-only checkout, a full disk: Endless is not broken
// and no retry with different arguments helps, so the person who owns the
// directory is the one who has to decide what to do about it.
func fsFailure(err error) error {
	return refusal.Report(err.Error(),
		"whether to fix the permissions or free the space where Endless is writing").
		Command("outputstyle").Cause(err)
}

func runInstall(args []string, stdout io.Writer) error {
	fs := refusal.NewFlags("install")
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	activate := fs.Bool("activate", false, "also select the style in .claude/settings.json")
	force := fs.Bool("force", false, "overwrite an existing style file")
	if err := fs.Parse(args); err != nil {
		return flagRefusal("install", fs, err)
	}

	root, err := resolveProjectRoot(*projectName)
	if err != nil {
		return err
	}
	dst := stylePath(root)

	data, err := embedded.ReadFile(embeddedPath)
	if err != nil {
		// The template is compiled into this binary, so its absence means the
		// binary was built wrong. Nothing the caller passes can fix that; the
		// user has to reinstall endless-go.
		return refusal.Faultf("embedded output style missing: %s", err).
			Command("outputstyle install").Cause(err)
	}

	_, statErr := os.Stat(dst)
	exists := statErr == nil
	switch {
	case exists && !*force:
		fmt.Fprintf(stdout, "• output style already present: %s\n", rel(root, dst))
	default:
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return fsFailure(err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fsFailure(err)
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
		warnInert()
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

// warnInert records the not-activated warning in the errors channel.
//
// The file is present, so every surface reads as installed while the style does
// nothing at all — worth saying, and worth saying to the right reader. That is
// the USER: activation is their opt-in by design (installing never activates),
// `/config output-style` is a slash command no agent can run, and `--activate`
// is right only when they asked for the style to be in effect. On stderr it
// cost the agent a message with no action behind it (E-2159 decision 5).
//
// Fingerprinted on the code alone: one project with an inactive style is one
// incident however many times anything reinstalls it.
func warnInert() {
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeOutputStyleInactive,
		Source:  "outputstyle:install",
		Summary: "the Endless output style is installed but not active",
		Detail:  inertWarningText(),
	})
}

// inertWarningText is the warning a person reads, unchanged.
func inertWarningText() string {
	return strings.Join([]string{
		"WARNING: the output style is installed but NOT ACTIVE.",
		"  The file is on disk and Claude Code can see it, but no session is using it",
		"  until the style is selected. Nothing about agent output changes yet.",
		"",
		"  To activate, either:",
		"    - in a Claude Code session, run:  " + activateHint,
		"    - or re-run this command with --activate",
		"",
		"  (There is no /output-style command; it is a /config setting.)",
	}, "\n")
}

func runRemove(args []string, stdout io.Writer) error {
	fs := refusal.NewFlags("remove")
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	if err := fs.Parse(args); err != nil {
		return flagRefusal("remove", fs, err)
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
		return fsFailure(err)
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
	fs := refusal.NewFlags("path")
	projectName := fs.String("project", "", "registered project name (overrides cwd-based resolution)")
	if err := fs.Parse(args); err != nil {
		return flagRefusal("path", fs, err)
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
		return false, fsFailure(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return false, fsFailure(err)
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
		return nil, fsFailure(err)
	}
	settings := map[string]any{}
	if err := json.Unmarshal(data, &settings); err != nil {
		// Whose syntax error it is decides who has to clear it, and this
		// process cannot tell: an agent that just edited settings.json can fix
		// its own mistake and retry, while edits the user made by hand carry
		// an intent nothing here can reconstruct. Only the agent holding the
		// conversation knows which happened (ED, 2026-09-18).
		return nil, refusal.ReportIf(
			fmt.Sprintf("%s is not valid JSON (refusing to overwrite it): %s", settingsPath(root), err),
			"the malformed settings.json holds the user's own edits rather than yours",
			"fix the syntax error you introduced and retry",
			"their intent cannot be inferred and overwriting the file would discard it",
		).Command("outputstyle").Cause(err)
	}
	return settings, nil
}

func resolveProjectRoot(projectName string) (string, error) {
	if projectName != "" {
		return projectRootByName(projectName)
	}
	root, err := monitor.ProjectRootFromCwd()
	if errors.Is(err, monitor.ErrNoProjectContext) {
		// Not the user's call: a directory with no .endless/ is either the
		// wrong directory or an unregistered project, and both are answered by
		// the caller passing something different.
		return "", refusal.NoReport(
			"outputstyle requires a project context — cd into a project or pass --project <name>",
			"cd into the project root or pass --project <name> and retry",
		).Command("outputstyle").Cause(err)
	}
	return root, err
}

func projectRootByName(name string) (string, error) {
	db, err := monitor.DB()
	if err != nil {
		return "", err
	}
	var path string
	if err := db.QueryRow("SELECT path FROM live_projects WHERE name = ?", name).Scan(&path); err != nil {
		// Every Scan failure reads as "not found", including a locked database
		// or schema drift. That masking predates this change and is left as it
		// stands; the class follows the message, which sends the caller back to
		// the project list rather than to the user.
		return "", refusal.NoReport(
			fmt.Sprintf("project not found: %s", name),
			"Check `endless project list` and retry with the right --project",
		).Command("outputstyle").Cause(err)
	}
	// The column is STORED form — `~/...` for a project under $HOME — and
	// filepath.Abs would turn that into `<cwd>/~/...` (E-2011).
	return monitor.ResolvedProjectPath(path)
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
