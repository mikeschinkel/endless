package main

import (
	"fmt"
	"os"
	"os/exec"

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

	// Record activity
	if err := monitor.RecordActivity(projectID, "prompt", dir, sessionCtx); err != nil {
		return err
	}

	// Trigger background recap if any sessions need it (throttled to every 5 minutes)
	recapThrottled, _ := monitor.ShouldThrottle(projectID, "recap", 300)
	if !recapThrottled {
		if triggerBackgroundRecap() {
			// Record activity to enable throttling
			monitor.RecordActivity(projectID, "recap", dir, nil)
		}
	}

	return nil
}

// triggerBackgroundRecap spawns a background process to recap one stale session.
// The process runs independently — the prompt hook exits immediately.
// Returns true if a recap was triggered.
func triggerBackgroundRecap() bool {
	ids := monitor.GetSessionsNeedingRecap()
	if len(ids) == 0 {
		return false
	}

	// Find our own binary path
	self, err := os.Executable()
	if err != nil {
		return false
	}

	// Recap just one session to limit cost/latency
	cmd := exec.Command(self, "recap", ids[0], "--force")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Start detached — don't wait for it
	if err := cmd.Start(); err != nil {
		return false
	}
	// Note: we intentionally don't call cmd.Wait() — the process runs in background
	return true
}
