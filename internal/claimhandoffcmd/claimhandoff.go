// Package claimhandoffcmd implements `endless-go claim-handoff`: it renders the
// per-type handoff for a task just claimed into an already-running session
// (E-1822).
//
// A spawned session is born with its per-type handoff: `endless task spawn`
// pre-claims the task and passes the rendered `handoff/<type>` text to the new
// Claude process as its opening prompt. A session that instead runs
// `endless task claim <id>` mid-flight receives none of it, and fills the gap by
// inference — which is how one session ends up working two tasks, editing the
// main checkout, or writing to the sandbox DB instead of the main database.
//
// `endless task claim` closes that gap by printing this render when an agent
// ran the claim, so the handoff arrives in the claim's own tool result.
//
// It used to arrive from the PostToolUse hook, which regex-matched the Bash
// command text for a claim (E-2177). That fired on any heredoc, quoted argument
// or commit message that merely NAMED a claim, and told the agent to switch to
// whatever task the text named. Only the command that performed the claim knows
// it did; so the command reports it, and this package is the renderer it calls.
// Kept in Go so E-1063's port inherits it rather than re-deriving it.
package claimhandoffcmd

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/agentenv"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/templatecmd"
)

// Run is the `endless-go claim-handoff <task-id>` entry point: it renders the
// claim handoff for the task to stdout. The id may carry an `E-` prefix.
func Run(args []string) {
	if err := run(args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go claim-handoff: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: endless-go claim-handoff <task-id>")
	}
	taskID, err := strconv.ParseInt(strings.TrimPrefix(strings.ToUpper(args[0]), "E-"), 10, 64)
	if err != nil || taskID <= 0 {
		return fmt.Errorf("invalid task id %q", args[0])
	}
	out, err := Render(taskID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, out)
	return err
}

// handoffTypes are the task types with a per-type handoff. Anything else — an
// absent or unrecognized type — renders as `todo`, matching the Python spawn
// path's `_HANDOFF_TYPES` fallback so both renderings pick the same branch.
var handoffTypes = map[string]bool{
	"todo":       true,
	"bugfix":     true,
	"research":   true,
	"epic":       true,
	"brainstorm": true,
}

// terminalBucket is the name of the collapsed bucket every terminal status
// folds into in the children-state breakdown (E-1567). It is a bucket label,
// not a status, which is why it is not in taskstatus: the display order there
// covers the non-terminal statuses and this is appended after them.
const terminalBucket = "terminal"

// childrenStateBuckets is the display order of the children-state breakdown:
// the non-terminal statuses in lifecycle progression, then the collapsed
// terminal bucket. Derived from taskstatus rather than hand-listed (E-1891) —
// this list previously omitted `submitted`, which pushed a submitted child into
// the out-of-order `extras` tail below. taskstatus asserts that
// ChildrenStateOrder and Terminal partition the vocabulary, so every status now
// has exactly one bucket and the "(N total)" suffix reconciles by construction.
func childrenStateBuckets() []string {
	return append(taskstatus.Get(taskstatus.ChildrenStateOrder), terminalBucket)
}

// Render returns the claim handoff for a task that has just been claimed. It
// fails when the task has no worktree — the claim creates one, so a missing one
// means the claim did not get that far, and a handoff pointing at a nonexistent
// directory would be worse than none.
func Render(taskID int64) (string, error) {
	projectID, err := taskProjectID(taskID)
	if err != nil {
		return "", err
	}
	vars, err := claimHandoffVars(projectID, taskID)
	if err != nil {
		return "", err
	}
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		return "", fmt.Errorf("project path for %d: %w", projectID, err)
	}
	out, err := templatecmd.Render(projectRoot, "handoff/claim", vars)
	if err != nil {
		return "", fmt.Errorf("render claim handoff for task %d: %w", taskID, err)
	}
	return strings.TrimSpace(out), nil
}

// taskProjectID reads the project a task belongs to.
func taskProjectID(taskID int64) (int64, error) {
	db, err := monitor.DB()
	if err != nil {
		return 0, fmt.Errorf("open db: %w", err)
	}
	var projectID int64
	err = db.QueryRow("SELECT project_id FROM live_tasks WHERE id = ?", taskID).Scan(&projectID)
	if err != nil {
		return 0, fmt.Errorf("load task %d: %w", taskID, err)
	}
	return projectID, nil
}

