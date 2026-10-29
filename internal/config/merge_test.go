package config

import (
	"testing"
)

func TestMerge_ChecksGlobalOnly(t *testing.T) {
	// An opt-in check set only in the CLI layer must propagate through merge
	// so the project sees it.
	project := &EndlessConfig{Name: "endless"}
	cli := &EndlessConfig{Checks: map[string]OptInText{"session_audit": "on"}}
	merged := project.Merge(cli).(*EndlessConfig)
	if got := merged.Checks["session_audit"]; got != "on" {
		t.Errorf("session_audit = %q, want on from the CLI layer", got)
	}
}

func TestMerge_ChecksProjectOverridesCLI(t *testing.T) {
	project := &EndlessConfig{Checks: map[string]OptInText{"session_audit": "off"}}
	cli := &EndlessConfig{Checks: map[string]OptInText{"session_audit": "on"}}
	merged := project.Merge(cli).(*EndlessConfig)
	if got := merged.Checks["session_audit"]; got != "off" {
		t.Errorf("session_audit = %q, want the project's off", got)
	}
}

// TestMerge_ChecksUnsetInherits: "unset" is the same as leaving the key out,
// so a project that writes it inherits the CLI value rather than masking it,
// and a key unset in both layers is absent, leaving the registered default.
func TestMerge_ChecksUnsetInherits(t *testing.T) {
	project := &EndlessConfig{Checks: map[string]OptInText{"session_audit": "unset", "task_required": "unset"}}
	cli := &EndlessConfig{Checks: map[string]OptInText{"session_audit": "on"}}
	merged := project.Merge(cli).(*EndlessConfig)
	if got := merged.Checks["session_audit"]; got != "on" {
		t.Errorf("session_audit = %q, want on inherited through the project's unset", got)
	}
	if _, ok := merged.Checks["task_required"]; ok {
		t.Errorf("task_required present after merge, want absent (unset in both layers)")
	}
}

func TestMerge_ChecksKeysFromBothLayers(t *testing.T) {
	project := &EndlessConfig{Checks: map[string]OptInText{"task_required": "off"}}
	cli := &EndlessConfig{Checks: map[string]OptInText{"session_audit": "on"}}
	merged := project.Merge(cli).(*EndlessConfig)
	if got := merged.Checks["task_required"]; got != "off" {
		t.Errorf("task_required = %q, want off from the project layer", got)
	}
	if got := merged.Checks["session_audit"]; got != "on" {
		t.Errorf("session_audit = %q, want on from the CLI layer", got)
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

// TestTmuxIsCLIOnly pins that the tmux object comes from the CLI layer alone
// (E-2240). Its socket fields decide which server every Endless command
// reaches, so a project-level value would let two projects reach two different
// servers of the same name.
func TestTmuxIsCLIOnly(t *testing.T) {
	proj := &EndlessConfig{Tmux: Tmux{SocketDir: "/tmp/project", TasksSession: "mine"}}
	glob := &EndlessConfig{Tmux: Tmux{SocketDir: "/tmp/user", Terminal: "wezterm start -- {cmd}"}}
	got := proj.Merge(glob).(*EndlessConfig).Tmux
	want := glob.Tmux
	if got != want {
		t.Errorf("merged tmux = %+v, want the CLI layer's %+v", got, want)
	}
}

// TestMerge_GitHooksLayered: each git_hooks field is the project's when it
// sets one, else the user's machine-file default (E-2490). "unset" inherits.
func TestMerge_GitHooksLayered(t *testing.T) {
	cli := &EndlessConfig{GitHooks: GitHooks{LefthookFile: "committed", Report: "off"}}

	merged := (&EndlessConfig{}).Merge(cli).(*EndlessConfig)
	if merged.GitHooks != cli.GitHooks {
		t.Errorf("GitHooks = %+v, want the CLI layer's %+v", merged.GitHooks, cli.GitHooks)
	}

	project := &EndlessConfig{GitHooks: GitHooks{LefthookFile: "local", Report: "unset"}}
	merged = project.Merge(cli).(*EndlessConfig)
	want := GitHooks{LefthookFile: "local", Report: "off"}
	if merged.GitHooks != want {
		t.Errorf("GitHooks = %+v, want %+v", merged.GitHooks, want)
	}
}
