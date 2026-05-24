package monitor

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsSandboxActive(t *testing.T) {
	cache := t.TempDir()
	sandboxRoot := filepath.Join(cache, "endless", "sandboxes")
	otherConfig := filepath.Join(t.TempDir(), "elsewhere")

	cases := []struct {
		name     string
		xdgCfg   string
		xdgCache string
		want     bool
	}{
		{
			name:     "no XDG_CONFIG_HOME, default ~/.config",
			xdgCfg:   "",
			xdgCache: cache,
			want:     false,
		},
		{
			name:     "config under sandbox root",
			xdgCfg:   filepath.Join(sandboxRoot, "worktree-e-1354"),
			xdgCache: cache,
			want:     true,
		},
		{
			name:     "config outside sandbox root",
			xdgCfg:   otherConfig,
			xdgCache: cache,
			want:     false,
		},
		{
			name:     "config in unrelated sandboxes-named dir",
			xdgCfg:   filepath.Join(t.TempDir(), "fake", "sandboxes", "x"),
			xdgCache: cache,
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.xdgCfg)
			t.Setenv("XDG_CACHE_HOME", tc.xdgCache)
			if got := IsSandboxActive(); got != tc.want {
				t.Errorf("IsSandboxActive() = %v, want %v (XDG_CONFIG_HOME=%q XDG_CACHE_HOME=%q)",
					got, tc.want, tc.xdgCfg, tc.xdgCache)
			}
		})
	}
}

func TestForceRealDB(t *testing.T) {
	// dbPathOverride is a package var; ensure each sub-test starts and ends clean
	// so it never leaks into DB()-using tests.
	reset := func() { dbPathOverride = "" }
	realSuffix := filepath.Join(".config", "endless", "endless.db")

	t.Run("redirects DB to real path without mutating env", func(t *testing.T) {
		reset()
		t.Cleanup(reset)
		cache := t.TempDir()
		sandbox := filepath.Join(cache, "endless", "sandboxes", "worktree-e-test")
		t.Setenv("XDG_CACHE_HOME", cache)
		t.Setenv("XDG_CONFIG_HOME", sandbox)

		if !IsSandboxActive() {
			t.Fatal("precondition: expected sandbox routing to be active")
		}
		if got := DBPath(); !strings.HasPrefix(got, sandbox) {
			t.Fatalf("precondition: DBPath() = %q, want under sandbox %q", got, sandbox)
		}

		ForceRealDB()

		if got := DBPath(); !strings.HasSuffix(got, realSuffix) {
			t.Errorf("after ForceRealDB(): DBPath() = %q, want suffix %q", got, realSuffix)
		}
		if strings.HasPrefix(DBPath(), sandbox) {
			t.Errorf("after ForceRealDB(): DBPath() = %q still under sandbox", DBPath())
		}
		// Env must be untouched: XDG_CONFIG_HOME stays sandbox-routed, so
		// IsSandboxActive() (and the log / config.json reads built on it) is
		// unaffected — only the DB path is redirected.
		if !IsSandboxActive() {
			t.Error("ForceRealDB() must not mutate env: IsSandboxActive() should still be true")
		}
		// Backups follow the DB, not ConfigDir().
		wantBackups := filepath.Join(filepath.Dir(DBPath()), "backups")
		if strings.HasPrefix(wantBackups, sandbox) {
			t.Errorf("backup dir %q should not be under sandbox after override", wantBackups)
		}
	})

	t.Run("no-op when not sandbox-routed", func(t *testing.T) {
		reset()
		t.Cleanup(reset)
		cache := t.TempDir()
		config := filepath.Join(t.TempDir(), "myconfig")
		t.Setenv("XDG_CACHE_HOME", cache)
		t.Setenv("XDG_CONFIG_HOME", config)

		if IsSandboxActive() {
			t.Fatal("precondition: expected no sandbox routing")
		}
		before := DBPath()

		ForceRealDB()

		if got := DBPath(); got != before {
			t.Errorf("ForceRealDB() should be a no-op outside a sandbox: DBPath() = %q, want %q", got, before)
		}
	})
}
