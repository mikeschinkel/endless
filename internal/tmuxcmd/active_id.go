package tmuxcmd

import (
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// runActiveID prints `E-NNNN` for the task held by the current pane's
// session, or nothing (exit 1) if it holds none. Used by menu items so
// they can pipe the ID into other commands without paying Python startup.
//
// Not advertised in the top-level usage — it's plumbing for the menus,
// not a user-facing verb. Power users may still call it.
func runActiveID(args []string) {
	fs := refusal.NewFlags("active-id")
	paneArg := fs.String("pane", "", "Tmux pane ID (overrides TMUX_PANE env)")
	if err := fs.Parse(args); err != nil {
		// Python passes only --pane (tmux_cmd.py), so a flag that misses here
		// was typed by hand. Text carries flag's own error line and usage
		// block, which is what a person read before this was captured.
		refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("tmux active-id").Text(fs.Output()).Exit(2)
	}

	pane := *paneArg
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}
	info, err := monitor.GetTaskForPane(pane)
	if err != nil {
		if errors.Is(err, monitor.ErrNoTask) {
			// Silent by design, and the silence is the interface: the menus and
			// the justfile take this command's stdout through $(...) where a
			// line on stderr would land in a tmux redraw, and `endless task id`
			// synthesizes the message a person reads from the exit status.
			os.Exit(1)
		}
		// tmux pins the main database (cmd/endless-go), so there is no --db
		// spelling an agent could retry: a read that fails here means the
		// install or the database itself has to be looked at.
		refusal.Report(
			fmt.Sprintf("endless-go tmux active-id: %v", err),
			"how to repair an Endless install whose database cannot say which task a pane holds",
		).Command("tmux active-id").Exit(1)
	}
	fmt.Printf("E-%d\n", info.TaskID)
}
