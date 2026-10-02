// Package config provides layered configuration loading for Endless using
// go-cfgstore. Configuration is loaded from two sources with project precedence:
//
//   - CLI:     ~/.config/endless/config.json
//   - Project: <project>/.endless/config.json
//
// Field-level merge semantics are documented on EndlessConfig and implemented
// in merge.go. See E-949 for the design discussion.
package config

import (
	"github.com/mikeschinkel/go-cfgstore"
)

// ConfigSlug is the directory name segment used by both CLI and project
// config locations: ~/.config/endless and <project>/.endless.
const ConfigSlug = "endless"

// ConfigFile is the filename within each config directory.
const ConfigFile = "config.json"

// EndlessConfig is the root configuration loaded from layered JSON files.
//
// Field categories:
//
//   - Global-only: Roots, ScanInterval, Ignore, Ownership, NodeID.
//     These have no project-layer analog; the project layer ignores them.
//   - Project-only: Name, Label, Description, Language, Status, Dependencies, Documents,
//     Migrations.
//     These have no CLI-layer analog; the CLI layer ignores them.
//   - Layered: Tracking, Checks, Tmux, ProjectStatus. Project values override
//     CLI values.
//
// JSON tags MUST stay byte-identical to the existing on-disk schema so files
// continue to load without migration.
type EndlessConfig struct {
	// Global-only fields.
	Roots        []string            `json:"roots,omitempty"`
	ScanInterval int                 `json:"scan_interval,omitempty"`
	Ignore       []string            `json:"ignore,omitempty"`
	Ownership    map[string][]string `json:"ownership,omitempty"`
	NodeID       string              `json:"node_id,omitempty"`

	// Project-only fields.
	Name         string     `json:"name,omitempty"`
	Label        string     `json:"label,omitempty"`
	Description  string     `json:"description,omitempty"`
	Language     string     `json:"language,omitempty"`
	Status       string     `json:"status,omitempty"`
	Dependencies []string   `json:"dependencies,omitempty"`
	Documents    Documents  `json:"documents,omitzero"`
	Migrations   Migrations `json:"migrations,omitzero"`

	// Layered fields.
	//
	// Tracking is one of "enforce", "track", "off", or "" (inherit).
	// An empty string on the project layer inherits the CLI value, which
	// is itself "" if unset. Callers map a final "" to "enforce" for
	// registered projects.
	Tracking string `json:"tracking,omitempty"`

	// Checks is a per-key enable/disable map. Merge is per-key with optional
	// per-key custom rules; see merge.go.
	Checks map[string]bool `json:"checks,omitempty"`

	// Tmux holds multiplexer preferences. Layered per field, not wholesale:
	// setting one of its fields in a project must not blank the others
	// inherited from the CLI layer.
	Tmux Tmux `json:"tmux,omitzero"`

	// AutoSpawn configures the auto-spawn job (E-1814). Its fields are split
	// across the layers by who decides them: whether a project takes part is
	// that project's decision, and the job's cadence is the user's, because one
	// job serves every project. See AutoSpawn.
	AutoSpawn AutoSpawn `json:"auto_spawn,omitzero"`

	// ProjectStatus holds display preferences for `project status` and
	// `project monitor`. Layered per list and per attribute.
	ProjectStatus ProjectStatus `json:"project_status,omitzero"`

	// Prime configures the prime job (E-1994): starting a task's session ahead
	// of need when a plan is attached. Project-only, for AutoSpawn.Enabled's
	// reason. Its cadence, tmux target and placement are AutoSpawn's: both jobs
	// start sessions nobody asked for, and the user's one setting governs both.
	Prime Prime `json:"prime,omitzero"`
}

// Prime is the "prime" object. Both fields are PROJECT-ONLY and never
// inherited from the CLI layer: a primed session is a live process and a tmux
// window per task, so opting in is that project's decision, read with
// LoadProject.
type Prime struct {
	// Enabled opts the project in. Absent or false is off — the kill switch.
	Enabled bool `json:"enabled,omitempty"`

	// Cap is how many primed sessions may be outstanding in the project at
	// once: live sessions bound to a task nobody has started yet. Zero means
	// DefaultPrimeCap. Each is a live process; tens are comfortable.
	Cap int `json:"cap,omitempty"`
}

// DefaultPrimeCap is the cap on outstanding primed sessions per project when
// none is configured.
const DefaultPrimeCap = 3

