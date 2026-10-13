package monitorvis

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// visServer is a private tmux server for one test: its own socket in its own
// directory, and a HOME of its own, so nothing it runs can reach the server or
// the config of the person running the tests.
type visServer struct {
	t    *testing.T
	sock string
	env  []string
}

func newVisServer(t *testing.T) *visServer {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Short, because a unix socket path is capped near 104 bytes on macOS and
	// t.TempDir() there can exceed it on its own.
	dir, err := os.MkdirTemp("/tmp", "e2229-")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	s := &visServer{t: t, sock: filepath.Join(dir, "s")}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") ||
			strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=") ||
			strings.HasPrefix(kv, "XDG_CACHE_HOME=") || strings.HasPrefix(kv, "ENDLESS_MONITOR_PROFILE=") {
			continue
		}
		s.env = append(s.env, kv)
	}
	s.env = append(s.env, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"))
	t.Cleanup(func() {
		_ = s.cmd("kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	return s
}

func (s *visServer) cmd(args ...string) *exec.Cmd {
	c := exec.Command("tmux", append([]string{"-S", s.sock, "-f", "/dev/null"}, args...)...)
	c.Env = s.env
	return c
}

func (s *visServer) run(args ...string) string {
	s.t.Helper()
	var stderr bytes.Buffer
	c := s.cmd(args...)
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		s.t.Fatalf("tmux %q: %v: %s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// attach attaches a control-mode client — a real client with a current window,
// which needs no terminal — and keeps it attached for the rest of the test.
func (s *visServer) attach(session string) {
	s.t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		s.t.Fatal(err)
	}
	c := s.cmd("-C", "attach", "-t", session)
	c.Stdin = r
	c.Stdout = io.Discard
	if err := c.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() {
		w.Close()
		_ = c.Process.Kill()
		_ = c.Wait()
		r.Close()
	})
}

// renders counts the lines a visprobe has written to its log.
func renders(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
}

// working reports whether the monitor logging to path renders over a span of
// many ticks; idle reports that it does not.
func working(t *testing.T, path string) bool {
	before := renders(t, path)
	time.Sleep(400 * time.Millisecond)
	return renders(t, path) > before
}

func idle(t *testing.T, path string) bool { return !working(t, path) }

func buildVisprobe(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "visprobe")
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/visprobe").CombinedOutput()
	if err != nil {
		t.Fatalf("building visprobe: %v\n%s", err, out)
	}
	return bin
}

// TestVisibilityEndToEnd runs two gated monitors in two windows of a real tmux
// server, with the real hooks they install, and switches between them:
//
//   - with no client attached, neither is visible and neither renders;
//   - attaching makes only the current window's monitor render;
//   - select-window moves the work to the other monitor within about a second,
//     and the one switched away from goes idle — the hooks, not the safety
//     net, which is set far longer than the test waits.
func TestVisibilityEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a helper and runs a tmux server")
	}
	bin := buildVisprobe(t)
	s := newVisServer(t)
	logs := t.TempDir()
	log0, log1 := filepath.Join(logs, "w0"), filepath.Join(logs, "w1")

	s.run("new-session", "-d", "-s", "t", "-n", "w0", bin, log0, "1h")
	s.run("new-window", "-d", "-t", "t:", "-n", "w1", bin, log1, "1h")

	eventually(t, 5*time.Second, "both monitors to tag their panes", func() bool {
		return strings.Count(s.run("list-panes", "-a", "-F", "#{"+TagOption+"}"), "@") == 2
	})
	if working(t, log0) || working(t, log1) {
		t.Fatal("a monitor rendered with no client attached")
	}

	s.attach("t")
	eventually(t, 3*time.Second, "w0 to render once a client shows it", func() bool { return working(t, log0) })
	if !idle(t, log1) {
		t.Fatal("w1 rendered while w0 was the window on screen")
	}

	s.run("select-window", "-t", "t:w1")
	eventually(t, 2*time.Second, "w1 to render after the switch", func() bool { return renders(t, log1) > 0 })
	eventually(t, 2*time.Second, "w0 to go idle after the switch", func() bool { return idle(t, log0) })

	for _, hook := range HookNames {
		out := s.run("show-hooks", "-g", hook)
		if n := len(wakeHookPattern.FindAllString(out, -1)); n != 1 {
			t.Errorf("%s carries %d wake hooks, want exactly 1:\n%s", hook, n, out)
		}
	}
}

// TestHooksRemovedAreReinstalledByThePoll pins the safety net's repair: hooks
// removed by hand are back by the next poll.
func TestHooksRemovedAreReinstalledByThePoll(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a helper and runs a tmux server")
	}
	bin := buildVisprobe(t)
	s := newVisServer(t)
	s.run("new-session", "-d", "-s", "t", bin, filepath.Join(t.TempDir(), "w0"), "200ms")
	hooked := func() bool {
		return strings.Contains(s.run("show-hooks", "-g", "client-detached"), WakeVerb)
	}
	eventually(t, 5*time.Second, "the monitor to install its hooks", hooked)

	for _, hook := range HookNames {
		s.run("set-hook", "-gu", hook)
	}
	if hooked() {
		t.Fatal("set-hook -gu left the hook in place")
	}
	eventually(t, 3*time.Second, "the next poll to reinstall the hooks", hooked)
}

// TestEnsureHooksIsIdempotentBesideUserHooks pins what `endless tmux apply`
// relies on: a second call adds nothing, and a user's own hook on the same
// name survives both.
func TestEnsureHooksIsIdempotentBesideUserHooks(t *testing.T) {
	s := newVisServer(t)
	s.run("new-session", "-d", "-s", "t", "sleep", "300")
	s.run("set-hook", "-g", "client-attached", "run-shell 'true my-own-hook'")
	t.Setenv("TMUX", s.sock+",0,0")
	t.Setenv("TMUX_TMPDIR", filepath.Dir(s.sock))
	useFaultCapture(t)

	for range 2 {
		if err := EnsureHooks("/opt/bin/endless-go"); err != nil {
			t.Fatalf("EnsureHooks: %v", err)
		}
	}
	out := s.run("show-hooks", "-g", "client-attached")
	if !strings.Contains(out, "my-own-hook") {
		t.Errorf("the user's own hook was lost:\n%s", out)
	}
	if n := len(wakeHookPattern.FindAllString(out, -1)); n != 1 {
		t.Errorf("client-attached carries %d wake hooks after two installs, want 1:\n%s", n, out)
	}
}