// claimHandoffVars assembles the same var map the Python spawn path builds in
// `render_handoff`, so the claim wrapper and the per-type spawn wrappers see
// identical data and the shared `handoff/_mechanics` partials render the same
// lines from either side.
func claimHandoffVars(projectID, taskID int64) (map[string]any, error) {
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		return nil, fmt.Errorf("project path for %d: %w", projectID, err)
	}

	db, err := monitor.DB()
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	var (
		title    string
		typeSlug string
		parentID sql.NullInt64
	)
	err = db.QueryRow(
		"SELECT COALESCE(t.title, t.description, ''), COALESCE(tt.slug, ''), t.parent_id "+
			"FROM live_tasks t LEFT JOIN task_types tt ON tt.id = t.type_id WHERE t.id = ?",
		taskID,
	).Scan(&title, &typeSlug, &parentID)
	if err != nil {
		return nil, fmt.Errorf("load task %d: %w", taskID, err)
	}

	effectiveType := typeSlug
	if !handoffTypes[effectiveType] {
		effectiveType = "todo"
	}

	childCount, childrenState, err := childrenBreakdown(db, taskID)
	if err != nil {
		return nil, err
	}

	worktreePath, err := monitor.WorktreePathForTask(projectID, taskID)
	if err != nil {
		return nil, fmt.Errorf("worktree for task %d: %w", taskID, err)
	}
	if worktreePath == "" {
		// The claim creates the worktree, so a missing one means the claim did
		// not get that far (refused by a gate, or errored). Rendering a handoff
		// that points at a nonexistent directory would be worse than silence.
		return nil, fmt.Errorf("task %d has no worktree", taskID)
	}

	return map[string]any{
		"spawned_id":     taskID,
		"label_prefix":   hierarchicalLabelPrefix(taskID, parentID),
		"title":          title,
		"task_type":      effectiveType,
		"worktree_path":  worktreePath,
		"branch":         worktreeBranch(worktreePath),
		"child_count":    childCount,
		"children_state": childrenState,
		"bg":             false,
		// E-1953: a project that switched the report channel off must not be
		// handed the reporting instructions. They would cost the session a
		// per-turn model round trip that nothing enforces and nothing reads.
		//
		// E-1962: same for an unsupported agent harness. This handoff is rendered
		// under the claiming session's own `task claim`, so the environment read
		// here IS that session's — a Desktop session claiming a task must not be handed
		// a contract its Stop hook will not enforce. (Contrast the Python spawn
		// handoff, which stays harness-agnostic on purpose: `task spawn` opens a
		// tmux window, so the session it describes is a terminal Claude Code one
		// by construction, whatever harness ran the command.)
		"report_gate": agentenv.Supported() && monitor.MinimizerEnabledForCwd(worktreePath, projectRoot),
	}, nil
}

// hierarchicalLabelPrefix renders `E-<parent>/E-<id>` for a parented task and a
// bare `E-<id>` for a root one, keying solely on parent presence (E-1620).
// Mirrors Python's `_hierarchical_label_prefix`.
func hierarchicalLabelPrefix(taskID int64, parentID sql.NullInt64) string {
	if parentID.Valid && parentID.Int64 > 0 {
		return fmt.Sprintf("E-%d/E-%d", parentID.Int64, taskID)
	}
	return fmt.Sprintf("E-%d", taskID)
}

// childrenBreakdown returns the direct-child count and the pre-formatted
// children-state string the epic handoff prints, e.g.
// "2 unplanned, 3 ready, 1 underway, 4 terminal (10 total)". With no children it
// returns "no children yet". Mirrors Python's `_children_state` (E-1567).
//
// E-2161: counted by effective_parent_id — the children the handoff describes
// are the ones the session will see under the epic, which after a removal
// partway down the tree is not the same set as `parent_id = ?`. The Python twin
// counts the same way, and the two must agree: they render the same line.
func childrenBreakdown(db *sql.DB, taskID int64) (int, string, error) {
	rows, err := db.Query(
		"SELECT status, count(*) FROM task_tree WHERE effective_parent_id = ? GROUP BY status",
		taskID,
	)
	if err != nil {
		return 0, "", fmt.Errorf("children of %d: %w", taskID, err)
	}
	defer rows.Close()

	counts := map[string]int{}
	total := 0
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return 0, "", fmt.Errorf("scan children of %d: %w", taskID, err)
		}
		total += n
		bucket := status
		if taskstatus.Has(taskstatus.Terminal, bucket) {
			bucket = terminalBucket
		}
		counts[bucket] += n
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("children of %d: %w", taskID, err)
	}
	if total == 0 {
		return 0, "no children yet", nil
	}

	var parts []string
	seen := map[string]bool{}
	for _, bucket := range childrenStateBuckets() {
		if counts[bucket] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[bucket], bucket))
			seen[bucket] = true
		}
	}
	// A status outside the known order must not vanish from the breakdown —
	// the "(N total)" suffix has to reconcile with the child count.
	var extras []string
	for bucket, n := range counts {
		if !seen[bucket] && n > 0 {
			extras = append(extras, fmt.Sprintf("%d %s", n, bucket))
		}
	}
	sort.Strings(extras)
	parts = append(parts, extras...)

	return total, fmt.Sprintf("%s (%d total)", strings.Join(parts, ", "), total), nil
}

// worktreeBranch returns the checked-out branch of the worktree, or
// "<task branch>" when git cannot answer — the same placeholder the Python
// spawn path falls back to. `branch --show-current` rather than `rev-parse
// --abbrev-ref HEAD`: it answers on an unborn branch (a worktree with no commit
// yet) and returns empty rather than the literal "HEAD" when detached, so both
// degenerate cases reach the placeholder instead of printing something wrong.
func worktreeBranch(worktreePath string) string {
	out, err := exec.Command(
		"git", "-C", worktreePath, "branch", "--show-current",
	).Output()
	if err != nil {
		return "<task branch>"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "<task branch>"
	}
	return branch
}
