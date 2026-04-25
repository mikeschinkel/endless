package main

import (
	"fmt"
	"strconv"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func runRecap(args []string) error {
	// Parse flags first
	force := false
	var sessionArgs []string
	for _, arg := range args {
		if arg == "--force" {
			force = true
		} else {
			sessionArgs = append(sessionArgs, arg)
		}
	}

	if len(sessionArgs) == 0 {
		// Recap all that need it (with force if specified)
		ids := monitor.GetSessionsNeedingRecap()
		if len(ids) == 0 {
			fmt.Println("No sessions need recaps")
			return nil
		}
		for _, sessionID := range ids {
			summary, err := monitor.RecapSession(sessionID, force)
			if err != nil {
				fmt.Printf("  Error recapping %s: %v\n", sessionID[:8], err)
				continue
			}
			if summary != "" {
				fmt.Printf("  ✓ %s\n", truncate(summary, 100))
			}
		}
		return nil
	}

	// Recap specific session(s)
	for _, arg := range sessionArgs {

		// Try as integer ID first
		if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
			summary, err := monitor.RecapSessionByID(id, force)
			if err != nil {
				return fmt.Errorf("recap session %d: %w", id, err)
			}
			if summary != "" {
				fmt.Printf("  ✓ %s\n", truncate(summary, 100))
			} else {
				fmt.Printf("  Session %d: skipped (not enough new messages)\n", id)
			}
			continue
		}

		// Try as session UUID
		summary, err := monitor.RecapSession(arg, force)
		if err != nil {
			return fmt.Errorf("recap session %s: %w", arg, err)
		}
		if summary != "" {
			fmt.Printf("  ✓ %s\n", truncate(summary, 100))
		}
	}

	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
