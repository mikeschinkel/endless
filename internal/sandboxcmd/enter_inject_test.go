package sandboxcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildShellInjectionDispatchesByBasename(t *testing.T) {
	cases := []struct {
		shell      string
		wantArgs   bool // bash sets --rcfile in Args
		wantEnv    bool // zsh sets ZDOTDIR in Env
		wantNoop   bool // unsupported shells get the noop
	}{
		{shell: "/bin/zsh", wantEnv: true},
		{shell: "/usr/bin/zsh", wantEnv: true},
		{shell: "/bin/bash", wantArgs: true},
		{shell: "/usr/local/bin/bash", wantArgs: true},
		{shell: "/usr/local/bin/fish", wantNoop: true},
		{shell: "/bin/sh", wantNoop: true},
		{shell: "/bin/dash", wantNoop: true},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.shell), func(t *testing.T) {
			inj := buildShellInjection(tc.shell)
			defer inj.Clean()

			hasArgs := len(inj.Args) > 0
			hasEnv := len(inj.Env) > 0

			if tc.wantNoop {
				if hasArgs || hasEnv {
					t.Fatalf("expected noop injection for %s, got Args=%v Env=%v", tc.shell, inj.Args, inj.Env)
				}
				return
			}
			if tc.wantArgs && !hasArgs {
				t.Fatalf("expected --rcfile Args for %s, got none", tc.shell)
			}
			if tc.wantEnv && !hasEnv {
				t.Fatalf("expected ZDOTDIR Env for %s, got none", tc.shell)
			}
		})
	}
}

func TestZshInjectionWritesZshrcWithEval(t *testing.T) {
	inj := buildZshInjection()
	defer inj.Clean()

	if len(inj.Env) != 1 || !strings.HasPrefix(inj.Env[0], "ZDOTDIR=") {
		t.Fatalf("expected single ZDOTDIR= env var, got %v", inj.Env)
	}
	zdotdir := strings.TrimPrefix(inj.Env[0], "ZDOTDIR=")

	zshrc := filepath.Join(zdotdir, ".zshrc")
	body, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	content := string(body)
	for _, want := range []string{
		`source "$HOME/.zshrc"`,
		`eval "$(endless shell-init)"`,
		`command -v esu`,
		"endless-sandbox auto-inject",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("zshrc missing %q\n--- content ---\n%s", want, content)
		}
	}
}

func TestBashInjectionWritesRcfileWithEval(t *testing.T) {
	inj := buildBashInjection()
	defer inj.Clean()

	if len(inj.Args) != 2 || inj.Args[0] != "--rcfile" {
		t.Fatalf("expected --rcfile <path> Args, got %v", inj.Args)
	}
	rcfile := inj.Args[1]
	body, err := os.ReadFile(rcfile)
	if err != nil {
		t.Fatalf("read rcfile: %v", err)
	}
	content := string(body)
	for _, want := range []string{
		`source "$HOME/.bashrc"`,
		`eval "$(endless shell-init)"`,
		`command -v esu`,
		"endless-sandbox auto-inject",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rcfile missing %q\n--- content ---\n%s", want, content)
		}
	}
}

func TestZshInjectionCleanRemovesTempdir(t *testing.T) {
	inj := buildZshInjection()
	zdotdir := strings.TrimPrefix(inj.Env[0], "ZDOTDIR=")
	if _, err := os.Stat(zdotdir); err != nil {
		t.Fatalf("zdotdir missing before clean: %v", err)
	}
	inj.Clean()
	if _, err := os.Stat(zdotdir); !os.IsNotExist(err) {
		t.Fatalf("zdotdir still exists after Clean(): %v", err)
	}
}

func TestBashInjectionCleanRemovesRcfile(t *testing.T) {
	inj := buildBashInjection()
	rcfile := inj.Args[1]
	if _, err := os.Stat(rcfile); err != nil {
		t.Fatalf("rcfile missing before clean: %v", err)
	}
	inj.Clean()
	if _, err := os.Stat(rcfile); !os.IsNotExist(err) {
		t.Fatalf("rcfile still exists after Clean(): %v", err)
	}
}

// TestZshInjectionSourcesUserZshenv asserts ~/.zshenv (if present) is
// symlinked into the ZDOTDIR so zsh still loads it under the override.
func TestZshInjectionSourcesUserZshenv(t *testing.T) {
	// Set up fake HOME with a .zshenv
	tmpHome := t.TempDir()
	zshenvPath := filepath.Join(tmpHome, ".zshenv")
	if err := os.WriteFile(zshenvPath, []byte("# fake user zshenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)

	inj := buildZshInjection()
	defer inj.Clean()

	zdotdir := strings.TrimPrefix(inj.Env[0], "ZDOTDIR=")
	link := filepath.Join(zdotdir, ".zshenv")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
	if target != zshenvPath {
		t.Fatalf("symlink target = %s, want %s", target, zshenvPath)
	}
}
