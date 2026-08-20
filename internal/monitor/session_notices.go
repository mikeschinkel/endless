package monitor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Notice is one undelivered change notice for a session (E-1917): a snapshot,
// taken at change time by the tasks_notify_sessions trigger, of what moved on a
// task this session holds.
//
// Changes is the raw JSON from the trigger, kept as text rather than decoded at
// query time so the log can record exactly what the trigger wrote.
type Notice struct {
	ID        int64
	TaskID    int64
	Changes   string
	ChangedAt string
}

// noticeChange is one field's before/after inside Notice.Changes. The values are
// `any` because they are heterogeneous by design: a string for status and phase,
// a number for tier, JSON null for an absent value, and the elision sentinel for
// freeform fields.
type noticeChange struct {
	Before any `json:"before"`
	After  any `json:"after"`
}

// elisionSentinel marks freeform content the notice deliberately does not
// reproduce. Mirrors the '…' written by the tasks_notify_sessions trigger in
// internal/schema/schema.sql — change it in both places or the renderer stops
// recognising elided content. It is one character (U+2026), not three dots.
const elisionSentinel = "…"

// noticeLandedKey is the synthetic key the task_landings_notify_sessions trigger
// writes (E-2005). It is deliberately NOT in noticeFieldOrder below: a landing
// is not a field that moved from one value to another, so it renders as its own
// sentence rather than as a "before → after" pair.
const noticeLandedKey = "landed"

// noticeFieldOrder pins the rendering order of a multi-field change. Go map
// iteration is randomised, so without this the same edit would render its fields
// in a different order on every run — noise in a log meant for eyeballing, and
// churn in golden test output.
var noticeFieldOrder = []string{
	"status", "phase", "tier", "description", "text", "analysis", "notes",
}

// noticeFreeform is the set of fields whose content the trigger elides. They
// render as a verb ("description edited") rather than a before → after pair,
// because their values are sentinels, not content.
var noticeFreeform = map[string]bool{
	"description": true, "text": true, "analysis": true, "notes": true,
}

