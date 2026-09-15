package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

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
// project — everything from freshly filed through in-flight. E-1845 added
// `untriaged`, which is where every new task now lands; omitting it would have
// made newly filed work invisible to every caller of this function.
func GetActiveTasks(projectID int64) ([]Task, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		"SELECT id, phase, description, status "+
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
		b.WriteString("Use `endless task import <file>` to import tasks, ")
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

// TaskPlan returns the tasks.plan content for a task id (E-1445). Returns an
// empty string (no error) when the row or the plan is absent — the caller
// treats "no plan" as "no plan file to materialize". This is the Go-side read
// that lets create_task_worktree materialize a plan file without a Python DB
// read (E-894).
func TaskPlan(taskID int64) (string, error) {
	return TaskField(taskID, "plan")
}

// taskDocColumns whitelists the multiline document columns TaskField may
// read. Keyed here (not interpolated freely) because the column name is
// substituted into SQL — the whitelist is the injection guard. These are the
// fields E-1747 mirrors to committed `.endless/<subdir>/E-NNN.md` files.
var taskDocColumns = map[string]bool{
	"plan":     true,
	"outcome":  true,
	"analysis": true,
}

// TaskField returns the raw value of one whitelisted multiline document
// column for a task (empty string when the row or the value is absent).
// column MUST be in taskDocColumns; anything else is rejected so the caller
// can never smuggle arbitrary SQL through the substituted identifier.
func TaskField(taskID int64, column string) (string, error) {
	if !taskDocColumns[column] {
		return "", fmt.Errorf("unsupported task field %q", column)
	}
	db, err := DB()
	if err != nil {
		return "", err
	}
	var value string
	err = db.QueryRow(
		fmt.Sprintf("SELECT COALESCE(%s, '') FROM live_tasks WHERE id = ?", column),
		taskID,
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
