package hookcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathWithin pins the cwd-containment logic behind the E-1586 gate: a
// session's cwd counts as "inside" its worktree only when it is the worktree
// root or a descendant — siblings sharing a name prefix must NOT match.
func TestPathWithin(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{"same dir", "/a/b", "/a/b", true},
		{"direct child", "/a/b", "/a/b/c", true},
		{"deep descendant", "/a/b", "/a/b/c/d/e", true},
		{"trailing slash normalized", "/a/b/", "/a/b/c", true},
		{"parent is not within child", "/a/b", "/a", false},
		{"sibling sharing prefix", "/a/b", "/a/bc", false},
		{"unrelated tree", "/a/b", "/x/y", false},
		{"main vs worktree", "/repo", "/repo/.endless/worktrees/e-1", true},
		{"worktree vs main", "/repo/.endless/worktrees/e-1", "/repo", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pathWithin(c.parent, c.child); got != c.want {
				t.Errorf("pathWithin(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
			}
		})
	}
}

// TestTildePath pins home-relative rendering used in the block message.
func TestTildePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir resolvable")
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"home itself", home, "~"},
		{"under home", filepath.Join(home, "Projects", "x"), "~/Projects/x"},
		{"outside home", "/etc/hosts", "/etc/hosts"},
		{"prefix-but-not-child", home + "-sibling", home + "-sibling"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tildePath(c.in); got != c.want {
				t.Errorf("tildePath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestCdRedirect verifies the block message hands the agent an absolute /cd
// target (no ~ / $(...), which /cd does not expand) and names the task.
func TestCdRedirect(t *testing.T) {
	wt := "/Users/dev/repo/.endless/worktrees/e-42"
	cwd := "/Users/dev/repo"
	msg := cdRedirect(42, wt, cwd)

	if !strings.Contains(msg, "/cd "+wt) {
		t.Errorf("expected literal absolute `/cd %s` in message, got:\n%s", wt, msg)
	}
	if !strings.Contains(msg, "E-42") {
		t.Errorf("expected task id E-42 in message, got:\n%s", msg)
	}
	if strings.Contains(msg, "/cd ~") || strings.Contains(msg, "/cd \"$(") {
		t.Errorf("/cd target must be absolute (no ~ or $()): \n%s", msg)
	}
}
