package upid

import (
	"os"
	"os/exec"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	want := UPID{PID: 4242, Start: 1727712345123456}
	got, err := Parse(want.String())
	if err != nil {
		t.Fatalf("Parse(%q): %v", want.String(), err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "4242", "@1", "x@1", "0@1", "-3@1", "4242@", "4242@x"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) = nil error, want one", s)
		}
	}
}

func TestSelfIsAlive(t *testing.T) {
	u, err := Self()
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if u.PID != os.Getpid() {
		t.Fatalf("Self().PID = %d, want %d", u.PID, os.Getpid())
	}
	if !u.Alive() {
		t.Fatalf("Self() %s reports not alive", u)
	}
}

// Same pid, different start time: a reissued pid names a different process.
func TestSamePidDifferentStartIsNotAlive(t *testing.T) {
	u, err := Self()
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	u.Start--
	if u.Alive() {
		t.Fatalf("%s reports alive with a start time that is not this process's", u)
	}
}

func TestExitedProcessIsNotAlive(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	u, err := Of(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Of(child): %v", err)
	}
	if !u.Alive() {
		t.Fatalf("child %s reports not alive before it is reaped", u)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if u.Alive() {
		t.Fatalf("reaped child %s still reports alive", u)
	}
}
