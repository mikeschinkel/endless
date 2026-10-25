package monitor

import (
	"os/exec"
	"slices"
	"testing"
)

// fakeTmux answers tmux calls the way tmux 3.8 does for @server_uuid, without a
// tmux server: `show-options -gv` on an unset option exits 1, `-gqv` exits 0
// with no output, and a failed read exits 1 either way. It records every
// set-option, which is the stamp under test.
type fakeTmux struct {
	uuid     string // the option's value; "" is unset
	readFail bool   // show-options fails as with "no server running"
	sets     []string
}

func (f *fakeTmux) exec(_ string, args ...string) *exec.Cmd {
	answer := func(out string, code int) *exec.Cmd {
		return exec.Command("sh", "-c", `printf '%s' "$1"; exit "$2"`, "sh", out, string(rune('0'+code)))
	}
	switch {
	case len(args) > 0 && args[0] == "set-option":
		f.sets = append(f.sets, args[len(args)-1])
		f.uuid = args[len(args)-1]
		return answer("", 0)
	case len(args) > 0 && args[0] == "show-options":
		if f.readFail {
			return answer("", 1)
		}
		if f.uuid != "" {
			return answer(f.uuid+"\n", 0)
		}
		if slices.Contains(args, "-gqv") {
			return answer("", 0)
		}
		return answer("", 1)
	}
	return answer("", 1)
}

func withFakeTmux(t *testing.T, f *fakeTmux) {
	t.Helper()
	prev := tmuxExec
	tmuxExec = f.exec
	t.Cleanup(func() { tmuxExec = prev })
}

// TestLiveTmuxServerUUID_FailedReadStampsNothing is E-2428's monitor half: a
// read that fails must not be taken for an unset option. On 2026-10-19 a failed
// read re-stamped a live server, and every session bound before it read dead.
func TestLiveTmuxServerUUID_FailedReadStampsNothing(t *testing.T) {
	f := &fakeTmux{uuid: "a6d29860", readFail: true}
	withFakeTmux(t, f)

	got, err := liveTmuxServerUUID()
	if err != nil {
		t.Fatalf("liveTmuxServerUUID: %v", err)
	}
	if got != "" {
		t.Errorf("failed read returned %q, want \"\"", got)
	}
	if len(f.sets) != 0 {
		t.Errorf("failed read stamped %q, want no stamp", f.sets)
	}
}

// TestLiveTmuxServerUUID_UnsetStamps: a read that succeeds with no value is the
// one case that stamps.
func TestLiveTmuxServerUUID_UnsetStamps(t *testing.T) {
	f := &fakeTmux{}
	withFakeTmux(t, f)

	got, err := liveTmuxServerUUID()
	if err != nil {
		t.Fatalf("liveTmuxServerUUID: %v", err)
	}
	if len(f.sets) != 1 || got != f.sets[0] {
		t.Errorf("unset option: got %q, stamps %q; want one stamp, returned", got, f.sets)
	}
}

// TestLiveTmuxServerUUID_SetReturnsIt: an initialized server keeps its id.
func TestLiveTmuxServerUUID_SetReturnsIt(t *testing.T) {
	f := &fakeTmux{uuid: "0ae20eaa"}
	withFakeTmux(t, f)

	got, err := liveTmuxServerUUID()
	if err != nil || got != "0ae20eaa" || len(f.sets) != 0 {
		t.Errorf("got %q, %v, stamps %q; want 0ae20eaa, no stamp", got, err, f.sets)
	}
}
