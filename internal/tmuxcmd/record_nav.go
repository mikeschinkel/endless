package tmuxcmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/navvia"
)

// navViaOption is the one-shot tmux server option `session goto` sets to
// "goto" immediately before its switch-client. The recorder reads it to tag the
// resulting focus change, then clears it so the next (manual) move records as
// manual. It is the only coupling between goto and the recorder.
const navViaOption = "@endless_nav_via"

// runRecordNav records one durable navigation edge (E-1682). It is invoked by
// the global tmux focus-change hooks (client-session-changed /
// session-window-changed) installed by `apply`, and indirectly by `session
// goto` (whose switch-client trips the same hook). tmux passes the navigating
// client and the newly-focused pane:
//
//	endless-go tmux record-nav --client=#{client_name} --pane=#{pane_id}
//
// It reads + clears the one-shot @endless_nav_via marker to distinguish a
// goto-driven move from a manual one, then appends the edge via
// monitor.RecordNav. It fires on every focus change, so it stays fast and
// silent: errors go to stderr (where run-shell discards them) and it never
// exits non-zero, so a transient DB lock can't surface a tmux error.
func runRecordNav(args []string) {
	fs := flag.NewFlagSet("record-nav", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	client := fs.String("client", "", "tmux client_name (the navigator)")
	pane := fs.String("pane", "", "newly-focused tmux pane id")
	if err := fs.Parse(args); err != nil {
		return
	}
	if *client == "" || *pane == "" {
		// Nothing to attribute the move to; silently no-op rather than spam the
		// hook path. (A pane with no client_name can't be a navigation.)
		return
	}

	via := readAndClearNavVia()

	if _, err := monitor.RecordNav(*client, *pane, via); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux record-nav: %v\n", err)
	}
}

// readAndClearNavVia reads the @endless_nav_via marker and immediately unsets
// it (one-shot semantics). An unset/empty marker, or any unrecognized value,
// resolves to NavViaManual — the recorder never fails on a bad marker.
func readAndClearNavVia() navvia.NavVia {
	out, err := exec.Command("tmux", "show-options", "-gqv", navViaOption).Output()
	slug := ""
	if err == nil {
		slug = strings.TrimSpace(string(out))
	}
	if slug != "" {
		// Clear unconditionally; failure to clear is harmless (next move would
		// over-read the stale marker, but goto re-sets it each time anyway).
		_ = exec.Command("tmux", "set-option", "-gu", navViaOption).Run()
	}
	via, err := navvia.Parse(slug)
	if err != nil {
		return navvia.NavViaManual
	}
	return via
}
