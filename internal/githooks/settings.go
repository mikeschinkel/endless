// Package githooks declares the settings of the Endless-Task trailer's Git
// hook setup (E-2490). The hook itself and its setup are the Python CLI's
// (`endless git-hook`, `endless project init`); Go owns only the registry.
package githooks

import (
	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/settings"
)

// The git_hooks keys.
const (
	LefthookFileSetting settings.ID = "git_hooks.lefthook_file"
	ReportSetting       settings.ID = "git_hooks.report"
)

// GitHooks declares the "git_hooks" keys. Nothing in Go reads them: Python
// reads their values through `endless-go settings list`.
type GitHooks struct{}

// Settings declares the git_hooks keys. Both may live in either file: the
// user's machine file sets their default, and a project may decide for
// itself.
func (GitHooks) Settings() []settings.Setting {
	return []settings.Setting{{
		ID:      LefthookFileSetting,
		Feature: "git_hooks",
		Files:   settings.BothFiles,
		Kind:    settings.ChoiceKind,
		Choices: []string{"local", "committed"},
		Default: config.DefaultLefthookFile,
	}, {
		ID:      ReportSetting,
		Feature: "git_hooks",
		Files:   settings.BothFiles,
		Kind:    settings.OptInKind,
		Default: settings.OnText,
	}}
}

var _ settings.Provider = GitHooks{}

func init() {
	settings.Register(GitHooks{}.Settings()...)
}
