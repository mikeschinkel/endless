package tmuxcmd

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestFormat_OmitsEmptyFields pins the compact-row contract: format
// builds "[E-N] · A · B · …" but drops any segment whose source field
// is empty, so a barely-populated task doesn't render gratuitous bullet
// separators.
func TestFormat_OmitsEmptyFields(t *testing.T) {
	tier := int64(3)
	tests := []struct {
		name           string
		info           *monitor.ActiveTaskInfo
		wantParts      []string
		notWant        []string
		wantSeparators int // -1 to skip the check
	}{
		{
			name: "all fields present",
			info: &monitor.ActiveTaskInfo{
				TaskID: 42, ProjectName: "proj", Type: "task",
				Phase: "now", Tier: &tier, Status: "in_progress",
			},
			wantParts:      []string{"[E-42]", "proj", "task", "now", "t3", "in_progress"},
			wantSeparators: 5, // all five non-ID fields contribute
		},
		{
			name: "tier nil drops the t-segment",
			info: &monitor.ActiveTaskInfo{
				TaskID: 7, ProjectName: "proj", Type: "task",
				Phase: "now", Tier: nil, Status: "ready",
			},
			wantParts:    []string{"[E-7]", "proj", "task", "now", "ready"},
			wantSeparators: 4, // proj, task, now, ready → 4 " · " separators (no tier)
		},
		{
			name: "blank project + type + phase omitted",
			info:           &monitor.ActiveTaskInfo{TaskID: 9, Status: "ready"},
			wantParts:      []string{"[E-9]", "ready"},
			wantSeparators: 1, // only status
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := format(tc.info)
			for _, want := range tc.wantParts {
				if !strings.Contains(got, want) {
					t.Errorf("format() = %q, missing %q", got, want)
				}
			}
			if tc.wantSeparators >= 0 && strings.Count(got, " · ") != tc.wantSeparators {
				t.Errorf("format() = %q, separator count = %d, want %d",
					got, strings.Count(got, " · "), tc.wantSeparators)
			}
			for _, bad := range tc.notWant {
				if strings.Contains(got, bad) {
					t.Errorf("format() = %q, should not contain %q", got, bad)
				}
			}
		})
	}
}

// TestTierString_HandlesNilAndZero pins the documented rule: nil or 0
// tier prints "" so callers can drop the segment entirely; positive
// values render as "tN".
func TestTierString_HandlesNilAndZero(t *testing.T) {
	zero := int64(0)
	five := int64(5)
	tests := []struct {
		name string
		in   *int64
		want string
	}{
		{"nil", nil, ""},
		{"zero", &zero, ""},
		{"positive", &five, "t5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tierString(tc.in); got != tc.want {
				t.Errorf("tierString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestHintAndPlaceholder_NonEmpty pins that each hint helper returns a
// non-empty string. The exact wording is product-content and will drift;
// these tests guard against accidentally returning "" (which would
// reflow tmux's status row).
func TestHintAndPlaceholder_NonEmpty(t *testing.T) {
	if hintNoTask() == "" {
		t.Error("hintNoTask returned empty")
	}
	if hintNoSession() == "" {
		t.Error("hintNoSession returned empty")
	}
	if placeholder() == "" {
		t.Error("placeholder returned empty")
	}
}
