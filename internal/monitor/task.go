package monitor

import (
	"fmt"
	"strings"
)

// Task represents a task item from the DB.
type Task struct {
	ID       int64
	Phase    string
	Text     string
	Status   string
	StableID string
}

// GetActiveTasks returns in-progress and needs_plan items for a project.
func GetActiveTasks(projectID int64) ([]Task, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		"SELECT id, phase, description, status "+
			"FROM tasks "+
			"WHERE project_id = ? AND status IN ('in_progress', 'needs_plan', 'ready') "+
			"ORDER BY CASE status WHEN 'in_progress' THEN 0 ELSE 1 END, sort_order",
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Task
	for rows.Next() {
		var item Task
		if err := rows.Scan(&item.ID, &item.Phase, &item.Text, &item.Status); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// FormatTasks formats task items as context text for Claude.
func FormatTasks(projectName string, items []Task) string {
	var b strings.Builder

	if len(items) == 0 {
		fmt.Fprintf(&b, "Endless is tracking project: %s\n", projectName)
		b.WriteString("No tasks yet. Ask the user what they'd like to work on.\n")
		b.WriteString("Use `endless task import <file>` to import tasks, ")
		b.WriteString("or `endless task show` to check status.")
		return b.String()
	}

	fmt.Fprintf(&b, "Endless has active tasks for %s.\n", projectName)
	b.WriteString("Present this to the user and ask which task to work on:\n\n")

	var inProgress, available []Task
	for _, item := range items {
		if item.Status == "in_progress" {
			inProgress = append(inProgress, item)
		} else {
			available = append(available, item)
		}
	}

	if len(inProgress) > 0 {
		b.WriteString("IN PROGRESS:\n")
		for _, item := range inProgress {
			fmt.Fprintf(&b, "  - E-%d %s\n", item.ID, item.Text)
		}
	}

	if len(available) > 0 {
		b.WriteString("NEXT UP:\n")
		limit := min(5, len(available))
		for _, item := range available[:limit] {
			fmt.Fprintf(&b, "  - E-%d %s\n", item.ID, item.Text)
		}
		if len(available) > 5 {
			fmt.Fprintf(&b, "  ... and %d more items\n", len(available)-5)
		}
	}

	b.WriteString("\nIMPORTANT: You MUST register a task before making any file changes.")
	b.WriteString("\n1. Present these tasks to the user")
	b.WriteString("\n2. Ask which task to work on")
	b.WriteString("\n3. Run `endless task start <id>` after user confirms")
	b.WriteString("\n4. If this is just a conversation (no code changes), run `endless task chat`")
	b.WriteString("\n")
	b.WriteString("\nUse `endless task complete <id>` when done with a task.")
	b.WriteString("\nRead-only operations (Read, Glob, Grep) work without registration.")

	return b.String()
}

// HasInjectedContext checks if we've already injected task context for this session.
func HasInjectedContext(sessionID string) bool {
	db, err := DB()
	if err != nil {
		return false
	}
	var count int
	err = db.QueryRow(
		"SELECT count(*) FROM activity "+
			"WHERE session_context LIKE ? "+
			"AND session_context LIKE '%\"injected_tasks\":\"true\"%'",
		fmt.Sprintf("%%\"session_id\":\"%s\"%%", sessionID),
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// MarkContextInjected records that task context was injected for this session.
func MarkContextInjected(projectID int64, sessionID, workingDir string) {
	RecordActivity(projectID, "claude", workingDir, map[string]string{
		"session_id":     sessionID,
		"event":          "task_context_injected",
		"injected_tasks": "true",
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
