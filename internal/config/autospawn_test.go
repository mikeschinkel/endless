package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/go-dt"
)

// TestMerge_AutoSpawnEnabledIsNeverInherited is the per-project opt-in (E-1814):
// a user-level `enabled: true` must not opt a project in, and a user-level cap
// must not become a project's.
func TestMerge_AutoSpawnEnabledIsNeverInherited(t *testing.T) {
	project := &EndlessConfig{Name: "p"}
	cli := &EndlessConfig{AutoSpawn: AutoSpawn{Enabled: true, Cap: 9}}
	merged := project.Merge(cli).(*EndlessConfig)
	if merged.AutoSpawn.Enabled {
		t.Errorf("auto_spawn.enabled inherited from the CLI layer")
	}
	if merged.AutoSpawn.Cap != 0 {
		t.Errorf("auto_spawn.cap = %d inherited from the CLI layer, want 0", merged.AutoSpawn.Cap)
	}
}

// TestMerge_AutoSpawnCadenceInherits: interval and target are the user's, so a
// project that sets neither sees the user's values.
func TestMerge_AutoSpawnCadenceInherits(t *testing.T) {
	project := &EndlessConfig{AutoSpawn: AutoSpawn{Enabled: true}}
	cli := &EndlessConfig{AutoSpawn: AutoSpawn{Interval: "10m", Target: "monitor"}}
	merged := project.Merge(cli).(*EndlessConfig)
	if !merged.AutoSpawn.Enabled {
		t.Errorf("project's own enabled lost in merge")
	}
	if merged.AutoSpawn.Interval != "10m" || merged.AutoSpawn.Target != "monitor" {
		t.Errorf("cadence = %q/%q, want 10m/monitor", merged.AutoSpawn.Interval, merged.AutoSpawn.Target)
	}
}

// TestLoadProject reads the project file alone, and an absent file is a zero
// config rather than an error — "off" for every project-only setting — even
// when the user's own config says otherwise.
func TestLoadProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cliDir := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "config.json"),
		[]byte(`{"auto_spawn":{"enabled":true,"cap":9}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := t.TempDir()
	cfg, err := LoadProject(dt.DirPath(proj))
	if err != nil {
		t.Fatalf("LoadProject with no project file: %v", err)
	}
	if cfg.AutoSpawn.Enabled || cfg.AutoSpawn.Cap != 0 {
		t.Errorf("no project file read as %+v, want the zero value", cfg.AutoSpawn)
	}

	if err = os.MkdirAll(filepath.Join(proj, ".endless"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(proj, ".endless", "config.json"),
		[]byte(`{"name":"p","auto_spawn":{"enabled":true,"cap":2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadProject(dt.DirPath(proj))
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if !cfg.AutoSpawn.Enabled || cfg.AutoSpawn.Cap != 2 {
		t.Errorf("project file read as %+v, want enabled with cap 2", cfg.AutoSpawn)
	}
}
