package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".endless")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestMinimizerEnabled_DefaultsOn pins the product decision (E-1953 answer 6):
// the gate ships enabled, and only an explicit `false` turns it off.
//
// The absent-key case is the one that matters. With a plain `bool` field,
// "absent" and "false" would collapse into the same zero value and every project
// on earth would ship ungated while appearing to be configured — which is why
// the fields are pointers.
func TestMinimizerEnabled_DefaultsOn(t *testing.T) {
	cases := []struct {
		name string
		body string // "" = no config file at all
		want bool
	}{
		{"no config file", "", true},
		{"config without the key", `{"name":"proj"}`, true},
		{"explicitly enabled", `{"minimizer": {"enabled": true}}`, true},
		{"explicitly disabled", `{"minimizer": {"enabled": false}}`, false},
		{"object naming only the optimizer", `{"minimizer": {"optimizer": false}}`, true},
		{"bare false is shorthand for both off", `{"minimizer": false}`, false},
		// Malformed must not silently disable the gate: a typo in an unrelated
		// field would otherwise switch off enforcement everywhere with no signal.
		{"malformed json", `{"minimizer": fal`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.body != "" {
				writeConfig(t, root, c.body)
			}
			if got := MinimizerEnabled(root); got != c.want {
				t.Errorf("MinimizerEnabled = %v, want %v", got, c.want)
			}
		})
	}
}

// TestMinimizerEnabled_LegacyReportGate pins the migration half of E-1975's
// rename. A config change that turns enforcement back ON by doing nothing is the
// one migration failure that costs the user something invisible, so the old
// scalar keeps being read until a project restates its answer under the new key.
func TestMinimizerEnabled_LegacyReportGate(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		wantEnabled   bool
		wantOptimizer bool
	}{
		{"old key off still means off", `{"report_gate": false}`, false, true},
		{"old key on still means on", `{"report_gate": true}`, true, true},
		// Both present: the new key wins outright. A project mid-migration must
		// not have its new answer overruled by the line it forgot to delete.
		{"new key beats old", `{"report_gate": false, "minimizer": {"enabled": true}}`, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, c.body)
			cfg, declared := readMinimizer(root)
			if !declared {
				t.Fatal("an explicit key was not reported as declared")
			}
			if cfg.Enabled != c.wantEnabled {
				t.Errorf("Enabled = %v, want %v", cfg.Enabled, c.wantEnabled)
			}
			if cfg.Optimizer != c.wantOptimizer {
				t.Errorf("Optimizer = %v, want %v", cfg.Optimizer, c.wantOptimizer)
			}
		})
	}
}

// TestMinimizerOptimizer_IndependentOfEnabled pins the reason there are two
// switches: a project may want the gate without the research bill. Folding them
// into one boolean would make that unexpressible.
func TestMinimizerOptimizer_IndependentOfEnabled(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"minimizer": {"enabled": true, "optimizer": false}}`)

	if !MinimizerEnabled(root) {
		t.Error("gate should be live")
	}
	if MinimizerOptimizerEnabled(root) {
		t.Error("optimizer should be frozen")
	}
}

// TestMinimizerConfigForCwd_NearestWins pins worktree resolution. A task branch
// carries its own `.endless/config.json`, so a branch changing the gate must be
// able to exempt its own sessions BEFORE it lands — reading only the project
// root would make the opt-out take effect one landing too late.
func TestMinimizerConfigForCwd_NearestWins(t *testing.T) {
	projectRoot := t.TempDir()
	writeConfig(t, projectRoot, `{"minimizer": {"enabled": true}}`)

	worktree := filepath.Join(projectRoot, ".endless", "worktrees", "e-1953")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeConfig(t, worktree, `{"minimizer": {"enabled": false}}`)

	if got := MinimizerEnabledForCwd(worktree, projectRoot); got {
		t.Error("worktree opt-out was ignored; the project root won instead")
	}
	// A subdirectory of the worktree resolves the same way — sessions do not sit
	// at the worktree root.
	deep := filepath.Join(worktree, "internal", "monitor")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	if got := MinimizerEnabledForCwd(deep, projectRoot); got {
		t.Error("opt-out not found from a subdirectory of the worktree")
	}
}

// TestMinimizerConfigForCwd_SilenceInherits pins the other half of nearest-wins:
// only an EXPLICIT key counts.
//
// A worktree whose config says nothing must inherit the project's answer, not
// reset it to the default. Otherwise deleting the key from a branch would
// silently re-enable a gate the project had deliberately switched off.
func TestMinimizerConfigForCwd_SilenceInherits(t *testing.T) {
	projectRoot := t.TempDir()
	writeConfig(t, projectRoot, `{"minimizer": {"enabled": false}}`)

	worktree := filepath.Join(projectRoot, ".endless", "worktrees", "e-1")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeConfig(t, worktree, `{"name":"proj"}`) // says nothing about the minimizer

	if got := MinimizerEnabledForCwd(worktree, projectRoot); got {
		t.Error("a silent worktree config reset the gate instead of inheriting")
	}
}

// A cwd outside any configured tree falls back to the project root.
func TestMinimizerConfigForCwd_FallsBackToProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	writeConfig(t, projectRoot, `{"minimizer": {"enabled": false}}`)

	if got := MinimizerEnabledForCwd(t.TempDir(), projectRoot); got {
		t.Error("unrelated cwd did not fall back to the project root's setting")
	}
}
