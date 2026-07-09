package hookcmd

import "testing"

// TestPlanFileRe pins the plan-file gate's path matcher (E-1202): a Write/Edit of
// .endless/plans/E-NNN.md is refused, but a subdir, a non-plan file, or a normal
// source path is allowed. Mirrors TestSqliteEndlessRe.
func TestPlanFileRe(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		match bool
	}{
		// Should block — canonical mirror path, absolute or repo-relative.
		{"repo-relative", ".endless/plans/E-1.md", true},
		{"dot-slash prefix", "./.endless/plans/E-1202.md", true},
		{"absolute", "/Users/x/proj/.endless/plans/E-999.md", true},
		{"absolute in worktree", "/abs/wt/e-1202/.endless/plans/E-1202.md", true},
		{"multi-digit", ".endless/plans/E-12345.md", true},

		// Should NOT block.
		{"subdir excluded", ".endless/plans/snapshots/E-1.md", false},
		{"non-plan file in dir", ".endless/plans/notes.md", false},
		{"wrong extension", ".endless/plans/E-1.txt", false},
		{"no digits", ".endless/plans/E-.md", false},
		{"lowercase prefix", ".endless/plans/e-1.md", false},
		{"normal source file", "src/foo.md", false},
		{"not a path component", "my.endless/plans/E-1.md", false},
		{"trailing suffix after md", ".endless/plans/E-1.md.bak", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planFileRe.MatchString(tc.path)
			if got != tc.match {
				t.Errorf("MatchString(%q) = %v, want %v", tc.path, got, tc.match)
			}
		})
	}
}
