package events

import (
	"fmt"

	"github.com/mikeschinkel/endless/internal/taskcontent"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// taskContentFields maps every payload key that carries task content to the
// content name it writes (E-1531). It is the content half of BOTH write paths'
// field handling — the executor's live apply and the projector's rebuild read
// this one map, so they cannot disagree about which content a payload carries.
//
// They used to. Each path had its own column map, and the projector's omitted
// `notes`: the executor wrote it live and a rebuild silently dropped it, across
// every research justification E-1544 files under `## Justification`. One map,
// with a test that both paths route through it, is what closes that for good.
//
// Derived from the taskcontent enum rather than listed, so a new content kind is
// accepted in an event payload the moment it is declared there. The one
// addition is legacyPlanKey: the pre-E-1000 spelling of `plan`, which historical
// events still carry and which must keep resolving to the plan.
var taskContentFields = func() map[string]taskcontent.Name {
	m := map[string]taskcontent.Name{legacyPlanKey: taskcontent.Plan}
	for _, n := range taskcontent.All() {
		m[n.Slug()] = n
	}
	return m
}()

// contentWrite is one content row a task event writes. Empty Content deletes
// the row: task_content holds no empty rows.
type contentWrite struct {
	Name    taskcontent.Name
	Content string
}

// outcomeName is where text written under the `outcome` key belongs, given the
// status the same event moves the task to. Arriving at an abandonment status,
// the text is WHY the task ended — a reason, not a deliverable (E-1531).
//
// Live emitters send `reason` for that case themselves; this routing is what
// keeps historical events, which all say `outcome`, landing where the E-1531
// migration put the same values.
func outcomeName(newStatus string) taskcontent.Name {
	if taskstatus.Has(taskstatus.Abandoned, newStatus) {
		return taskcontent.Reason
	}
	return taskcontent.Outcome
}

// contentWrites extracts the content a task.fields_updated payload carries, in
// display order. newStatus is the status the same payload sets, or "" when it
// sets none — it decides where an `outcome` key lands.
//
// Two spellings can name one content: the legacy `text` beside `plan`, and
// `outcome` beside `reason` when the update abandons the task. The explicit
// spelling wins in both cases, by construction rather than by map iteration
// order — otherwise a payload carrying both would write whichever came last.
func contentWrites(fields map[string]any, newStatus string) ([]contentWrite, error) {
	byName := map[taskcontent.Name]string{}
	explicit := map[taskcontent.Name]bool{}
	for key, value := range fields {
		name, ok := taskContentFields[key]
		if !ok {
			continue
		}
		isExplicit := key == name.Slug()
		if key == taskcontent.Outcome.Slug() {
			name = outcomeName(newStatus)
			isExplicit = name == taskcontent.Outcome
		}
		if explicit[name] && !isExplicit {
			continue
		}
		var content string
		switch v := value.(type) {
		case nil:
		case string:
			content = v
		default:
			return nil, fmt.Errorf("events: content field %q must be a string, got %T", key, value)
		}
		byName[name] = content
		explicit[name] = explicit[name] || isExplicit
	}
	var out []contentWrite
	for _, n := range taskcontent.All() {
		if content, ok := byName[n]; ok {
			out = append(out, contentWrite{Name: n, Content: content})
		}
	}
	return out, nil
}

// isTaskContentField reports whether a payload key carries task content.
func isTaskContentField(key string) bool {
	_, ok := taskContentFields[key]
	return ok
}

// writeTaskContent applies content writes to one task: an UPSERT per non-empty
// value, a DELETE per empty one. UNIQUE(task_id, name) is what makes an upsert
// the whole answer — there is never a question of which row is current.
func writeTaskContent(db dbQuerier, taskID any, writes []contentWrite) error {
	for _, w := range writes {
		if w.Content == "" {
			if _, err := db.Exec(
				"DELETE FROM task_content WHERE task_id = ? AND name = ?",
				taskID, w.Name.Slug(),
			); err != nil {
				return fmt.Errorf("events: clear task %v %s: %w", taskID, w.Name.Slug(), err)
			}
			continue
		}
		if _, err := db.Exec(
			`INSERT INTO task_content (task_id, name, content)
			 VALUES (?, ?, ?)
			 ON CONFLICT(task_id, name) DO UPDATE
			    SET content = excluded.content,
			        updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
			  WHERE content IS NOT excluded.content`,
			taskID, w.Name.Slug(), w.Content,
		); err != nil {
			return fmt.Errorf("events: write task %v %s: %w", taskID, w.Name.Slug(), err)
		}
	}
	return nil
}

// createdContent is the content a task.created payload carries, in display
// order. Shared by the executor and the projector for the reason
// taskContentFields gives: the projector's INSERT once named plan and analysis
// but not notes, so a rebuilt task lost the notes it was filed with.
func (p TaskCreatedPayload) createdContent() []contentWrite {
	var out []contentWrite
	for _, w := range []contentWrite{
		{taskcontent.Context, p.Context},
		{taskcontent.Analysis, p.Analysis},
		{taskcontent.Plan, p.PlanText()},
		{taskcontent.Notes, p.Notes},
	} {
		if w.Content != "" {
			out = append(out, w)
		}
	}
	return out
}

// statusChangedContent is the content a task.status_changed payload carries.
// Only non-empty values write: a status change never clears content.
func (p TaskStatusChangedPayload) statusChangedContent() []contentWrite {
	var out []contentWrite
	if p.Outcome != "" {
		out = append(out, contentWrite{outcomeName(p.NewStatus), p.Outcome})
	}
	if p.Reason != "" {
		out = append(out, contentWrite{taskcontent.Reason, p.Reason})
	}
	return out
}
