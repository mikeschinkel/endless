package config

import "testing"

// TestMerge_MainSyncEnabledIsNeverInherited: the job publishes main to its
// remote, so a user-level `enabled: true` must not opt any project in (E-2233).
func TestMerge_MainSyncEnabledIsNeverInherited(t *testing.T) {
	project := &EndlessConfig{Name: "p"}
	cli := &EndlessConfig{MainSync: MainSync{Enabled: true, Interval: "2m"}}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.MainSync.Enabled {
		t.Errorf("main_sync.enabled inherited from the CLI layer")
	}
	if merged.MainSync.Interval != "2m" {
		t.Errorf("main_sync.interval = %q, want 2m inherited from the CLI layer", merged.MainSync.Interval)
	}

	project = &EndlessConfig{MainSync: MainSync{Enabled: true}}
	merged = project.Merge(&EndlessConfig{}).(*EndlessConfig)
	if !merged.MainSync.Enabled {
		t.Errorf("project's own main_sync.enabled lost in merge")
	}
}
