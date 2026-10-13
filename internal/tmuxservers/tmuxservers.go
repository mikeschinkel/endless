// Package tmuxservers locates and starts Endless's own tmux servers (E-2240).
//
// Endless runs on two tmux servers of its own, never on the user's default one,
// which stays theirs:
//
//   - the console server, session `console`: one window per use — a project
//     monitor above a shell prompt, one per project.
//   - the task-session server, session `tasks` by default: where Claude task
//     sessions live.
//
// Every command that reaches one of them addresses it by a FULL socket path,
// `tmux -S <path>`, never `-L <name>`. A `-L` name resolves inside
// TMUX_TMPDIR, which differs between environments, so two shells could reach
// two different servers of the same name; a full path cannot be redirected by
// the environment, and it lets liveness ask one known socket.
//
// The socket directory defaults to `/tmp/endless-<uid>`: short (a socket path
// is limited to ~104 bytes), and independent of HOME. Not `$TMPDIR`, which on
// macOS is a long `/var/folders/…` path, and not under the config directory,
// which grows with HOME and holds config rather than runtime state. `/tmp` is
// shared, so the directory is created 0700 and refused when someone else owns
// it or others can write it: a pre-created directory would hand another user
// the sockets.
//
// Every location is read from the user's own config file only. See
// config.Tmux for why a project may not choose a server.
package tmuxservers

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mikeschinkel/endless/internal/config"
)

const (
	// DefaultConsoleServer is the console server's socket file name.
	DefaultConsoleServer = "endless-console"
	// DefaultTasksServer is the task-session server's socket file name.
	DefaultTasksServer = "endless-tasks"
	// ConsoleSession is the console server's one session. Not configurable:
	// nothing outside Endless addresses it, and its windows are the variable.
	ConsoleSession = "console"
	// DefaultTasksSession is the session task windows open in.
	DefaultTasksSession = "tasks"
)

// Role names which of the two servers a Server is.
type Role string

const (
	RoleConsole Role = "console"
	RoleTasks   Role = "tasks"
)

// Server is one of Endless's tmux servers: where its socket is and the
// session Endless uses on it.
type Server struct {
	Role    Role
	Socket  string
	Session string
}

// Servers is everything Resolve reads from config.
type Servers struct {
	Dir      string
	Console  Server
	Tasks    Server
	Terminal string // the configured tmux.terminal template, "" for detection
}

// DefaultDir is the socket directory used when tmux.socket_dir is unset.
func DefaultDir() string {
	return filepath.Join("/tmp", fmt.Sprintf("endless-%d", os.Getuid()))
}

// Resolve reads the tmux settings from the user's config and fills in every
// default. A config file that cannot be read is an error rather than a silent
// fall back to the defaults: reaching a different server than the one the
// user configured is exactly the failure fixed socket paths exist to prevent.
func Resolve() (s Servers, err error) {
	var cfg *config.EndlessConfig
	cfg, err = config.Load("")
	if err != nil {
		err = fmt.Errorf("read the tmux settings: %w", err)
		goto end
	}
	s = resolveFrom(cfg.Tmux)
end:
	return s, err
}

// resolveFrom applies the defaults to one tmux config object. Pure.
func resolveFrom(t config.Tmux) Servers {
	dir := t.SocketDir
	if dir == "" {
		dir = DefaultDir()
	}
	dir = expandHome(dir)
	console := orDefault(t.ConsoleServer, DefaultConsoleServer)
	tasks := orDefault(t.TasksServer, DefaultTasksServer)
	return Servers{
		Dir:      dir,
		Console:  Server{Role: RoleConsole, Socket: filepath.Join(dir, console), Session: ConsoleSession},
		Tasks:    Server{Role: RoleTasks, Socket: filepath.Join(dir, tasks), Session: orDefault(t.TasksSession, DefaultTasksSession)},
		Terminal: t.Terminal,
	}
}

// RunningTasks returns the task-session server when it is running with its
// session — where every spawned task window goes (E-2240).
func RunningTasks() (Server, bool) {
	s, err := Resolve()
	if err != nil || !s.Tasks.HasSession() {
		return Server{}, false
	}
	return s.Tasks, true
}

