package monitor

import (
	"database/sql"
	"errors"
	"fmt"
)

// E-1859 — the two reads the description-sufficiency triager needs.
//
// Triage decides, for each `untriaged` task, whether its description is
// already a sufficient spec (→ `submitted`) or design work is needed first
// (→ `unplanned`). The decision logic and the model call live in Python
// (src/endless/triage.py); the DB access lives here, so the triager adds no
// new Python SQLite surface to E-1486's backlog.
//
// The defining constraint is WHAT GOES IN THE CONTEXT: persisted artifacts
// only. The filing session's transcript is deliberately excluded — triage must
// judge what is written down, so the call is reproducible and matches what a
// future implementer will actually have to work from. Every field below is
// something `task show` would print.

// UntriagedTask is one row of the triage queue.
type UntriagedTask struct {
	ID int64 `json:"id"`
	// Project is the registered project NAME (not id): the Python caller
	// passes it straight to `endless-go template render --project`, and the
	// sweep is ledger-wide so it cannot assume a cwd-resolved project.
	Project string `json:"project"`
	Title   string `json:"title"`
}

// TriageParent is the focal task's parent, when it has one. Description is
// included because a child's description is routinely only sufficient when
// read against its parent's — "the Go job" means nothing without the epic
// that defines which job.
type TriageParent struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// TriageDecision is one decision linked to the focal task. Title carries the
// decision statement (decisions are titled with the decision itself), which is
// the part that constrains whether a description is a sufficient spec.
type TriageDecision struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Relation string `json:"relation"`
}

