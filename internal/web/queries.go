package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/web/data"
)

func shortPath(path string) string {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(path, home) {
		return filepath.Join("~", path[len(home):])
	}
	return path
}

func GetDashboardProjects() []data.DashboardProject {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT p.id, p.name, COALESCE(NULLIF(p.label,''),'') as label,
		 COALESCE(NULLIF(p.description,''),'') as description,
		 p.status, COALESCE(NULLIF(p.language,''),'') as language,
		 p.path, COALESCE(p.group_name,'') as group_name,
		 (SELECT count(*) FROM notes n WHERE n.project_id = p.id AND n.resolved = 0) as pending_notes,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id AND pi.status IN ('needs_plan','ready','in_progress')) as active_plan,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id) as task_total,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id AND pi.status = 'completed') as task_completed,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id AND pi.status = 'in_progress') as task_in_progress,
		 COALESCE((SELECT a.created_at FROM activity a WHERE a.project_id = p.id ORDER BY a.created_at DESC LIMIT 1),'') as last_activity
		 FROM projects p WHERE p.status IN ('active','paused','idea')
		 ORDER BY last_activity DESC, p.name`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var projects []data.DashboardProject
	for rows.Next() {
		var p data.DashboardProject
		rows.Scan(&p.ID, &p.Name, &p.Label, &p.Description, &p.Status, &p.Language,
			&p.Path, &p.GroupName, &p.PendingNotes, &p.ActiveTasks,
			&p.TaskTotal, &p.TaskCompleted, &p.TaskInProgress, &p.LastActivity)
		p.ShortPath = shortPath(p.Path)
		projects = append(projects, p)
	}
	return projects
}

func GetCurrentWork() []data.CurrentWorkItem {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT p.name, COALESCE(pi.title, substr(pi.description, 1, 80)),
		 pi.description, pi.id,
		 COALESCE(s.state, '') as session_state,
		 COALESCE(s.last_activity, '') as last_activity
		 FROM tasks pi
		 JOIN projects p ON pi.project_id = p.id
		 LEFT JOIN sessions s ON s.active_task_id = pi.id AND s.state = 'working'
		 WHERE pi.status = 'in_progress'
		 ORDER BY p.name, pi.sort_order`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var items []data.CurrentWorkItem
	for rows.Next() {
		var item data.CurrentWorkItem
		rows.Scan(&item.Project, &item.Title, &item.Text, &item.TaskID,
			&item.SessionState, &item.LastActivity)
		items = append(items, item)
	}
	return items
}

func GetRecentActivity(limit int) []data.ActivityView {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT a.id, p.name, a.source,
		 COALESCE(json_extract(a.session_context,'$.event'),'') as event,
		 a.working_dir, a.created_at
		 FROM activity a
		 JOIN projects p ON a.project_id = p.id
		 ORDER BY a.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var activities []data.ActivityView
	for rows.Next() {
		var a data.ActivityView
		rows.Scan(&a.ID, &a.Project, &a.Source, &a.Event, &a.WorkDir, &a.CreatedAt)
		activities = append(activities, a)
	}
	return activities
}

func GetProjectDetail(name string) (*data.DashboardProject, error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, err
	}

	var p data.DashboardProject
	err = db.QueryRow(
		`SELECT p.id, p.name, COALESCE(NULLIF(p.label,''),'') as label,
		 COALESCE(NULLIF(p.description,''),'') as description,
		 p.status, COALESCE(NULLIF(p.language,''),'') as language,
		 p.path, COALESCE(p.group_name,'') as group_name,
		 (SELECT count(*) FROM notes n WHERE n.project_id = p.id AND n.resolved = 0) as pending_notes,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id AND pi.status IN ('needs_plan','ready','in_progress')) as active_plan,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id) as task_total,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id AND pi.status = 'completed') as task_completed,
		 (SELECT count(*) FROM tasks pi WHERE pi.project_id = p.id AND pi.status = 'in_progress') as task_in_progress,
		 COALESCE((SELECT a.created_at FROM activity a WHERE a.project_id = p.id ORDER BY a.created_at DESC LIMIT 1),'') as last_activity
		 FROM projects p WHERE p.name = ?`, name,
	).Scan(&p.ID, &p.Name, &p.Label, &p.Description, &p.Status, &p.Language,
		&p.Path, &p.GroupName, &p.PendingNotes, &p.ActiveTasks,
		&p.TaskTotal, &p.TaskCompleted, &p.TaskInProgress, &p.LastActivity)
	if err != nil {
		return nil, err
	}
	p.ShortPath = shortPath(p.Path)
	return &p, nil
}

