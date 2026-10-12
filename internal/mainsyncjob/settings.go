package mainsyncjob

import (
	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/settings"
)

// The main_sync keys (E-2284).
const (
	EnabledSetting  settings.ID = "main_sync.enabled"
	IntervalSetting settings.ID = "main_sync.interval"
)

// MainSync is the runtime form of the project's "main_sync" object: what the
// job trusts after ParseMainSync has refused anything malformed.
type MainSync struct {
	Enabled settings.OptIn
}

// ParseMainSync turns the file layer into the runtime form.
func ParseMainSync(c config.MainSync) (ms MainSync, err error) {
	ms.Enabled, err = settings.ParseOptIn(string(c.Enabled))
	return ms, err
}

// Settings declares the main_sync keys.
func (MainSync) Settings() []settings.Setting {
	return []settings.Setting{{
		ID:          EnabledSetting,
		Feature:     "main_sync",
		Files:       settings.ProjectFile,
		Kind:        settings.OptInKind,
		Suggestable: true,
		Default:     settings.OffText,
		Rationale: "Keeps the project's main branch in step with its upstream: " +
			"fetches, fast-forwards main when only the remote moved, pushes when " +
			"only main moved, and records a fault when both did.",
	}, {
		ID:      IntervalSetting,
		Feature: "main_sync",
		Files:   settings.MachineFile,
		Kind:    settings.DurationKind,
		Default: config.DefaultMainSyncInterval,
	}}
}

var _ settings.Provider = MainSync{}

func init() {
	settings.Register(MainSync{}.Settings()...)
}
