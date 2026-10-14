package tmuxservers

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"

	"github.com/mikeschinkel/endless/internal/config"
)

// config.Load logs through go-cfgstore, which panics without a logger.
func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestDefaultsAreFullSocketPaths pins the decided layout: both servers under
// one short directory, addressed by full path, with the decided names.
func TestDefaultsAreFullSocketPaths(t *testing.T) {
	s := resolveFrom(config.Tmux{})
	dir := filepath.Join("/tmp", "endless-"+strconv.Itoa(os.Getuid()))
	if s.Dir != dir {
		t.Errorf("Dir = %q, want %q", s.Dir, dir)
	}
	if s.Console.Socket != filepath.Join(dir, "endless-console") || s.Console.Session != "console" {
		t.Errorf("console = %+v", s.Console)
	}
	if s.Tasks.Socket != filepath.Join(dir, "endless-tasks") || s.Tasks.Session != "tasks" {
		t.Errorf("tasks = %+v", s.Tasks)
	}
}

func TestConfiguredValuesWin(t *testing.T) {
	s := resolveFrom(config.Tmux{SocketDir: "/x", ConsoleServer: "c", TasksServer: "t", TasksSession: "work"})
	if s.Console.Socket != "/x/c" || s.Tasks.Socket != "/x/t" || s.Tasks.Session != "work" {
		t.Errorf("configured values lost: %+v", s)
	}
	// The console session is not configurable.
	if s.Console.Session != ConsoleSession {
		t.Errorf("console session = %q", s.Console.Session)
	}
}

// TestEnsureDirCreatesPrivate: a directory Endless creates is 0700.
func TestEnsureDirCreatesPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sock")
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %04o, want 0700", info.Mode().Perm())
	}
	// A second call accepts the directory it made.
	if err = EnsureDir(dir); err != nil {
		t.Errorf("second EnsureDir: %v", err)
	}
}

func TestEnsureDirRefusesUnsafe(t *testing.T) {
	base := t.TempDir()

	open := filepath.Join(base, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(open); !errors.Is(err, ErrUnsafeSocketDir) {
		t.Errorf("a world-writable directory was accepted: %v", err)
	}

	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(file); !errors.Is(err, ErrUnsafeSocketDir) {
		t.Errorf("a file was accepted: %v", err)
	}

	link := filepath.Join(base, "link")
	mine := filepath.Join(base, "mine")
	_ = os.Mkdir(mine, 0o700)
	if err := os.Symlink(mine, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(link); !errors.Is(err, ErrUnsafeSocketDir) {
		t.Errorf("a symlink was accepted: %v", err)
	}
}

// TestCheckDirRefusesAnotherOwner: ownership is judged against the uid, which
// a test cannot chown to, so it is checked against a uid that is not the
// directory's.
func TestCheckDirRefusesAnotherOwner(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o700)
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkDir(dir, info, os.Getuid()+1); !errors.Is(err, ErrUnsafeSocketDir) {
		t.Errorf("a directory owned by another uid was accepted: %v", err)
	}
	if err = checkDir(dir, info, os.Getuid()); err != nil {
		t.Errorf("own directory refused: %v", err)
	}
}

// TestConfSourcesTheUserFirst: the user's configuration loads before Endless's
// layer, and only the task-session server carries the record hooks.
func TestConfSourcesTheUserFirst(t *testing.T) {
	tasks := Server{Role: RoleTasks, Socket: "/s/endless-tasks", Session: "tasks"}
	body := confBody(tasks, "/bin/endless-go", []string{"/home/u/.tmux.conf"})
	src := strings.Index(body, `source-file -q "/home/u/.tmux.conf"`)
	own := strings.Index(body, "set -g allow-rename off")
	if src < 0 || own < 0 || src > own {
		t.Fatalf("user conf is not sourced before Endless's settings:\n%s", body)
	}
	for _, hook := range []string{"window-linked", "window-unlinked", "window-renamed"} {
		if !strings.Contains(body, "set-hook -g "+hook) {
			t.Errorf("tasks conf lacks the %s hook:\n%s", hook, body)
		}
	}
	if !strings.Contains(body, "record-windows --socket /s/endless-tasks --session tasks") {
		t.Errorf("the hook does not name its server:\n%s", body)
	}
	console := Server{Role: RoleConsole, Socket: "/s/endless-console", Session: "console"}
	if strings.Contains(confBody(console, "/bin/endless-go", nil), "set-hook") {
		t.Errorf("the console server records task windows")
	}
}

func TestShellQuoteGuardsZshEquals(t *testing.T) {
	got := Server{Socket: "/tmp/x y", Session: "console"}.AttachCommand()
	want := `tmux -S '/tmp/x y' attach-session -t '=console'`
	if got != want {
		t.Errorf("ShellJoin = %s, want %s", got, want)
	}
}

func TestTemplateLauncherSubstitutesTheCommand(t *testing.T) {
	l := templateLauncher("ghostty -e {cmd}")
	got := l.Argv(Server{Socket: "/s", Session: "tasks"}.AttachArgv())
	want := []string{"sh", "-c", "ghostty -e tmux -S /s attach-session -t '=tasks'"}
	if !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	// No placeholder: the command is appended.
	got = templateLauncher("my-term").Argv([]string{"top"})
	if got[2] != "my-term top" {
		t.Errorf("argv = %q", got)
	}
}

func TestDetectLauncher(t *testing.T) {
	for _, tt := range []struct {
		term, goos, first string
		ok                bool
	}{
		{"ghostty", "darwin", "open", true},
		{"xterm-ghostty", "linux", "ghostty", true},
		{"wezterm", "linux", "wezterm", true},
		{"xterm-kitty", "darwin", "kitty", true},
		{"apple_terminal", "darwin", "osascript", true},
		{"iterm.app", "darwin", "osascript", true},
		{"xterm-256color", "darwin", "", false},
		{"", "linux", "", false},
	} {
		l, ok := detectLauncher(tt.term, tt.goos)
		if ok != tt.ok {
			t.Errorf("%s/%s: ok = %v", tt.term, tt.goos, ok)
			continue
		}
		if ok && l.Argv([]string{"top"})[0] != tt.first {
			t.Errorf("%s/%s: argv = %q", tt.term, tt.goos, l.Argv([]string{"top"}))
		}
	}
}

func TestTaskIDOfWindow(t *testing.T) {
	for name, want := range map[string]int64{"E-12": 12, " *E-7 ": 7, "e-3": 3, "zsh": 0, "E-x": 0, "E-12a": 0} {
		got, _ := TaskIDOfWindow(name)
		if got != want {
			t.Errorf("TaskIDOfWindow(%q) = %d, want %d", name, got, want)
		}
	}
}

// TestStateWritesOnlyOnChange: the record is rewritten when the set changes,
// and left alone when it does not.
func TestStateWritesOnlyOnChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got, err := ReadState()
	if err != nil || len(got) != 0 {
		t.Fatalf("a missing record = %v, %v; want empty", got, err)
	}
	if wrote, err := WriteState([]int64{7, 5, 7}); err != nil || !wrote {
		t.Fatalf("first write = %v, %v", wrote, err)
	}
	if got, _ = ReadState(); !slices.Equal(got, []int64{5, 7}) {
		t.Errorf("record = %v, want [5 7]", got)
	}
	if wrote, _ := WriteState([]int64{5, 7}); wrote {
		t.Errorf("an unchanged set was rewritten")
	}
	if wrote, _ := WriteState(nil); !wrote {
		t.Errorf("an emptied set was not written")
	}
}
