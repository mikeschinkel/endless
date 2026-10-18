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
	"github.com/mikeschinkel/endless/internal/settings"
)

// config.Load logs through go-cfgstore, which panics without a logger.
func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestDefaultsAreFullSocketPaths pins the decided layout: both servers under
// one short directory, addressed by full path, with the decided names.
func TestDefaultsAreFullSocketPaths(t *testing.T) {
	s, err := resolveFrom(config.Tmux{})
	if err != nil {
		t.Fatal(err)
	}
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
	s, err := resolveFrom(config.Tmux{SocketDir: "/x", ConsoleServer: "c", TasksServer: "t", TasksSession: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Console.Socket != "/x/c" || s.Tasks.Socket != "/x/t" || s.Tasks.Session != "work" {
		t.Errorf("configured values lost: %+v", s)
	}
	// The console session is not configurable.
	if s.Console.Session != ConsoleSession {
		t.Errorf("console session = %q", s.Console.Session)
	}
}

// TestResolveRefusesAMalformedValue: a configured value that fails its key's
// shape check is refused, naming the key and what it takes.
func TestResolveRefusesAMalformedValue(t *testing.T) {
	for _, c := range []struct {
		tmux  config.Tmux
		key   settings.ID
		takes string
	}{
		{config.Tmux{SocketDir: "relative"}, SocketDirSetting, settings.PathAccepts},
		{config.Tmux{ConsoleServer: "a/b"}, ConsoleServerSetting, settings.NameAccepts},
		{config.Tmux{TasksServer: "a b"}, TasksServerSetting, settings.NameAccepts},
		{config.Tmux{TasksSession: "work:1"}, TasksSessionSetting, settings.NameAccepts},
		{config.Tmux{TasksSession: "work.1"}, TasksSessionSetting, settings.NameAccepts},
		{config.Tmux{Terminal: "ghostty -e"}, TerminalSetting, settings.CommandAccepts},
	} {
		_, err := resolveFrom(c.tmux)
		if !errors.Is(err, ErrInvalidTmuxSetting) || !errors.Is(err, settings.ErrInvalidValue) {
			t.Errorf("resolveFrom(%+v) = %v, want ErrInvalidTmuxSetting wrapping ErrInvalidValue", c.tmux, err)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, string(c.key)) || !strings.Contains(msg, c.takes) {
			t.Errorf("resolveFrom(%+v) = %q, want it to name %s and %q", c.tmux, msg, c.key, c.takes)
		}
	}
}

// TestResolveAcceptsWellShapedValues: home-relative directories, blank
// (unset) values and a terminal template all resolve.
func TestResolveAcceptsWellShapedValues(t *testing.T) {
	// A short HOME, so the expanded socket path stays under the length limit
	// whatever the caller's HOME is.
	home := "/h"
	t.Setenv("HOME", home)
	s, err := resolveFrom(config.Tmux{SocketDir: "~/s", TasksSession: " ", Terminal: "ghostty -e {cmd}"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Dir != filepath.Join(home, "s") || s.Tasks.Session != DefaultTasksSession {
		t.Errorf("resolveFrom = %+v", s)
	}
}

// TestResolveRefusesASocketPathTooLong: tmux cannot bind a socket path longer
// than sun_path, so Resolve refuses it rather than let `tmux -S` fail later.
func TestResolveRefusesASocketPathTooLong(t *testing.T) {
	fits := "/" + strings.Repeat("d", maxSocketPath-1-len("/"+DefaultConsoleServer))
	if _, err := resolveFrom(config.Tmux{SocketDir: fits, TasksServer: "t"}); err != nil {
		t.Errorf("a %d-byte socket path: %v", maxSocketPath, err)
	}
	_, err := resolveFrom(config.Tmux{SocketDir: fits + "d", TasksServer: "t"})
	if !errors.Is(err, ErrInvalidTmuxSetting) || !strings.Contains(err.Error(), string(SocketDirSetting)) {
		t.Errorf("a %d-byte socket path = %v, want ErrInvalidTmuxSetting naming %s", maxSocketPath+1, err, SocketDirSetting)
	}
}

// TestSettingsDefaultsMatchResolve: each registered default is what Resolve
// supplies when the key is unset, and every key is machine-only and never
// suggested.
func TestSettingsDefaultsMatchResolve(t *testing.T) {
	s, err := resolveFrom(config.Tmux{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[settings.ID]string{
		SocketDirSetting:     s.Dir,
		ConsoleServerSetting: filepath.Base(s.Console.Socket),
		TasksServerSetting:   filepath.Base(s.Tasks.Socket),
		TasksSessionSetting:  s.Tasks.Session,
		TerminalSetting:      s.Terminal,
	}
	got := Servers{}.Settings()
	if len(got) != len(want) {
		t.Fatalf("Settings() has %d keys, want %d", len(got), len(want))
	}
	for _, st := range got {
		if st.Default != want[st.ID] {
			t.Errorf("%s default = %q, want %q", st.ID, st.Default, want[st.ID])
		}
		if st.Feature != "tmux" || st.Files != settings.MachineFile || st.Suggestable {
			t.Errorf("%s = %+v, want feature tmux, machine file only, not suggestable", st.ID, st)
		}
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

// TestStateWritesOnlyOnChange: the record is rewritten when the windows or
// their order change (E-2385), and left alone when neither does. Repeats keep
// their first position.
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
	if got, _ = ReadState(); !slices.Equal(got, []int64{7, 5}) {
		t.Errorf("record = %v, want [7 5], in the order given", got)
	}
	if wrote, _ := WriteState([]int64{7, 5}); wrote {
		t.Errorf("an unchanged record was rewritten")
	}
	if wrote, _ := WriteState([]int64{5, 7}); !wrote {
		t.Errorf("a reorder alone was not written")
	}
	if got, _ = ReadState(); !slices.Equal(got, []int64{5, 7}) {
		t.Errorf("record = %v, want [5 7] after the reorder", got)
	}
	if wrote, _ := WriteState(nil); !wrote {
		t.Errorf("an emptied set was not written")
	}
}

// TestParseWindowsIsTabOrder: windows come back by index, with the task each
// one is named for.
func TestParseWindowsIsTabOrder(t *testing.T) {
	got := parseWindows("3\t@9\tE-12\n0\t@1\tzsh\n1\t@4\t *E-5\nbad line\n")
	want := []Window{{"@1", 0, "zsh", 0}, {"@4", 1, " *E-5", 5}, {"@9", 3, "E-12", 12}}
	if !slices.Equal(got, want) {
		t.Errorf("parseWindows = %+v, want %+v", got, want)
	}
}

// TestRecordHooksIncludeReorders: a reorder fires no window-* hook, so the
// commands that reorder are hooked too.
func TestRecordHooksIncludeReorders(t *testing.T) {
	body := confBody(Server{Role: RoleTasks, Socket: "/s/t", Session: "tasks"}, "/bin/endless-go", nil)
	for _, hook := range []string{"after-move-window", "after-swap-window"} {
		if !strings.Contains(body, "set-hook -g "+hook+" ") {
			t.Errorf("tasks conf lacks the %s hook:\n%s", hook, body)
		}
	}
}

// TestDefaultLauncher: with no terminal configured or detected, macOS opens
// Terminal.app, Linux the first of xdg-terminal-exec and x-terminal-emulator on
// PATH, and anything else nothing (E-2385).
func TestDefaultLauncher(t *testing.T) {
	has := func(names ...string) func(string) bool {
		return func(n string) bool { return slices.Contains(names, n) }
	}
	for _, tt := range []struct {
		goos   string
		onPath func(string) bool
		argv   []string // nil: no launcher
	}{
		{"darwin", has(), []string{"osascript"}},
		{"linux", has("xdg-terminal-exec", "x-terminal-emulator"), []string{"xdg-terminal-exec", "top"}},
		{"linux", has("x-terminal-emulator"), []string{"x-terminal-emulator", "-e", "top"}},
		{"linux", has(), nil},
		{"freebsd", has("xdg-terminal-exec"), nil},
	} {
		l, ok := defaultLauncher(tt.goos, tt.onPath)
		if ok != (tt.argv != nil) {
			t.Errorf("%s: ok = %v", tt.goos, ok)
			continue
		}
		if !ok {
			continue
		}
		got := l.Argv([]string{"top"})
		if tt.argv[0] == "osascript" {
			if got[0] != "osascript" || !strings.Contains(strings.Join(got, " "), `application "Terminal"`) {
				t.Errorf("%s: argv = %q, want Terminal.app", tt.goos, got)
			}
			continue
		}
		if !slices.Equal(got, tt.argv) {
			t.Errorf("%s: argv = %q, want %q", tt.goos, got, tt.argv)
		}
	}
}
