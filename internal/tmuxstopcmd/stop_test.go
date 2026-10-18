package tmuxstopcmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikeschinkel/go-cfgstore"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/tmuxservers"
	"github.com/mikeschinkel/endless/internal/tmuxtest"
)

// config.Load logs through go-cfgstore, which panics without a logger.
func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestParseTarget(t *testing.T) {
	for _, tt := range []struct {
		all  bool
		args []string
		want string // "" for a refusal
	}{
		{false, []string{"tasks"}, "tasks"},
		{false, []string{"console"}, "console"},
		{false, []string{"all"}, "all"},
		{true, nil, "all"},
		{true, []string{"all"}, "all"},
		{false, nil, ""},
		{true, []string{"tasks"}, ""},
		{false, []string{"tasks", "console"}, ""},
		{false, []string{"everything"}, ""},
	} {
		got, err := parseTarget(tt.all, tt.args)
		if got != tt.want || (err == nil) != (tt.want != "") {
			t.Errorf("parseTarget(%v, %q) = %q, %v; want %q", tt.all, tt.args, got, err, tt.want)
		}
	}
}

// TestParseInterspersed: flags count on either side of the target.
func TestParseInterspersed(t *testing.T) {
	for _, args := range [][]string{
		{"tasks", "--timeout", "10s", "--force"},
		{"--force", "tasks", "--timeout=10s"},
		{"--timeout", "10s", "--force", "tasks"},
	} {
		fs := refusal.NewFlags("tmux stop")
		force := fs.Bool("force", false, "")
		timeout := fs.Duration("timeout", time.Second, "")
		got, err := parseInterspersed(fs, args)
		if err != nil || !slices.Equal(got, []string{"tasks"}) || !*force || *timeout != 10*time.Second {
			t.Errorf("parseInterspersed(%q) = %q, %v; force %v, timeout %v", args, got, err, *force, *timeout)
		}
	}
}

// TestClaudePanes: /exit goes to a pane bound to a session or running Claude
// (whose process title is its version), never to one at a shell prompt.
func TestClaudePanes(t *testing.T) {
	panes := parsePanes("%1\t2.1.220\n%2\tzsh\n%3\tendless-go\n%4\tclaude\n%5\tnode\n%6\tbash\n")
	sessions := []monitor.PaneSession{{Pane: "%5"}, {Pane: "%6"}}
	if got, want := claudePanes(panes, sessions), []string{"%1", "%4", "%5"}; !slices.Equal(got, want) {
		t.Errorf("claudePanes = %v, want %v", got, want)
	}
}

// TestStillRunning: a pane has quit when it is gone or back at a shell.
func TestStillRunning(t *testing.T) {
	now := parsePanes("%1\t2.1.220\n%2\tzsh\n")
	if got := stillRunning([]string{"%1", "%2", "%3"}, now); !slices.Equal(got, []string{"%1"}) {
		t.Errorf("stillRunning = %v, want [%%1]", got)
	}
}

func TestDescribe(t *testing.T) {
	got := describe([]monitor.PaneSession{
		{EndlessSessionID: 9, TaskID: 12, State: "working"},
		{EndlessSessionID: 10, State: "working"},
	})
	want := "  E-12  ES-9  working\n  no task  ES-10  working"
	if got != want {
		t.Errorf("describe =\n%s\nwant\n%s", got, want)
	}
	left := describePanes([]string{"%4", "%7"}, []monitor.PaneSession{{EndlessSessionID: 3, TaskID: 5, State: "idle", Pane: "%4"}})
	if !strings.Contains(left, "pane %4 E-5  ES-3  idle") || !strings.Contains(left, "pane %7  (no Endless session)") {
		t.Errorf("describePanes = %q", left)
	}
}

