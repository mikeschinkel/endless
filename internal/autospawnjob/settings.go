package autospawnjob

import (
	"strconv"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/settings"
	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
)

// The auto_spawn and prime keys (E-2284).
const (
	EnabledSetting   settings.ID = "auto_spawn.enabled"
	CapSetting       settings.ID = "auto_spawn.cap"
	IntervalSetting  settings.ID = "auto_spawn.interval"
	TargetSetting    settings.ID = "auto_spawn.target"
	PlacementSetting settings.ID = "auto_spawn.placement"

	PrimeEnabledSetting settings.ID = "prime.enabled"
	PrimeCapSetting     settings.ID = "prime.cap"
)

// AutoSpawn is the runtime form of the project's "auto_spawn" opt-in.
type AutoSpawn struct {
	Enabled settings.OptIn
}

// ParseAutoSpawn turns the file layer into the runtime form.
func ParseAutoSpawn(c config.AutoSpawn) (a AutoSpawn, err error) {
	a.Enabled, err = settings.ParseOptIn(string(c.Enabled))
	return a, err
}

// Settings declares the auto_spawn keys.
func (AutoSpawn) Settings() []settings.Setting {
	return []settings.Setting{{
		ID:          EnabledSetting,
		Feature:     "auto_spawn",
		Files:       settings.ProjectFile,
		Kind:        settings.OptInKind,
		Suggestable: true,
		Default:     settings.OffText,
		Rationale: "Starts a Claude session, unasked, on a task rated low " +
			"complexity and low risk, so small safe work gets done without " +
			"anyone spawning it.",
	}, {
		ID:      CapSetting,
		Feature: "auto_spawn",
		Files:   settings.ProjectFile,
		Kind:    settings.IntKind,
		Default: strconv.Itoa(config.DefaultAutoSpawnCap),
	}, {
		ID:      IntervalSetting,
		Feature: "auto_spawn",
		Files:   settings.MachineFile,
		Kind:    settings.DurationKind,
		Default: config.DefaultAutoSpawnInterval,
	}, {
		ID:      TargetSetting,
		Feature: "auto_spawn",
		Files:   settings.MachineFile,
		Kind:    settings.ChoiceKind,
		Choices: []string{config.AutoSpawnTargetActive, config.AutoSpawnTargetMonitor},
		Default: config.AutoSpawnTargetActive,
	}, {
		ID:      PlacementSetting,
		Feature: "auto_spawn",
		Files:   settings.MachineFile,
		Kind:    settings.ChoiceKind,
		Choices: []string{
			string(spawnlaunchcmd.PlaceFirst), string(spawnlaunchcmd.PlaceLast),
			string(spawnlaunchcmd.PlaceLeft), string(spawnlaunchcmd.PlaceRight),
		},
		Default: config.DefaultAutoSpawnPlacement,
	}}
}

// Prime is the runtime form of the project's "prime" opt-in.
type Prime struct {
	Enabled settings.OptIn
}

// ParsePrime turns the file layer into the runtime form.
func ParsePrime(c config.Prime) (p Prime, err error) {
	p.Enabled, err = settings.ParseOptIn(string(c.Enabled))
	return p, err
}

// Settings declares the prime keys.
func (Prime) Settings() []settings.Setting {
	return []settings.Setting{{
		ID:          PrimeEnabledSetting,
		Feature:     "prime",
		Files:       settings.ProjectFile,
		Kind:        settings.OptInKind,
		Suggestable: true,
		Default:     settings.OffText,
		Rationale: "Starts a task's session ahead of need when a plan is " +
			"attached, so it has read in by the time you pick the task up.",
	}, {
		ID:      PrimeCapSetting,
		Feature: "prime",
		Files:   settings.ProjectFile,
		Kind:    settings.IntKind,
		Default: strconv.Itoa(config.DefaultPrimeCap),
	}}
}

var (
	_ settings.Provider = AutoSpawn{}
	_ settings.Provider = Prime{}
)

func init() {
	settings.Register(AutoSpawn{}.Settings()...)
	settings.Register(Prime{}.Settings()...)
}

// resolveEnabled is an opt-in's effective on/off: the file's value, or the
// registered default when unset.
func resolveEnabled(id settings.ID, v settings.OptIn) (on bool, err error) {
	var o settings.OptIn
	o, err = settings.Resolve(id, v)
	return o.IsOn(), err
}
