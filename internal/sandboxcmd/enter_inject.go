package sandboxcmd

import (
	"fmt"
	"os"
	"path/filepath"
)

// shellInjection describes how to launch an interactive shell so that
// 'eval $(endless shell-init)' runs after the user's normal rc files.
// Path entries point at a tempdir (zsh: ZDOTDIR) or tempfile (bash:
// --rcfile target) that Clean() removes after the supervised subshell exits.
//
// Returned by buildShellInjection. For shells we don't know how to inject
// into (fish, sh, dash, etc.) the zero value is used: no extra args, no
// extra env, no-op cleanup. The user gets a working subshell without esu/
// esp/esf — filed as E-1183 for fish if/when someone asks.
type shellInjection struct {
	Args  []string
	Env   []string
	Clean func()
}

const _shellInjectionContent = `# >>> endless-sandbox auto-inject (E-1182) >>>
[ -f "$HOME/%[1]s" ] && source "$HOME/%[1]s"
eval "$(endless shell-init)"
command -v esu >/dev/null 2>&1 || \
    echo "endless-sandbox: warning: shell-init failed to define esu (helpers unavailable)" >&2
# <<< endless-sandbox auto-inject <<<
`

func buildShellInjection(shellPath string) shellInjection {
	switch filepath.Base(shellPath) {
	case "zsh":
		return buildZshInjection()
	case "bash":
		return buildBashInjection()
	default:
		return shellInjection{Clean: func() {}}
	}
}

func buildZshInjection() shellInjection {
	tmp, err := os.MkdirTemp("", "endless-sandbox-zdotdir-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox: warning: create ZDOTDIR tempdir: %v (helpers unavailable)\n", err)
		return shellInjection{Clean: func() {}}
	}
	if home := os.Getenv("HOME"); home != "" {
		// Symlink ~/.zshenv into ZDOTDIR so zsh still loads it. Without this,
		// setting ZDOTDIR would silently disable .zshenv (zsh looks for it
		// in $ZDOTDIR before $HOME).
		userEnv := filepath.Join(home, ".zshenv")
		if _, err := os.Stat(userEnv); err == nil {
			_ = os.Symlink(userEnv, filepath.Join(tmp, ".zshenv"))
		}
	}
	rc := filepath.Join(tmp, ".zshrc")
	if err := os.WriteFile(rc, []byte(fmt.Sprintf(_shellInjectionContent, ".zshrc")), 0o644); err != nil {
		os.RemoveAll(tmp)
		fmt.Fprintf(os.Stderr, "endless-sandbox: warning: write zshrc: %v (helpers unavailable)\n", err)
		return shellInjection{Clean: func() {}}
	}
	return shellInjection{
		Env:   []string{"ZDOTDIR=" + tmp},
		Clean: func() { os.RemoveAll(tmp) },
	}
}

func buildBashInjection() shellInjection {
	f, err := os.CreateTemp("", "endless-sandbox-bashrc-*.sh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox: warning: create bashrc tempfile: %v (helpers unavailable)\n", err)
		return shellInjection{Clean: func() {}}
	}
	if _, err := f.WriteString(fmt.Sprintf(_shellInjectionContent, ".bashrc")); err != nil {
		f.Close()
		os.Remove(f.Name())
		fmt.Fprintf(os.Stderr, "endless-sandbox: warning: write bashrc: %v (helpers unavailable)\n", err)
		return shellInjection{Clean: func() {}}
	}
	f.Close()
	return shellInjection{
		Args:  []string{"--rcfile", f.Name()},
		Clean: func() { os.Remove(f.Name()) },
	}
}
