package config

import (
	"github.com/mikeschinkel/go-cfgstore"
)

// Normalize applies post-load defaults and validation to an EndlessConfig.
// Called by go-cfgstore after each layer is loaded and before merging.
//
// Intentionally minimal: defaults that depend on layer (like "tracking
// defaults to enforce") are NOT applied here, because applying them in
// the CLI layer would defeat the inherit-from-CLI semantic on the
// project layer. Layer-aware defaults live in the lookup helpers
// instead (see Tracking and Checks accessors).
func (c *EndlessConfig) Normalize(_ cfgstore.NormalizeArgs) error {
	return nil
}

// DefaultCheckEnabled returns the hardcoded default for a check name when
// neither the CLI nor project layer has set it. Conservative-by-default:
// new checks ship OFF until explicitly enabled, except task_required which
// preserves backwards-compatible PreToolUse session enforcement.
func DefaultCheckEnabled(name string) bool {
	switch name {
	case "task_required":
		return true
	case "drift_detection", "decision_checkpoint", "session_audit":
		return false
	default:
		return true
	}
}

// IsCheckEnabled returns whether the named check is enabled, honoring the
// merged Checks map and falling back to DefaultCheckEnabled when the key
// was not set in either layer.
func (c *EndlessConfig) IsCheckEnabled(name string) bool {
	if v, ok := c.Checks[name]; ok {
		return v
	}
	return DefaultCheckEnabled(name)
}
