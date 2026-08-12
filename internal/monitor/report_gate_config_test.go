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

// TestReportGateEnabled_DefaultsOn pins the product decision (E-1953 answer 6):
// the gate ships enabled, and only an explicit `false` turns it off.
//
// The absent-key case is the one that matters. With a plain `bool` field,
// "absent" and "false" would collapse into the same zero value and every project
// on earth would ship ungated while appearing to be configured — which is why
// the field is a pointer.
func TestReportGateEnabled_DefaultsOn(t *testing.T) {
	cases := []struct {
		name string
		body string // "" = no config file at all
		want bool
	}{
		{"no config file", "", true},
		{"config without the key", `{"name":"proj"}`, true},
		{"explicitly enabled", `{"report_gate": true}`, true},
		{"explicitly disabled", `{"report_gate": false}`, false},
		// Malformed must not silently disable the gate: a typo in an unrelated
		// field would otherwise switch off enforcement everywhere with no signal.
		{"malformed json", `{"report_gate": fal`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.body != "" {
				writeConfig(t, root, c.body)
			}
			if got := ReportGateEnabled(root); got != c.want {
				t.Errorf("ReportGateEnabled = %v, want %v", got, c.want)
			}
		})
	}
}

// TestReportGateEnabledForCwd_NearestWins pins worktree resolution. A task
// branch carries its own `.endless/config.json`, so a branch changing the gate
// must be able to exempt its own sessions BEFORE it lands — reading only the
// project root would make the opt-out take effect one landing too late.
func TestReportGateEnabledForCwd_NearestWins(t *testing.T) {
	projectRoot := t.TempDir()
	writeConfig(t, projectRoot, `{"report_gate": true}`)

	worktree := filepath.Join(projectRoot, ".endless", "worktrees", "e-1953")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeConfig(t, worktree, `{"report_gate": false}`)

	if got := ReportGateEnabledForCwd(worktree, projectRoot); got {
		t.Error("worktree opt-out was ignored; the project root won instead")
	}
	// A subdirectory of the worktree resolves the same way — sessions do not sit
	// at the worktree root.
	deep := filepath.Join(worktree, "internal", "monitor")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	if got := ReportGateEnabledForCwd(deep, projectRoot); got {
		t.Error("opt-out not found from a subdirectory of the worktree")
	}
}

// TestReportGateEnabledForCwd_SilenceInherits pins the other half of
// nearest-wins: only an EXPLICIT key counts.
//
// A worktree whose config says nothing must inherit the project's answer, not
// reset it to the default. Otherwise deleting the key from a branch would
// silently re-enable a gate the project had deliberately switched off.
func TestReportGateEnabledForCwd_SilenceInherits(t *testing.T) {
	projectRoot := t.TempDir()
	writeConfig(t, projectRoot, `{"report_gate": false}`)

	worktree := filepath.Join(projectRoot, ".endless", "worktrees", "e-1")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeConfig(t, worktree, `{"name":"proj"}`) // no report_gate key

	if got := ReportGateEnabledForCwd(worktree, projectRoot); got {
		t.Error("a silent worktree config reset the gate instead of inheriting")
	}
}

// A cwd outside any configured tree falls back to the project root.
func TestReportGateEnabledForCwd_FallsBackToProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	writeConfig(t, projectRoot, `{"report_gate": false}`)

	if got := ReportGateEnabledForCwd(t.TempDir(), projectRoot); got {
		t.Error("unrelated cwd did not fall back to the project root's setting")
	}
}
