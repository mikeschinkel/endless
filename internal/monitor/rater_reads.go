package monitor

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mikeschinkel/endless/internal/taskcontent"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// E-2203 — the two reads the rater job needs.
//
// The rater proposes complexity and risk for a `submitted` task nobody rated —
// in practice one a human filed, since an agent is refused the plan-attach
// promotion until it rates (E-2203 part 1). The model call lives in Python
// (src/endless/rater.py); the DB access lives here, so the rater adds no Python
// SQLite surface.
//
// These are the retired triage reads (E-1859, removed by E-1993) restored and
// retargeted. The defining constraint is unchanged — persisted artifacts only,
// never a transcript — but the material differs: triage judged a description,
// the rater judges a plan, so the plan and the context ride along in full.

// UnratedTask is one row of the rater queue.
type UnratedTask struct {
	ID int64 `json:"id"`
	// Project is the registered project NAME (not id): the Python caller
	// passes it straight to `endless-go template render --project`, and the
	// sweep is database-wide so it cannot assume a cwd-resolved project.
	Project string `json:"project"`
	Title   string `json:"title"`
}

// RaterParent is the focal task's parent, when it has one. A child's plan is
// routinely only legible against its parent's description.
type RaterParent struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// RaterDecision is one decision linked to the focal task. Title carries the
// decision statement, which can raise or lower the risk of the work it binds.
type RaterDecision struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Relation string `json:"relation"`
}

