package monitor

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ReapDeadTmuxPanes marks as 'ended' any non-ended sessions for projectID
// whose `process` is a tmux pane id but no longer corresponds to a live
// pane. Cleans up rows for sessions that terminated without firing
// SessionEnd (pane closed, tmux killed, machine rebooted, ...).
//
// Tmux-specific by design: only rows whose `process` matches tmux's
// %<digits> form are eligible. Non-tmux process values (e.g. "pid:1234")
// are untouched and governed by their own platform's lifecycle. A future
// non-tmux harness adds its own reaper rather than extending this one.
//
// Cleanly no-ops when tmux is unavailable; the reaper is opportunistic,
// not authoritative. Lifecycle hooks (SessionEnd → EndSession) and
// SessionStart's collision invalidation (TouchSession) remain the
// primary mechanisms; this is the safety net for the unclean cases.
func ReapDeadTmuxPanes(projectID int64) error {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		// tmux not running, no server, or any other tmux-side failure:
		// silently skip. The reaper is opportunistic, not authoritative.
		return nil
	}

	alive := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if pane := strings.TrimSpace(scanner.Text()); pane != "" {
			alive[pane] = struct{}{}
		}
	}

	db, err := DB()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	// No live %-panes at all (tmux running but empty, or only non-pane
	// output): every tmux-format row in this project is dead. NULL out
	// `process` along with the state flip so reused pane ids can't pull
	// these rows back into a lookup (E-1530, Layer A).
	if len(alive) == 0 {
		_, err = db.Exec(
			`UPDATE sessions SET state='ended', process=NULL, last_activity=?
			 WHERE state != 'ended' AND process GLOB '%[0-9]*' AND project_id = ?`,
			now, projectID,
		)
		return err
	}

	placeholders := make([]string, 0, len(alive))
	args := []any{now, projectID}
	for pane := range alive {
		placeholders = append(placeholders, "?")
		args = append(args, pane)
	}
	// The GLOB literal contains a `%` which fmt.Sprintf reads as a verb;
	// escape it as `%%` so the final SQL has `'%[0-9]*'`.
	query := fmt.Sprintf(
		`UPDATE sessions SET state='ended', process=NULL, last_activity=?
		 WHERE state != 'ended'
		   AND process GLOB '%%[0-9]*'
		   AND project_id = ?
		   AND process NOT IN (%s)`,
		strings.Join(placeholders, ","),
	)
	_, err = db.Exec(query, args...)
	return err
}
