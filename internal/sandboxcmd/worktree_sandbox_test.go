package sandboxcmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorktreeRemovalTakesItsSandbox pins what replaced the reap hook E-2481
// removed: a worktree's sandbox lives inside it and ignores itself, so the
// `git worktree remove` that both `worktree drop` (without --force) and the
// reaper (with it) run takes the sandbox along, and nothing has to go looking
// for it afterwards.
func TestWorktreeRemovalTakesItsSandbox(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "drop"
		if force {
			name = "reap"
		}
		t.Run(name, func(t *testing.T) {
			main := t.TempDir()
			gitOrFatal(t, main, "init", "-q", "-b", "main")
			gitOrFatal(t, main, "config", "user.email", "test@example.com")
			gitOrFatal(t, main, "config", "user.name", "test")
			gitOrFatal(t, main, "config", "commit.gpgsign", "false")
			gitOrFatal(t, main, "commit", "-q", "--allow-empty", "-m", "init")
			worktree := filepath.Join(main, ".endless", "worktrees", "e-42")
			gitOrFatal(t, main, "worktree", "add", "-q", "-b", "task/42", worktree)

			sandbox := filepath.Join(worktree, ".endless", "sandbox")
			if err := EnsureSandboxDir(sandbox); err != nil {
				t.Fatal(err)
			}
			db := filepath.Join(sandbox, "endless", "endless.db")
			if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(db, []byte("state"), 0o644); err != nil {
				t.Fatal(err)
			}

			args := []string{"worktree", "remove"}
			if force {
				args = append(args, "--force")
			}
			gitOrFatal(t, main, append(args, worktree)...)

			if _, err := os.Stat(sandbox); !os.IsNotExist(err) {
				t.Errorf("sandbox %s survived its worktree's removal (stat err = %v)", sandbox, err)
			}
		})
	}
}
