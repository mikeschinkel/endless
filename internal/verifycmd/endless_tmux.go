package verifycmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// Endless's own tmux servers, kept out of a run's reach (E-2240).
//
// Endless's console and task-session servers live at fixed socket paths,
// `/tmp/endless-<uid>/` by default — deliberately independent of HOME and of
// TMUX_TMPDIR, so the isolation that relocates both does not relocate them.
// While the user's task-session server runs, every spawn routes its window
// there, so a suite that spawns would reach the user's real server.
//
// The only thing that moves those sockets is `tmux.socket_dir` in the user's
// config file, so the runner writes one into the run's temporary HOME,
// pointing inside the run's private tmux directory, and kills whatever servers
// a suite started there when the run ends.
//
// _guard.sh refuses a suite when an Endless config directory is reachable,
// because under the runner none should be. This file is the one exception it
// accepts: a directory holding nothing but runnerTmuxConfig's exact shape,
// which a real config never has. Keep the two in step.

// endlessTmuxDirName is the socket directory's name inside the run's private
// tmux directory.
const endlessTmuxDirName = "endless"

// runnerTmuxConfig is the whole config file the runner writes. _guard.sh
// matches this shape exactly.
func runnerTmuxConfig(socketDir dt.DirPath) []byte {
	return fmt.Appendf(nil, "{\"tmux\":{\"socket_dir\":%q}}\n", string(socketDir))
}

// writeEndlessTmuxConfig writes runnerTmuxConfig into both spellings of the
// isolated config directory: Endless's config loader reads ~/.config on macOS
// and XDG_CONFIG_HOME on Linux.
func writeEndlessTmuxConfig(env []string, tmuxDir dt.DirPath) (err error) {
	body := runnerTmuxConfig(tmuxDir.Join(endlessTmuxDirName))
	for _, dir := range []string{
		filepath.Join(envValue(env, "HOME"), ".config", "endless"),
		filepath.Join(envValue(env, "XDG_CONFIG_HOME"), "endless"),
	} {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			err = doterr.NewErr(ErrIsolatingEnv, err, "dir", dir)
			goto end
		}
		file := filepath.Join(dir, "config.json")
		if err = os.WriteFile(file, body, 0o644); err != nil {
			err = doterr.NewErr(ErrIsolatingEnv, err, "file", file)
			goto end
		}
	}
end:
	return err
}

// killEndlessTmux stops every server with a socket in the run's Endless socket
// directory. Best-effort and silent, like killPrivateTmux, which removes the
// directory afterwards.
func killEndlessTmux(tmuxDir dt.DirPath) {
	dir := string(tmuxDir.Join(endlessTmuxDirName))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		_ = tmuxCommand(os.Environ(), tmuxDir, "-S", filepath.Join(dir, e.Name()), "kill-server").Run()
	}
}

// envValue is the value env gives key, or "".
func envValue(env []string, key string) (v string) {
	for _, kv := range env {
		if rest, ok := strings.CutPrefix(kv, key+"="); ok {
			v = rest
		}
	}
	return v
}
