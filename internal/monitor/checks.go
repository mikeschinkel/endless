package monitor

import (
	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/settings"
	"github.com/mikeschinkel/go-dt"
)

// The registered `checks` keys (E-2284): the hook gate switches. Only
// bash_git_writes has a reader today (hookcmd's write-target gate); the other
// three are registered because they are documented keys with defaults, and a
// registered default is the only place a default may live.
const (
	TaskRequiredCheck       = "task_required"
	DecisionCheckpointCheck = "decision_checkpoint"
	SessionAuditCheck       = "session_audit"
	BashGitWritesCheck      = "bash_git_writes"
)

// Checks is the runtime form of the merged "checks" map: each key's opt-in,
// parsed. A key the files do not set is absent and resolves to its registered
// default; a key nobody registered resolves to on.
type Checks map[string]settings.OptIn

// ParseChecks turns the file layer into the runtime form.
func ParseChecks(m map[string]config.OptInText) (c Checks, err error) {
	c = make(Checks, len(m))
	for name, text := range m {
		var o settings.OptIn
		o, err = settings.ParseOptIn(string(text))
		if err != nil {
			goto end
		}
		c[name] = o
	}
end:
	return c, err
}

// Enabled resolves the named check against its registered default.
func (c Checks) Enabled(name string) (on bool, err error) {
	var o settings.OptIn
	o, err = settings.Resolve(CheckSetting(name), c[name])
	return o.IsOn(), err
}

// CheckSetting is the setting ID of the named check.
func CheckSetting(name string) settings.ID {
	return settings.ID("checks." + name)
}

// Settings declares the known checks keys.
func (Checks) Settings() []settings.Setting {
	check := func(name, def string) settings.Setting {
		return settings.Setting{
			ID:      CheckSetting(name),
			Feature: "checks",
			Files:   settings.BothFiles,
			Kind:    settings.OptInKind,
			Default: def,
		}
	}
	return []settings.Setting{
		// On by default for backwards compatibility with the original
		// PreToolUse session-required block.
		check(TaskRequiredCheck, settings.OnText),
		check(DecisionCheckpointCheck, settings.OffText),
		check(SessionAuditCheck, settings.OffText),
		// E-940: treat git commands that rewrite a working tree as writes.
		check(BashGitWritesCheck, settings.OnText),
	}
}

var _ settings.Provider = Checks{}

func init() {
	settings.Register(Checks{}.Settings()...)
}

// IsCheckEnabled returns whether the named check is enabled for the
// project, honoring the layered Endless config (CLI defaults plus
// per-project overrides) and falling back to the check's registered default
// when neither layer sets the key.
//
// On any DB or config-load failure this defers to the registered default
// rather than blocking, matching the conservative behavior of the prior
// bespoke reader.
func IsCheckEnabled(projectID int64, name string) bool {
	var checks Checks

	db, err := DB()
	if err != nil {
		return checkDefault(name)
	}
	var projectPath string
	err = db.QueryRow(`SELECT path FROM projects WHERE id=?`, projectID).Scan(&projectPath)
	if err != nil {
		return checkDefault(name)
	}
	// The column is STORED form; config.Load reads a directory (E-2011).
	projectPath, err = ResolvedProjectPath(projectPath)
	if err != nil {
		return checkDefault(name)
	}
	cfg, err := config.Load(dt.DirPath(projectPath))
	if err != nil {
		return checkDefault(name)
	}
	checks, err = ParseChecks(cfg.Checks)
	if err != nil {
		return checkDefault(name)
	}
	return checkEnabled(checks, name)
}

// checkDefault is the named check with no file setting it.
func checkDefault(name string) bool {
	return checkEnabled(nil, name)
}

// checkEnabled is Checks.Enabled with the failure folded into the answer. The
// only failure is an uninitialized registry, which endless-go's startup
// refuses before any command runs; should it happen anyway, the check takes
// the unknown-key rule and is on.
func checkEnabled(c Checks, name string) bool {
	on, err := c.Enabled(name)
	if err != nil {
		return true
	}
	return on
}
