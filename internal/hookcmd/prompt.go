package hookcmd

import (
	"fmt"
	"log"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func runPrompt(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: endless-hook prompt <directory>")
	}
	// Normalized for the same reason claude.go normalizes payload.CWD (E-2002):
	// the directory arrives from the shell, the project row was written
	// symlink-resolved by the Python CLI, and an unresolved spelling misses.
	dir := monitor.NormalizeProjectPath(args[0])

	// Look up project
	projectID, _, err := monitor.ProjectIDForPath(dir)
	if err != nil {
		return fmt.Errorf("looking up project for %s: %w", dir, err)
	}

	// Throttle: skip if last run < 5 seconds ago
	throttled, err := monitor.ShouldThrottle(projectID, "prompt", 5)
	if err != nil {
		return err
	}
	if throttled {
		return nil
	}

	// Get tmux context if available
	tmuxCtx, _ := monitor.GetTmuxContext()
	var sessionCtx map[string]string
	if tmuxCtx != nil {
		sessionCtx = tmuxCtx.ToMap()
	}

	// Backup DB (throttled internally to every 60s). A failed backup must not
	// fail the prompt hook — log it and carry on.
	if _, err := monitor.BackupDB(); err != nil {
		log.Printf("prompt hook: backup: %v", err)
	}

	// Record activity
	if err := monitor.RecordActivity(projectID, "prompt", dir, sessionCtx); err != nil {
		return err
	}

	return nil
}
