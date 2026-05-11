package monitor

import (
	"database/sql"
	"errors"
	"os/exec"
	"strings"
)

// ActiveTaskInfo is the read-only projection used by the tmux status
// line and menu: enough to render the second status row
// ("[E-NNNN] · project · type · phase · tier · status") plus the title
// for popup display.
//
// Tier is a *int64 because `tasks.tier` is nullable; nil means
// "not set", which the renderer skips so the row doesn't show "tier: ".
type ActiveTaskInfo struct {
	TaskID      int64
	Title       string
	Status      string
	Type        string
	Phase       string
	Tier        *int64
	ProjectName string
}

// ErrNoActiveTask is returned when no working session, in either the
// requested pane or anywhere else in the same tmux window, has a
// non-NULL active_task_id. Callers should render an empty/placeholder
// status line rather than treat this as a fatal error.
var ErrNoActiveTask = errors.New("no active task for this tmux context")

// GetActiveTaskForPane resolves a tmux pane identifier (the value tmux
// passes in $TMUX_PANE, stored in sessions.process) to the active task
// the user should see in the status line.
//
// Lookup order:
//  1. Pane-specific: an Endless session whose process column matches
//     this exact pane and whose active_task_id is non-NULL.
//  2. Window-scoped fallback: any pane in the same tmux WINDOW has an
//     Endless session with a non-NULL active_task_id. Most recent
//     last_activity wins.
//
// The fallback exists because tmux's #() substitution runs in the
// FOCUSED pane's environment. Without it, focusing on a shell pane
// next to a Claude pane in the same window would blank the status
// row. Limiting the fallback to the focused WINDOW (not the entire
// tmux session) ensures different windows show different tasks when
// the user has multiple Claude sessions across windows.
//
// Returns ErrNoActiveTask when both lookups come up empty.
func GetActiveTaskForPane(tmuxPane string) (*ActiveTaskInfo, error) {
	if tmuxPane == "" {
		return nil, ErrNoActiveTask
	}

	db, err := DB()
	if err != nil {
		return nil, err
	}

	if info, err := queryActiveTaskForPanes(db, []string{tmuxPane}); err == nil {
		return info, nil
	} else if !errors.Is(err, ErrNoActiveTask) {
		return nil, err
	}

	panes, err := listPanesInSameWindow(tmuxPane)
	if err != nil || len(panes) == 0 {
		return nil, ErrNoActiveTask
	}
	return queryActiveTaskForPanes(db, panes)
}

func queryActiveTaskForPanes(db *sql.DB, panes []string) (*ActiveTaskInfo, error) {
	if len(panes) == 0 {
		return nil, ErrNoActiveTask
	}

	placeholders := strings.Repeat("?,", len(panes))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing comma

	args := make([]any, len(panes))
	for i, p := range panes {
		args[i] = p
	}

	q := `SELECT t.id, t.title, t.status, t.type, t.phase, t.tier, COALESCE(p.name, '')
	      FROM sessions s
	      JOIN tasks t ON t.id = s.active_task_id
	      LEFT JOIN projects p ON p.id = t.project_id
	      WHERE s.process IN (` + placeholders + `)
	        AND s.active_task_id IS NOT NULL
	      ORDER BY s.last_activity DESC
	      LIMIT 1`

	var info ActiveTaskInfo
	err := db.QueryRow(q, args...).Scan(
		&info.TaskID, &info.Title, &info.Status,
		&info.Type, &info.Phase, &info.Tier, &info.ProjectName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoActiveTask
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// listPanesInSameWindow asks tmux for every pane in the same tmux
// WINDOW as targetPane. Returns the list of pane IDs (`%N` form).
// Uses `tmux list-panes -t <pane>` (no `-s`/`-a` flag) so the result
// is scoped to the target's window only — not the whole session, not
// all sessions on the server.
func listPanesInSameWindow(targetPane string) ([]string, error) {
	out, err := exec.Command("tmux",
		"list-panes", "-t", targetPane, "-F", "#{pane_id}",
	).Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	panes := lines[:0]
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			panes = append(panes, l)
		}
	}
	return panes, nil
}
