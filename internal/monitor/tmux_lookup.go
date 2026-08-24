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
//
// EpicID is the session's epic_id (E-1571): nil for a non-epic
// session, the epic task id otherwise. The renderer compares it against TaskID
// to pick the [E-NNNN] / [E-EEEE] / [E-EEEE:E-CCCC] prefix shape.
type ActiveTaskInfo struct {
	TaskID      int64
	Title       string
	Status      string
	Type        string
	Phase       string
	Tier        *int64
	ProjectName string
	EpicID      *int64
}

// ErrNoActiveTask is returned when no working session, in either the
// requested pane or anywhere else in the same tmux window, has a
// non-NULL task_id. Callers should render an empty/placeholder
// status line rather than treat this as a fatal error.
var ErrNoActiveTask = errors.New("no active task for this tmux context")

// GetActiveTaskForPane resolves a tmux pane identifier (the value tmux
// passes in $TMUX_PANE, stored in sessions.process) to the active task
// the user should see in the status line.
//
// Lookup order:
//  1. Pane-specific: an Endless session whose process column matches
//     this exact pane and whose task_id is non-NULL.
//  2. Window-scoped fallback: any pane in the same tmux WINDOW has an
//     Endless session with a non-NULL task_id. Most recent
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
	ids, err := ProcessIDsForPanes(panes)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrNoActiveTask
	}

	placeholders, args := processIDArgs(ids)

	// Matching on process_id rather than a bare pane string is what makes this
	// lookup server-scoped (E-1898): ProcessIDsForPanes resolved these panes
	// against the CURRENT server's uuid, so a row bound to "%414" on a previous
	// server holds a different process_id and cannot win here. That is the
	// structural version of what E-1530 could only approximate by NULLing the
	// pane out of dead rows.
	//
	// state != 'ended' is still required, for the unrelated case of a session
	// that ended cleanly in a pane still open and rebound to a new session.
	q := `SELECT t.id, t.title, t.status, COALESCE(tt.slug, ''), t.phase, t.tier, COALESCE(p.name, ''), s.epic_id
	      FROM sessions s
	      JOIN live_tasks t ON t.id = s.task_id
	      LEFT JOIN projects p ON p.id = t.project_id
	      LEFT JOIN task_types tt ON tt.id = t.type_id
	      WHERE s.process_id IN (` + placeholders + `)
	        AND s.task_id IS NOT NULL
	        AND s.state != 'ended'
	      ORDER BY s.last_activity DESC
	      LIMIT 1`

	var info ActiveTaskInfo
	err = db.QueryRow(q, args...).Scan(
		&info.TaskID, &info.Title, &info.Status,
		&info.Type, &info.Phase, &info.Tier, &info.ProjectName, &info.EpicID,
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
	// non-NULL task_id. The Task field is populated.
	PaneStatusActive
	// PaneStatusNoTask — a window pane has an Endless session, but no
	// session has task_id set. Hint the user to `task claim`.
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

	// Inverted from "does this look like Claude" to "is this a bare shell"
	// (E-1898): the pane of a live Claude reports its version string, which no
	// name-matching test can keep up with. The trade is that a pane running
	// something else entirely — vim, less, a long build — in a window with NO
	// Endless session now shows the register hint instead of the placeholder.
	// Accepted: it is a hint, it is self-correcting, and the alternative is the
	// hint never firing at all, which is the state this replaced.
	//
	// `known` gates the inversion. If tmux could not tell us what the pane is
	// running, we have not learned that it is NOT a shell — we have learned
	// nothing, and the placeholder is the honest render.
	if isShell, known := paneIsRunningShell(tmuxPane); known && !isShell {
		return &PaneStatus{Kind: PaneStatusClaudeNoSession}, nil
	}

	return &PaneStatus{Kind: PaneStatusNone}, nil
}

// anySessionForPanes returns true when at least one Endless session row
// exists for any of the given pane IDs, regardless of task_id.
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

	ids, err := ProcessIDsForPanes(panes)
	if err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return false, nil
	}
	placeholders, args := processIDArgs(ids)

	var found int
	err = db.QueryRow(
		"SELECT 1 FROM sessions WHERE process_id IN ("+placeholders+") AND state != 'ended' LIMIT 1",
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

// knownShells is the set of interactive shells a pane reports when nothing else
// is in its foreground. Positively enumerated because shell names are stable —
// the alternative, enumerating what Claude looks like, is not (see
// isShellCommand).
var knownShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true,
}

// isShellCommand reports whether a pane_current_command value is an interactive
// shell — i.e. the pane is sitting at a prompt rather than running a harness.
//
// This inverts the old paneIsRunningClaude, which tested `cmd == "claude" ||
// cmd == "claude-code"` and was WRONG for every real Claude pane: Claude Code
// sets its process title to its version, so a live pane reports "2.1.220".
// The old helper therefore always returned false and the hint it gated never
// fired correctly. Shell names do not change between releases; version strings
// change every release, which is why the test is framed this way round.
//
// Pure function of the command string, so the truth table is testable without
// tmux.
func isShellCommand(cmd string) bool {
	return knownShells[strings.TrimSpace(cmd)]
}

