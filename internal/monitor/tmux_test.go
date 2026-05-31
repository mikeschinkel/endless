package monitor

import (
	"testing"
)

// TestInTmux_DetectsByEnvVar pins the contract that InTmux returns true iff
// the TMUX environment variable is non-empty. Three rows cover unset, a
// realistic socket-path value, and the explicit empty string (which is
// semantically the same as unset for os.Getenv).
func TestInTmux_DetectsByEnvVar(t *testing.T) {
	tests := []struct {
		name   string
		setEnv bool
		value  string
		want   bool
	}{
		{name: "unset", setEnv: true, value: "", want: false},
		{name: "non_empty_socket_path", setEnv: true, value: "/tmp/tmux-501/default,1234,0", want: true},
		{name: "explicit_empty_string", setEnv: true, value: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv("TMUX", tc.value)
			}
			got := InTmux()
			if got != tc.want {
				t.Errorf("InTmux() with TMUX=%q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
