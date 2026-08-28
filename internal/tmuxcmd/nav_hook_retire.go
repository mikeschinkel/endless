// This file exists only to clean up after E-1682, and is meant to be deleted.
//
// E-1682 installed two GLOBAL tmux focus-change hooks that shelled out to
// `endless-go tmux record-nav`, feeding a durable navigation trail. E-2081
// removed the trail, the recorder and the hook installation — but a tmux server
// that ran the old `apply` keeps those hooks for its whole lifetime: `tmux init`
// gates on @server_uuid, so nothing re-applies until the server restarts. Until
// then every pane switch spawns a process to invoke a subcommand that is gone.
//
// It lives in a file of its own, and is the ONE place in the tree still allowed
// to name `record-nav`, so the E-2081 reference sweep's exemption for it stays
// exactly this narrow — and so retiring the cleanup is `git rm` of two files.

package tmuxcmd

import (
	"os/exec"
	"strings"
)

// navHookNames are the two global tmux hooks E-1682 installed to feed the
// session-navigation trail, and navRecorderMarker is the fragment its hook
// command always contained.
var navHookNames = []string{"client-session-changed", "session-window-changed"}

const navRecorderMarker = "tmux record-nav"

// retireNavHooks clears the focus-change hooks an older `apply` installed for
// the E-2081-removed navigation trail. `tmux init` gates on @server_uuid and so
// runs once per server lifetime, which means a server started before this
// change keeps invoking `endless-go tmux record-nav` — a subcommand that no
// longer exists — on every pane switch until it restarts. Re-running `apply`
// is the escape hatch, so apply is where the cleanup belongs.
//
// It unsets a hook ONLY when the installed command still names our recorder
// (see isNavRecorderHook): these are global hook names any user may have bound
// for their own purposes, and an unconditional `set-hook -gu` would silently
// delete theirs. Failures are ignored — nothing about `apply` should fail
// because a hook could not be inspected.
//
// Delete this once no tmux server predating E-2081 can plausibly still be up.
func retireNavHooks() {
	for _, hook := range navHookNames {
		// `show-options -gqv` prints the bare value and stays quiet (exit 0,
		// empty output) for a name that is not set. `show-hooks` lists names
		// only, so it cannot answer this question.
		out, err := exec.Command("tmux", "show-options", "-gqv", hook).Output()
		if err != nil || !isNavRecorderHook(string(out)) {
			continue
		}
		_ = runTmux("set-hook", "-gu", hook)
	}
}

// isNavRecorderHook reports whether a hook's installed command is the removed
// nav-trail recorder. The marker is the `tmux record-nav` verb rather than the
// whole command line, because the binary path in the installed hook is whatever
// argv[0] was when the old `apply` ran and differs per machine and per install.
func isNavRecorderHook(hookCommand string) bool {
	return strings.Contains(hookCommand, navRecorderMarker)
}
