package config

import (
	"github.com/mikeschinkel/go-cfgstore"

	"github.com/mikeschinkel/endless/internal/settings"
)

// Merge produces a new EndlessConfig by merging the CLI layer (passed as
// `other`) into the project layer (the receiver). Project takes precedence
// for layered fields except where a per-key custom rule overrides.
//
// go-cfgstore calls Merge as `project.Merge(cli)` per its mergeRootConfigs
// loop in load_config.go, so:
//
//   - receiver (c)        = project layer
//   - other (parameter)   = CLI / global layer
//
// Field categories (see EndlessConfig doc):
//
//   - Global-only fields: receiver is empty by definition; copy from other.
//   - Project-only fields: keep receiver value; other is empty by definition.
//   - Layered fields:     explicit per-field rule below.
func (c *EndlessConfig) Merge(other cfgstore.RootConfig) cfgstore.RootConfig {
	o, ok := other.(*EndlessConfig)
	if !ok || o == nil {
		// Shouldn't happen in practice; cfgstore guarantees same type.
		// Return a copy of receiver to avoid mutating caller state.
		out := *c
		return &out
	}
	out := *c

	// Global-only fields: inherit from CLI when project did not set them.
	// (Project SHOULD never set these, but if it does, project wins by
	// keeping the receiver value — matches the general "receiver wins
	// on non-empty" pattern.)
	if len(out.Roots) == 0 {
		out.Roots = o.Roots
	}
	if out.ScanInterval == 0 {
		out.ScanInterval = o.ScanInterval
	}
	if len(out.Ignore) == 0 {
		out.Ignore = o.Ignore
	}
	if len(out.Ownership) == 0 {
		out.Ownership = o.Ownership
	}
	if out.NodeID == "" {
		out.NodeID = o.NodeID
	}

	// Project-only fields: keep receiver values. CLI is expected empty
	// for these; receiver-wins-on-non-empty also handles the defensive
	// case where CLI mistakenly sets one.
	if out.Name == "" {
		out.Name = o.Name
	}
	if out.Label == "" {
		out.Label = o.Label
	}
	if out.Description == "" {
		out.Description = o.Description
	}
	if out.Language == "" {
		out.Language = o.Language
	}
	if out.Status == "" {
		out.Status = o.Status
	}
	if len(out.Dependencies) == 0 {
		out.Dependencies = o.Dependencies
	}
	if len(out.Documents.Rules) == 0 {
		out.Documents.Rules = o.Documents.Rules
	}
	if len(out.Migrations.Dirs) == 0 {
		out.Migrations.Dirs = o.Migrations.Dirs
	}

	// Layered field: Tracking. Project value wins if explicitly set;
	// empty string means inherit from CLI. Final empty string after
	// merge means "no layer set it"; callers map that to a registered/
	// anonymous-aware default at lookup time.
	if out.Tracking == "" {
		out.Tracking = o.Tracking
	}

	// Layered field: Checks. Per-key merge with optional per-key custom
	// rules. See mergeChecks.
	out.Checks = mergeChecks(c.Checks, o.Checks)

	// Tmux is CLI-only (E-2240): every field is read from the CLI layer alone,
	// so nothing a project sets here may reach the merged config.
	out.Tmux = o.Tmux

	// Layered field: ProjectStatus.Colors, per list and per attribute, so a
	// project recoloring one list keeps the others it inherits.
	out.ProjectStatus.Colors.Urgent = mergeListColor(c.ProjectStatus.Colors.Urgent, o.ProjectStatus.Colors.Urgent)
	out.ProjectStatus.Colors.Epics = mergeListColor(c.ProjectStatus.Colors.Epics, o.ProjectStatus.Colors.Epics)
	out.ProjectStatus.Colors.Other = mergeListColor(c.ProjectStatus.Colors.Other, o.ProjectStatus.Colors.Other)

	// AutoSpawn: Enabled and Cap are project-only and deliberately NOT
	// inherited — see AutoSpawn. Interval, Target and Placement are CLI-only:
	// inherit.
	out.AutoSpawn.Enabled = c.AutoSpawn.Enabled
	out.AutoSpawn.Cap = c.AutoSpawn.Cap
	if out.AutoSpawn.Interval == "" {
		out.AutoSpawn.Interval = o.AutoSpawn.Interval
	}
	if out.AutoSpawn.Target == "" {
		out.AutoSpawn.Target = o.AutoSpawn.Target
	}
	if out.AutoSpawn.Placement == "" {
		out.AutoSpawn.Placement = o.AutoSpawn.Placement
	}

	// Prime: project-only, never inherited — see Prime.
	out.Prime = c.Prime

	// MainSync: Enabled is project-only and deliberately NOT inherited;
	// Interval is CLI-only. See MainSync.
	out.MainSync.Enabled = c.MainSync.Enabled
	if out.MainSync.Interval == "" {
		out.MainSync.Interval = o.MainSync.Interval
	}

	// FaultTriage: Enabled is project-only and deliberately NOT inherited;
	// Interval is CLI-only. See FaultTriage.
	out.FaultTriage.Enabled = c.FaultTriage.Enabled
	if out.FaultTriage.Interval == "" {
		out.FaultTriage.Interval = o.FaultTriage.Interval
	}

	// GitHooks: layered, field by field — the project wins when it sets one.
	if out.GitHooks.LefthookFile == "" {
		out.GitHooks.LefthookFile = o.GitHooks.LefthookFile
	}
	if out.GitHooks.Report == "" || out.GitHooks.Report == settings.UnsetText {
		out.GitHooks.Report = o.GitHooks.Report
	}

	return &out
}

