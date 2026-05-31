package matchers

import (
	"testing"
)

// boolPtr returns a pointer to b for populating Matcher.Enabled in tests.
func boolPtr(b bool) *bool {
	return &b
}

// TestGetPivotMatchers_Filters pins that GetPivotMatchers returns only
// matchers whose Type=="pivot" AND IsEnabled() is true. Non-pivot types are
// dropped regardless of enabled state, and an explicit Enabled=false on a
// pivot is also dropped (per Matcher.IsEnabled semantics where nil=on).
func TestGetPivotMatchers_Filters(t *testing.T) {
	tests := []struct {
		name string
		in   []Matcher
		want int
	}{
		{
			name: "empty_input",
			in:   []Matcher{},
			want: 0,
		},
		{
			name: "only_non_pivots",
			in: []Matcher{
				{Type: "verb", Method: "exact"},
				{Type: "action", Method: "regex"},
			},
			want: 0,
		},
		{
			name: "enabled_pivots_kept",
			in: []Matcher{
				{Type: "pivot", Method: "substring"},               // nil Enabled = on
				{Type: "pivot", Method: "substring", Enabled: boolPtr(true)},
			},
			want: 2,
		},
		{
			name: "disabled_pivots_dropped",
			in: []Matcher{
				{Type: "pivot", Method: "substring", Enabled: boolPtr(false)},
				{Type: "pivot", Method: "substring", Enabled: boolPtr(true)},
			},
			want: 1,
		},
		{
			name: "mixed_types_filtered_to_pivots_only",
			in: []Matcher{
				{Type: "pivot", Method: "substring"},
				{Type: "verb", Method: "exact"},
				{Type: "pivot", Method: "exact", Enabled: boolPtr(false)},
				{Type: "action", Method: "regex"},
				{Type: "pivot", Method: "substring", Enabled: boolPtr(true)},
			},
			want: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetPivotMatchers(tc.in)
			if len(got) != tc.want {
				t.Errorf("GetPivotMatchers returned %d matchers, want %d", len(got), tc.want)
			}
			for _, m := range got {
				if m.Type != "pivot" {
					t.Errorf("non-pivot in result: type=%q", m.Type)
				}
				if !m.IsEnabled() {
					t.Errorf("disabled matcher in result")
				}
			}
		})
	}
}

// TestFindPivotMatch_Semantics pins the v1 contract: empty text yields "",
// case-insensitive substring matching by default, opt-in case-sensitivity
// per matcher, regex pivots are ignored, disabled pivots are skipped, and
// the first matching phrase encountered is returned.
func TestFindPivotMatch_Semantics(t *testing.T) {
	tests := []struct {
		name     string
		matchers []Matcher
		text     string
		want     string
	}{
		{
			name:     "empty_text_returns_empty",
			matchers: []Matcher{{Type: "pivot", Method: "substring", Match: []interface{}{"foo"}}},
			text:     "",
			want:     "",
		},
		{
			name: "case_insensitive_default_substring",
			matchers: []Matcher{
				{Type: "pivot", Method: "substring", Match: []interface{}{"REFACTOR"}},
			},
			text: "let us refactor this",
			want: "REFACTOR",
		},
		{
			name: "case_sensitive_no_match_when_case_differs",
			matchers: []Matcher{
				{Type: "pivot", Method: "substring", Match: []interface{}{"REFACTOR"}, CaseSensitive: true},
			},
			text: "let us refactor this",
			want: "",
		},
		{
			name: "case_sensitive_match_when_case_aligns",
			matchers: []Matcher{
				{Type: "pivot", Method: "substring", Match: []interface{}{"REFACTOR"}, CaseSensitive: true},
			},
			text: "do a REFACTOR now",
			want: "REFACTOR",
		},
		{
			name: "no_match_returns_empty",
			matchers: []Matcher{
				{Type: "pivot", Method: "substring", Match: []interface{}{"nope"}},
			},
			text: "different content entirely",
			want: "",
		},
		{
			name: "regex_method_pivots_ignored",
			matchers: []Matcher{
				{Type: "pivot", Method: "regex", Match: "ref.*tor"},
			},
			text: "refactor this",
			want: "",
		},
		{
			name: "disabled_pivot_skipped",
			matchers: []Matcher{
				{Type: "pivot", Method: "substring", Match: []interface{}{"first"}, Enabled: boolPtr(false)},
				{Type: "pivot", Method: "substring", Match: []interface{}{"second"}},
			},
			text: "first or second",
			want: "second",
		},
		{
			name: "non_pivot_types_ignored",
			matchers: []Matcher{
				{Type: "verb", Method: "substring", Match: []interface{}{"land"}},
			},
			text: "please land this",
			want: "",
		},
		{
			name: "empty_phrase_in_list_skipped",
			matchers: []Matcher{
				{Type: "pivot", Method: "substring", Match: []interface{}{"", "good"}},
			},
			text: "looks good to me",
			want: "good",
		},
		{
			name: "exact_method_treated_as_substring_in_v1",
			matchers: []Matcher{
				{Type: "pivot", Method: "exact", Match: []interface{}{"abort"}},
			},
			text: "going to abort now",
			want: "abort",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FindPivotMatch(tc.matchers, tc.text)
			if got != tc.want {
				t.Errorf("FindPivotMatch(text=%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
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
