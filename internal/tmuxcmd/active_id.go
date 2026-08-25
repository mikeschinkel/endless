package tmuxcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// runActiveID prints `E-NNNN` for the task held by the current pane's
// session, or nothing (exit 1) if it holds none. Used by menu items so
// they can pipe the ID into other commands without paying Python startup.
//
// Not advertised in the top-level usage — it's plumbing for the menus,
// not a user-facing verb. Power users may still call it.
func runActiveID(args []string) {
	fs := flag.NewFlagSet("active-id", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	paneArg := fs.String("pane", "", "Tmux pane ID (overrides TMUX_PANE env)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	pane := *paneArg
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}
	info, err := monitor.GetTaskForPane(pane)
	if err != nil {
		if errors.Is(err, monitor.ErrNoTask) {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "endless-tmux active-id: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("E-%d\n", info.TaskID)
}