// SocketDir is the configured socket directory, or the default when the config
// cannot be read. For observation (liveness), which must never fail over a
// config file; commands that START or ADDRESS a server use Resolve.
func SocketDir() string {
	s, err := Resolve()
	if err != nil {
		return DefaultDir()
	}
	return s.Dir
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// ErrUnsafeSocketDir is returned by EnsureDir for a directory Endless will not
// put its sockets in.
var ErrUnsafeSocketDir = errors.New("unsafe tmux socket directory")

// EnsureDir creates the socket directory 0700 when missing, and refuses one
// that is not a directory (a symlink included), is not owned by this user, or
// is writable by group or others.
func EnsureDir(dir string) error {
	err := os.Mkdir(dir, 0o700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		// A missing parent is created too; the default's parent is /tmp,
		// which always exists, so this is for a configured directory.
		if errors.Is(err, os.ErrNotExist) {
			err = os.MkdirAll(dir, 0o700)
		}
		if err != nil {
			return fmt.Errorf("create the tmux socket directory %s: %w", dir, err)
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect the tmux socket directory %s: %w", dir, err)
	}
	return checkDir(dir, info, os.Getuid())
}

// checkDir is EnsureDir's verdict on an existing directory. Split out so the
// refusals are tested without chown.
func checkDir(dir string, info os.FileInfo, uid int) error {
	if !info.IsDir() {
		return fmt.Errorf("%w: %s exists but is not a directory", ErrUnsafeSocketDir, dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
		return fmt.Errorf("%w: %s is owned by uid %d, not by you (uid %d)",
			ErrUnsafeSocketDir, dir, st.Uid, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: %s is writable by others (mode %04o)",
			ErrUnsafeSocketDir, dir, info.Mode().Perm())
	}
	return nil
}

// ── talking to a server ───────────────────────────────────────────────────

// Command builds `tmux -S <socket> <args…>` with TMUX and TMUX_PANE removed
// from its environment, so the answer never depends on which server the
// caller happens to be inside, and an attach is never refused as nested.
func (s Server) Command(args ...string) *exec.Cmd {
	cmd := exec.Command("tmux", append([]string{"-S", s.Socket}, args...)...)
	cmd.Env = withoutTmux(os.Environ())
	return cmd
}

// Run runs one tmux command, returning tmux's own message on failure.
func (s Server) Run(args ...string) error {
	out, err := s.Command(args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// Out runs one tmux command and returns its trimmed stdout.
func (s Server) Out(args ...string) (string, error) {
	cmd := s.Command(args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Reachable reports whether a server answers on the socket at all.
func (s Server) Reachable() bool {
	return s.Command("list-sessions").Run() == nil
}

// HasSession reports whether the server is running with Endless's session.
func (s Server) HasSession() bool {
	return s.Command("has-session", "-t", "="+s.Session).Run() == nil
}

// Attached reports whether any terminal is attached to the session.
func (s Server) Attached() bool {
	out, err := s.Out("list-clients", "-t", "="+s.Session, "-F", "#{client_name}")
	return err == nil && out != ""
}

// AttachArgv is the command that attaches a terminal to the session.
func (s Server) AttachArgv() []string {
	return []string{"tmux", "-S", s.Socket, "attach-session", "-t", "=" + s.Session}
}

// AttachCommand is AttachArgv as one line a person can paste.
func (s Server) AttachCommand() string {
	return ShellJoin(s.AttachArgv())
}

// ConfFile is where the Endless layer of this server's tmux configuration is
// written, beside its socket.
func (s Server) ConfFile() string {
	return s.Socket + ".conf"
}

// StartSession starts the server with Endless's session if that session is
// not already running, and initializes the server (status line and
// @server_uuid). extra is passed to new-session — a window name (`-n`) and
// directory (`-c`). Returns the first pane's id when it created the session;
// created is false, and nothing is touched, when the session was running.
func (s Server) StartSession(self string, extra ...string) (pane string, created bool, err error) {
	if s.HasSession() {
		return "", false, nil
	}
	if err = writeConf(s, self); err != nil {
		return "", false, err
	}
	args := append([]string{"-f", s.ConfFile(), "new-session", "-d", "-s", s.Session}, extra...)
	args = append(args, "-P", "-F", "#{pane_id}")
	pane, err = s.Out(args...)
	if err != nil {
		return "", false, fmt.Errorf("start the %s tmux server (%s): %w", s.Role, s.Socket, err)
	}
	if err = s.Init(self); err != nil {
		return pane, true, err
	}
	return pane, true, nil
}

// Init runs `endless-go tmux init` against this server: the Endless status
// line and menus, and the @server_uuid that session liveness identifies the
// server by. init is gated on @server_uuid, so calling it on an initialized
// server is a no-op.
//
// init addresses "the server this process is inside", which tmux reads from
// $TMUX when no -S is given; pointing $TMUX at this socket is how it reaches
// this server without growing a socket flag of its own.
func (s Server) Init(self string) error {
	cmd := exec.Command(self, "tmux", "init")
	cmd.Env = append(withoutTmux(os.Environ()), "TMUX="+s.Socket+",0,0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initialize the %s tmux server: %v: %s", s.Role, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// withoutTmux drops TMUX and TMUX_PANE from an environment.
func withoutTmux(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ShellJoin quotes each word for a POSIX shell and joins them with spaces.
func ShellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = ShellQuote(a)
	}
	return strings.Join(q, " ")
}

// ShellQuote single-quotes s for a POSIX shell, leaving plainly safe words bare
// so a printed command stays readable. A word starting with `=` or `~` is
// quoted: zsh expands `=word` to the path of command `word`, so a pasted
// `-t =console` would fail there.
func ShellQuote(s string) string {
	if s != "" && !strings.HasPrefix(s, "=") && !strings.HasPrefix(s, "~") && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("-_./=:@%+,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