func GetProjectTasks(projectID int64, excludeStatuses ...string) []data.TaskView {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	// Build the NOT IN clause for child_count
	childExclude := "'completed'"
	if len(excludeStatuses) > 0 {
		parts := make([]string, len(excludeStatuses))
		for i, s := range excludeStatuses {
			parts[i] = "'" + s + "'"
		}
		childExclude = strings.Join(parts, ", ")
	}

	rows, err := db.Query(
		fmt.Sprintf(`SELECT pi.id, COALESCE(pi.title,'') as title, pi.description, pi.phase, pi.status,
		 COALESCE(pi.type,'task') as type, pi.parent_id,
		 (SELECT count(*) FROM tasks c WHERE c.parent_id = pi.id AND c.status NOT IN (%s)) as child_count,
		 COALESCE((SELECT GROUP_CONCAT('E-' || td.target_id || ': ' ||
		   CASE td.target_type
		     WHEN 'task' THEN COALESCE((SELECT substr(COALESCE(t.title, t.description),1,50) FROM tasks t WHERE t.id = td.target_id),'')
		     WHEN 'project' THEN COALESCE((SELECT p2.name FROM projects p2 WHERE p2.id = td.target_id),'')
		     ELSE ''
		   END, ', ')
		   FROM task_deps td
		   WHERE td.source_type = 'task' AND td.source_id = pi.id AND td.dep_type = 'needs'
		 ),'') as blocked_by,
		 pi.tier,
		 COALESCE(pi.created_at,'') as created_at,
		 COALESCE(pi.updated_at,'') as updated_at,
		 COALESCE(pi.completed_at,'') as completed_at
		 FROM tasks pi
		 WHERE pi.project_id = ?
		 ORDER BY pi.parent_id,
		 CASE pi.phase
		   WHEN 'now' THEN 0
		   WHEN 'next' THEN 1
		   WHEN 'later' THEN 2
		   ELSE 3
		 END,
		 CASE pi.status
		   WHEN 'in_progress' THEN 0
		   WHEN 'verify' THEN 1
		   WHEN 'ready' THEN 2
		   WHEN 'needs_plan' THEN 3
		   WHEN 'revisit' THEN 4
		   WHEN 'blocked' THEN 5
		   WHEN 'completed' THEN 6
		   ELSE 7
		 END,
		 CASE WHEN pi.tier IS NULL THEN 99 ELSE pi.tier END,
		 pi.updated_at DESC`, childExclude), projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	// Collect all items indexed by ID
	var allItems []data.TaskView
	byID := make(map[int64]*data.TaskView)
	childrenOf := make(map[int64][]int) // parent_id -> child indices
	var rootIndices []int

	for rows.Next() {
		var pi data.TaskView
		rows.Scan(&pi.ID, &pi.Title, &pi.Text, &pi.Phase, &pi.Status,
			&pi.Type, &pi.ParentID, &pi.ChildCount, &pi.BlockedBy, &pi.Tier,
			&pi.CreatedAt, &pi.UpdatedAt, &pi.CompletedAt)
		idx := len(allItems)
		allItems = append(allItems, pi)
		byID[pi.ID] = &allItems[idx]
		if pi.ParentID != nil {
			childrenOf[*pi.ParentID] = append(childrenOf[*pi.ParentID], idx)
		} else {
			rootIndices = append(rootIndices, idx)
		}
	}

	// Flatten tree in depth-first order with computed Depth and SiblingNum
	var result []data.TaskView
	var walk func(indices []int, depth int)
	walk = func(indices []int, depth int) {
		for i, idx := range indices {
			item := allItems[idx]
			item.Depth = depth
			item.SiblingNum = i + 1
			result = append(result, item)
			if kids, ok := childrenOf[item.ID]; ok {
				walk(kids, depth+1)
			}
		}
	}
	walk(rootIndices, 0)

	return result
}

func GetProjectTaskGroups(projectID int64) []data.TaskGroup {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	// Get active tasks grouped by plan, ordered so in_progress comes first
	rows, err := db.Query(
		`SELECT pi.task_id,
		 COALESCE((SELECT COALESCE(p2.title, p2.description) FROM tasks p2 WHERE p2.id = pi.task_id), 'Ungrouped') as group_name,
		 pi.id, COALESCE(pi.title, substr(pi.description, 1, 80)) as title,
		 pi.description, pi.phase, pi.status
		 FROM tasks pi
		 WHERE pi.project_id = ? AND pi.status IN ('in_progress', 'needs_plan', 'ready')
		 ORDER BY pi.task_id,
		   CASE pi.status WHEN 'in_progress' THEN 0 ELSE 1 END,
		   pi.sort_order`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	planMap := make(map[int64]*data.TaskGroup)
	var planOrder []int64
	for rows.Next() {
		var planID int64
		var planName string
		var item data.TaskView
		rows.Scan(&planID, &planName, &item.ID, &item.Title, &item.Text, &item.Phase, &item.Status)

		p, ok := planMap[planID]
		if !ok {
			p = &data.TaskGroup{GroupID: planID, GroupName: planName}
			planMap[planID] = p
			planOrder = append(planOrder, planID)
		}
		if len(p.Tasks) < 3 {
			p.Tasks = append(p.Tasks, item)
		}
	}

	// Get totals per plan
	for _, pid := range planOrder {
		p := planMap[pid]
		db.QueryRow(
			"SELECT count(*) FROM tasks WHERE project_id=? AND task_id=?",
			projectID, pid,
		).Scan(&p.Total)
		db.QueryRow(
			"SELECT count(*) FROM tasks WHERE project_id=? AND task_id=? AND status='completed'",
			projectID, pid,
		).Scan(&p.Done)
	}

	var result []data.TaskGroup
	for _, pid := range planOrder {
		result = append(result, *planMap[pid])
	}
	return result
}

func GetStatusDetail(name string) (*data.StatusDetail, error) {
	project, err := GetProjectDetail(name)
	if err != nil {
		return nil, err
	}

	taskItems := GetProjectTasks(project.ID, "confirmed", "verify")

	return &data.StatusDetail{
		Project:      *project,
		TaskItems:    taskItems,
		PendingNotes: project.PendingNotes,
	}, nil
}

func GetProjectDependencies(projectID int64) []data.DependencyView {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT td.source_type, td.source_id,
		 CASE td.source_type
		   WHEN 'task' THEN COALESCE((SELECT COALESCE(t.title, substr(t.description,1,60)) FROM tasks t WHERE t.id = td.source_id),'')
		   WHEN 'project' THEN COALESCE((SELECT p.name FROM projects p WHERE p.id = td.source_id),'')
		   ELSE ''
		 END as source_name,
		 td.target_type, td.target_id,
		 CASE td.target_type
		   WHEN 'task' THEN COALESCE((SELECT COALESCE(t.title, substr(t.description,1,60)) FROM tasks t WHERE t.id = td.target_id),'')
		   WHEN 'project' THEN COALESCE((SELECT p.name FROM projects p WHERE p.id = td.target_id),'')
		   ELSE ''
		 END as target_name,
		 td.dep_type
		 FROM task_deps td
		 WHERE (td.source_type = 'project' AND td.source_id = ?)
		    OR (td.target_type = 'project' AND td.target_id = ?)
		    OR (td.source_type = 'task' AND td.source_id IN (SELECT id FROM tasks WHERE project_id = ?))
		    OR (td.target_type = 'task' AND td.target_id IN (SELECT id FROM tasks WHERE project_id = ?))`,
		projectID, projectID, projectID, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var deps []data.DependencyView
	for rows.Next() {
		var d data.DependencyView
		rows.Scan(&d.SourceType, &d.SourceID, &d.SourceName,
			&d.TargetType, &d.TargetID, &d.TargetName, &d.DepType)
		deps = append(deps, d)
	}
	return deps
}

func GetProjectActivity(projectID int64, limit int) []data.ActivityView {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT a.id, p.name, a.source,
		 COALESCE(json_extract(a.session_context,'$.event'),'') as event,
		 a.working_dir, a.created_at
		 FROM activity a JOIN projects p ON a.project_id = p.id
		 WHERE a.project_id = ?
		 ORDER BY a.created_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var activities []data.ActivityView
	for rows.Next() {
		var a data.ActivityView
		rows.Scan(&a.ID, &a.Project, &a.Source, &a.Event, &a.WorkDir, &a.CreatedAt)
		activities = append(activities, a)
	}
	return activities
}

func GetProjectNotes(projectID int64) []data.NoteView {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT n.id, p.name, n.note_type, n.message, n.created_at, n.resolved
		 FROM notes n JOIN projects p ON n.project_id = p.id
		 WHERE n.project_id = ?
		 ORDER BY n.created_at DESC`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notes []data.NoteView
	for rows.Next() {
		var n data.NoteView
		var resolved int
		rows.Scan(&n.ID, &n.Project, &n.Type, &n.Message, &n.Created, &resolved)
		n.Resolved = resolved != 0
		notes = append(notes, n)
	}
	return notes
}

func UpdateTaskTitle(itemID int64, newTitle string) error {
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE tasks SET title = ? WHERE id = ?", newTitle, itemID)
	return err
}

func UpdateTaskStatus(itemID int64, newStatus string) error {
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE tasks SET status = ? WHERE id = ?", newStatus, itemID)
	return err
}

