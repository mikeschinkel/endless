package hookcmd

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/docmirror"
)

// TestDocMirrorGatePaths pins the document-mirror gate's path matcher
// (E-1202, widened by E-2137): a Write/Edit of a database-owned mirror is
// refused, and everything else — including the task's OWN verification suite in
// the very same directory — is allowed. Mirrors TestSqliteEndlessRe.
//
// The second half is the one that would be expensive to get wrong. E-1202's
// gate lived on `.endless/plans/`, a directory nothing else used, so an
// over-broad pattern cost nothing. The mirrors now share `.endless/tasks/e-N/`
// with `verify.sh` and `verify.toml`, which sessions write constantly; a gate
// that matched the directory would refuse a session's own work and look, from
// the inside, like Endless had broken.
func TestDocMirrorGatePaths(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		match bool
	}{
		// Should block — consolidated mirror paths, absolute or repo-relative.
		{"plan, repo-relative", ".endless/tasks/e-1/plan.md", true},
		{"outcome", ".endless/tasks/e-2137/outcome.md", true},
		{"analysis", ".endless/tasks/e-2137/analysis.md", true},
		{"dot-slash prefix", "./.endless/tasks/e-1202/plan.md", true},
		{"absolute", "/Users/x/proj/.endless/tasks/e-999/plan.md", true},
		{"absolute in worktree", "/abs/wt/e-1202/.endless/tasks/e-1202/plan.md", true},
		{"multi-digit", ".endless/tasks/e-12345/plan.md", true},

		// Should block — legacy paths, still reachable in a tree the sweep has
		// not converged yet.
		{"legacy plan", ".endless/plans/E-1.md", true},
		{"legacy outcome", ".endless/outcomes/E-1.md", true},
		{"legacy analysis", ".endless/analyses/E-1.md", true},
		{"legacy absolute", "/Users/x/proj/.endless/plans/E-999.md", true},

		// Should NOT block — the task's own files, beside the database's.
		{"the task's verify script", ".endless/tasks/e-1/verify.sh", false},
		{"the task's manifest", ".endless/tasks/e-1/verify.toml", false},
		{"a note the task wrote", ".endless/tasks/e-1/notes.md", false},
		{"the shared harness", ".endless/tasks/_harness.sh", false},
		{"the directory's own rules", ".endless/tasks/CLAUDE.md", false},

		// Should NOT block — near misses.
		{"uppercase task dir", ".endless/tasks/E-1/plan.md", false},
		{"subdir under the task", ".endless/tasks/e-1/sub/plan.md", false},
		{"wrong extension", ".endless/tasks/e-1/plan.txt", false},
		{"no digits", ".endless/tasks/e-/plan.md", false},
		{"legacy subdir excluded", ".endless/plans/snapshots/E-1.md", false},
		{"legacy non-plan file", ".endless/plans/notes.md", false},
		{"legacy lowercase prefix", ".endless/plans/e-1.md", false},
		{"normal source file", "src/foo.md", false},
		{"not a path component", "my.endless/tasks/e-1/plan.md", false},
		{"trailing suffix after md", ".endless/tasks/e-1/plan.md.bak", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := docmirror.TaskDocRe.MatchString(tc.path) ||
				docmirror.LegacyTaskDocRe.MatchString(tc.path)
			if got != tc.match {
				t.Errorf("gate matches %q = %v, want %v", tc.path, got, tc.match)
			}
		})
	}
}
