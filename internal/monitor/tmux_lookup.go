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

// PaneStatusKind classifies what the status row should render for a
// given tmux pane. Drives the contextual-hint logic in the printer.
type PaneStatusKind int

const (
	// PaneStatusNone — no Endless context to display for this pane or
	// any pane in its window, and the pane is not running Claude.
	// Render the dim placeholder dot.
	PaneStatusNone PaneStatusKind = iota
	// PaneStatusActive — a window pane has an Endless session with a
	// non-NULL active_task_id. The Task field is populated.
	PaneStatusActive
	// PaneStatusNoTask — a window pane has an Endless session, but no
	// session has active_task_id set. Hint the user to `task claim`.
	PaneStatusNoTask
	// PaneStatusClaudeNoSession — the focused pane is running Claude,
	// but no Endless session has been registered for any pane in this
	// window. Hint the user to register (usually means the Claude hook
	// isn't installed, or the session predates the install).
	PaneStatusClaudeNoSession
)

// PaneStatus is the result of inspecting a tmux pane for status-bar
// content. Task is populated only when Kind == PaneStatusActive.
type PaneStatus struct {
	Kind PaneStatusKind
	Task *ActiveTaskInfo
}

// GetPaneStatus is the higher-level companion to GetActiveTaskForPane.
// Beyond "find an active task," it classifies what the bar should show:
//
//  1. PaneStatusActive — there's an active task; render it.
//  2. PaneStatusNoTask — a session exists in this window but no task
//     is claimed; render a "claim a task" hint.
//  3. PaneStatusClaudeNoSession — the focused pane is running Claude
//     but Endless has no session row for any pane in this window;
//     render a "register session" hint.
//  4. PaneStatusNone — none of the above; render the placeholder.
//
// Detection uses two extra tmux queries beyond the existing DB lookup:
// `tmux list-panes` for the window's pane set (already used by the
// fallback) and `tmux display-message -p -t <pane> #{pane_current_command}`
// to detect Claude in the focused pane. Both are cheap (<5ms).
func GetPaneStatus(tmuxPane string) (*PaneStatus, error) {
	if tmuxPane == "" {
		return &PaneStatus{Kind: PaneStatusNone}, nil
	}

	if info, err := GetActiveTaskForPane(tmuxPane); err == nil {
		return &PaneStatus{Kind: PaneStatusActive, Task: info}, nil
	} else if !errors.Is(err, ErrNoActiveTask) {
		return nil, err
	}

	// No active task. Determine which hint (if any) to show.
	panes, err := listPanesInSameWindow(tmuxPane)
	if err != nil {
		panes = []string{tmuxPane}
	}
	if len(panes) == 0 {
		panes = []string{tmuxPane}
	}

	hasSession, err := anySessionForPanes(panes)
	if err != nil {
		return nil, err
	}
	if hasSession {
		return &PaneStatus{Kind: PaneStatusNoTask}, nil
	}

	if paneIsRunningClaude(tmuxPane) {
		return &PaneStatus{Kind: PaneStatusClaudeNoSession}, nil
	}

	return &PaneStatus{Kind: PaneStatusNone}, nil
}

// anySessionForPanes returns true when at least one Endless session row
// exists for any of the given pane IDs, regardless of active_task_id.
// Used to distinguish "session exists but no task" from "no session at
// all" — the two states drive different hint text.
func anySessionForPanes(panes []string) (bool, error) {
	if len(panes) == 0 {
		return false, nil
	}

	db, err := DB()
	if err != nil {
		return false, err
	}

	placeholders := strings.Repeat("?,", len(panes))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(panes))
	for i, p := range panes {
		args[i] = p
	}

	var found int
	err = db.QueryRow(
		"SELECT 1 FROM sessions WHERE process IN ("+placeholders+") LIMIT 1",
		args...,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// paneIsRunningClaude asks tmux for the pane's current foreground
// command and returns true when it looks like a Claude session.
// Matches Claude Code's known process names ("claude", "claude-code").
// Best-effort: returns false on any tmux error rather than propagating.
func paneIsRunningClaude(tmuxPane string) bool {
	out, err := exec.Command("tmux",
		"display-message", "-p", "-t", tmuxPane, "#{pane_current_command}",
	).Output()
	if err != nil {
		return false
	}
	cmd := strings.TrimSpace(string(out))
	return cmd == "claude" || cmd == "claude-code"
}

// GetLiveSessionByProcess returns the most-recently-active live session
// whose `process` column matches the given identifier (typically a tmux
// pane id like "%124"). Filters out state='ended' rows so the result is
// always the live binding for the given process.
//
// Per E-1312, this is the canonical session-discovery function for
// callers that know their process identifier — used by `endless session
// status add` and `endless task id` to map "I'm running in this tmux
// pane" to "I'm session N."
//
// Returns sql.ErrNoRows when no live session matches.
func GetLiveSessionByProcess(process string) (int64, error) {
	if process == "" {
		return 0, sql.ErrNoRows
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.QueryRow(
		`SELECT id FROM sessions
		 WHERE process = ? AND state != 'ended'
		 ORDER BY last_activity DESC LIMIT 1`,
		process,
	).Scan(&id)
	return id, err
}
