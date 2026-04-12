package main

import (
	"fmt"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func runPrompt(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: endless-hook prompt <directory>")
	}
	dir := args[0]

	// Look up project
	projectID, _, err := monitor.ProjectIDForPath(dir)
	if err != nil {
		return nil
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

	// Record activity
	err = monitor.RecordActivity(projectID, "prompt", dir, sessionCtx)
	if err != nil {
		return err
	}

	// Detect and record file changes
	row := struct{ path string }{}
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	err = db.QueryRow(
		"SELECT path FROM projects WHERE id = ?", projectID,
	).Scan(&row.path)
	if err != nil {
		return err
	}

	changes, err := monitor.DetectFileChanges(projectID, row.path)
	if err != nil {
		return err
	}

	if len(changes) > 0 {
		err = monitor.RecordFileChanges(projectID, changes, "prompt")
		if err != nil {
			return err
		}
	}

	return nil
}
