package config

import (
	"testing"
)

func TestMerge_DriftDetectionGlobalOnly(t *testing.T) {
	// Reproduces the E-917 bug fix: drift_detection set only in CLI layer
	// must propagate through merge so the project sees it enabled.
	project := &EndlessConfig{
		Name: "endless",
	}
	cli := &EndlessConfig{
		Checks: map[string]bool{"drift_detection": true},
	}
	merged := project.Merge(cli).(*EndlessConfig)
	if !merged.IsCheckEnabled("drift_detection") {
		t.Errorf("expected drift_detection enabled from CLI layer, got disabled")
	}
}

func TestMerge_ChecksProjectOverridesCLI(t *testing.T) {
	project := &EndlessConfig{
		Name:   "endless",
		Checks: map[string]bool{"drift_detection": false},
	}
	cli := &EndlessConfig{
		Checks: map[string]bool{"drift_detection": true},
	}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.IsCheckEnabled("drift_detection") {
		t.Errorf("expected project value (false) to win, got enabled")
	}
}

func TestMerge_ChecksKeysFromBothLayers(t *testing.T) {
	project := &EndlessConfig{
		Checks: map[string]bool{"task_required": false},
	}
	cli := &EndlessConfig{
		Checks: map[string]bool{"drift_detection": true},
	}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.IsCheckEnabled("task_required") {
		t.Errorf("expected task_required disabled from project layer")
	}
	if !merged.IsCheckEnabled("drift_detection") {
		t.Errorf("expected drift_detection enabled from CLI layer")
	}
}

func TestMerge_TrackingProjectInheritsCLI(t *testing.T) {
	project := &EndlessConfig{
		Name: "endless",
	}
	cli := &EndlessConfig{
		Tracking: "track",
	}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.Tracking != "track" {
		t.Errorf("expected Tracking=track from CLI, got %q", merged.Tracking)
	}
}

func TestMerge_TrackingProjectOverrides(t *testing.T) {
	project := &EndlessConfig{
		Tracking: "off",
	}
	cli := &EndlessConfig{
		Tracking: "enforce",
	}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.Tracking != "off" {
		t.Errorf("expected Tracking=off from project, got %q", merged.Tracking)
	}
}

func TestMerge_GlobalOnlyFieldsFlowThrough(t *testing.T) {
	project := &EndlessConfig{
		Name: "endless",
	}
	cli := &EndlessConfig{
		Roots:        []string{"~/Projects"},
		ScanInterval: 300,
		NodeID:       "a7f3",
	}
	merged := project.Merge(cli).(*EndlessConfig)
	if len(merged.Roots) != 1 || merged.Roots[0] != "~/Projects" {
		t.Errorf("Roots not inherited from CLI, got %v", merged.Roots)
	}
	if merged.ScanInterval != 300 {
		t.Errorf("ScanInterval not inherited, got %d", merged.ScanInterval)
	}
	if merged.NodeID != "a7f3" {
		t.Errorf("NodeID not inherited, got %q", merged.NodeID)
	}
}

func TestMerge_ProjectOnlyFieldsPreserved(t *testing.T) {
	project := &EndlessConfig{
		Name:        "endless",
		Label:       "Endless Project Tracker",
		Description: "Project awareness system",
	}
	cli := &EndlessConfig{
		NodeID: "a7f3",
	}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.Name != "endless" {
		t.Errorf("Name lost, got %q", merged.Name)
	}
	if merged.Label != "Endless Project Tracker" {
		t.Errorf("Label lost, got %q", merged.Label)
	}
	if merged.Description != "Project awareness system" {
		t.Errorf("Description lost, got %q", merged.Description)
	}
}

func TestIsCheckEnabled_FallsBackToDefault(t *testing.T) {
	cfg := &EndlessConfig{}
	if !cfg.IsCheckEnabled("task_required") {
		t.Errorf("task_required default should be true")
	}
	if cfg.IsCheckEnabled("drift_detection") {
		t.Errorf("drift_detection default should be false")
	}
	if !cfg.IsCheckEnabled("totally_unknown_check") {
		t.Errorf("unknown checks default to true (current policy)")
	}
}

func TestDefaultCheckEnabled(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"task_required", true},
		{"drift_detection", false},
		{"decision_checkpoint", false},
		{"session_audit", false},
		{"some_future_check", true},
	}
	for _, tt := range tests {
		got := DefaultCheckEnabled(tt.name)
		if got != tt.want {
			t.Errorf("DefaultCheckEnabled(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
