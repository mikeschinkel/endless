package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCompanion writes a minimal <wt>/.endless/worktree.json with the given
// branch so the branch-mismatch probe has something to read.
func writeCompanion(t *testing.T, wt, branch string) {
	t.Helper()
	dir := filepath.Join(wt, ".endless")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir companion dir: %v", err)
	}
	body := `{"kind":"task","branch":"` + branch + `"}`
	if err := os.WriteFile(filepath.Join(dir, "worktree.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write companion: %v", err)
	}
}

// gitStub drives runGit from canned outputs keyed by the git subcommand.
type gitStub struct {
	status       string // `git status --porcelain`
	statusErr    error
	branch       string // `git symbolic-ref --short --quiet HEAD` (empty+err => detached)
	branchErr    error
	worktreeList string // `git worktree list --porcelain`
	worktreeErr  error
}

func (s gitStub) install(t *testing.T) {
	t.Helper()
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	runGit = func(dir string, args ...string) (string, error) {
		switch args[0] {
		case "status":
			return s.status, s.statusErr
		case "symbolic-ref":
			return s.branch, s.branchErr
		case "worktree":
			return s.worktreeList, s.worktreeErr
		}
		return "", nil
	}
}

func kinds(as []WorktreeAnomaly) []AnomalyKind {
	out := make([]AnomalyKind, len(as))
	for i, a := range as {
		out[i] = a.Kind
	}
	return out
}

func TestWorktreeAnomaliesAt(t *testing.T) {
	const branch = "task/1758-x"
	// wtEntry is the porcelain block a healthy `git worktree list` prints for wt.
	wtEntry := func(wt string) string {
		return "worktree " + wt + "\nHEAD abc123\nbranch refs/heads/" + branch + "\n"
	}

	tests := []struct {
		name string
		stub func(wt string) gitStub
		want []AnomalyKind
	}{
		{
			name: "clean worktree → no anomalies",
			stub: func(wt string) gitStub {
				return gitStub{branch: branch, worktreeList: wtEntry(wt)}
			},
			want: nil,
		},
		{
			name: "auto-managed modifications alone → still clean",
			stub: func(wt string) gitStub {
				return gitStub{
					status:       " M .endless/verbs.jsonl\n?? .endless/db-ledger/2026.jsonl",
					branch:       branch,
					worktreeList: wtEntry(wt),
				}
			},
			want: nil,
		},
		{
			name: "untracked user file → uncommitted",
			stub: func(wt string) gitStub {
				return gitStub{
					status:       "?? newfile.go\n M .endless/verbs.jsonl",
					branch:       branch,
					worktreeList: wtEntry(wt),
				}
			},
			want: []AnomalyKind{AnomalyUncommitted},
		},
		{
			name: "detached HEAD → detached",
			stub: func(wt string) gitStub {
				return gitStub{branch: "", branchErr: os.ErrInvalid, worktreeList: wtEntry(wt)}
			},
			want: []AnomalyKind{AnomalyDetachedHead},
		},
		{
			name: "wrong branch → branch-mismatch",
			stub: func(wt string) gitStub {
				return gitStub{branch: "some-other-branch", worktreeList: wtEntry(wt)}
			},
			want: []AnomalyKind{AnomalyBranchMismatch},
		},
		{
			name: "locked worktree → prunable",
			stub: func(wt string) gitStub {
				return gitStub{
					branch:       branch,
					worktreeList: "worktree " + wt + "\nlocked stale\n",
				}
			},
			want: []AnomalyKind{AnomalyPrunable},
		},
		{
			name: "prunable worktree → prunable",
			stub: func(wt string) gitStub {
				return gitStub{
					branch:       branch,
					worktreeList: "worktree " + wt + "\nprunable gitdir file points to non-existent location\n",
				}
			},
			want: []AnomalyKind{AnomalyPrunable},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wt := t.TempDir()
			root := filepath.Dir(wt)
			writeCompanion(t, wt, branch)
			tc.stub(wt).install(t)

			got := kinds(worktreeAnomaliesAt(root, wt))
			if len(got) != len(tc.want) {
				t.Fatalf("kinds = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("kinds = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestUserStatusPaths(t *testing.T) {
	porcelain := strings.Join([]string{
		" M .endless/verbs.jsonl",
		"?? .endless/db-ledger/2026-07.jsonl",
		" M src/foo.go",
		"?? bar.txt",
		"R  old.go -> new.go",
	}, "\n")
	got := userStatusPaths(porcelain)
	want := []string{"src/foo.go", "bar.txt", "new.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("userStatusPaths = %v, want %v", got, want)
	}
}

func TestIsAutoManagedPath(t *testing.T) {
	managed := []string{
		".endless/db-ledger/2026-07.jsonl",
		".endless/verbs.jsonl",
	}
	for _, p := range managed {
		if !isAutoManagedPath(p) {
			t.Errorf("isAutoManagedPath(%q) = false, want true", p)
		}
	}
	// .endless/verbs.json is deprecated and dropped from the glob list (E-1858):
	// the E-1268 migration is complete, so it is never produced and is no longer
	// auto-managed.
	unmanaged := []string{
		"src/foo.go",
		".endless/plans/E-1.md",
		".claude/settings.json",
		".endless/verbs.json",
		// E-2055: `endless lesson write` commits the corrections log on the
		// main checkout as it writes, so land never sweeps it and a modified
		// copy inside a worktree is user work, not ambient churn.
		".endless/LESSONS.md",
	}
	for _, p := range unmanaged {
		if isAutoManagedPath(p) {
			t.Errorf("isAutoManagedPath(%q) = true, want false", p)
		}
	}
}

func TestWorktreeAnomalyLine(t *testing.T) {
	a := WorktreeAnomaly{Kind: AnomalyBranchMismatch, Detail: "on x, expected y"}
	if got, want := a.Line(), "branch-mismatch: on x, expected y"; got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
}