// RaterContext is everything the rating prompt is allowed to see about one
// task. It is rendered to JSON and piped to `endless-go template render`, so
// every field name here is a template variable.
type RaterContext struct {
	TaskID  int64  `json:"task_id"`
	Project string `json:"project"`
	// ProjectRoot is the project's registered path, RESOLVED (E-2011). The
	// Python caller hands it to emit_event so the write never looks the path up
	// through Python SQLite, and git does not expand the stored `~/` form.
	ProjectRoot string `json:"project_root"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Context is why the task exists; Plan is the spec being rated. Both are
	// "" when absent.
	Context string `json:"context"`
	Plan    string `json:"plan"`
	Type    string `json:"type"`
	Phase   string `json:"phase"`
	Status  string `json:"status"`
	// Complexity and Risk are the task's current rating slugs, "" when unrated.
	// The rater writes only an axis still unrated, so a rating anyone gave is
	// never replaced by the model's guess.
	Complexity string `json:"complexity"`
	Risk       string `json:"risk"`

	// Parent is nil for a root task.
	Parent *RaterParent `json:"parent"`
	// Siblings are the titles of the parent's OTHER children. Empty for a root
	// task — a root task's "siblings" would be every root task in the project,
	// unbounded and saying nothing about this one. Capped at raterSiblingLimit.
	Siblings []string `json:"siblings"`
	// Decisions linked to this task in either direction, capped at
	// raterDecisionLimit.
	Decisions []RaterDecision `json:"decisions"`
}

const (
	// raterSiblingLimit bounds the sibling titles carried into the prompt.
	raterSiblingLimit = 25
	// raterDecisionLimit bounds the linked decisions carried into the prompt.
	raterDecisionLimit = 25
)

// UnratedSubmittedTasks returns the rater queue against the global monitor
// DB: `submitted` tasks with either rating unset, OLDEST FIRST, capped at
// limit.
//
// Oldest first so a busy filing day cannot starve a task that has waited
// longer. projectName empty means every project: the job runner has a
// database but no cwd, so a database-wide sweep is the only scope it can
// express.
func UnratedSubmittedTasks(projectName string, limit int) ([]UnratedTask, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	return unratedSubmittedTasks(db, projectName, limit)
}

func unratedSubmittedTasks(db *sql.DB, projectName string, limit int) ([]UnratedTask, error) {
	// id breaks created_at ties (same-second filings are ordinary), so the
	// sweep's order is deterministic across invocations.
	query := `SELECT t.id, p.name, t.title
	            FROM live_tasks t
	            JOIN projects p ON p.id = t.project_id
	           WHERE t.status = '` + string(taskstatus.Submitted) + `'
	             AND (t.complexity_id IS NULL OR t.risk_id IS NULL)`
	args := []any{}
	if projectName != "" {
		query += " AND p.name = ?"
		args = append(args, projectName)
	}
	query += " ORDER BY t.created_at ASC, t.id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read rater queue: %w", err)
	}
	defer rows.Close()

	tasks := make([]UnratedTask, 0, limit)
	for rows.Next() {
		var t UnratedTask
		if err = rows.Scan(&t.ID, &t.Project, &t.Title); err != nil {
			return nil, fmt.Errorf("scan rater queue: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read rater queue: %w", err)
	}
	return tasks, nil
}

// BuildRaterContext assembles one task's rater context against the global
// monitor DB. raterContext is the db-taking core, split out for tests.
func BuildRaterContext(taskID int64) (RaterContext, error) {
	db, err := DB()
	if err != nil {
		return RaterContext{}, err
	}
	return raterContext(db, taskID)
}

func raterContext(db *sql.DB, taskID int64) (RaterContext, error) {
	ctx := RaterContext{TaskID: taskID}

	// A missing task is a caller error, not an empty context — the rater would
	// otherwise prompt about nothing. type_id and description are nullable.
	var parentID sql.NullInt64
	err := db.QueryRow(
		`SELECT p.name, p.path, t.title, COALESCE(t.description, ''),
		        COALESCE(tt.slug, ''), t.phase, t.status, t.effective_parent_id,
		        COALESCE(cl.slug, ''), COALESCE(rl.slug, ''),
		        COALESCE((SELECT c.content FROM task_content c
		                   WHERE c.task_id = t.id AND c.name = ?), ''),
		        COALESCE((SELECT c.content FROM task_content c
		                   WHERE c.task_id = t.id AND c.name = ?), '')
		   FROM task_tree t
		   JOIN projects p ON p.id = t.project_id
		   LEFT JOIN task_types tt ON tt.id = t.type_id
		   LEFT JOIN complexity_levels cl ON cl.id = t.complexity_id
		   LEFT JOIN risk_levels rl ON rl.id = t.risk_id
		  WHERE t.id = ?`,
		taskcontent.Plan.Slug(), taskcontent.Context.Slug(), taskID,
	).Scan(&ctx.Project, &ctx.ProjectRoot, &ctx.Title, &ctx.Description,
		&ctx.Type, &ctx.Phase, &ctx.Status, &parentID,
		&ctx.Complexity, &ctx.Risk, &ctx.Plan, &ctx.Context)
	if errors.Is(err, sql.ErrNoRows) {
		return ctx, fmt.Errorf("no such task E-%d", taskID)
	}
	if err != nil {
		return ctx, fmt.Errorf("read task E-%d: %w", taskID, err)
	}
	if ctx.ProjectRoot, err = ResolvedProjectPath(ctx.ProjectRoot); err != nil {
		return ctx, fmt.Errorf("resolving project root for E-%d: %w", taskID, err)
	}

	ctx.Siblings = []string{}
	if parentID.Valid {
		ctx.Parent, err = raterParent(db, parentID.Int64)
		if err != nil {
			return ctx, err
		}
		ctx.Siblings, err = raterSiblings(db, parentID.Int64, taskID)
		if err != nil {
			return ctx, err
		}
	}

	ctx.Decisions, err = raterDecisions(db, taskID)
	if err != nil {
		return ctx, err
	}
	return ctx, nil
}

// raterParent reads the parent row. It is called with an EFFECTIVE parent id
// (E-2161), so the row is live by construction. A missing row still yields nil
// rather than an error: a missing parent degrades the prompt, it does not
// invalidate the task.
func raterParent(db *sql.DB, parentID int64) (*RaterParent, error) {
	p := RaterParent{ID: parentID}
	err := db.QueryRow(
		`SELECT title, COALESCE(description, ''), status
		   FROM live_tasks WHERE id = ?`, parentID,
	).Scan(&p.Title, &p.Description, &p.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read parent E-%d: %w", parentID, err)
	}
	return &p, nil
}

func raterSiblings(db *sql.DB, parentID, selfID int64) ([]string, error) {
	rows, err := db.Query(
		`SELECT title FROM task_tree
		  WHERE effective_parent_id = ? AND id != ?
		  ORDER BY sort_order, id
		  LIMIT ?`, parentID, selfID, raterSiblingLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("read siblings of E-%d: %w", selfID, err)
	}
	defer rows.Close()

	titles := []string{}
	for rows.Next() {
		var title string
		if err = rows.Scan(&title); err != nil {
			return nil, fmt.Errorf("scan sibling of E-%d: %w", selfID, err)
		}
		titles = append(titles, title)
	}
	return titles, rows.Err()
}

// raterDecisions collects linked decisions from BOTH relation tables, because
// E-1378 split them by source: a decision that `documents` this task lives in
// decision_relations, while a relation this task holds toward a decision
// still lives in task_deps. Reading only one would silently drop half.
func raterDecisions(db *sql.DB, taskID int64) ([]RaterDecision, error) {
	rows, err := db.Query(
		`SELECT d.id, d.title, d.status, dr.relation_type
		   FROM decision_relations dr
		   JOIN decisions d ON d.id = dr.source_decision_id
		  WHERE dr.target_kind = 'task' AND dr.target_id = ?
		 UNION
		 SELECT d.id, d.title, d.status, td.dep_type
		   FROM task_deps td
		   JOIN decisions d ON d.id = td.target_id
		  WHERE td.source_type = 'task' AND td.source_id = ?
		    AND td.target_type = 'decision'
		 ORDER BY 1
		 LIMIT ?`, taskID, taskID, raterDecisionLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("read decisions for E-%d: %w", taskID, err)
	}
	defer rows.Close()

	decisions := []RaterDecision{}
	for rows.Next() {
		var d RaterDecision
		if err = rows.Scan(&d.ID, &d.Title, &d.Status, &d.Relation); err != nil {
			return nil, fmt.Errorf("scan decision for E-%d: %w", taskID, err)
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}