// CheckMergeFunc resolves a single check key given its CLI-layer value and
// project-layer value. Either pointer may be nil to indicate the layer did
// not set the key — absent, or written as "unset". Returning a value adds the
// key to the merged map; the merge function never calls a CheckMergeFunc when
// both values are nil, so implementations can assume at least one of
// `globalVal` or `projectVal` is non-nil.
type CheckMergeFunc func(globalVal, projectVal *OptInText, name string) OptInText

// checkMergeRules registers per-key custom merge rules for entries in the
// Checks map. Keys not present here use defaultCheckMerge.
//
// Add an entry here when a check needs non-standard merge semantics. The
// extension point exists because Mike noted on E-949 that each key can
// potentially have its own merge requirements; default behavior covers
// every case we have today.
var checkMergeRules = map[string]CheckMergeFunc{
	// example shape:
	//   "some_check": func(globalVal, projectVal *OptInText, name string) OptInText { ... },
}

// defaultCheckMerge: project value wins when set; otherwise inherit CLI.
// Readers (monitor.Checks) supply the registered default when neither layer
// set the key, so this function is only invoked for keys that ARE set in at
// least one layer.
func defaultCheckMerge(globalVal, projectVal *OptInText, _ string) OptInText {
	if projectVal != nil {
		return *projectVal
	}
	if globalVal != nil {
		return *globalVal
	}
	// Unreachable in practice; mergeChecks skips keys absent from both layers.
	return ""
}

// isSet reports whether an opt-in text records a decision: "unset" is the
// same as leaving the key out, so it inherits rather than overriding.
func isSet(t OptInText) bool {
	return t != "" && t != OptInText(settings.UnsetText)
}

// mergeChecks combines two Checks maps using per-key dispatch into
// checkMergeRules (with defaultCheckMerge as fallback). Only keys set in at
// least one layer end up in the result; keys set in neither fall through to
// the registered default at query time.
func mergeChecks(project, global map[string]OptInText) map[string]OptInText {
	if len(project) == 0 && len(global) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(project)+len(global))
	for k := range project {
		keys[k] = struct{}{}
	}
	for k := range global {
		keys[k] = struct{}{}
	}
	out := make(map[string]OptInText, len(keys))
	for name := range keys {
		var pv, gv *OptInText
		if v, ok := project[name]; ok && isSet(v) {
			pv = &v
		}
		if v, ok := global[name]; ok && isSet(v) {
			gv = &v
		}
		if pv == nil && gv == nil {
			continue
		}
		rule, ok := checkMergeRules[name]
		if !ok {
			rule = defaultCheckMerge
		}
		out[name] = rule(gv, pv, name)
	}
	return out
}

// mergeListColor takes each attribute from the receiver when it set one, else
// from the other layer.
func mergeListColor(c, o ListColor) ListColor {
	if c.BG == nil {
		c.BG = o.BG
	}
	if c.FG == nil {
		c.FG = o.FG
	}
	return c
}
