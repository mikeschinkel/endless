package tmuxservers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The tmux configuration each Endless server starts with: the user's own
// configuration first, then what Endless needs on top of it. Written beside
// the socket on every start, because it names this binary's path and the
// socket, and either can change between starts.
//
// tmux reads only one -f file, so the Endless file sources the user's. tmux's
// own default search is reproduced: /etc/tmux.conf, then the first of
// ~/.tmux.conf, $XDG_CONFIG_HOME/tmux/tmux.conf and ~/.config/tmux/tmux.conf.
//
// What Endless adds:
//
//   - allow-rename off, so a program in a pane cannot retitle its window. A
//     task window is named for its task, and that name is what restore and
//     the resume sweep read back.
//   - @endless_server, naming the server's role for anything that inspects it.
//   - on the task-session server, hooks that re-record the open task windows
//     whenever a window is linked, unlinked or renamed, and — since the record
//     keeps tab order (E-2385) — moved or swapped (see state.go).
//
// The status line and @server_uuid are applied by `tmux init` (Init), not
// here: init is the one place that owns them, on every server.

// RecordHooks are the task-session server's hooks that re-record its task
// windows. A reorder fires none of the window-* hooks, so the commands that
// reorder are hooked after they run.
var RecordHooks = []string{"window-linked", "window-unlinked", "window-renamed",
	"after-move-window", "after-swap-window"}

// writeConf writes the server's configuration file, 0600.
func writeConf(s Server, self string) error {
	body := confBody(s, self, userConfFiles())
	if err := os.WriteFile(s.ConfFile(), []byte(body), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", s.ConfFile(), err)
	}
	return nil
}

// confBody renders the configuration. Pure.
func confBody(s Server, self string, userConf []string) string {
	var b strings.Builder
	b.WriteString("# Written by Endless (`endless tmux start`) on every start of this server.\n")
	b.WriteString("# Edits are overwritten; put your own settings in your tmux.conf.\n\n")
	for _, f := range userConf {
		fmt.Fprintf(&b, "source-file -q %s\n", tmuxQuote(f))
	}
	b.WriteString("\nset -g allow-rename off\n")
	fmt.Fprintf(&b, "set -g @endless_server %s\n", tmuxQuote(string(s.Role)))
	if s.Role == RoleTasks {
		record := ShellJoin([]string{self, "tmux", "record-windows",
			"--socket", s.Socket, "--session", s.Session})
		b.WriteString("\n# Keep the record of open task windows that `endless tmux start` restores.\n")
		for _, hook := range RecordHooks {
			fmt.Fprintf(&b, "set-hook -g %s %s\n", hook,
				tmuxQuote("run-shell -b "+tmuxQuote(record)))
		}
	}
	return b.String()
}

// userConfFiles is tmux's default configuration search: the system file, and
// the first user file that exists.
func userConfFiles() []string {
	var files []string
	if fileExists("/etc/tmux.conf") {
		files = append(files, "/etc/tmux.conf")
	}
	home, _ := os.UserHomeDir()
	var candidates []string
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".tmux.conf"))
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		candidates = append(candidates, filepath.Join(x, "tmux", "tmux.conf"))
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "tmux", "tmux.conf"))
	}
	for _, c := range candidates {
		if fileExists(c) {
			return append(files, c)
		}
	}
	return files
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// tmuxQuote double-quotes a string for a tmux configuration file, escaping
// what tmux interprets inside double quotes: backslash, the quote itself, and
// `$`, which tmux would expand as an environment variable.
func tmuxQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
	return `"` + r.Replace(s) + `"`
}
