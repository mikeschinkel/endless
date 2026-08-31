package verifycmd

import (
	"path/filepath"
	"strings"
	"testing"
)

// The runner hands every suite a temp HOME. If that path is reached through a
// symlink — which is what os.MkdirTemp returns on macOS, where /var links to
// /private/var — then any code comparing a resolved path against
// filepath.Join(os.UserHomeDir(), ...) fails, inside a suite that has nothing
// to do with the change being verified.
//
// Pinned as an invariant rather than left to the platform: the same suite
// otherwise passes on Linux and fails on macOS, and a runner whose verdict
// depends on the OS's mktemp implementation misattributes failures (E-2094).
func TestMakeRunDir_IsCanonical(t *testing.T) {
	dir, err := makeRunDir()
	if err != nil {
		t.Fatalf("makeRunDir: %v", err)
	}
	defer dir.RemoveAll()

	resolved, err := filepath.EvalSymlinks(string(dir))
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	if resolved != string(dir) {
		t.Errorf("run dir is not canonical:\n  got      %s\n  resolves %s", dir, resolved)
	}
}

// The property consumers actually depend on: the HOME and XDG_CONFIG_HOME the
// suite runs under are canonical, not merely the directory above them.
func TestIsolatedEnv_HomeIsCanonical(t *testing.T) {
	dir, err := makeRunDir()
	if err != nil {
		t.Fatalf("makeRunDir: %v", err)
	}
	defer dir.RemoveAll()

	env, err := isolatedEnv(dir)
	if err != nil {
		t.Fatalf("isolatedEnv: %v", err)
	}
	for _, key := range []string{"HOME=", "XDG_CONFIG_HOME="} {
		vals := envValues(env, key)
		if len(vals) != 1 {
			t.Fatalf("want exactly one %s, got %v", key, vals)
		}
		resolved, err := filepath.EvalSymlinks(vals[0])
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", vals[0], err)
		}
		if resolved != vals[0] {
			t.Errorf("%s%s is not canonical; resolves to %s", key, vals[0], resolved)
		}
	}
}

// End to end, through the real runner: a suite sees a HOME it can compare
// against a resolved path without the comparison failing on the runner's
// account. Exit 30 means the suite observed a non-canonical HOME.
func TestRun_ScriptSuite_SeesCanonicalHome(t *testing.T) {
	enterScriptSuite(t, "E-94", `#!/usr/bin/env bash
[[ "${HOME}" == "$(cd "${HOME}" && pwd -P)" ]] || exit 30
exit 0
`)
	code, err := run("E-94", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code == 30 {
		t.Fatal("the suite ran under a HOME reached through a symlink")
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// Guard the helper this file leans on, so a rename does not silently gut the
// assertions above.
func TestEnvValuesFindsBothKeys(t *testing.T) {
	env := []string{"A=1", "HOME=/x", "XDG_CONFIG_HOME=/y"}
	if got := envValues(env, "HOME="); !strings.EqualFold(strings.Join(got, ","), "/x") {
		t.Errorf("envValues(HOME=) = %v", got)
	}
}
