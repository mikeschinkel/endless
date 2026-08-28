// The test half of nav_hook_retire.go, in a file of its own for the same
// reason: it must quote the `record-nav` hook command verbatim to prove the
// matcher is narrow, and the E-2081 reference sweep exempts exactly these two
// files. Delete both together.

package tmuxcmd

import "testing"

// TestIsNavRecorderHook is the safety property behind retireNavHooks: apply
// clears a stale E-1682 focus-change hook, and `client-session-changed` /
// `session-window-changed` are global names any user may have bound for their
// own purposes. Only a hook that still names OUR recorder may be unset.
func TestIsNavRecorderHook(t *testing.T) {
	// Verbatim from a live tmux 3.6 server that ran the old apply. The binary
	// path differs per machine, which is why the marker is the verb.
	ours := `run-shell "/usr/local/bin/endless-go tmux record-nav --client=#{client_name} --pane=#{pane_id}"`
	if !isNavRecorderHook(ours) {
		t.Errorf("the recorder hook was not recognized: %q", ours)
	}
	if !isNavRecorderHook(`run-shell "/opt/homebrew/bin/endless-go tmux record-nav --client=#{client_name} --pane=#{pane_id}"`) {
		t.Error("a recorder hook installed from a different binary path was not recognized")
	}
	for _, theirs := range []string{
		"",
		`run-shell "~/bin/my-focus-logger #{pane_id}"`,
		`display-message "switched"`,
		`run-shell "/usr/local/bin/endless-go tmux status-line --pane=#{pane_id}"`,
	} {
		if isNavRecorderHook(theirs) {
			t.Errorf("a hook that is not ours would be unset: %q", theirs)
		}
	}
}