// liveTasks starts a task-session server on a private socket with one window
// per name, each running cmd[name], and a HOME of its own for the record.
func liveTasks(t *testing.T, windows []string, cmd map[string][]string) tmuxservers.Server {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmuxtest.Private(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Under tmux-*/ so tmuxtest's cleanup kills it.
	dir := filepath.Join(os.Getenv("TMUX_TMPDIR"), "tmux-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := tmuxservers.Server{Role: tmuxservers.RoleTasks, Socket: filepath.Join(dir, "endless-tasks"), Session: "tasks"}
	for i, name := range windows {
		args := []string{"new-window", "-d", "-t", "=tasks:", "-n", name}
		if i == 0 {
			args = []string{"-f", "/dev/null", "new-session", "-d", "-s", "tasks", "-n", name}
		}
		if err := s.Run(append(append(args, "--"), cmd[name]...)...); err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
	}
	return s
}

// fakeClaude builds a binary named `claude`, so the pane's command reads as
// Claude's. It quits on reading the line `/exit` — what stop types — and, run
// with `stay`, never quits at all. (A copied system binary would do, but macOS
// kills a platform binary run from outside its system volume.)
func fakeClaude(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fake-claude-")
		if err != nil {
			fakeErr = err
			return
		}
		src := filepath.Join(dir, "main.go")
		fakeErr = os.WriteFile(src, []byte(fakeClaudeSrc), 0o600)
		if fakeErr != nil {
			return
		}
		fakeBin = filepath.Join(dir, "claude")
		out, err := exec.Command("go", "build", "-o", fakeBin, src).CombinedOutput()
		if err != nil {
			fakeErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if fakeErr != nil {
		t.Skipf("cannot build the fake claude: %v", fakeErr)
	}
	return fakeBin
}

// TestMain removes the fake claude's build directory once every test is done.
func TestMain(m *testing.M) {
	code := m.Run()
	if fakeBin != "" {
		_ = os.RemoveAll(filepath.Dir(fakeBin))
	}
	os.Exit(code)
}

var (
	fakeOnce sync.Once
	fakeBin  string
	fakeErr  error
)

const fakeClaudeSrc = `package main

import (
	"bufio"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "stay" {
		time.Sleep(time.Hour)
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if sc.Text() == "/exit" {
			return
		}
	}
}
`

func fast(t *testing.T) {
	prevPoll, prevGap := pollInterval, exitKeyGap
	pollInterval, exitKeyGap = 50*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { pollInterval, exitKeyGap = prevPoll, prevGap })
}

// TestStopTasksQuitsClaudeThenTheServer: /exit reaches each Claude, the server
// is stopped once every one has quit, a shell window is left alone, and the
// record holds the windows open when the stop began, in tab order.
func TestStopTasksQuitsClaudeThenTheServer(t *testing.T) {
	fast(t)
	claude := fakeClaude(t)
	s := liveTasks(t, []string{"E-13", "E-11", "E-12"}, map[string][]string{
		"E-13": {claude},
		"E-11": {"sh"},
		"E-12": {claude},
	})
	time.Sleep(200 * time.Millisecond) // let each pane's command start

	var out strings.Builder
	if !stopTasks(&out, s, options{timeout: 5 * time.Second}) {
		t.Fatalf("stopTasks left the server running:\n%s", out.String())
	}
	if s.Reachable() {
		t.Error("the server is still running")
	}
	if !strings.Contains(out.String(), "asked 2 Claude session(s) to quit") {
		t.Errorf("output = %q, want both Claude panes asked", out.String())
	}
	if got, _ := tmuxservers.ReadState(); !slices.Equal(got, []int64{13, 11, 12}) {
		t.Errorf("record = %v, want [13 11 12]", got)
	}
}

// TestStopTasksLeavesTheServerForAStraggler: a Claude still running at the
// timeout keeps the server up, and the record is unfrozen again; --force
// stops it anyway.
func TestStopTasksLeavesTheServerForAStraggler(t *testing.T) {
	fast(t)
	claude := fakeClaude(t)
	s := liveTasks(t, []string{"E-21"}, map[string][]string{"E-21": {claude, "stay"}})
	time.Sleep(200 * time.Millisecond)

	if stopTasks(io.Discard, s, options{timeout: 300 * time.Millisecond}) {
		t.Fatal("stopTasks reported the server stopped with Claude still running")
	}
	if !s.Reachable() {
		t.Fatal("the server was stopped despite the straggler")
	}
	if s.Stopping() {
		t.Error("the record is still frozen on a server left running")
	}
	if !stopTasks(io.Discard, s, options{timeout: 300 * time.Millisecond, force: true}) || s.Reachable() {
		t.Error("--force did not stop the server")
	}
}

// TestRecordIsFrozenWhileStopping: with the stop option set, a window closing
// does not rewrite the record.
func TestRecordIsFrozenWhileStopping(t *testing.T) {
	s := liveTasks(t, []string{"E-31", "E-32"}, map[string][]string{"E-31": {"sh"}, "E-32": {"sh"}})
	if _, err := s.Record(); err != nil {
		t.Fatal(err)
	}
	if err := s.Run("set-option", "-g", tmuxservers.StoppingOption, "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Run("kill-window", "-t", "=tasks:E-32"); err != nil {
		t.Fatal(err)
	}
	if wrote, _ := s.Record(); wrote {
		t.Error("Record wrote while the server was stopping")
	}
	if got, _ := tmuxservers.ReadState(); !slices.Equal(got, []int64{31, 32}) {
		t.Errorf("record = %v, want [31 32]", got)
	}
}

func TestStopAnAlreadyStoppedServer(t *testing.T) {
	tmuxtest.Private(t)
	s := tmuxservers.Server{Role: tmuxservers.RoleTasks, Socket: filepath.Join(os.Getenv("TMUX_TMPDIR"), "none"), Session: "tasks"}
	var out strings.Builder
	if !stopTasks(&out, s, options{}) || !strings.Contains(out.String(), "already stopped") {
		t.Errorf("stopTasks on no server = %q", out.String())
	}
}
