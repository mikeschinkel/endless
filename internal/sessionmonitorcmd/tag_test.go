package sessionmonitorcmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mikeschinkel/endless/internal/upid"
)

// privateTmuxSeq makes each privateTmux socket unique within the process.
// `kill-server` returns before the server has exited, so a test reusing the
// previous test's socket name can reach the dying server and fail to start
// its session — intermittently, and mostly under load.
var privateTmuxSeq atomic.Int64

// privateTmux starts a throwaway tmux server and points this process's tmux
// commands at it through $TMUX, so Tag's bare `tmux` calls can never reach the
// server the person running the tests is sitting in. It returns the one pane.
func privateTmux(t *testing.T) (pane string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock := fmt.Sprintf("endless-e2194-test-%d-%d", os.Getpid(), privateTmuxSeq.Add(1))
	tm := func(args ...string) string {
		out, err := exec.Command("tmux", append([]string{"-L", sock, "-f", "/dev/null"}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %q: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	pane = tm("new-session", "-d", "-P", "-F", "#{pane_id}", "sleep", "300")
	path := tm("display-message", "-p", "-t", pane, "#{socket_path}")
	// kill-server leaves the socket file behind; remove it so unique names
	// do not accumulate in the tmux socket directory.
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
		_ = os.Remove(path)
	})
	t.Setenv("TMUX", path+",0,0")
	return pane
}

func readTag(t *testing.T, pane string) string {
	t.Helper()
	out, _ := exec.Command("tmux", readTagArgs(pane)...).Output()
	return strings.TrimSpace(string(out))
}

func TestTagWritesThisProcessUPIDAndUntagClearsIt(t *testing.T) {
	pane := privateTmux(t)
	self, err := upid.Self()
	if err != nil {
		t.Skipf("no UPID on this platform: %v", err)
	}
	untag := Tag(pane)
	if got := readTag(t, pane); got != self.String() {
		t.Fatalf("tag = %q, want %q", got, self.String())
	}
	untag()
	if got := readTag(t, pane); got != "" {
		t.Fatalf("tag after untag = %q, want none", got)
	}
}

// A replacement monitor may tag the pane before the old one finishes exiting;
// the old one's untag must leave the new tag alone.
func TestUntagLeavesSomeoneElsesTag(t *testing.T) {
	pane := privateTmux(t)
	untag := Tag(pane)
	if err := exec.Command("tmux", tagArgs(pane, "1@2")...).Run(); err != nil {
		t.Fatalf("overwrite tag: %v", err)
	}
	untag()
	if got := readTag(t, pane); got != "1@2" {
		t.Fatalf("tag = %q, want the replacement's 1@2 kept", got)
	}
}

func TestTagWithoutPaneIsInert(t *testing.T) {
	Tag("")() // must not panic or call tmux
}
