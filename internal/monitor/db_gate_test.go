package monitor

import (
	"errors"
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

// TestConsumeDBFlags covers the --db-dir escape and the argv surgery every
// spelling shares. `--db main` and `--db sandbox` resolve against the
// environment and cwd, so they get their own test below.
func TestConsumeDBFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantDir  string
		wantArgs []string
	}{
		{
			name:     "space form before subcommand",
			args:     []string{"endless-go", "--db-dir", "/c/endless", "event", "emit", "--kind", "x"},
			wantDir:  "/c/endless",
			wantArgs: []string{"endless-go", "event", "emit", "--kind", "x"},
		},
		{
			name:     "equals form",
			args:     []string{"endless-go", "--db-dir=/c/endless", "event", "emit"},
			wantDir:  "/c/endless",
			wantArgs: []string{"endless-go", "event", "emit"},
		},
		{
			name:     "flag after subcommand still stripped",
			args:     []string{"endless-go", "event", "emit", "--db-dir", "/c/endless", "--kind", "x"},
			wantDir:  "/c/endless",
			wantArgs: []string{"endless-go", "event", "emit", "--kind", "x"},
		},
		{
			name:     "absent leaves args and dir untouched",
			args:     []string{"endless-go", "event", "emit", "--kind", "x"},
			wantDir:  "",
			wantArgs: []string{"endless-go", "event", "emit", "--kind", "x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDBContext(t)
			orig := os.Args
			t.Cleanup(func() { os.Args = orig })
			os.Args = append([]string(nil), tc.args...)

			if err := ConsumeDBFlags(); err != nil {
				t.Fatalf("ConsumeDBFlags() = %v, want nil", err)
			}

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

	t.Run("gated worktree, --db-dir context -> allow", func(t *testing.T) {
		resetDBContext(t)
		t.Chdir(newProject(t, true))
		SetDBContextDir(t.TempDir())
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("an explicit DB context should satisfy the gate: %v", err)
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
// newGatedWorktree builds <root>/.endless/{config.json, worktrees/<name>} and
// the matching sandbox config dir under XDG_CACHE_HOME, returning the worktree
// dir to chdir into and the sandbox dir `--db sandbox` must resolve to.
// CacheDir() reads the same XDG_CACHE_HOME string, so expected and computed
// paths match exactly (no symlink-resolution mismatch).
func newGatedWorktree(t *testing.T, name string, selfDev bool) (wt, sandboxDir string) {
	t.Helper()
	root := t.TempDir()
	endless := filepath.Join(root, ".endless")
	wt = filepath.Join(endless, "worktrees", name)
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
	sandboxDir = filepath.Join(cache, "endless", "sandboxes", name, "endless")
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		t.Fatal(err)
	}
	return wt, sandboxDir
}

// consume runs ConsumeDBFlags over a synthetic argv, restoring os.Args after.
func consume(t *testing.T, args ...string) error {
	t.Helper()
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = append([]string{"endless-go"}, args...)
	return ConsumeDBFlags()
}

// TestConsumeDBFlags_Choices covers the two named databases. `--db main` must
// follow $HOME (so a verify suite under a temp HOME means its own main, not the
// developer's), and `--db sandbox` must read cwd for the ADDRESS while still
// requiring the flag for the PERMISSION.
func TestConsumeDBFlags_Choices(t *testing.T) {
	t.Run("--db main follows $HOME and ignores XDG_CONFIG_HOME", func(t *testing.T) {
		resetDBContext(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // must lose
		if err := consume(t, "--db", "main", "event", "emit"); err != nil {
			t.Fatalf("ConsumeDBFlags() = %v, want nil", err)
		}
		want := filepath.Join(home, ".config", "endless")
		if dbContextDir != want {
			t.Errorf("dbContextDir = %q, want %q", dbContextDir, want)
		}
	})

	t.Run("--db sandbox resolves this worktree's sandbox from cwd", func(t *testing.T) {
		resetDBContext(t)
		wt, sandboxDir := newGatedWorktree(t, "e-1668", true)
		t.Chdir(wt)
		if err := consume(t, "--db", "sandbox", "session-query", "list-live"); err != nil {
			t.Fatalf("ConsumeDBFlags() = %v, want nil", err)
		}
		if dbContextDir != sandboxDir {
			t.Errorf("dbContextDir = %q, want %q", dbContextDir, sandboxDir)
		}
	})

	t.Run("--db sandbox outside a self-dev worktree is refused", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		t.Chdir(t.TempDir())
		err := consume(t, "--db", "sandbox")
		if err == nil {
			t.Fatal("want a refusal: there is no sandbox to name")
		}
		if !strings.Contains(err.Error(), "self-dev worktree") {
			t.Errorf("refusal = %q, want it to say why", err)
		}
		if dbContextDir != "" {
			t.Errorf("dbContextDir = %q, want empty on a refused choice", dbContextDir)
		}
	})

	t.Run("--db sandbox in a NON-self-dev worktree is refused", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := newGatedWorktree(t, "e-1668", false)
		t.Chdir(wt)
		if err := consume(t, "--db", "sandbox"); err == nil {
			t.Fatal("want a refusal: the project is not self-dev")
		}
	})

	t.Run("unknown --db value is refused, naming the two", func(t *testing.T) {
		resetDBContext(t)
		err := consume(t, "--db", "worktree")
		if err == nil {
			t.Fatal("want a refusal for an unknown --db value")
		}
		if !strings.Contains(err.Error(), "main") || !strings.Contains(err.Error(), "sandbox") {
			t.Errorf("refusal = %q, want it to name both accepted values", err)
		}
	})

	t.Run("--db with no value is refused, not silently skipped", func(t *testing.T) {
		resetDBContext(t)
		if err := consume(t, "--db"); err == nil {
			t.Fatal("want a refusal: a caller that meant to choose and did not")
		}
	})

	t.Run("--db-dir with no value is refused", func(t *testing.T) {
		resetDBContext(t)
		if err := consume(t, "--db-dir"); err == nil {
			t.Fatal("want a refusal for --db-dir with nothing after it")
		}
	})

	t.Run("--db and --db-dir together are refused", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("HOME", t.TempDir())
		err := consume(t, "--db", "main", "--db-dir", "/tmp/x")
		if !errors.Is(err, ErrDBFlagConflict) {
			t.Fatalf("err = %v, want ErrDBFlagConflict", err)
		}
	})
}

// TestGateRefusesWithoutAFlag is E-1668's regression, at the unit level: the
// E-1429 gate must refuse inside a self-dev worktree when nothing was said,
// EVEN THOUGH the sandbox exists on disk and cwd names it unambiguously.
//
// That "even though" is the whole bug. E-1368 read those same facts and routed
// to the sandbox, which set dbContextDir, which satisfied dbContextExplicit() —
// so the gate passed while nobody had chosen. Detection decides where to look;
// only a flag decides that you may open it.
func TestGateRefusesWithoutAFlag(t *testing.T) {
	t.Run("sandbox on disk, cwd inside it, no flag -> still refuse", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := newGatedWorktree(t, "e-1668", true)
		t.Chdir(wt)

		if dbContextDir != "" {
			t.Fatalf("dbContextDir = %q before any flag; nothing may set it but a flag", dbContextDir)
		}
		err := guardWorktreeDBContext()
		if err == nil {
			t.Fatal("gate allowed an unchosen database — this is the E-1368 regression")
		}
		// The refusal has to name the remedy this binary actually takes.
		for _, want := range []string{"--db main", "--db sandbox"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %q", err, want)
			}
		}
	})

	t.Run("the same cwd WITH --db sandbox -> allowed", func(t *testing.T) {
		resetDBContext(t)
		wt, sandboxDir := newGatedWorktree(t, "e-1668", true)
		t.Chdir(wt)
		if err := consume(t, "--db", "sandbox"); err != nil {
			t.Fatalf("ConsumeDBFlags() = %v, want nil", err)
		}
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("an explicit --db sandbox must satisfy the gate: %v", err)
		}
		if got := ConfigDir(); got != sandboxDir {
			t.Errorf("ConfigDir() = %q, want %q", got, sandboxDir)
		}
	})

	t.Run("the same cwd WITH --db main -> allowed, and points at main", func(t *testing.T) {
		resetDBContext(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		wt, sandboxDir := newGatedWorktree(t, "e-1668", true)
		t.Chdir(wt)
		if err := consume(t, "--db", "main"); err != nil {
			t.Fatalf("ConsumeDBFlags() = %v, want nil", err)
		}
		if err := guardWorktreeDBContext(); err != nil {
			t.Fatalf("an explicit --db main must satisfy the gate: %v", err)
		}
		if got := DBPath(); got != filepath.Join(home, ".config", "endless", "endless.db") {
			t.Errorf("DBPath() = %q, want main under the test HOME", got)
		}
		if strings.HasPrefix(DBPath(), sandboxDir) {
			t.Errorf("DBPath() = %q, must not be the sandbox", DBPath())
		}
	})
}

