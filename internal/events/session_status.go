// Session-status event handling (E-1312 / E-1314). Inserts a row into
// the session_statuses table after dedup against the latest row for the
// same session. Renders the row back as markdown for chat display.
//
// E-1314 schema:
// - One `tasks` column carries all task elements; disposition (resolved/
//   pending/blocked/verify) is derived at render time from each task's
//   status attribute, removing the redundant 4-column shape.
// - `active_task_id` resolved from sessions at INSERT time; not in
//   payload (Go-side concern).
// - `summary` carries structured `<layer name="..." files="...">purpose
//   </layer>` children that render as a 3-column markdown table.

package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// execSessionStatusRecorded handles the KindSessionStatusRecorded event.
// Resolves the session via the payload's `process` identifier, runs a
// dedup check against the latest row for that session, INSERTs if new,
// and returns the rendered markdown either way.
//
// Dedup-skip path: returns ExecuteResult{Skipped: true, Markdown: "..."}
// without inserting. The ledger entry has already been written by the
// caller (per the events-authoritative-first design), so the audit log
// still reflects "this session attempted to record state X at time Y."
func execSessionStatusRecorded(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p SessionStatusRecordedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal session_status.recorded payload: %w", err)
	}

	sessionID, err := monitor.GetLiveSessionByProcess(p.Process)
	if err != nil {
		return nil, fmt.Errorf(
			"events: no live session for process %q: %w", p.Process, err,
		)
	}

	// E-1314: pull the session's currently bound task at the moment of
	// the status row, so SQL joins to tasks can find this row without an
	// extra subquery against sessions.
	activeTaskID, err := sessionActiveTaskID(db, sessionID)
	if err != nil {
		return nil, fmt.Errorf("events: lookup session active_task_id: %w", err)
	}

	if dup, err := isDuplicateOfLatest(db, sessionID, &p); err != nil {
		return nil, fmt.Errorf("events: dedup check: %w", err)
	} else if dup {
		return &ExecuteResult{
			Skipped: true,
			Markdown: renderSessionStatusMarkdown(&p) +
				"\n\n_(skipped: identical to latest status for this session)_\n",
		}, nil
	}

	res, err := db.Exec(
		`INSERT INTO session_statuses
		 (session_id, active_task_id, headline, tasks, decisions,
		  commits, memory, summary, notes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, activeTaskID,
		p.Headline, p.Tasks, p.Decisions, p.Commits, p.Memory, p.Summary, p.Notes,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert session_status: %w", err)
	}
	rowID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("events: read inserted row id: %w", err)
	}

	return &ExecuteResult{
		SessionStatusID: rowID,
		Markdown:        renderSessionStatusMarkdown(&p),
	}, nil
}

// sessionActiveTaskID returns the session's currently bound task id, or
// nil if no task is bound. Wrapped in *int64 so the INSERT can pass it
// straight through to the nullable column.
func sessionActiveTaskID(db dbQuerier, sessionID int64) (*int64, error) {
	var atid *int64
	err := db.QueryRow(
		`SELECT active_task_id FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&atid)
	if err != nil {
		return nil, err
	}
	return atid, nil
}

