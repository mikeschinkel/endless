package monitor

import (
	"fmt"
	"strings"
)

// PlanItem represents a plan item from the DB.
type PlanItem struct {
	ID       int64
	Phase    string
	Text     string
	Status   string
	StableID string
}

// GetActivePlanItems returns in-progress and pending items for a project.
func GetActivePlanItems(projectID int64) ([]PlanItem, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		"SELECT id, phase, task_text, status "+
			"FROM plan_items "+
			"WHERE project_id = ? AND status IN ('in_progress', 'pending') "+
			"ORDER BY CASE status WHEN 'in_progress' THEN 0 ELSE 1 END, sort_order",
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PlanItem
	for rows.Next() {
		var item PlanItem
		if err := rows.Scan(&item.ID, &item.Phase, &item.Text, &item.Status); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// FormatPlanContext formats plan items as context text for Claude.
func FormatPlanContext(projectName string, items []PlanItem) string {
	var b strings.Builder

	if len(items) == 0 {
		fmt.Fprintf(&b, "Endless is tracking project: %s\n", projectName)
		b.WriteString("No plan items yet. Ask the user what they'd like to work on.\n")
		b.WriteString("Use `endless plan import <file>` to import a plan, ")
		b.WriteString("or `endless plan show` to check status.")
		return b.String()
	}

	fmt.Fprintf(&b, "Endless has an active plan for %s. ", projectName)
	b.WriteString("Present this to the user and get confirmation before proceeding:\n\n")

	var inProgress, pending []PlanItem
	for _, item := range items {
		if item.Status == "in_progress" {
			inProgress = append(inProgress, item)
		} else {
			pending = append(pending, item)
		}
	}

	if len(inProgress) > 0 {
		b.WriteString("IN PROGRESS:\n")
		for _, item := range inProgress {
			b.WriteString(fmt.Sprintf("  - #%d %s\n", item.ID, item.Text))
		}
	}

	if len(pending) > 0 {
		// Show at most 5 pending items
		b.WriteString("NEXT UP:\n")
		limit := 5
		if len(pending) < limit {
			limit = len(pending)
		}
		for _, item := range pending[:limit] {
			b.WriteString(fmt.Sprintf("  - #%d %s\n", item.ID, item.Text))
		}
		if len(pending) > 5 {
			b.WriteString(fmt.Sprintf("  ... and %d more pending items\n", len(pending)-5))
		}
	}

	b.WriteString("\nUse `endless plan complete <id>` when done with a task.")
	b.WriteString("\nUse `endless plan start <id>` to mark a task as in progress.")

	return b.String()
}

// HasInjectedContext checks if we've already injected plan context for this session.
func HasInjectedContext(sessionID string) bool {
	db, err := DB()
	if err != nil {
		return false
	}
	var count int
	err = db.QueryRow(
		"SELECT count(*) FROM activity "+
			"WHERE session_context LIKE ? "+
			"AND session_context LIKE '%\"injected_plan\":\"true\"%'",
		fmt.Sprintf("%%\"session_id\":\"%s\"%%", sessionID),
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// MarkContextInjected records that plan context was injected for this session.
func MarkContextInjected(projectID int64, sessionID, workingDir string) {
	RecordActivity(projectID, "claude", workingDir, map[string]string{
		"session_id":    sessionID,
		"event":         "plan_context_injected",
		"injected_plan": "true",
	})
}

// GetProjectName returns the project name for a project ID.
func GetProjectName(projectID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var name string
	err = db.QueryRow("SELECT name FROM projects WHERE id = ?", projectID).Scan(&name)
	return name, err
}
