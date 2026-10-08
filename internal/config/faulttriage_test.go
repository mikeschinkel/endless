package config

import "testing"

// TestMerge_FaultTriageEnabledIsNeverInherited: the job starts sessions in the
// project, so a user-level `enabled: true` must not opt any project in (E-2272).
func TestMerge_FaultTriageEnabledIsNeverInherited(t *testing.T) {
	project := &EndlessConfig{Name: "p"}
	cli := &EndlessConfig{FaultTriage: FaultTriage{Enabled: true, Interval: "30s"}}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.FaultTriage.Enabled {
		t.Errorf("fault_triage.enabled inherited from the CLI layer")
	}
	if merged.FaultTriage.Interval != "30s" {
		t.Errorf("fault_triage.interval = %q, want 30s inherited from the CLI layer", merged.FaultTriage.Interval)
	}

	project = &EndlessConfig{FaultTriage: FaultTriage{Enabled: true}}
	merged = project.Merge(&EndlessConfig{}).(*EndlessConfig)
	if !merged.FaultTriage.Enabled {
		t.Errorf("project's own fault_triage.enabled lost in merge")
	}
}
