package main

import (
	"fmt"
	"log"

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
		log.Printf("looking up project for %s: %v", dir, err)
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
	return monitor.RecordActivity(projectID, "prompt", dir, sessionCtx)
}
