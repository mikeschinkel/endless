package hookcmd

import (
	"database/sql"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/templatecmd"
)

// E-1822 — deliver the type handoff on a claim into an already-running session.
//
// A spawned session is born with its per-type handoff: `endless task spawn`
// pre-claims the task and passes the rendered `handoff/<type>` text to the new
// Claude process as its opening prompt. A session that instead runs
// `endless task claim <id>` mid-flight receives none of it, and fills the gap by
// inference — which is how one session ends up working two tasks, editing the
// main checkout, or writing to the sandbox DB instead of the real ledger.
//
// The PostToolUse hook closes that gap: the Bash call that ran `task claim` is
// itself the signal, so the claim handoff rides back on that tool result as
// `additionalContext` (the E-1803 mechanism).
//
// Retrofit-vs-spawn needs no flag. `endless task spawn` claims the task in the
// *spawning* process, before the target session exists, so a claim never runs as
// a PostToolUse inside a spawned session — a PostToolUse `endless task claim` is
// definitionally the live-session retrofit.
//
// Best-effort throughout: every failure path returns "" so a hook that cannot
// render the handoff still lets the claim itself succeed. The claim's own stdout
// (which already prints the `/cd` line) remains the floor.

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

// terminalChildStatuses collapse into one `terminal` bucket in the children-state
// breakdown, mirroring Python's `_TERMINAL_STATUSES` (E-1567).
var terminalChildStatuses = map[string]bool{
	"confirmed": true,
	"assumed":   true,
	"completed": true,
	"declined":  true,
	"obsolete":  true,
}

// childrenStateOrder is the display order of the children-state buckets:
// lifecycle progression of the in-flight statuses, then the collapsed terminal
// bucket. Mirrors Python's `_CHILDREN_STATE_ORDER`.
var childrenStateOrder = []string{
	"untriaged", "unplanned", "ready", "underway",
	"blocked", "revisit", "unverified", "terminal",
}

// claimHandoffContext renders the claim handoff for a task just claimed into a
// live session, or "" when it cannot be produced. Never fatal: the caller folds
// a non-empty result into PostToolUse additionalContext and ignores an empty one.
//
// Subagents are skipped — an Agent-tool subagent shares its parent's cwd and
// represents tool use, not a session taking ownership of a task, so injecting a
// full session handoff into it would be noise aimed at the wrong reader.
func claimHandoffContext(projectID, taskID int64, payload claudePayload) string {
	if payload.AgentID != "" {
		return ""
	}
	vars, err := claimHandoffVars(projectID, taskID)
	if err != nil {
		log.Printf("claim handoff vars for task %d: %v", taskID, err)
		return ""
	}
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		log.Printf("claim handoff project path for %d: %v", projectID, err)
		return ""
	}
	out, err := templatecmd.Render(projectRoot, "handoff/claim", vars)
	if err != nil {
		log.Printf("claim handoff render for task %d: %v", taskID, err)
		return ""
	}
	return strings.TrimSpace(out)
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
		// by the claiming session's own hook, so the environment read here IS
		// that session's — a Desktop session claiming a task must not be handed
		// a contract its Stop hook will not enforce. (Contrast the Python spawn
		// handoff, which stays harness-agnostic on purpose: `task spawn` opens a
		// tmux window, so the session it describes is a terminal Claude Code one
		// by construction, whatever harness ran the command.)
		"report_gate": supportedAgent() && monitor.MinimizerEnabledForCwd(worktreePath, projectRoot),
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
func childrenBreakdown(db *sql.DB, taskID int64) (int, string, error) {
	rows, err := db.Query(
		"SELECT status, count(*) FROM live_tasks WHERE parent_id = ? GROUP BY status",
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
		if terminalChildStatuses[bucket] {
			bucket = "terminal"
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
	for _, bucket := range childrenStateOrder {
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
