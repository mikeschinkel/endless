// Session-status event handling (E-1312). Inserts a row into the
// session_statuses table after dedup against the latest row for the
// same session. Renders the row back as markdown for chat display.

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

	if dup, err := isDuplicateOfLatest(db, sessionID, &p); err != nil {
		return nil, fmt.Errorf("events: dedup check: %w", err)
	} else if dup {
		return &ExecuteResult{
			Skipped:  true,
			Markdown: renderSessionStatusMarkdown(&p) +
				"\n\n_(skipped: identical to latest status for this session)_\n",
		}, nil
	}

	res, err := db.Exec(
		`INSERT INTO session_statuses
		 (session_id, headline, resolved, pending, blocked, verify,
		  decisions, commits, memory, notes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		p.Headline, p.Resolved, p.Pending, p.Blocked, p.Verify,
		p.Decisions, p.Commits, p.Memory, p.Notes,
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

// isDuplicateOfLatest returns true iff the latest row for sessionID has
// every text column byte-equal to the payload's. NULL columns in the
// existing row compare against "" in the payload — they're equivalent
// for the "no content" case.
func isDuplicateOfLatest(db dbQuerier, sessionID int64, p *SessionStatusRecordedPayload) (bool, error) {
	var (
		headline, resolved, pending, blocked, verify  *string
		decisions, commits, memoryCol, notes          *string
	)
	err := db.QueryRow(
		`SELECT headline, resolved, pending, blocked, verify,
		        decisions, commits, memory, notes
		 FROM session_statuses
		 WHERE session_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		sessionID,
	).Scan(&headline, &resolved, &pending, &blocked, &verify,
		&decisions, &commits, &memoryCol, &notes)
	if err != nil {
		// No prior row → not a duplicate. The "no rows" sentinel from
		// sql is sql.ErrNoRows but dbQuerier.QueryRow may wrap it; the
		// simplest correct check is "any error means not-dup."
		return false, nil
	}
	return nullableEq(headline, p.Headline) &&
		nullableEq(resolved, p.Resolved) &&
		nullableEq(pending, p.Pending) &&
		nullableEq(blocked, p.Blocked) &&
		nullableEq(verify, p.Verify) &&
		nullableEq(decisions, p.Decisions) &&
		nullableEq(commits, p.Commits) &&
		nullableEq(memoryCol, p.Memory) &&
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
// display. Sections render as tables for task-shaped data and bulleted
// lists for free-text decisions. Empty sections render `(empty)` so the
// document structure stays visible.
func renderSessionStatusMarkdown(p *SessionStatusRecordedPayload) string {
	var b strings.Builder
	if p.Headline != "" {
		b.WriteString("## Status\n")
		b.WriteString(p.Headline)
		b.WriteString("\n\n")
	}

	renderTaskSection(&b, "Resolved", p.Resolved)
	renderTaskSection(&b, "Pending", p.Pending)
	renderTaskSection(&b, "Blocked", p.Blocked)
	renderTaskSection(&b, "Verify", p.Verify)
	renderDecisions(&b, p.Decisions)
	renderCommits(&b, p.Commits)
	renderMemory(&b, p.Memory)

	if p.Notes != "" {
		b.WriteString("## Notes\n")
		b.WriteString(p.Notes)
		b.WriteString("\n")
	}
	return b.String()
}

// renderTaskSection emits a `## <heading>` markdown section. body holds
// zero-or-more `<task ...>note</task>` elements; the storage convention
// puts them one-per-top-level-line, but a task's body text may itself
// contain newlines (preserved verbatim in storage). To handle both, we
// split on the `</task>` boundary rather than on bare `\n`.
func renderTaskSection(b *strings.Builder, heading, body string) {
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n")
	body = strings.TrimSpace(body)
	if body == "" {
		b.WriteString("(empty)\n\n")
		return
	}
	b.WriteString("| Task | Status | Note |\n|---|---|---|\n")
	for _, elem := range splitElements(body, "task") {
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
			// Malformed; stop. Whatever validation upstream did failed.
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

// parseTaskLine extracts the id, status, filed-bool, and body text from
// a single `<task id=... status=... filed=...>note</task>` line. Returns
// best-effort empty strings on malformed lines (validation already ran
// in Python; this is rendering, not validation).
func parseTaskLine(line string) (id, status string, filed bool, note string) {
	id = extractAttr(line, "id")
	status = extractAttr(line, "status")
	filed = extractAttr(line, "filed") == "true"
	note = extractElementText(line, "task")
	return
}

// extractAttr returns the value of attr from a single-element XML line.
// Naive substring match; payload was already parsed/validated upstream.
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

// extractElementText returns the body text of <tag>text</tag>. Naive;
// assumes element appears once on the line.
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
