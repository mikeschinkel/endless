package events

import (
	"strings"

	"github.com/mikeschinkel/endless/internal/taskcontent"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// legacyUntriaged is the status E-1845 introduced and E-1993 removed. It held a
// newly filed task until something judged whether its description was a
// sufficient spec. With a plan required to spawn, no description is, so the
// judgment and the status both went.
//
// The ledger is immutable, and it carries the status: task.created events filed
// at it, task.fields_updated events from the description-edit reset, and
// task.status_changed events from reconsidering an abandoned task. A rebuild
// must still land those tasks somewhere the vocabulary knows, and must land
// them where the E-1993 schema change put the live rows — so both read this.
const legacyUntriaged = "untriaged"

// currentStatus maps a status an event carries onto today's vocabulary. Every
// status but the retired one passes through unchanged; `untriaged` becomes
// what a task filed today would be — `submitted` when it has a plan, else
// `unplanned`.
func currentStatus(status string, hasPlan bool) string {
	if status != legacyUntriaged {
		return status
	}
	if hasPlan {
		return taskstatus.Submitted
	}
	return taskstatus.Unplanned
}

// taskHasPlan reports whether the task holds a non-empty plan row, or the
// payload about to be applied carries one.
func taskHasPlan(db dbQuerier, taskID any, fields map[string]any) bool {
	for _, key := range []string{taskcontent.Plan.Slug(), legacyPlanKey} {
		if v, ok := fields[key].(string); ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	var one int
	err := db.QueryRow(
		"SELECT 1 FROM task_content WHERE task_id = ? AND name = ? AND trim(content) != ''",
		taskID, taskcontent.Plan.Slug(),
	).Scan(&one)
	return err == nil
}