// isDuplicateOfLatest returns true iff the latest row for sessionID has
// every text column byte-equal to the payload's. NULL columns in the
// existing row compare against "" in the payload — they're equivalent
// for the "no content" case.
func isDuplicateOfLatest(db dbQuerier, sessionID int64, p *SessionStatusRecordedPayload) (bool, error) {
	var (
		headline, tasks, decisions, commits, memoryCol, summary, notes *string
	)
	err := db.QueryRow(
		`SELECT headline, tasks, decisions, commits, memory, summary, notes
		 FROM session_statuses
		 WHERE session_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		sessionID,
	).Scan(&headline, &tasks, &decisions, &commits, &memoryCol, &summary, &notes)
	if err != nil {
		// No prior row → not a duplicate.
		return false, nil
	}
	return nullableEq(headline, p.Headline) &&
		nullableEq(tasks, p.Tasks) &&
		nullableEq(decisions, p.Decisions) &&
		nullableEq(commits, p.Commits) &&
		nullableEq(memoryCol, p.Memory) &&
		nullableEq(summary, p.Summary) &&
		nullableEq(notes, p.Notes), nil
}

// nullableEq compares a *string from a nullable column to a non-null
// string from the payload. A NULL column equals "" payload (both mean
// "no content"). Otherwise byte-equal.
func nullableEq(col *string, payload string) bool {
	if col == nil {
		return payload == ""
	}
	return *col == payload
}

// renderSessionStatusMarkdown formats a payload as markdown for chat
// display. Tasks are grouped by status-derived disposition; structured
// sections render as tables; empty sections render `(empty)` so the
// document structure stays visible.
func renderSessionStatusMarkdown(p *SessionStatusRecordedPayload) string {
	var b strings.Builder
	if p.Headline != "" {
		b.WriteString("## Status\n")
		b.WriteString(p.Headline)
		b.WriteString("\n\n")
	}

	renderTasksGrouped(&b, p.Tasks)
	renderDecisions(&b, p.Decisions)
	renderCommits(&b, p.Commits)
	renderMemory(&b, p.Memory)
	renderSummary(&b, p.Summary)

	if p.Notes != "" {
		b.WriteString("## Notes\n")
		b.WriteString(p.Notes)
		b.WriteString("\n")
	}
	return b.String()
}

// renderTasksGrouped walks the flat <task> list and emits 4 sections
// (Resolved / Pending / Blocked / Verify), with each task placed by a
// status→disposition mapping. Sections with no tasks render `(empty)`.
//
// Status → disposition mapping:
//   - resolved: confirmed, assumed, completed, obsolete, declined
//   - pending:  needs_plan, ready, in_progress, revisit
//   - blocked:  blocked
//   - verify:   verify
//
// An unknown status falls into Pending so it surfaces somewhere rather
// than silently disappearing.
func renderTasksGrouped(b *strings.Builder, body string) {
	body = strings.TrimSpace(body)
	buckets := map[string][]string{
		"Resolved": nil,
		"Pending":  nil,
		"Blocked":  nil,
		"Verify":   nil,
	}
	if body != "" {
		for _, elem := range splitElements(body, "task") {
			_, status, _, _ := parseTaskLine(elem)
			buckets[statusToDisposition(status)] = append(
				buckets[statusToDisposition(status)], elem,
			)
		}
	}
	for _, heading := range []string{"Resolved", "Pending", "Blocked", "Verify"} {
		b.WriteString("## ")
		b.WriteString(heading)
		b.WriteString("\n")
		elems := buckets[heading]
		if len(elems) == 0 {
			b.WriteString("(empty)\n\n")
			continue
		}
		b.WriteString("| Task | Status | Note |\n|---|---|---|\n")
		for _, elem := range elems {
			id, status, filed, note := parseTaskLine(elem)
			idCell := id
			if filed {
				idCell += " (filed)"
			}
			b.WriteString("| ")
			b.WriteString(idCell)
			b.WriteString(" | ")
			b.WriteString(status)
			b.WriteString(" | ")
			b.WriteString(escapeNewlinesForMarkdown(note))
			b.WriteString(" |\n")
		}
		b.WriteString("\n")
	}
}

// statusToDisposition maps a task status to the bucket the renderer
// places it in. Unknown statuses fall into "Pending" so they surface.
func statusToDisposition(status string) string {
	switch status {
	case "confirmed", "assumed", "completed", "obsolete", "declined":
		return "Resolved"
	case "blocked":
		return "Blocked"
	case "verify":
		return "Verify"
	case "needs_plan", "ready", "in_progress", "revisit":
		return "Pending"
	default:
		return "Pending"
	}
}

// splitElements walks a body containing one or more <tag>...</tag>
// elements (possibly separated by whitespace/newlines) and yields each
// element's complete serialized form. Tolerates internal newlines inside
// element bodies — splits on the closing tag boundary.
func splitElements(body, tag string) []string {
	open := "<" + tag
	close := "</" + tag + ">"
	var out []string
	cursor := 0
	for {
		start := strings.Index(body[cursor:], open)
		if start < 0 {
			break
		}
		start += cursor
		endTag := strings.Index(body[start:], close)
		if endTag < 0 {
			break
		}
		endTag += start + len(close)
		out = append(out, body[start:endTag])
		cursor = endTag
	}
	return out
}

// renderDecisions emits decisions as a bulleted list.
func renderDecisions(b *strings.Builder, body string) {
	b.WriteString("## Decisions\n")
	body = strings.TrimSpace(body)
	if body == "" {
		b.WriteString("(empty)\n\n")
		return
	}
	for _, elem := range splitElements(body, "decision") {
		text := extractElementText(elem, "decision")
		b.WriteString("- ")
		b.WriteString(escapeNewlinesForMarkdown(text))
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// renderCommits emits a `| SHA | Description |` table.
func renderCommits(b *strings.Builder, body string) {
	b.WriteString("## Commits\n")
	body = strings.TrimSpace(body)
	if body == "" {
		b.WriteString("(empty)\n\n")
		return
	}
	b.WriteString("| SHA | Description |\n|---|---|\n")
	for _, elem := range splitElements(body, "commit") {
		sha := extractAttr(elem, "sha")
		text := extractElementText(elem, "commit")
		b.WriteString("| ")
		b.WriteString(sha)
		b.WriteString(" | ")
		b.WriteString(escapeNewlinesForMarkdown(text))
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

// renderMemory emits a `| Path | Summary |` table.
func renderMemory(b *strings.Builder, body string) {
	b.WriteString("## Memory\n")
	body = strings.TrimSpace(body)
	if body == "" {
		b.WriteString("(empty)\n\n")
		return
	}
	b.WriteString("| Path | Summary |\n|---|---|\n")
	for _, elem := range splitElements(body, "entry") {
		path := extractAttr(elem, "path")
		text := extractElementText(elem, "entry")
		b.WriteString("| ")
		b.WriteString(path)
		b.WriteString(" | ")
		b.WriteString(escapeNewlinesForMarkdown(text))
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

// renderSummary emits a `| Layer | Files | Purpose |` table from
// <layer name="..." files="...">purpose</layer> children (E-1314).
func renderSummary(b *strings.Builder, body string) {
	b.WriteString("## Summary\n")
	body = strings.TrimSpace(body)
	if body == "" {
		b.WriteString("(empty)\n\n")
		return
	}
	b.WriteString("| Layer | Files | Purpose |\n|---|---|---|\n")
	for _, elem := range splitElements(body, "layer") {
		name := extractAttr(elem, "name")
		files := extractAttr(elem, "files")
		text := extractElementText(elem, "layer")
		b.WriteString("| ")
		b.WriteString(name)
		b.WriteString(" | ")
		b.WriteString(escapeNewlinesForMarkdown(files))
		b.WriteString(" | ")
		b.WriteString(escapeNewlinesForMarkdown(text))
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

// parseTaskLine extracts the id, status, filed-bool, and body text from
// a single `<task id=... status=... filed=...>note</task>` line.
func parseTaskLine(line string) (id, status string, filed bool, note string) {
	id = extractAttr(line, "id")
	status = extractAttr(line, "status")
	filed = extractAttr(line, "filed") == "true"
	note = extractElementText(line, "task")
	return
}

// extractAttr returns the value of attr from a single-element XML line.
func extractAttr(line, attr string) string {
	needle := attr + `="`
	idx := strings.Index(line, needle)
	if idx < 0 {
		return ""
	}
	start := idx + len(needle)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return ""
	}
	return line[start : start+end]
}

// extractElementText returns the body text of <tag>text</tag>.
func extractElementText(line, tag string) string {
	open := ">"
	openIdx := strings.Index(line, open)
	if openIdx < 0 {
		return ""
	}
	close := "</" + tag + ">"
	closeIdx := strings.LastIndex(line, close)
	if closeIdx < 0 || closeIdx < openIdx {
		return ""
	}
	return line[openIdx+1 : closeIdx]
}

// escapeNewlinesForMarkdown replaces `\n` with `<br>` so multi-line task
// bodies render correctly inside markdown table cells.
func escapeNewlinesForMarkdown(s string) string {
	return strings.ReplaceAll(s, "\n", "<br>")
}
