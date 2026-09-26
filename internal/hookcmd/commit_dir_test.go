package hookcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// E-2177: the commit-on-main gate judges the directory the commit RUNS in, not
// the session's cwd. `cd <path> && git commit` is the most common form an agent
// uses, and it used to be examined against the session's cwd — or, since the
// match was anchored to the start of the command, not examined at all.

// commitRepos builds a main checkout with one linked worktree and returns both
// paths, symlink-resolved so they compare equal to what git reports.
func commitRepos(t *testing.T) (main, worktree string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	main = filepath.Join(dir, "proj")
	worktree = filepath.Join(main, ".endless", "worktrees", "e-7")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "--initial-branch=main", main)
	git("-C", main, "commit", "-q", "--allow-empty", "-m", "init")
	git("-C", main, "worktree", "add", "-q", "-b", "task/7", worktree)
	return main, worktree
}

func TestCommitRunsOnMain_JudgesTheCommitsDirectory(t *testing.T) {
	main, wt := commitRepos(t)
	gc := "git " + "commit"

	cases := []struct {
		name, cwd, cmd string
		refuse         bool
	}{
		// A bare commit behaves as it always did.
		{"bare, from main", main, gc + " -m x", true},
		{"bare, from a worktree", wt, gc + " -m x", false},

		// The commit's own directory wins over the session's.
		{"cd main &&, from a worktree", wt, "cd " + main + " && " + gc + " -m x", true},
		{"cd main ;, from a worktree", wt, "cd " + main + "; " + gc + " -m x", true},
		{"git -C main, from a worktree", wt, "git -C " + main + " commit -m x", true},
		{"cd worktree &&, from main", main, "cd " + wt + " && " + gc + " -m x", false},
		{"git -C worktree, from main", main, "git -C " + wt + " commit -m x", false},

		// Relative, chained and quoted paths resolve against the session cwd.
		{"relative cd into the worktree", main, "cd .endless/worktrees/e-7 && " + gc, false},
		{"chained cds", main, "cd .endless && cd worktrees/e-7 && " + gc, false},
		{"relative cd back to main", wt, "cd ../../.. && " + gc, true},
		{"quoted cd path", wt, `cd "` + main + `" && ` + gc, true},
		{"relative -C after cd", main, "cd .endless && git -C worktrees/e-7 commit", false},

		// `~` expands. The home directory is not a repo, so this allows.
		{"tilde", main, "cd ~ && " + gc, false},

		// Mentions are not commits.
		{"quoted mention, from main", main, `echo "` + gc + `"`, false},
		{"heredoc mention, from main", main, "cat > f <<'EOF'\n" + gc + "\nEOF", false},
		{"commit-tree is not commit", main, "git commit-tree HEAD^{tree}", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commitRunsOnMain(c.cmd, c.cwd); got != c.refuse {
				t.Errorf("commitRunsOnMain(%q, cwd=%s) = %v, want %v", c.cmd, c.cwd, got, c.refuse)
			}
		})
	}
}

// TestCommitDir pins the directory resolution on its own, independent of git.
func TestCommitDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	gc := "git " + "commit"
	cases := []struct {
		name, cwd, cmd, want string
	}{
		{"bare", "/s", gc, "/s"},
		{"absolute cd", "/s", "cd /a && " + gc, "/a"},
		{"relative cd", "/s", "cd a && " + gc, "/s/a"},
		{"last cd wins", "/s", "cd /a && cd /b && " + gc, "/b"},
		{"cd after the commit is ignored", "/s", gc + " && cd /a", "/s"},
		{"-C", "/s", "git -C /c commit", "/c"},
		{"-C relative to cd", "/s", "cd /a && git -C c commit", "/a/c"},
		{"tilde", "/s", "cd ~/p && " + gc, filepath.Join(home, "p")},
		{"bare tilde", "/s", "cd ~ && " + gc, home},
		{"single-quoted", "/s", "cd '/a b' && " + gc, "/a b"},
		{"cd - is unknowable, keep what we had", "/s", "cd - && " + gc, "/s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := commitDir(c.cmd, c.cwd)
			if !ok {
				t.Fatalf("commitDir(%q) found no commit", c.cmd)
			}
			if got != c.want {
				t.Errorf("commitDir(%q, %q) = %q, want %q", c.cmd, c.cwd, got, c.want)
			}
		})
	}
}
