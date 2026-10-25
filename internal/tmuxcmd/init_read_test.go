package tmuxcmd

import (
	"os/exec"
	"slices"
	"testing"
)

// fakeTmux answers show-options the way tmux 3.8 does: `-gv` on an unset
// option exits 1, `-gqv` exits 0 with no output, and a failed read exits 1
// either way.
type fakeTmux struct {
	value    string
	readFail bool
}

func (f fakeTmux) exec(_ string, args ...string) *exec.Cmd {
	answer := func(out, code string) *exec.Cmd {
		return exec.Command("sh", "-c", `printf '%s' "$1"; exit "$2"`, "sh", out, code)
	}
	switch {
	case f.readFail:
		return answer("", "1")
	case f.value != "":
		return answer(f.value+"\n", "0")
	case slices.Contains(args, "-gqv"):
		return answer("", "0")
	}
	return answer("", "1")
}

func withFakeTmux(t *testing.T, f fakeTmux) {
	t.Helper()
	prev := tmuxExec
	tmuxExec = f.exec
	t.Cleanup(func() { tmuxExec = prev })
}

// TestReadServerOption_FailedReadIsNotUnset is E-2428's init half: a failed
// read must be told from an unset option, or init stamps a fresh id on a live
// server whose read merely failed. runInit refuses on the error before it
// reaches the stamp.
func TestReadServerOption_FailedReadIsNotUnset(t *testing.T) {
	tests := []struct {
		name    string
		fake    fakeTmux
		want    string
		wantErr bool
	}{
		{name: "failed_read", fake: fakeTmux{readFail: true}, wantErr: true},
		{name: "unset", fake: fakeTmux{}, want: ""},
		{name: "set", fake: fakeTmux{value: "0ae20eaa"}, want: "0ae20eaa"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFakeTmux(t, tc.fake)
			got, err := readServerOption("@server_uuid")
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("readServerOption = %q, %v; want %q, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
