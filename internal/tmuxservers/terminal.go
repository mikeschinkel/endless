package tmuxservers

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Opening a terminal window onto a server.
//
// A configured `tmux.terminal` wins: a shell command with `{cmd}` where the
// attach command goes, run by `sh -c` with `{cmd}` replaced by that command,
// shell-quoted. Without one, Endless detects the terminal it is running in
// and uses a built-in launcher for it. With neither, nothing is opened and
// the caller prints the attach command instead.
//
// Detection reads TERM_PROGRAM, which the common macOS and cross-platform
// terminals set. Inside tmux TERM_PROGRAM is tmux's own, so the attached
// client's terminal type (`#{client_termname}`) is asked instead; that names
// Ghostty, kitty and WezTerm, but not Terminal.app or iTerm2, which report a
// generic xterm type — for those, configure tmux.terminal.

// Launcher opens a terminal window running a command.
type Launcher struct {
	// Name says which launcher this is, for messages: the terminal's name, or
	// "tmux.terminal" for a configured one.
	Name string
	argv func(cmd []string) []string
}

// Argv is the command that opens a terminal running cmd.
func (l Launcher) Argv(cmd []string) []string {
	return l.argv(cmd)
}

// ResolveLauncher picks the launcher: the configured template, else a
// detected terminal. ok is false when there is neither.
func ResolveLauncher(template string) (l Launcher, ok bool) {
	if strings.TrimSpace(template) != "" {
		return templateLauncher(template), true
	}
	return detectLauncher(detectTerminal(), runtime.GOOS)
}

// templateLauncher runs a configured template through `sh -c`.
func templateLauncher(template string) Launcher {
	return Launcher{Name: "terminal (tmux.terminal)", argv: func(cmd []string) []string {
		line := template
		if strings.Contains(line, "{cmd}") {
			line = strings.ReplaceAll(line, "{cmd}", ShellJoin(cmd))
		} else {
			line += " " + ShellJoin(cmd)
		}
		return []string{"sh", "-c", line}
	}}
}

// detectTerminal names the terminal Endless is running in, lower-cased, or "".
func detectTerminal() string {
	tp := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if tp != "" && tp != "tmux" && tp != "screen" {
		return tp
	}
	if os.Getenv("TMUX") != "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{client_termname}").Output()
		if err == nil {
			return strings.ToLower(strings.TrimSpace(string(out)))
		}
	}
	return strings.ToLower(os.Getenv("TERM"))
}

// detectLauncher maps a terminal name to its built-in launcher. Pure.
func detectLauncher(term, goos string) (Launcher, bool) {
	switch {
	case strings.Contains(term, "ghostty"):
		if goos == "darwin" {
			return Launcher{Name: "Ghostty", argv: func(cmd []string) []string {
				return append([]string{"open", "-na", "Ghostty.app", "--args", "-e"}, cmd...)
			}}, true
		}
		return Launcher{Name: "Ghostty", argv: func(cmd []string) []string {
			return append([]string{"ghostty", "-e"}, cmd...)
		}}, true
	case strings.Contains(term, "wezterm"):
		return Launcher{Name: "WezTerm", argv: func(cmd []string) []string {
			return append([]string{"wezterm", "start", "--"}, cmd...)
		}}, true
	case strings.Contains(term, "kitty"):
		return Launcher{Name: "kitty", argv: func(cmd []string) []string {
			return append([]string{"kitty", "--detach"}, cmd...)
		}}, true
	case term == "apple_terminal" && goos == "darwin":
		return Launcher{Name: "Terminal", argv: func(cmd []string) []string {
			return []string{"osascript",
				"-e", fmt.Sprintf(`tell application "Terminal" to do script "%s"`, appleScriptEscape(ShellJoin(cmd))),
				"-e", `tell application "Terminal" to activate`}
		}}, true
	case term == "iterm.app" && goos == "darwin":
		return Launcher{Name: "iTerm2", argv: func(cmd []string) []string {
			return []string{"osascript",
				"-e", fmt.Sprintf(`tell application "iTerm" to create window with default profile command "%s"`, appleScriptEscape(ShellJoin(cmd))),
				"-e", `tell application "iTerm" to activate`}
		}}, true
	}
	return Launcher{}, false
}

func appleScriptEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// launchWait is how long Open waits for a launcher to fail before taking its
// still running as success. Most launchers (open, osascript, kitty --detach)
// hand off and exit at once; some (wezterm start, ghostty on Linux) run for
// the life of the window, so a launcher still running is not a failure.
var launchWait = 1500 * time.Millisecond

// Open opens a terminal window attached to the server's session. The terminal
// is started in its own process group with no stdio and without TMUX, so it
// outlives this command and its tmux client is never refused as nested.
func (l Launcher) Open(s Server) error {
	argv := l.Argv(s.AttachArgv())
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = withoutTmux(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", l.Name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return fmt.Errorf("%s: %v: %s", l.Name, err, msg)
			}
			return fmt.Errorf("%s: %w", l.Name, err)
		}
	case <-time.After(launchWait):
	}
	return nil
}
