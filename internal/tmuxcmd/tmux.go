// Package tmuxcmd implements the `endless-go tmux` subcommand which
// drives the second tmux status line (E-1236).
//
// Subcommands:
//
//	apply        Ephemerally configure the running tmux server: enable a
//	             second status line, wire status-format[1] to the printer,
//	             install hotkey and right-click popup menus. No file I/O;
//	             survives until tmux server restart.
//	status-line  Print one styled line to stdout for tmux's status-format[1].
//	             Invoked by tmux on each status-interval refresh.
//
// Tmux config invokes the status-line subcommand directly so the binary's
// fast startup (~5ms) stays well under the <50ms latency budget. The
// `endless tmux <verb>` Python wrapper is provided for ergonomics; tmux
// itself should never go through it.
//
// The dispatcher (cmd/endless-go) invokes monitor.PinMainDB() before
// calling Run() — see E-1429/E-1450 notes in cmd/endless-go/main.go.
package tmuxcmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
)

func Run(args []string) {
	if len(args) < 1 {
		refusal.NoReport(
			"endless-go tmux: no command given",
			"Re-run with one of the listed subcommands",
		).Command("tmux").Text(usageText()).Exit(2)
	}

	switch args[0] {
	case "apply":
		runApply(args[1:])
	case "init":
		runInit(args[1:])
	// `reset` was removed by E-1898. It wrapped the dead-pane reaper, which no
	// longer exists: liveness is derived at read time from a per-invocation
	// observation rather than swept into the sessions table, so there is no
	// stale state for an operator verb to clear.
	case "status-line":
		runStatusLine(args[1:])
	case "active-id":
		runActiveID(args[1:])
	case "show-menu":
		runShowMenu(args[1:])
	// `record-nav` was removed by E-2081 along with the whole
	// session-navigation trail. A tmux server still running the focus-change
	// hooks an older `apply` installed will invoke it until the server
	// restarts; `apply` now retires those hooks (see retireNavHooks).
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		// Two readers, and only one of them is a person. A tmux server started
		// before E-2081 still fires `tmux record-nav` from the focus-change
		// hooks an older apply installed, on every pane switch; run-shell
		// discards that stderr and shows only the non-zero exit in view mode.
		// Either way the fix is the same verb list, so the class does not turn
		// on which reader got here.
		refusal.NoReport(
			fmt.Sprintf("endless-go tmux: unknown command %q", args[0]),
			"Re-run with one of the listed subcommands",
		).Command("tmux").Detail(usageText()).Exit(2)
	}
}

// usageText is the command list, printed for --help and carried as the body of
// the two usage refusals above. It advertises init/apply/status-line only:
// active-id and show-menu are plumbing for the menus, not verbs a user types.
func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go tmux <command> [flags]",
		"Commands:",
		"  init         Init the current tmux server (apply, gated by @server_uuid)",
		"  apply        Configure the running tmux server (ephemeral)",
		"  status-line  Print one styled line for status-format[1]",
	}, "\n") + "\n"
}
