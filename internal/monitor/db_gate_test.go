package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetDBContext clears the process-global DB-context vars so a sub-test
// starts clean and never leaks into DB()-using tests. Mirrors the pattern in
// sandbox_test.go for dbPathOverride.
func resetDBContext(t *testing.T) {
	t.Helper()
	dbContextDir = ""
	dbPathOverride = ""
	t.Cleanup(func() {
		dbContextDir = ""
		dbPathOverride = ""
	})
}

func TestSelfDevProjectRoot(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{
			name: "worktree root",
			dir:  "/home/x/proj/.endless/worktrees/e-1429",
			want: "/home/x/proj",
		},
		{
			name: "subdir of worktree",
			dir:  "/home/x/proj/.endless/worktrees/e-1429-some-slug/internal/monitor",
			want: "/home/x/proj",
		},
		{
			name: "main checkout (no worktrees segment)",
			dir:  "/home/x/proj",
			want: "",
		},
		{
			name: "unrelated dir",
			dir:  "/home/x/other/project",
			want: "",
		},
		{
			name: "marker present but not an e-NNN worktree",
			dir:  "/home/x/proj/.endless/worktrees/scratch",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selfDevProjectRoot(tc.dir); got != tc.want {
				t.Errorf("selfDevProjectRoot(%q) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}
}

func TestProjectWantsWorktreeSandbox(t *testing.T) {
	writeConfig := func(t *testing.T, body string) string {
		root := t.TempDir()
		dir := filepath.Join(root, ".endless")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}

	t.Run("flag true", func(t *testing.T) {
		root := writeConfig(t, `{"worktree_sandbox": true}`)
		if !projectWantsWorktreeSandbox(root) {
			t.Error("want true for worktree_sandbox: true")
		}
	})
	t.Run("flag false", func(t *testing.T) {
		root := writeConfig(t, `{"worktree_sandbox": false}`)
		if projectWantsWorktreeSandbox(root) {
			t.Error("want false for worktree_sandbox: false")
		}
	})
	t.Run("flag absent", func(t *testing.T) {
		root := writeConfig(t, `{"name": "proj"}`)
		if projectWantsWorktreeSandbox(root) {
			t.Error("want false when flag absent")
		}
	})
	t.Run("config missing", func(t *testing.T) {
		root := writeConfig(t, "")
		if projectWantsWorktreeSandbox(root) {
			t.Error("want false when config.json missing")
		}
	})
}

func TestConsumeDBContextFlag(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantDir  string
		wantArgs []string
	}{
		{
			name:     "space form before subcommand",
			args:     []string{"endless-event", "--config-dir", "/c/endless", "emit", "--kind", "x"},
			wantDir:  "/c/endless",
			wantArgs: []string{"endless-event", "emit", "--kind", "x"},
		},
		{
			name:     "equals form",
			args:     []string{"endless-event", "--config-dir=/c/endless", "emit"},
			wantDir:  "/c/endless",
			wantArgs: []string{"endless-event", "emit"},
		},
		{
			name:     "flag after subcommand still stripped",
			args:     []string{"endless-event", "emit", "--config-dir", "/c/endless", "--kind", "x"},
			wantDir:  "/c/endless",
			wantArgs: []string{"endless-event", "emit", "--kind", "x"},
		},
		{
			name:     "absent leaves args and dir untouched",
			args:     []string{"endless-event", "emit", "--kind", "x"},
			wantDir:  "",
			wantArgs: []string{"endless-event", "emit", "--kind", "x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDBContext(t)
			orig := os.Args
			t.Cleanup(func() { os.Args = orig })
			os.Args = append([]string(nil), tc.args...)

			ConsumeDBContextFlag()

			if dbContextDir != tc.wantDir {
				t.Errorf("dbContextDir = %q, want %q", dbContextDir, tc.wantDir)
			}
			if len(os.Args) != len(tc.wantArgs) {
				t.Fatalf("os.Args = %v, want %v", os.Args, tc.wantArgs)
			}
			for i := range tc.wantArgs {
				if os.Args[i] != tc.wantArgs[i] {
					t.Fatalf("os.Args = %v, want %v", os.Args, tc.wantArgs)
				}
			}
		})
	}
}

func TestPinMainDB(t *testing.T) {
	resetDBContext(t)
	// XDG points into a sandbox; PinMainDB must move the DB to main while
	// leaving ConfigDir() (config.json, logs) on the sandbox.
	cache := t.TempDir()
	sandbox := filepath.Join(cache, "endless", "sandboxes", "worktree-e-test")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_CONFIG_HOME", sandbox)

	PinMainDB()

	wantSuffix := filepath.Join(".config", "endless", "endless.db")
	if got := DBPath(); !strings.HasSuffix(got, wantSuffix) || strings.HasPrefix(got, sandbox) {
		t.Errorf("DBPath() = %q, want suffix %q and not under sandbox %q", got, wantSuffix, sandbox)
	}
	// ConfigDir() (config.json, logs) must stay on the sandbox: PinMainDB
	// moves only the DB path.
	wantConfig := filepath.Join(sandbox, "endless")
	if got := ConfigDir(); got != wantConfig {
		t.Errorf("ConfigDir() = %q, want sandbox %q (config.json/logs stay in worktree)", got, wantConfig)
	}
	if !dbContextExplicit() {
		t.Error("PinMainDB() must satisfy the worktree gate (dbContextExplicit)")
	}
}

func TestGuardWorktreeDBContext(t *testing.T) {
	// Build <root>/.endless/{config.json, worktrees/e-777} and chdir into the
	// worktree so guardWorktreeDBContext()'s os.Getwd() sees a self-dev cwd.
	newProject := func(t *testing.T, sandbox bool) string {
		root := t.TempDir()
		endless := filepath.Join(root, ".endless")
		wt := filepath.Join(endless, "worktrees", "e-777")
		if err := os.MkdirAll(wt, 0755); err != nil {
			t.Fatal(err)
		}
		body := `{"worktree_sandbox": false}`
		if sandbox {
			body = `{"worktree_sandbox": true}`
		}
		if err := os.WriteFile(filepath.Join(endless, "config.json"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return wt
	}

	t.Run("gated worktree, no context -> refuse", func(t *testing.T) {
		resetDBContext(t)
		t.Chdir(newProject(t, true))
		if err := guardWorktreeDBContext(); err == nil {
			t.Fatal("want refusal in a worktree_sandbox worktree without explicit context")
		}
	})

	t.Run("gated worktree, --config-dir context -> allow", func(t *testing.T) {
		resetDBContext(t)
		t.Chdir(newProject(t, true))
		SetDBContextDir(t.TempDir())
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("explicit --config-dir context should satisfy the gate: %v", err)
		}
	})

	t.Run("gated worktree, PinMainDB context -> allow", func(t *testing.T) {
		resetDBContext(t)
		t.Chdir(newProject(t, true))
		PinMainDB()
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("PinMainDB (hook/channel/tmux) should satisfy the gate: %v", err)
		}
	})

	t.Run("non-sandbox worktree -> allow (downstream projects)", func(t *testing.T) {
		resetDBContext(t)
		t.Chdir(newProject(t, false))
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("a project without worktree_sandbox must never trip the gate: %v", err)
		}
	})

	t.Run("not in a worktree -> allow", func(t *testing.T) {
		resetDBContext(t)
		t.Chdir(t.TempDir())
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("outside a self-dev worktree no flag is required: %v", err)
		}
	})
}
