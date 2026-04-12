package web

import (
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
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id AND pi.status IN ('pending','in_progress')) as active_plan,
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id) as task_total,
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id AND pi.status = 'completed') as task_completed,
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id AND pi.status = 'in_progress') as task_in_progress,
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
			&p.Path, &p.GroupName, &p.PendingNotes, &p.ActivePlan,
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
		`SELECT p.name, COALESCE(pi.title, substr(pi.task_text, 1, 80)),
		 pi.task_text, pi.id
		 FROM plan_items pi
		 JOIN projects p ON pi.project_id = p.id
		 WHERE pi.status = 'in_progress'
		 ORDER BY p.name, pi.sort_order`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var items []data.CurrentWorkItem
	for rows.Next() {
		var item data.CurrentWorkItem
		rows.Scan(&item.Project, &item.Title, &item.Text, &item.TaskID)
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
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id AND pi.status IN ('pending','in_progress')) as active_plan,
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id) as task_total,
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id AND pi.status = 'completed') as task_completed,
		 (SELECT count(*) FROM plan_items pi WHERE pi.project_id = p.id AND pi.status = 'in_progress') as task_in_progress,
		 COALESCE((SELECT a.created_at FROM activity a WHERE a.project_id = p.id ORDER BY a.created_at DESC LIMIT 1),'') as last_activity
		 FROM projects p WHERE p.name = ?`, name,
	).Scan(&p.ID, &p.Name, &p.Label, &p.Description, &p.Status, &p.Language,
		&p.Path, &p.GroupName, &p.PendingNotes, &p.ActivePlan,
		&p.TaskTotal, &p.TaskCompleted, &p.TaskInProgress, &p.LastActivity)
	if err != nil {
		return nil, err
	}
	p.ShortPath = shortPath(p.Path)
	return &p, nil
}

func GetProjectPlanItems(projectID int64) []data.PlanItemView {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT pi.id, COALESCE(pi.title,'') as title, pi.task_text, pi.phase, pi.status,
		 pi.parent_item_id,
		 (SELECT count(*) FROM plan_items c WHERE c.parent_item_id = pi.id) as child_count,
		 COALESCE((SELECT
		   CASE td.target_type
		     WHEN 'task' THEN (SELECT COALESCE(t.title, substr(t.task_text,1,60)) FROM plan_items t WHERE t.id = td.target_id)
		     WHEN 'project' THEN (SELECT p2.name FROM projects p2 WHERE p2.id = td.target_id)
		     ELSE ''
		   END
		   FROM task_dependencies td
		   WHERE td.source_type = 'task' AND td.source_id = pi.id AND td.dep_type = 'needs'
		   LIMIT 1
		 ),'') as blocked_by
		 FROM plan_items pi
		 WHERE pi.project_id = ?
		 ORDER BY pi.phase, pi.sort_order`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var items []data.PlanItemView
	for rows.Next() {
		var pi data.PlanItemView
		rows.Scan(&pi.ID, &pi.Title, &pi.Text, &pi.Phase, &pi.Status,
			&pi.ParentID, &pi.ChildCount, &pi.BlockedBy)
		items = append(items, pi)
	}
	return items
}

func GetProjectDependencies(projectID int64) []data.DependencyView {
	db, err := monitor.DB()
	if err != nil {
		return nil
	}

	rows, err := db.Query(
		`SELECT td.source_type, td.source_id,
		 CASE td.source_type
		   WHEN 'task' THEN COALESCE((SELECT COALESCE(t.title, substr(t.task_text,1,60)) FROM plan_items t WHERE t.id = td.source_id),'')
		   WHEN 'project' THEN COALESCE((SELECT p.name FROM projects p WHERE p.id = td.source_id),'')
		   ELSE ''
		 END as source_name,
		 td.target_type, td.target_id,
		 CASE td.target_type
		   WHEN 'task' THEN COALESCE((SELECT COALESCE(t.title, substr(t.task_text,1,60)) FROM plan_items t WHERE t.id = td.target_id),'')
		   WHEN 'project' THEN COALESCE((SELECT p.name FROM projects p WHERE p.id = td.target_id),'')
		   ELSE ''
		 END as target_name,
		 td.dep_type
		 FROM task_dependencies td
		 WHERE (td.source_type = 'project' AND td.source_id = ?)
		    OR (td.target_type = 'project' AND td.target_id = ?)
		    OR (td.source_type = 'task' AND td.source_id IN (SELECT id FROM plan_items WHERE project_id = ?))
		    OR (td.target_type = 'task' AND td.target_id IN (SELECT id FROM plan_items WHERE project_id = ?))`,
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

