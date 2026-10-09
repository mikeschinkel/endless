package verifycmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// callerGoEnv asks go, under the test process's environment, for key — the
// value the runner should hand a suite.
func callerGoEnv(t *testing.T, key string) (val string) {
	t.Helper()
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", key, err)
	}
	val = strings.TrimSpace(string(out))
	if val == "" {
		t.Fatalf("go env %s is empty", key)
	}
	return val
}

// The suite runs under a temp HOME, and Go locates its build cache under HOME
// by default. Without the caller's cache pinned, every suite that builds Go
// compiles every dependency cold (E-1908). The temp HOME and XDG_CONFIG_HOME
// must survive alongside it: the caches are pinned, the config is not.
func TestIsolatedEnv_CarriesCallerBuildCaches(t *testing.T) {
	t.Setenv("GOCACHE", "")
	t.Setenv("GOMODCACHE", "")
	wantCache := callerGoEnv(t, "GOCACHE")
	wantMod := callerGoEnv(t, "GOMODCACHE")

	dir, err := makeRunDir()
	if err != nil {
		t.Fatalf("makeRunDir: %v", err)
	}
	defer dir.RemoveAll()

	env, err := isolatedEnv(dir, os.Environ())
	if err != nil {
		t.Fatalf("isolatedEnv: %v", err)
	}
	if got := envValues(env, "GOCACHE="); len(got) != 1 || got[0] != wantCache {
		t.Errorf("GOCACHE = %v, want [%s]", got, wantCache)
	}
	if got := envValues(env, "GOMODCACHE="); len(got) != 1 || got[0] != wantMod {
		t.Errorf("GOMODCACHE = %v, want [%s]", got, wantMod)
	}
	if got := envValues(env, "HOME="); len(got) != 1 || got[0] != string(dir.Join("home")) {
		t.Errorf("HOME = %v, want the temp HOME under %s", got, dir)
	}
	if got := envValues(env, "XDG_CONFIG_HOME="); len(got) != 1 || got[0] != string(dir.Join("xdg")) {
		t.Errorf("XDG_CONFIG_HOME = %v, want the temp XDG under %s", got, dir)
	}
	if _, err := exec.LookPath("uv"); err == nil {
		if got := envValues(env, "UV_CACHE_DIR="); len(got) != 1 || got[0] == "" {
			t.Errorf("UV_CACHE_DIR = %v, want the caller's uv cache", got)
		}
	}
}

// A cache the caller set explicitly passes through as-is, exactly once.
func TestIsolatedEnv_KeepsCallerSetCache(t *testing.T) {
	t.Setenv("GOCACHE", "/explicit/go-build")

	dir, err := makeRunDir()
	if err != nil {
		t.Fatalf("makeRunDir: %v", err)
	}
	defer dir.RemoveAll()

	env, err := isolatedEnv(dir, os.Environ())
	if err != nil {
		t.Fatalf("isolatedEnv: %v", err)
	}
	if got := envValues(env, "GOCACHE="); len(got) != 1 || got[0] != "/explicit/go-build" {
		t.Errorf("GOCACHE = %v, want [/explicit/go-build]", got)
	}
}

// A toolchain that is not installed is skipped, not an error.
func TestCallerBuildCaches_SkipsMissingTool(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	if got := callerBuildCaches([]string{"PATH=" + empty}); len(got) != 0 {
		t.Errorf("callerBuildCaches with no toolchain = %v, want none", got)
	}
}

// End to end, through the real runner: go inside a suite reports the caller's
// cache, not one under the runner's temp HOME. Exit 31 means it did not.
func TestRun_ScriptSuite_SeesCallerGOCACHE(t *testing.T) {
	root := enterScriptSuite(t, "E-95", "")
	t.Setenv("GOCACHE", "")
	want := callerGoEnv(t, "GOCACHE")
	script := filepath.Join(root, verify.SuitesDir, "e-95", verify.ScriptFile)
	body := fmt.Sprintf("#!/usr/bin/env bash\n[[ \"$(go env GOCACHE)\" == %q ]] || exit 31\nexit 0\n", want)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	code, err := run("E-95", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code == 31 {
		t.Fatalf("the suite's go saw a GOCACHE other than the caller's %s", want)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}
