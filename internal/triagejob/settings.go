package triagejob

import (
	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/settings"
)

// The fault_triage keys (E-2284).
const (
	EnabledSetting  settings.ID = "fault_triage.enabled"
	IntervalSetting settings.ID = "fault_triage.interval"
)

// FaultTriage is the runtime form of the project's "fault_triage" opt-in.
type FaultTriage struct {
	Enabled settings.OptIn
}

// ParseFaultTriage turns the file layer into the runtime form.
func ParseFaultTriage(c config.FaultTriage) (f FaultTriage, err error) {
	f.Enabled, err = settings.ParseOptIn(string(c.Enabled))
	return f, err
}

// Settings declares the fault_triage keys.
func (FaultTriage) Settings() []settings.Setting {
	return []settings.Setting{{
		ID:          EnabledSetting,
		Feature:     "fault_triage",
		Files:       settings.ProjectFile,
		Kind:        settings.OptInKind,
		Suggestable: true,
		Default:     settings.OffText,
		Rationale: "Routes each new error to a session that fixes it: messages " +
			"the live session that raised it, resumes an ended one, or files and " +
			"spawns a bugfix task.",
	}, {
		ID:      IntervalSetting,
		Feature: "fault_triage",
		Files:   settings.MachineFile,
		Kind:    settings.DurationKind,
		Default: config.DefaultFaultTriageInterval,
	}}
}

var _ settings.Provider = FaultTriage{}

func init() {
	settings.Register(FaultTriage{}.Settings()...)
}
