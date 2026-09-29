package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/taskcontent"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// Task represents a task item from the DB.
type Task struct {
	ID       int64
	Phase    string
	Text     string
	Status   string
	StableID string
}

// GetActiveTasks returns the open (non-terminal, non-blocked) items for a
// project — everything from freshly filed through in-flight.
func GetActiveTasks(projectID int64) ([]Task, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		"SELECT id, phase, COALESCE(description, ''), status "+
			"FROM live_tasks "+
			"WHERE project_id = ? AND status IN ("+taskstatus.SQLList(taskstatus.Open)+") "+
			"ORDER BY CASE status WHEN '"+taskstatus.Underway+"' THEN 0 ELSE 1 END, sort_order",
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
		b.WriteString("Use `endless task add \"<title>\"` to file one, ")
		b.WriteString("or `endless task show` to check status.")
		return b.String()
	}

	fmt.Fprintf(&b, "Endless has active tasks for %s.\n", projectName)
	b.WriteString("Present this to the user and ask which task to work on:\n\n")

	var inProgress, available []Task
	for _, item := range items {
		if item.Status == taskstatus.Underway {
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
	b.WriteString("\n3. Run `endless task claim <id>` after user confirms")
	b.WriteString("\n4. If this is just a conversation (no code changes), no action is needed — nothing is refused until you write")
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

// TaskPlan returns a task's plan (E-1445). Returns an empty string (no error)
// when the task or its plan is absent. A thin alias for TaskContent, kept for
// the `session-query task-plan` verb.
func TaskPlan(taskID int64) (string, error) {
	return TaskContent(taskID, taskcontent.Plan)
}

// hasContentExpr is a SQL boolean: does the task whose id is idExpr carry
// content under name? For the reads that ask "has a plan" of many rows at once,
// which the plan column used to answer inline. The name is the enum's slug, not
// input, so interpolating it is safe.
func hasContentExpr(idExpr string, name taskcontent.Name) string {
	return "EXISTS(SELECT 1 FROM task_content c WHERE c.task_id = " + idExpr +
		" AND c.name = '" + name.Slug() + "')"
}

// TaskContent returns one content row of a live task (E-1531) — the content a
// `.endless/tasks/e-NNNN/<name>.md` mirror projects. An empty string (no error)
// when the task or that content is absent: task_content holds no empty rows, so
// the two mean the same thing.
//
// Through live_tasks, so a removed task's content reads as absent, exactly as
// its columns did.
func TaskContent(taskID int64, name taskcontent.Name) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var value string
	err = db.QueryRow(
		`SELECT c.content FROM task_content c
		   JOIN live_tasks t ON t.id = c.task_id
		  WHERE c.task_id = ? AND c.name = ?`,
		taskID, name.Slug(),
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
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