// PendingNotices returns this session's undelivered notices, oldest first.
//
// Delivery is one-shot by design: re-asserting task state on every turn would
// cost tokens on every turn forever, and the cost of a missed notice is only the
// status quo — the user correcting the agent by hand, exactly as they do today.
func PendingNotices(sessionID int64) ([]Notice, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT id, task_id, changes, changed_at
		   FROM session_notices
		  WHERE session_id = ? AND notified = 0
		  ORDER BY id`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying session notices: %w", err)
	}
	defer rows.Close()

	var out []Notice
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.TaskID, &n.Changes, &n.ChangedAt); err != nil {
			return nil, fmt.Errorf("scanning session notice: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading session notices: %w", err)
	}
	return out, nil
}

// MarkNoticesDelivered flags notices as delivered so they are never shown twice.
//
// Called only after the rendered text has been handed to the hook response, so a
// notice that fails to render stays pending rather than being silently consumed.
func MarkNoticesDelivered(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	_, err = db.Exec(
		"UPDATE session_notices SET notified = 1 WHERE id IN ("+
			strings.Join(placeholders, ",")+")",
		args...,
	)
	if err != nil {
		return fmt.Errorf("marking session notices delivered: %w", err)
	}
	return nil
}

// RenderNotice turns one notice into the single line injected into the agent's
// context, e.g.
//
//	FYI — E-1234 status: ready → revisit; description edited
//
// Returns ok=false when the notice carries nothing renderable (malformed or
// empty JSON), so the caller can leave it pending rather than deliver a blank.
func RenderNotice(n Notice) (string, bool) {
	var changes map[string]noticeChange
	if err := json.Unmarshal([]byte(n.Changes), &changes); err != nil {
		return "", false
	}
	// A landing arrives alone, from its own trigger on its own table, so it is
	// answered before the field loop rather than merged into it.
	if landed, ok := changes[noticeLandedKey]; ok {
		return fmt.Sprintf("FYI — E-%d %s", n.TaskID, landedPhrase(landed.After)), true
	}
	var parts []string
	for _, field := range noticeFieldOrder {
		change, ok := changes[field]
		if !ok {
			continue
		}
		if noticeFreeform[field] {
			parts = append(parts, field+" "+freeformVerb(change))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s → %s",
			field, noticeValue(change.Before), noticeValue(change.After)))
	}
	if len(parts) == 0 {
		return "", false
	}
	return fmt.Sprintf("FYI — E-%d %s", n.TaskID, strings.Join(parts, "; ")), true
}

// landedPhrase renders the `landed` notice's after-value, which the trigger
// encodes as "<base_branch>@<sha7>" — or as a bare sha7 when the landing
// recorded no base branch (a record-only backfill, E-1719, which has none to
// record).
//
// Split on the LAST '@' because a git branch name may legally contain one
// (`feature@2`) while an abbreviated sha never does.
func landedPhrase(after any) string {
	value, _ := after.(string)
	if at := strings.LastIndex(value, "@"); at > 0 {
		return fmt.Sprintf("landed on %s (%s)", value[:at], value[at+1:])
	}
	if value == "" {
		return "landed"
	}
	return fmt.Sprintf("landed (%s)", value)
}

// freeformVerb names what happened to an elided field. The four cases are
// exactly the transitions the trigger's sentinel encoding preserves: content is
// never reproduced, but whether it arrived, left, or merely changed still is.
func freeformVerb(c noticeChange) string {
	hadContent := c.Before == elisionSentinel
	hasContent := c.After == elisionSentinel
	switch {
	case c.After == nil:
		return "cleared"
	case !hasContent:
		// Not NULL and not the sentinel: the empty string.
		return "emptied"
	case !hadContent:
		// Sentinel now, NULL or empty before.
		return "added"
	default:
		return "edited"
	}
}

// noticeValue renders one side of an enumerated change. An em dash stands in for
// a value that was absent, so "tier: — → 2" reads as a value arriving rather
// than as a missing field.
func noticeValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "—"
	case string:
		if t == "" {
			return "—"
		}
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// TaskHeadline is the mutable state of a task worth re-stating on every turn.
type TaskHeadline struct {
	Title   string
	Status  string
	Phase   string
	Tier    int64
	HasTier bool
}

// GetTaskHeadline loads the fields the per-prompt active-task line re-asserts.
//
// This is the one place state IS re-stated every turn rather than delivered once
// (E-1917 Arm 2), and the redundancy with the notice above is deliberate:
// notices are one-shot, so a context compaction discards any already delivered
// and nothing will ever re-tell the session. This line is re-injected after a
// compaction, and it costs a handful of tokens on a line that already ships.
// Scoped to the ACTIVE task only — the one most likely to still be under
// discussion after a compaction, and the one where a stale belief is most
// expensive. Widening it to every session task is what would turn a cheap line
// into per-turn bloat.
func GetTaskHeadline(taskID int64) (TaskHeadline, error) {
	var h TaskHeadline
	db, err := DB()
	if err != nil {
		return h, err
	}
	var title, status, phase sql.NullString
	var tier sql.NullInt64
	err = db.QueryRow(
		`SELECT title, status, phase, tier FROM live_tasks WHERE id=?`, taskID,
	).Scan(&title, &status, &phase, &tier)
	if err == sql.ErrNoRows {
		return h, nil
	}
	if err != nil {
		return h, fmt.Errorf("loading task headline: %w", err)
	}
	h.Title = title.String
	h.Status = status.String
	h.Phase = phase.String
	h.Tier = tier.Int64
	h.HasTier = tier.Valid && tier.Int64 > 0
	return h, nil
}

// Render formats the active-task line, e.g.
//
//	Active task: E-1234 (underway · tier 2 · now) — Some title.
//
// The parenthetical is omitted entirely when nothing is known, so a task row
// that has gone missing degrades to the old ID-and-title line rather than
// rendering empty separators.
func (h TaskHeadline) Render(taskID int64) string {
	var facts []string
	if h.Status != "" {
		facts = append(facts, h.Status)
	}
	if h.HasTier {
		facts = append(facts, fmt.Sprintf("tier %d", h.Tier))
	}
	if h.Phase != "" {
		facts = append(facts, h.Phase)
	}
	if len(facts) == 0 {
		return fmt.Sprintf("Active task: E-%d — %s.", taskID, h.Title)
	}
	return fmt.Sprintf("Active task: E-%d (%s) — %s.",
		taskID, strings.Join(facts, " · "), h.Title)
}

// noticeLogRelPath is where delivery is recorded, relative to the project root.
// `logs/` (plural) matches ~/.claude/logs/; the basename matches the table.
const noticeLogRelPath = ".endless/logs/session-notices.jsonl"

// NoticeLogPath returns the delivery log's absolute path for a project root.
func NoticeLogPath(projectRoot string) string {
	return filepath.Join(projectRoot, noticeLogRelPath)
}

// AppendNoticeLog records that a notice was DELIVERED to a session.
//
// Only delivery is logged, and only from the hook. The write side is already
// durably recorded in session_notices itself, so logging it again would be
// redundant with a table that can simply be queried — whereas delivery otherwise
// leaves no trace of when it happened or what the agent was actually shown. A
// notice written but never delivered is a tree falling in an empty forest, and
// its absence from this log is precisely how that shows up.
//
// Best-effort: a logging failure must never break the hook, which is on the
// critical path of every turn. Errors go to stderr, where the hook's own
// diagnostics already go, and are otherwise swallowed.
func AppendNoticeLog(projectRoot string, sessionID int64, n Notice, rendered string) {
	path := NoticeLogPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "endless: notice log mkdir: %v\n", err)
		return
	}
	entry := map[string]any{
		"at":         time.Now().UTC().Format(time.RFC3339),
		"session":    sessionID,
		"task":       n.TaskID,
		"notice_id":  n.ID,
		"changed_at": n.ChangedAt,
		"changes":    json.RawMessage(n.Changes),
		"rendered":   rendered,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless: notice log marshal: %v\n", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless: notice log open: %v\n", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "endless: notice log write: %v\n", err)
	}
}

// ReapNoticesForEndedSessions deletes undelivered notices belonging to sessions
// that have ended (E-1917 fix).
//
// The trigger's fan-out already skips ended sessions, but that is not enough on
// its own: a session can end AFTER its notice is written, and that row is then
// undeliverable forever because an ended session never takes another turn. Left
// alone the table grows without bound — 553 of 679 rows (82%) were stranded this
// way before this existed, the largest holders being sessions ended days
// earlier.
//
// Only notified = 0 rows are removed. A delivered notice is a record of what an
// agent was actually shown and is left alone, matching the delivery log.
//
// Opportunistic and best-effort, called from the hook alongside the other
// reapers: cheap when there is nothing to reap, and a failure must never break
// the turn it runs on.
func ReapNoticesForEndedSessions() error {
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`DELETE FROM session_notices
		  WHERE notified = 0
		    AND session_id IN (SELECT id FROM sessions WHERE state = 'ended')`,
	)
	if err != nil {
		return fmt.Errorf("reaping notices for ended sessions: %w", err)
	}
	return nil
}
