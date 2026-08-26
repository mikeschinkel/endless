package hookcmd

import "testing"

// TestWorktreeRemovalRes is the matcher half of the categorical removal gate.
// It mirrors TestSqliteEndlessRe: the block function itself calls os.Exit, so
// what is unit-testable is the decision that precedes it.
//
// The blocked table is the point of the gate — every entry is a different route
// to the same removed worktree, and the rule fails the moment one of them is
// reachable. The allowed table is what keeps the gate usable: a gate that fires
// on ordinary work teaches the session to route around it, which is worse than
// no gate at all.
func TestWorktreeRemovalRes(t *testing.T) {
	matches := func(cmd string) bool {
		for _, re := range worktreeRemovalRes {
			if re.MatchString(cmd) {
				return true
			}
		}
		return false
	}

	blocked := []string{
		// The three routes the rule names.
		"endless worktree drop E-123",
		"endless worktree reap",
		"git worktree remove .endless/worktrees/e-123",
		// …and the fourth, which is why the rule names an outcome.
		"rm -rf .endless/worktrees/e-123",
		"rm -rf /Users/x/proj/.endless/worktrees/e-123/",
		"rm -fr .endless/worktrees/e-2073",
		// Flags, ids and force do not change the outcome.
		"endless worktree drop E-123 --force",
		"endless worktree drop --force E-123",
		"git worktree remove --force .endless/worktrees/e-9",
		"git worktree prune",
		"git -C /Users/x/proj worktree remove .endless/worktrees/e-1",
		// Wrapper- and path-prefixed forms.
		"uv run endless worktree drop E-5",
		"/usr/local/bin/endless worktree drop E-5",
		"./bin/endless-go worktree drop E-5",
		// After a separator, which is how a removal usually arrives.
		"cd /tmp && endless worktree drop E-7",
		"cd /tmp; git worktree remove .endless/worktrees/e-7",
		"endless task show E-1 | cat && rm -rf .endless/worktrees/e-7",
		// Leading whitespace, as blockCommitOnMainIfApplicable tolerates.
		"   endless worktree drop E-3",
	}
	for _, cmd := range blocked {
		if !matches(cmd) {
			t.Errorf("expected BLOCK, got allow: %q", cmd)
		}
	}

	allowed := []string{
		// Landing retains the worktree — it is the normal end of a task, and
		// blocking it would break every task.
		"endless worktree land E-123",
		"endless worktree land E-123 --force",
		// The read-only surface.
		"endless worktree list",
		"endless worktree check",
		"endless worktree show e-123",
		"endless worktree current",
		"endless worktree for-task E-123",
		"git worktree list",
		"endless-go worktree in-use --dir /tmp/wt --task 123",
		// Creating one is not removing one.
		"git worktree add -b task/9-x .endless/worktrees/e-9 main",
		// Deleting something INSIDE a worktree is ordinary work. This is the
		// false positive that would matter most: it happens constantly, and a
		// block here would train the session to stop trusting the gate.
		"rm -rf .endless/worktrees/e-123/bin",
		"rm -rf .endless/worktrees/e-123/node_modules",
		"rm -f .endless/worktrees/e-123/scratch.txt",
		// A non-recursive rm cannot remove a directory.
		"rm .endless/worktrees/e-123",
		// Unrelated removals elsewhere in the tree.
		"rm -rf ./bin",
		"rm -rf /tmp/scratch",
		// Merely naming the path is not invoking it.
		"ls .endless/worktrees/",
		"cat .endless/worktrees/e-123/README.md",
		// MENTIONING a removal is not performing one. This gate's own rule is
		// documented in the handoff templates, in the guide and in this task's
		// verify script, all of which quote the commands in order to forbid
		// them — so a session writing or grepping that documentation must not
		// be blocked. This was a real false positive before the matcher was
		// anchored to a command position.
		"echo 'never run endless worktree drop'",
		`echo "do not run endless worktree drop"`,
		"grep -rn 'endless worktree drop' docs/",
		`ROUTES=('endless worktree drop' 'git worktree remove')`,
		`grep -q 'git worktree remove' tests/tasks/e-2073-verify.sh`,
		`git commit -m "block endless worktree drop at the tool layer"`,
		`echo "rm -rf .endless/worktrees/e-1 is refused"`,
	}
	for _, cmd := range allowed {
		if matches(cmd) {
			t.Errorf("expected ALLOW, got block: %q", cmd)
		}
	}
}
