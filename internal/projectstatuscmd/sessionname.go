package projectstatuscmd

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// Naming the monitor's tmux session (E-1976).
//
// The name is a preference: `tmux.session_name`, a Go text/template, so it can
// fit the user's workflow.

// DefaultSessionNameTemplate is the built-in `tmux.session_name`: the project's
// own name.
//
// It was `e-{{project}}-monitor` while the monitor shared the user's tmux
// server, where a bare project name would collide with the session the user
// already keeps for that project and could not be told apart from it in a
// truncated status line. Since E-2156 the monitor runs on its own server
// (`tmux -L endless`), which holds nothing but monitors, so neither problem
// exists there and the plain name is the one to read.
const DefaultSessionNameTemplate = "{{project}}"

// sessionNameFor renders the configured template for one project.
//
// Errors are NOT fatal. A malformed template is a typo in a preference, and a
// preference must not be able to stop the monitor from opening: the built-in
// default is used and the reason is returned for the caller to print. The
// monitor
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
// A configuration that cannot be loaded must not stop the monitor from opening:
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
