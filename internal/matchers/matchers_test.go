package matchers

import (
	"testing"
)

// boolPtr returns a pointer to b for populating Matcher.Enabled in tests.
func boolPtr(b bool) *bool {
	return &b
}

// TestActionRegex_FilterAndCompile pins the lookup: returns the compiled
// regex of the first enabled (type,scope) regex matcher; nil when no entry
// matches the (type,scope) pair, when the matching entry is disabled, when
// the method is not "regex", or when the pattern fails to compile.
func TestActionRegex_FilterAndCompile(t *testing.T) {
	tests := []struct {
		name     string
		matchers []Matcher
		action   string
		scope    string
		wantNil  bool
		wantHit  string // a string the returned regex must match (when not nil)
	}{
		{
			name: "happy_path_compiles_and_matches",
			matchers: []Matcher{
				{Type: "session_end", Scope: "land", Method: "regex", Match: "^landing E-\\d+$"},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: false,
			wantHit: "landing E-1506",
		},
		{
			name: "type_mismatch_returns_nil",
			matchers: []Matcher{
				{Type: "other", Scope: "land", Method: "regex", Match: "^x$"},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: true,
		},
		{
			name: "scope_mismatch_returns_nil",
			matchers: []Matcher{
				{Type: "session_end", Scope: "drop", Method: "regex", Match: "^x$"},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: true,
		},
		{
			name: "disabled_entry_skipped",
			matchers: []Matcher{
				{Type: "session_end", Scope: "land", Method: "regex", Match: "^x$", Enabled: boolPtr(false)},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: true,
		},
		{
			name: "non_regex_method_skipped",
			matchers: []Matcher{
				{Type: "session_end", Scope: "land", Method: "substring", Match: []interface{}{"land"}},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: true,
		},
		{
			name: "empty_pattern_skipped",
			matchers: []Matcher{
				{Type: "session_end", Scope: "land", Method: "regex", Match: ""},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: true,
		},
		{
			name: "invalid_regex_returns_nil",
			matchers: []Matcher{
				{Type: "session_end", Scope: "land", Method: "regex", Match: "[unterminated"},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: true,
		},
		{
			name: "first_match_wins_when_multiple_eligible",
			matchers: []Matcher{
				{Type: "session_end", Scope: "land", Method: "regex", Match: "^first$"},
				{Type: "session_end", Scope: "land", Method: "regex", Match: "^second$"},
			},
			action:  "session_end",
			scope:   "land",
			wantNil: false,
			wantHit: "first",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ActionRegex(tc.matchers, tc.action, tc.scope)
			if tc.wantNil {
				if got != nil {
					t.Errorf("ActionRegex = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ActionRegex returned nil, want compiled regex")
			}
			if !got.MatchString(tc.wantHit) {
				t.Errorf("returned regex %q does not match %q", got.String(), tc.wantHit)
			}
		})
	}
}