// TriageContext is everything the sufficiency prompt is allowed to see about
// one task. It is rendered to JSON and piped to `endless-go template render`,
// so every field name here is a template variable.
type TriageContext struct {
	TaskID  int64  `json:"task_id"`
	Project string `json:"project"`
	// ProjectRoot is the project's registered path. It is on the wire so the
	// Python caller can pass it to emit_event directly; without it emit_event
	// looks the path up itself through Python's `db.query`, which would put
	// SQLite back on the triage path this feature exists to keep off it.
	ProjectRoot string `json:"project_root"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Phase       string `json:"phase"`
	Status      string `json:"status"`
	// HasText reports whether a plan is already attached. It is a boolean, not
	// the text: a task with a plan is not a triage candidate at all, so the
	// prompt needs to know only that one exists.
	HasText bool `json:"has_text"`

	// Parent is nil for a root task.
	Parent *TriageParent `json:"parent"`
	// Siblings are the titles of the parent's OTHER children, oldest first.
	// Empty for a root task — deliberately, not for lack of a query: a root
	// task's "siblings" would be every root task in the project, which is an
	// unbounded list that says nothing about this task. Capped at
	// triageSiblingLimit so one enormous epic cannot blow up the prompt.
	Siblings []string `json:"siblings"`
	// Decisions are the accepted/proposed decisions linked to this task in
	// either direction (a decision that documents it, or a relation it holds
	// to a decision). Capped at triageDecisionLimit.
	Decisions []TriageDecision `json:"decisions"`
}

const (
	// triageSiblingLimit bounds the sibling titles carried into the prompt.
	triageSiblingLimit = 25
	// triageDecisionLimit bounds the linked decisions carried into the prompt.
	triageDecisionLimit = 25
)

// UntriagedTasks returns the triage queue against the global monitor DB:
// tasks in `untriaged`, OLDEST FIRST, capped at limit.
//
// Oldest-first matters. The sweep is the correctness guarantee behind the
// fire-and-forget inline path, so its job is to drain what the inline path
// missed — and what it missed is old. Newest-first would let a busy filing day
// starve a task that has sat unrouted for a week.
//
// projectName empty means every project: the job runner has a database but no
// cwd, so a ledger-wide sweep is the only scope it can express. A human
// running `endless triage run` inside a project passes the name.
func UntriagedTasks(projectName string, limit int) ([]UntriagedTask, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	return untriagedTasks(db, projectName, limit)
}

func untriagedTasks(db *sql.DB, projectName string, limit int) ([]UntriagedTask, error) {
	// Ordering ties on created_at (same-second filings are ordinary — a script
	// filing three follow-ups lands them in one second), so id breaks the tie
	// and keeps the sweep's order deterministic across invocations.
	query := `SELECT t.id, p.name, t.title
	            FROM live_tasks t
	            JOIN projects p ON p.id = t.project_id
	           WHERE t.status = 'untriaged'`
	args := []any{}
	if projectName != "" {
		query += " AND p.name = ?"
		args = append(args, projectName)
	}
	query += " ORDER BY t.created_at ASC, t.id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read untriaged queue: %w", err)
	}
	defer rows.Close()

	tasks := make([]UntriagedTask, 0, limit)
	for rows.Next() {
		var t UntriagedTask
		if err = rows.Scan(&t.ID, &t.Project, &t.Title); err != nil {
			return nil, fmt.Errorf("scan untriaged queue: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read untriaged queue: %w", err)
	}
	return tasks, nil
}

// BuildTriageContext assembles one task's triage context against the global
// monitor DB. Exported entry point for the session-query handler;
// triageContext is the db-taking core split out for tests (mirroring
// BuildTaskReportFacts).
func BuildTriageContext(taskID int64) (TriageContext, error) {
	db, err := DB()
	if err != nil {
		return TriageContext{}, err
	}
	return triageContext(db, taskID)
}

func triageContext(db *sql.DB, taskID int64) (TriageContext, error) {
	ctx := TriageContext{TaskID: taskID}

	// A missing task is a caller error, not an empty context — triage would
	// otherwise happily prompt about nothing and route a row that isn't there.
	// type_id is nullable, so LEFT JOIN + COALESCE; description/text are too.
	var parentID sql.NullInt64
	err := db.QueryRow(
		`SELECT p.name, p.path, t.title, COALESCE(t.description, ''),
		        COALESCE(tt.slug, ''), t.phase, t.status,
		        COALESCE(t.text, '') != '', t.parent_id
		   FROM live_tasks t
		   JOIN projects p ON p.id = t.project_id
		   LEFT JOIN task_types tt ON tt.id = t.type_id
		  WHERE t.id = ?`, taskID,
	).Scan(&ctx.Project, &ctx.ProjectRoot, &ctx.Title, &ctx.Description,
		&ctx.Type, &ctx.Phase, &ctx.Status, &ctx.HasText, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return ctx, fmt.Errorf("no such task E-%d", taskID)
	}
	if err != nil {
		return ctx, fmt.Errorf("read task E-%d: %w", taskID, err)
	}

	ctx.Siblings = []string{}
	if parentID.Valid {
		ctx.Parent, err = triageParent(db, parentID.Int64)
		if err != nil {
			return ctx, err
		}
		ctx.Siblings, err = triageSiblings(db, parentID.Int64, taskID)
		if err != nil {
			return ctx, err
		}
	}

	ctx.Decisions, err = triageDecisions(db, taskID)
	if err != nil {
		return ctx, err
	}
	return ctx, nil
}

// triageParent reads the parent row. A dangling parent_id (the FK is ON DELETE
// SET NULL, so this should not happen) yields nil rather than an error: a
// missing parent degrades the prompt, it does not invalidate the task.
func triageParent(db *sql.DB, parentID int64) (*TriageParent, error) {
	p := TriageParent{ID: parentID}
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

func triageSiblings(db *sql.DB, parentID, selfID int64) ([]string, error) {
	rows, err := db.Query(
		`SELECT title FROM live_tasks
		  WHERE parent_id = ? AND id != ?
		  ORDER BY sort_order, id
		  LIMIT ?`, parentID, selfID, triageSiblingLimit,
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

// triageDecisions collects linked decisions from BOTH relation tables, because
// E-1378 split them by source: a decision that `documents` this task lives in
// decision_relations, while a relation this task holds toward a decision
// (`implements`, `cleans_up`, …) still lives in task_deps. Reading only one
// would silently drop half the decisions a task is constrained by.
func triageDecisions(db *sql.DB, taskID int64) ([]TriageDecision, error) {
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
		 LIMIT ?`, taskID, taskID, triageDecisionLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("read decisions for E-%d: %w", taskID, err)
	}
	defer rows.Close()

	decisions := []TriageDecision{}
	for rows.Next() {
		var d TriageDecision
		if err = rows.Scan(&d.ID, &d.Title, &d.Status, &d.Relation); err != nil {
			return nil, fmt.Errorf("scan decision for E-%d: %w", taskID, err)
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}
