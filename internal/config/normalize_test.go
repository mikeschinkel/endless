package config

import (
	"testing"
)

// TestDefaultCheckEnabled pins the hardcoded per-check default policy:
// task_required is ON (preserves PreToolUse session enforcement), the three
// named opt-in checks are OFF, and any unknown check name defaults to ON per
// the documented fall-through in DefaultCheckEnabled.
func TestDefaultCheckEnabled(t *testing.T) {
	tests := []struct {
		name  string
		check string
		want  bool
	}{
		{name: "task_required_on", check: "task_required", want: true},
		{name: "drift_detection_off", check: "drift_detection", want: false},
		{name: "decision_checkpoint_off", check: "decision_checkpoint", want: false},
		{name: "session_audit_off", check: "session_audit", want: false},
		{name: "unknown_name_defaults_on", check: "nonexistent_check_xyz", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultCheckEnabled(tc.check)
			if got != tc.want {
				t.Errorf("DefaultCheckEnabled(%q) = %v, want %v", tc.check, got, tc.want)
			}
		})
	}
}
