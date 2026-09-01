package projectstatuscmd

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// Naming the board's tmux session (E-1976).
//
// Two things are settled here, and they pull against each other:
//
//   - The name should be whatever fits the user's workflow, which means
//     configurable (`tmux.session_name`, a Go text/template).
//   - The launcher must never adopt a session it did not create, which a
//     configurable name makes possible — the whole point of configuring it is
//     that someone can choose `{{project}}` and collide with the session they
//     already keep open for that project.
//
// So the name is a preference and OWNERSHIP is a fact, checked separately (see
// window.go). Before this was configurable the built-in name was distinctive
// enough that no check was needed; making it configurable is what brings the
// check back, and that is a cost of the feature rather than an oversight in it.

// DefaultSessionNameTemplate is the built-in `tmux.session_name`.
//
// Every part of the shape answers the same constraint: a tmux status line
// truncates a session name to the width it has — nine characters on the machine
// this was reported from — so whatever distinguishes one board from another has
// to be at the FRONT.
//
//   - The PROJECT leads, because that is what differs. `{{project}}-monitor`
//     spends the visible nine on `endless-m`, which reads as a mangled copy of
//     an `endless` session sitting beside it in the list.
//   - `e-` leads the project: two characters that say "Endless made this", using
//     the prefix Endless already wears on its ids (E-NNNN, ES-NNNN, ED-NNNN),
//     and enough to keep the board out of the namespace a user picks by hand.
//   - `-monitor` trails, where truncation usually eats it. That is fine — it is
//     there for the full name, which is what `tmux ls` and `tmux attach -t`
//     show, and it survives into the tab for short project names (`e-h2pp-mo`).
const DefaultSessionNameTemplate = "e-{{project}}-monitor"

// sessionNameFor renders the configured template for one project.
//
// Errors are NOT fatal. A malformed template is a typo in a preference, and a
// preference must not be able to stop the board from opening: the built-in
// default is used and the reason is returned for the caller to print. The board
// still comes up, under a name the user can predict from the docs.
func sessionNameFor(project, tmpl string) (name string, warn error) {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultSessionNameTemplate
	}
	name, err := renderSessionName(project, tmpl)
	if err != nil {
		fallback, ferr := renderSessionName(project, DefaultSessionNameTemplate)
		if ferr != nil {
			// The built-in template is a compile-time constant; if it cannot
			// render, nothing here can, and a literal is better than nothing.
			fallback = monitor.SanitizeTmuxName(project) + "-monitor"
		}
		return fallback, fmt.Errorf("tmux.session_name %q: %w (using %q)", tmpl, err, fallback)
	}
	return name, nil
}

// renderSessionName applies one template and folds the result into a legal tmux
// session name.
//
// `{{project}}` is a FUNCTION rather than only a field, because that is how the
// setting reads when written down and a user should not have to know Go's dot
// syntax to name a window. `{{.Project}}` resolves to the same string for anyone
// who prefers it.
//
// The fold is applied to the RENDERED result, not to the project name going in:
// a template may legitimately contain spaces or punctuation of its own, and
// folding only the substitution would leave those in place to be rejected by
// tmux later, at a point far from the setting that caused it.
func renderSessionName(project, tmpl string) (string, error) {
	data := struct{ Project string }{Project: project}
	t, err := template.New("session_name").
		Option("missingkey=error").
		Funcs(template.FuncMap{"project": func() string { return project }}).
		Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err = t.Execute(&b, data); err != nil {
		return "", err
	}
	name := monitor.SanitizeTmuxName(b.String())
	if name == "" || name == "project" && strings.TrimSpace(b.String()) == "" {
		return "", fmt.Errorf("rendered to an empty session name")
	}
	return name, nil
}

// sessionNameTemplate reads `tmux.session_name` for a project, merged across the
// CLI layer (~/.config/endless/config.json) and the project layer
// (<project>/.endless/config.json), project winning.
//
// Returns "" for every failure, which sessionNameFor reads as "use the default".
// A configuration that cannot be loaded must not stop the board from opening:
// the setting is a preference, and the built-in name is a working answer. The
// alternative — refusing to open a window because a config file has a syntax
// error — trades a cosmetic preference for the feature itself.
func sessionNameTemplate(projectDir string) string {
	cfg, err := config.Load(dt.DirPath(projectDir))
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.Tmux.SessionName
}
