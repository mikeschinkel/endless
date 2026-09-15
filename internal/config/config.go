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
//   - Project-only: Name, Label, Description, Language, Status, Dependencies, Documents.
//     These have no CLI-layer analog; the CLI layer ignores them.
//   - Layered: Tracking, Checks, Tmux. Project values override CLI values.
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
	Name         string    `json:"name,omitempty"`
	Label        string    `json:"label,omitempty"`
	Description  string    `json:"description,omitempty"`
	Language     string    `json:"language,omitempty"`
	Status       string    `json:"status,omitempty"`
	Dependencies []string  `json:"dependencies,omitempty"`
	Documents    Documents `json:"documents,omitzero"`

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
}

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

// RootConfig satisfies cfgstore.RootConfig (marker method).
func (c *EndlessConfig) RootConfig() {}

// compile-time checks
var _ cfgstore.RootConfig = (*EndlessConfig)(nil)
