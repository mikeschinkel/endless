package spawnlaunchcmd

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"

	"github.com/mikeschinkel/endless/internal/tmuxtest"
)

// spawnHelperEnv makes this test binary run spawn-window itself, so a test can
// watch it refuse and exit without the refusal ending the test process.
const spawnHelperEnv = "ENDLESS_TEST_SPAWN_WINDOW"

func TestMain(m *testing.M) {
	if os.Getenv(spawnHelperEnv) == "1" {
		cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
		args := os.Args[1:]
		for i, a := range args {
			if a == "--" {
				args = args[i+1:]
				break
			}
		}
		runSpawnWindow(args)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestSpawnWindowRefusesWithoutTheTasksServer: with endless-tasks not running,
// spawn-window refuses before creating anything — outside tmux, inside another
// tmux server, and naming a session there (E-2385). It never falls back to the
// server the caller is in.
func TestSpawnWindowRefusesWithoutTheTasksServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmuxtest.Server(t) // the "caller's" server, on the private default socket
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	// Endless's servers live in a socket directory with nothing in it.
	sockets := filepath.Join(os.Getenv("TMUX_TMPDIR"), "endless")
	if err := os.WriteFile(filepath.Join(cfg, "config.json"),
		[]byte(`{"tmux":{"socket_dir":"`+sockets+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	callerSession, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		t.Fatalf("read the scratch session: %v", err)
	}
	callerTmux, err := exec.Command("tmux", "display-message", "-p", "#{socket_path},#{pid},0").Output()
	if err != nil {
		t.Fatalf("read the scratch server: %v", err)
	}
	windows := func() string {
		out, _ := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}").Output()
		return string(out)
	}
	before := windows()

	base := []string{"--claude-bin", "/bin/false", "--handoff-file", filepath.Join(home, "handoff"),
		"--task-id", "11", "--window-name", "E-11"}
	for _, tt := range []struct {
		name   string
		inTmux bool
		extra  []string
	}{
		{"outside tmux", false, nil},
		{"inside another tmux server", true, nil},
		{"naming a session on that server", true, []string{"--tmux-session", strings.TrimSpace(string(callerSession))}},
	} {
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^$", "--"}, append(base, tt.extra...)...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			spawnHelperEnv+"=1")
		if tt.inTmux {
			cmd.Env = append(cmd.Env, "TMUX="+strings.TrimSpace(string(callerTmux)), "TMUX_PANE=%0")
		}
		out, err := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); err == nil || code != 1 {
			t.Errorf("%s: exit %d (%v), want 1:\n%s", tt.name, code, err, out)
		}
		if !strings.Contains(string(out), NotRunningMessage) {
			t.Errorf("%s: output lacks %q:\n%s", tt.name, NotRunningMessage, out)
		}
	}
	if after := windows(); after != before {
		t.Errorf("a window was created on the caller's server:\nbefore %q\nafter  %q", before, after)
	}
	if entries, _ := os.ReadDir(sockets); len(entries) != 0 {
		t.Errorf("a server was started in the socket directory: %v", entries)
	}
}
