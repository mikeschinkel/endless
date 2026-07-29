package verify

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/go-dt"
)

func TestParseRunner(t *testing.T) {
	cases := []struct {
		runner      string
		wantFamily  string
		wantVariant string
		wantErr     bool
	}{
		{runner: "gotest", wantFamily: "gotest"},
		{runner: "pytest", wantFamily: "pytest"},
		{runner: "pytest/uv", wantFamily: "pytest", wantVariant: "uv"},
		{runner: "vnd.newclarity.foo/bar", wantFamily: "vnd.newclarity.foo", wantVariant: "bar"},
		{runner: "", wantErr: true},
		{runner: "/uv", wantErr: true},
		{runner: "pytest/", wantErr: true},
		{runner: "a/b/c", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.runner, func(t *testing.T) {
			family, variant, err := parseRunner(tc.runner)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseRunner(%q) = (%q,%q), want error", tc.runner, family, variant)
				}
				if !errors.Is(err, ErrMalformedRunner) {
					t.Errorf("err = %v, want ErrMalformedRunner", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRunner(%q) unexpected err: %v", tc.runner, err)
			}
			if family != tc.wantFamily || variant != tc.wantVariant {
				t.Errorf("parseRunner(%q) = (%q,%q), want (%q,%q)",
					tc.runner, family, variant, tc.wantFamily, tc.wantVariant)
			}
		})
	}
}

// The venv's plain pytest executable is preferred over every other launcher
// because it survives HOME-isolation with no network — the fix at the heart of
// E-1605.
func TestResolvePytestLauncher_PrefersVenv(t *testing.T) {
	root := dt.DirPath(t.TempDir())
	venv := writeExecutable(t, string(root), ".venv/bin/pytest")

	for _, variant := range []string{"", "uv"} {
		launcher, err := resolvePytestLauncher(variant, root)
		if err != nil {
			t.Fatalf("variant %q: %v", variant, err)
		}
		if len(launcher) != 1 || launcher[0] != venv {
			t.Errorf("variant %q launcher = %v, want [%s]", variant, launcher, venv)
		}
	}
}

// With no venv and nothing on PATH, resolution fails loudly for both variants
// rather than silently running a bare pytest that isolation broke.
func TestResolvePytestLauncher_NoneAvailable(t *testing.T) {
	root := dt.DirPath(t.TempDir())
	t.Setenv("PATH", "")

	for _, variant := range []string{"", "uv"} {
		_, err := resolvePytestLauncher(variant, root)
		if !errors.Is(err, ErrPytestLauncher) {
			t.Errorf("variant %q err = %v, want ErrPytestLauncher", variant, err)
		}
	}
}

// The bare "" family falls through to a pytest on PATH; the declared "uv" variant
// does not — declaring uv means the project's pytest, never a stray PATH one.
func TestResolvePytestLauncher_BareFallsToPath(t *testing.T) {
	root := dt.DirPath(t.TempDir())
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pyPath := writeExecutable(t, binDir, "pytest")
	t.Setenv("PATH", binDir)

	bare, err := resolvePytestLauncher("", root)
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	if len(bare) != 1 || bare[0] != "pytest" {
		t.Errorf("bare launcher = %v, want [pytest]", bare)
	}
	_ = pyPath

	if _, err = resolvePytestLauncher("uv", root); !errors.Is(err, ErrPytestLauncher) {
		t.Errorf("uv variant err = %v, want ErrPytestLauncher (no fallthrough to PATH pytest)", err)
	}
}

// writeExecutable writes a mode-0755 stub at dir/rel (creating parents) and
// returns its full path, for standing in as a resolvable launcher executable.
func writeExecutable(t *testing.T, dir, rel string) (path string) {
	t.Helper()
	path = filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