// paneIsRunningShell asks tmux for the pane's current foreground command and
// reports whether it is a shell, plus whether we actually found out.
//
// The second return is not ceremony. Callers invert this test ("not a shell, so
// something is running here"), and a tmux failure returning a bare false would
// invert into a confident "something is running" about a pane we could not see
// at all — the same collapse of "unknown" into a verdict that liveness.go
// exists to prevent, in miniature. known=false means: draw no conclusion.
//
// SCOPE LIMIT, load-bearing: this may inform the cosmetic "pane is running
// Claude but has no session row" hint and NOTHING ELSE. It must never reach
// liveness. Ctrl+Z puts the shell back in the foreground, so a
// suspended-but-alive Claude reports "zsh" here — treating that as death would
// drop the session's status line and free its task to be claimed out from under
// it. A hint that is briefly wrong misleads nobody and owns nothing; a liveness
// verdict that is briefly wrong loses work. See internal/monitor/liveness.go.
func paneIsRunningShell(tmuxPane string) (isShell, known bool) {
	out, err := exec.Command("tmux",
		"display-message", "-p", "-t", tmuxPane, "#{pane_current_command}",
	).Output()
	if err != nil {
		return false, false
	}
	// An EMPTY answer is also "we did not find out". tmux exits 0 and prints
	// nothing when `-t` names a pane that does not exist on this server, so a
	// blank command is not evidence that the pane is running something other
	// than a shell — it is evidence there is no such pane to ask about.
	cmd := strings.TrimSpace(string(out))
	if cmd == "" {
		return false, false
	}
	return isShellCommand(cmd), true
}

// ResolveSessionStatusSession returns the emitting session's integer id for the
// given tmux pane, used by `session status` when NO goal is claimed so it can
// list the session's own surfaced/revisited rows (E-1802). Resolution mirrors
// the focal path (GetActiveTaskForPane): the pane's own live session first, then
// any live session in the same tmux WINDOW (most-recent last_activity). Unlike
// the focal path it does NOT require task_id — an unclaimed session still
// has surfaced/revisited work to show. Returns 0 (no error) when not in tmux
// (pane == "") or no live session is found.
func ResolveSessionStatusSession(pane string) (int64, error) {
	if pane == "" {
		return 0, nil
	}
	if id, err := sessionForPanes([]string{pane}); err != nil {
		return 0, err
	} else if id != 0 {
		return id, nil
	}
	panes, err := listPanesInSameWindow(pane)
	if err != nil || len(panes) == 0 {
		return 0, nil
	}
	return sessionForPanes(panes)
}

// sessionForPanes returns the most-recently-active live session id whose process
// matches any of the given panes, or 0 when none. Companion to anySessionForPanes
// (which only tests existence); this returns the id the no-goal view anchors on.
func sessionForPanes(panes []string) (int64, error) {
	if len(panes) == 0 {
		return 0, nil
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	ids, err := ProcessIDsForPanes(panes)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders, args := processIDArgs(ids)
	var id int64
	err = db.QueryRow(
		"SELECT id FROM sessions WHERE process_id IN ("+placeholders+") AND state != 'ended' ORDER BY last_activity DESC LIMIT 1",
		args...,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetActiveBlockers returns up to 3 task IDs that currently block taskID,
// ordered by id ASC. "Active" means the blocker's status is NOT in the
// terminal set {confirmed, assumed, declined, obsolete} — those statuses
// unblock dependents (see `endless guide tasks`, blocking semantics), so
// they don't belong in the status-line segment.
//
// The cap is 3 because the renderer shows at most two IDs inline plus a
// `+` overflow marker; fetching a third row tells the caller "there is
// more" without fetching all of them. Caller (status_line.format) limits
// display to the first two IDs and appends "+" when len == 3.
//
// Source rows are restricted to source_type='task' — project- and
// decision-sourced blockers are not surfaced in the per-task status
// segment.
//
// Returns nil (no error) when taskID has no active blockers.
func GetActiveBlockers(taskID int64) ([]int64, error) {
	if taskID == 0 {
		return nil, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT t.id
		   FROM task_deps td
		   JOIN live_tasks t ON t.id = td.source_id
		  WHERE td.target_type = 'task'
		    AND td.target_id = ?
		    AND td.source_type = 'task'
		    AND td.dep_type = 'blocks'
		    AND t.status NOT IN ('confirmed', 'assumed', 'declined', 'obsolete')
		  ORDER BY t.id ASC
		  LIMIT 3`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// GetLiveSessionByProcess returns the most-recently-active live session bound
// to the given tmux pane id (e.g. "%124") ON THE SERVER THIS PROCESS CAN REACH.
// Filters out state='ended' rows so the result is always the live binding.
//
// Per E-1312, this is the canonical session-discovery function for callers that
// know their pane — used by `endless session status add` and `endless task id`
// to map "I'm running in this tmux pane" to "I'm session N."
//
// The server scoping (E-1898) is the important part and is why this cannot be a
// plain string match: "%124" on a restarted tmux server is a different pane
// than "%124" was an hour ago, and answering "you are session N" from the wrong
// server's binding is how a session ends up writing under someone else's
// identity.
//
// Returns sql.ErrNoRows when no live session matches — including when the
// server cannot be identified, because an unidentifiable server must match
// nothing rather than fall back to a bare pane comparison.
func GetLiveSessionByProcess(process string) (int64, error) {
	if process == "" {
		return 0, sql.ErrNoRows
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	ids, err := ProcessIDsForPanes([]string{process})
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, sql.ErrNoRows
	}
	var id int64
	err = db.QueryRow(
		`SELECT id FROM sessions
		 WHERE process_id = ? AND state != 'ended'
		 ORDER BY last_activity DESC LIMIT 1`,
		ids[0],
	).Scan(&id)
	return id, err
}