// TestMainPinRoutingSurvivesTheDeletion is the E-1700 routing regression,
// re-stated for the world without cwd self-detection. It exercises both
// self-dev use cases through the exact main.go guard
// (`if !HasExplicitDBContext() { PinMainDB() }`):
//
//  1. Developing endless (a real dev session): no flag is passed, so
//     HasExplicitDBContext() is false and session/pane-state writes are pinned
//     to MAIN — where the spawned task exists. This used to depend on
//     setDetectedContextDir deliberately NOT marking its guess explicit; with
//     the guess gone it falls out of "no flag, no context".
//  2. Testing endless (an explicit --db-dir): the pin is suppressed so writes
//     go to the chosen temp DB.
func TestMainPinRoutingSurvivesTheDeletion(t *testing.T) {
	// applyGuard mirrors cmd/endless-go/main.go's hook/tmux pin.
	applyGuard := func() {
		if !HasExplicitDBContext() {
			PinMainDB()
		}
	}

	t.Run("dev session (no flag): DB->main, config->XDG", func(t *testing.T) {
		resetDBContext(t)
		wt, sandboxDir := newGatedWorktree(t, "e-1700", true)
		t.Chdir(wt)
		// The worktree's .claude/settings.local.json exports this for a Claude
		// session's hook processes, which is what keeps config.json and logs on
		// the sandbox now that nothing self-detects it.
		t.Setenv("XDG_CONFIG_HOME", filepath.Dir(sandboxDir))

		applyGuard()

		if got := DBPath(); !strings.HasSuffix(got, filepath.Join(".config", "endless", "endless.db")) || strings.HasPrefix(got, sandboxDir) {
			t.Errorf("DBPath() = %q, want the real main DB (not under sandbox %q)", got, sandboxDir)
		}
		if got := ConfigDir(); got != sandboxDir {
			t.Errorf("ConfigDir() = %q, want sandbox %q (config/logs follow XDG)", got, sandboxDir)
		}
	})

	t.Run("test harness (explicit --db-dir): DB->that dir", func(t *testing.T) {
		resetDBContext(t)
		wt, _ := newGatedWorktree(t, "e-1700", true)
		t.Chdir(wt)
		explicit := t.TempDir()

		if err := consume(t, "--db-dir", explicit); err != nil {
			t.Fatalf("ConsumeDBFlags() = %v, want nil", err)
		}
		applyGuard() // no-ops: HasExplicitDBContext() is true

		if got := DBPath(); got != filepath.Join(explicit, "endless.db") {
			t.Errorf("DBPath() = %q, want explicit %q (flag beats the main pin)", got, filepath.Join(explicit, "endless.db"))
		}
	})
}
