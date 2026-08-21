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
	dbContextFromFlag = false
	t.Cleanup(func() {
		dbContextDir = ""
		dbPathOverride = ""
		dbContextFromFlag = false
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
			dir:  "/home/x/proj/.endless/worktrees/e-1429/internal/monitor",
			want: "/home/x/proj",
		},
		{
			name: "named-alternate dir not recognized (ED-1515)",
			dir:  "/home/x/proj/.endless/worktrees/e-1429-some-slug",
			want: "",
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

func TestProjectIsSelfDev(t *testing.T) {
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
		root := writeConfig(t, `{"self_dev": true}`)
		if !projectIsSelfDev(root) {
			t.Error("want true for self_dev: true")
		}
	})
	t.Run("flag false", func(t *testing.T) {
		root := writeConfig(t, `{"self_dev": false}`)
		if projectIsSelfDev(root) {
			t.Error("want false for self_dev: false")
		}
	})
	t.Run("flag absent", func(t *testing.T) {
		root := writeConfig(t, `{"name": "proj"}`)
		if projectIsSelfDev(root) {
			t.Error("want false when flag absent")
		}
	})
	t.Run("config missing", func(t *testing.T) {
		root := writeConfig(t, "")
		if projectIsSelfDev(root) {
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
	sandbox := filepath.Join(cache, "endless", "sandboxes", "e-test")
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
		body := `{"self_dev": false}`
		if sandbox {
			body = `{"self_dev": true}`
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
			t.Fatal("want refusal in a self_dev worktree without explicit context")
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
			t.Fatalf("PinMainDB (hook/tmux) should satisfy the gate: %v", err)
		}
	})

	t.Run("non-sandbox worktree -> allow (downstream projects)", func(t *testing.T) {
		resetDBContext(t)
		t.Chdir(newProject(t, false))
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("a project without self_dev must never trip the gate: %v", err)
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

func TestWorktreeDirName(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{
			name: "plain worktree root",
			dir:  "/home/x/proj/.endless/worktrees/e-1368",
			want: "e-1368",
		},
		{
			name: "subdir of worktree",
			dir:  "/home/x/proj/.endless/worktrees/e-1368/internal/monitor",
			want: "e-1368",
		},
		{
			name: "named-alternate dir not recognized (ED-1515)",
			dir:  "/home/x/proj/.endless/worktrees/e-1368-some-slug",
			want: "",
		},
		{
			name: "main checkout (no marker)",
			dir:  "/home/x/proj",
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
			if got := worktreeDirName(tc.dir); got != tc.want {
				t.Errorf("worktreeDirName(%q) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}
}

func TestSelfDetectWorktreeSandbox(t *testing.T) {
	// Build <root>/.endless/{config.json, worktrees/<name>} and, when
	// makeSandbox, the sandbox config dir under XDG_CACHE_HOME/endless/
	// sandboxes/<name>/endless. Returns the worktree dir to chdir into and the
	// sandbox dir self-detect should resolve to. CacheDir() reads the same
	// XDG_CACHE_HOME string, so the expected and computed paths match exactly
	// (no symlink-resolution mismatch).
	setup := func(t *testing.T, name string, selfDev, makeSandbox bool) (string, string) {
		root := t.TempDir()
		endless := filepath.Join(root, ".endless")
		wt := filepath.Join(endless, "worktrees", name)
		if err := os.MkdirAll(wt, 0755); err != nil {
			t.Fatal(err)
		}
		body := `{"self_dev": false}`
		if selfDev {
			body = `{"self_dev": true}`
		}
		if err := os.WriteFile(filepath.Join(endless, "config.json"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		cache := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", cache)
		sandboxDir := filepath.Join(cache, "endless", "sandboxes", name, "endless")
		if makeSandbox {
			if err := os.MkdirAll(sandboxDir, 0755); err != nil {
				t.Fatal(err)
			}
		}
		return wt, sandboxDir
	}

	t.Run("self-dev worktree + sandbox exists -> routes", func(t *testing.T) {
		resetDBContext(t)
		wt, sandboxDir := setup(t, "e-1368", true, true)
		t.Chdir(wt)
		SelfDetectWorktreeSandbox()
		if dbContextDir != sandboxDir {
			t.Errorf("dbContextDir = %q, want %q", dbContextDir, sandboxDir)
		}
		// A cwd-detected sandbox must NOT count as an explicit flag context, so
		// the hook/tmux main pin still applies (E-1700). Otherwise a
		// self-dev dev session's session/pane-state writes land in the sandbox,
		// where the spawned task doesn't exist (FK-fails) instead of main.
		if HasExplicitDBContext() {
			t.Error("HasExplicitDBContext() = true after cwd self-detect; want false so PinMainDB still runs")
		}
	})

	t.Run("named-alternate dir not recognized -> no-op (ED-1515)", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := setup(t, "e-1368-my-slug", true, true)
		t.Chdir(wt)
		SelfDetectWorktreeSandbox()
		if dbContextDir != "" {
			t.Errorf("dbContextDir = %q, want empty (named alternate is not a task worktree)", dbContextDir)
		}
	})

	t.Run("sandbox absent -> no-op (gate still refuses)", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := setup(t, "e-1368", true, false)
		t.Chdir(wt)
		SelfDetectWorktreeSandbox()
		if dbContextDir != "" {
			t.Errorf("dbContextDir = %q, want empty (no sandbox on disk)", dbContextDir)
		}
	})

	t.Run("not a self-dev project -> no-op", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := setup(t, "e-1368", false, true)
		t.Chdir(wt)
		SelfDetectWorktreeSandbox()
		if dbContextDir != "" {
			t.Errorf("dbContextDir = %q, want empty (not self_dev)", dbContextDir)
		}
	})

	t.Run("explicit context already set -> no-op", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := setup(t, "e-1368", true, true)
		t.Chdir(wt)
		SetDBContextDir("/explicit/dir")
		SelfDetectWorktreeSandbox()
		if dbContextDir != "/explicit/dir" {
			t.Errorf("dbContextDir = %q, want /explicit/dir (self-detect must not override explicit)", dbContextDir)
		}
	})

	t.Run("cwd outside any worktree -> no-op", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		t.Chdir(t.TempDir())
		SelfDetectWorktreeSandbox()
		if dbContextDir != "" {
			t.Errorf("dbContextDir = %q, want empty (cwd not in a worktree)", dbContextDir)
		}
	})
}

// TestSelfDetectVsExplicit_MainPinRouting is the E-1700 routing regression. It
// exercises both self-dev use cases through the exact main.go guard
// (`if !HasExplicitDBContext() { PinMainDB() }`):
//
//  1. Developing endless (a real dev session): the sandbox is discovered from
//     cwd, so session/pane-state writes must go to MAIN (where the spawned task
//     exists) while config/logs follow the sandbox.
//  2. Testing endless (an explicit --config-dir): the pin is suppressed so
//     writes go to the chosen sandbox/temp DB.
func TestSelfDetectVsExplicit_MainPinRouting(t *testing.T) {
	newSelfDevWorktree := func(t *testing.T) (wt, sandboxDir string) {
		root := t.TempDir()
		endless := filepath.Join(root, ".endless")
		wt = filepath.Join(endless, "worktrees", "e-1700")
		if err := os.MkdirAll(wt, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(endless, "config.json"), []byte(`{"self_dev": true}`), 0644); err != nil {
			t.Fatal(err)
		}
		cache := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", cache)
		sandboxDir = filepath.Join(cache, "endless", "sandboxes", "e-1700", "endless")
		if err := os.MkdirAll(sandboxDir, 0755); err != nil {
			t.Fatal(err)
		}
		return wt, sandboxDir
	}

	// applyGuard mirrors cmd/endless-go/main.go's hook/tmux pin.
	applyGuard := func() {
		if !HasExplicitDBContext() {
			PinMainDB()
		}
	}

	t.Run("dev session (cwd self-detect): DB->main, config->sandbox", func(t *testing.T) {
		resetDBContext(t)
		wt, sandboxDir := newSelfDevWorktree(t)
		t.Chdir(wt)

		SelfDetectWorktreeSandbox() // as main.go runs it, before the pin
		applyGuard()

		if got := DBPath(); !strings.HasSuffix(got, filepath.Join(".config", "endless", "endless.db")) || strings.HasPrefix(got, sandboxDir) {
			t.Errorf("DBPath() = %q, want the real main DB (not under sandbox %q)", got, sandboxDir)
		}
		if got := ConfigDir(); got != sandboxDir {
			t.Errorf("ConfigDir() = %q, want sandbox %q (config/logs follow the sandbox)", got, sandboxDir)
		}
	})

	t.Run("test harness (explicit --config-dir): DB->that dir", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := newSelfDevWorktree(t)
		t.Chdir(wt)
		explicit := t.TempDir()

		SetDBContextDir(explicit) // stands in for ConsumeDBContextFlag(--config-dir)
		SelfDetectWorktreeSandbox() // no-ops: explicit already set
		applyGuard()                // no-ops: HasExplicitDBContext() is true

		if got := DBPath(); got != filepath.Join(explicit, "endless.db") {
			t.Errorf("DBPath() = %q, want explicit %q (flag beats the main pin)", got, filepath.Join(explicit, "endless.db"))
		}
	})
}