// ProjectStatus is the "project_status" object.
type ProjectStatus struct {
	// Colors sets each list's row colors. A list or attribute left out keeps
	// the built-in default.
	Colors ListColors `json:"colors,omitzero"`
}

// ListColors is "project_status.colors": one entry per list, in the order the
// lists render.
type ListColors struct {
	Urgent ListColor `json:"urgent,omitzero"`
	Epics  ListColor `json:"epics,omitzero"`
	Other  ListColor `json:"other,omitzero"`
}

// ListColor is one list's background and foreground as 256-color indexes
// (0–255). Absent inherits; -1 means none — the terminal's own default — which
// is how a list goes without a background.
type ListColor struct {
	BG *int `json:"bg,omitempty"`
	FG *int `json:"fg,omitempty"`
}

// AutoSpawn is the "auto_spawn" object.
//
// Enabled and Cap are PROJECT-ONLY and are never inherited from the CLI layer:
// opting a project into sessions nobody asked for is a decision about that
// project, and a user-level `enabled: true` silently opting in every project
// is the failure the per-project switch exists to prevent. Read them with
// LoadProject.
//
// Interval, Target and Placement are CLI-ONLY: there is one auto-spawn job for
// the whole database, so a per-project cadence has nothing to attach to. Read
// them with Load("").
type AutoSpawn struct {
	// Enabled opts the project in. Absent or false is off, which is also the
	// kill switch.
	Enabled bool `json:"enabled,omitempty"`

	// Cap is how many auto-spawned tasks may be outstanding (underway or
	// unverified) in the project at once. Zero means DefaultAutoSpawnCap.
	Cap int `json:"cap,omitempty"`

	// Interval is the job's cadence as a Go duration ("5m"). Empty means
	// DefaultAutoSpawnInterval. At most one task is spawned per interval.
	Interval string `json:"interval,omitempty"`

	// Target names the tmux session a spawned window opens in: "active" (the
	// session of the most recently active attached client) or "monitor" (the
	// session the job runs in). Empty means "active".
	Target string `json:"target,omitempty"`

	// Placement is where a spawned window's tab lands in the target session
	// (E-2234): "first", "last", "left" or "right", the last two relative to
	// that session's active window. Empty means DefaultAutoSpawnPlacement.
	Placement string `json:"placement,omitempty"`
}

const (
	// DefaultAutoSpawnCap is E-1815's initial cap on outstanding auto-spawned
	// work per project.
	DefaultAutoSpawnCap = 3

	// DefaultAutoSpawnInterval is the job cadence when none is configured.
	DefaultAutoSpawnInterval = "5m"

	// DefaultAutoSpawnPlacement puts an auto-spawned window after every
	// existing one, out of the way of the windows the user arranged. A manual
	// `task spawn` defaults to first instead, because its user asked for it.
	DefaultAutoSpawnPlacement = "last"

	// AutoSpawnTargetActive and AutoSpawnTargetMonitor are the two Target
	// values.
	AutoSpawnTargetActive  = "active"
	AutoSpawnTargetMonitor = "monitor"
)

// Tmux is the "tmux" object: how Endless names and shapes what it creates in
// the multiplexer.
type Tmux struct {
	// SessionName is the Go text/template rendering the tmux session name that
	// `endless project monitor --tmux` opens for a project (E-1976). Empty
	// inherits, and a fully-empty result falls back to projectstatuscmd's
	// built-in default.
	//
	// `{{project}}` is available as a function, so the value reads the way a user
	// would write it; `{{.Project}}` resolves to the same string for anyone who
	// prefers the data form.
	//
	// The rendered result is folded to a legal tmux session name, so a template
	// may hold spaces, dots or slashes without the user having to know tmux's
	// rules.
	SessionName string `json:"session_name,omitempty"`
}

// Documents is the per-project "documents" object. Currently holds only
// "rules" but may grow.
type Documents struct {
	Rules []string `json:"rules,omitempty"`
}

// Migrations is the per-project "migrations" object: where the project keeps
// its versioned migration files, so `endless worktree land` can refuse a land
// whose migrations would collide with ones main gained meanwhile (E-2184).
//
// Tool-agnostic by design: the land gate diffs these paths in git and knows
// nothing of goose, Alembic or Prisma.
type Migrations struct {
	// Dirs are repo-relative directories holding migration files.
	Dirs []string `json:"dirs,omitempty"`
}

// RootConfig satisfies cfgstore.RootConfig (marker method).
func (c *EndlessConfig) RootConfig() {}

// compile-time checks
var _ cfgstore.RootConfig = (*EndlessConfig)(nil)
