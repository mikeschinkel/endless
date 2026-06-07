package tmuxcmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// runReset wraps monitor.ReapDeadTmuxPanes for the current project and
// exposes it as a CLI verb. Marks any non-ended sessions whose `process`
// is a tmux pane id no longer present on the live server as ended
// (and NULLs their process), so reused pane ids after a tmux restart
// can't resolve to ghost rows (E-1530).
//
// Useful for debugging and as the building block for `endless tmux init`,
// which calls this on the first invocation after a fresh tmux server.
//
// Refuses to run outside tmux: the reaper inspects the live tmux server's
// pane set to decide what's dead. Without tmux, "alive" is empty and
// every tmux-pane row in the project would be marked ended — a footgun.
func runReset(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	fs.Parse(args)

	if os.Getenv("TMUX") == "" {
		fmt.Fprintln(os.Stderr, "endless-go tmux reset: not inside a tmux session ($TMUX is empty)")
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux reset: getwd: %v\n", err)
		os.Exit(1)
	}
	projectID, _, err := monitor.ProjectIDForPath(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux reset: resolve project: %v\n", err)
		os.Exit(1)
	}

	before, err := countTmuxGhosts(projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux reset: count before: %v\n", err)
		os.Exit(1)
	}

	if err = monitor.ReapDeadTmuxPanes(projectID); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux reset: reap: %v\n", err)
		os.Exit(1)
	}

	after, err := countTmuxGhosts(projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux reset: count after: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("endless-go tmux reset: marked %d session row(s) ended (project=%d)\n",
		before-after, projectID)
}

// countTmuxGhosts counts non-ended sessions for the project whose
// process looks like a tmux pane id. Drives the before/after delta in
// `reset`'s summary line.
func countTmuxGhosts(projectID int64) (int, error) {
	db, err := monitor.DB()
	if err != nil {
		return 0, err
	}
	var n int
	err = db.QueryRow(
		`SELECT count(*) FROM sessions
		 WHERE state != 'ended' AND process GLOB '%[0-9]*' AND project_id = ?`,
		projectID,
	).Scan(&n)
	return n, err
}
