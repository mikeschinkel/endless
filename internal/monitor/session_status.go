package monitor

import (
	"database/sql"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// SessionStatusRow is one task row in the per-session "what's next" view
// (E-1465). It carries the raw fields the renderer needs; all icon/letter/
// phase-char/sort derivation happens in the caller (internal/sessionstatuscmd)
// so the rendering rules stay testable without a DB.
//
// IsFocal/IsParent/IsFrom/InFlight are mutually-prioritized decorations computed
// in-query: IsFocal is the window's own active task; IsParent the focal's real
// task-tree parent (tasks.parent_id); IsFrom the SPAWNING session's active task
// (session lineage — "where this session came from", NOT a tree relation);
// InFlight any OTHER live session's active task. IsParent and IsFrom are distinct
// (E-1694): the spawner is rarely the task-tree parent, and conflating them was
// E-1465's original mislabel. BlockedByN counts open tasks that block this one;
// BlocksN counts tasks this one blocks (regardless of their status — it drives
// the ⏸ "blocks" marker).
type SessionStatusRow struct {
	ID        int64
	ProjectID int64
	Title     string
	Status    string
	Phase     string
	TypeSlug  string
	HasPlan   bool
	IsFocal   bool
	IsParent  bool
	IsFrom    bool
	InFlight  bool
	// Landed is true when the task has >=1 task_landings row (its work has
	// merged). classify() routes a landed task to actLanded (⏚) so merged work is
	// never offered as a fresh actionable verb (E-1693; the ⁇ catch-all this
	// comment used to name was split into ⏚ landed + ⁇ unknown by E-1750). The
	// focal/parent/from/in-flight decorations still win over it, and ⏚ in turn
	// wins over ⇥ closed for a landed terminal task (E-1871).
	Landed bool
	// Unsettled is the landed-vs-worktree delta the flat view marks with ◆
	// (E-1701): the task's worktree exists AND diverges from main — modified
	// (uncommitted changes) or unlanded (commits not yet on main). It is NOT read
	// from the DB (task_landings only records that a land happened, not whether the
	// tree moved since); it is filled in by AnnotateSessionStatusUnsettled, which
	// shells out to git, and only on the flat render path. --tree leaves it false.
	Unsettled bool
	// UnsettledKnown says whether Unsettled above is an ANSWER or a placeholder
	// (E-2128). The unlanded half of the verdict is now read from a cache one
	// background job writes rather than computed per row per tick (ED-1589), and a
	// cache miss has no verdict to report — so the renderer draws `~` (not yet
	// determined) instead of choosing between ◆ and a blank on no evidence.
	//
	// Like Unsettled it is an annotation, filled only on the flat path; --tree
	// leaves it false, which is harmless there because --tree draws no marker.
	//
	// It is true in more cases than "the cache had an entry": a task with no
	// worktree has nothing to land, and a DIRTY worktree is known to be unsettled
	// from `git status` alone, which stays live. See UnsettledDetail.UnsettledKnown.
	UnsettledKnown bool
	// Hidden / HiddenAt are the VIEWING session's per-session suppression of this
	// task (E-1914): a session_hidden_tasks row for (viewer, task). Like Unsettled
	// they are NOT part of the row query — the row set is viewer-agnostic, and
	// hiding is a property of the (session, task) pair, never of the task alone.
	// AnnotateSessionStatusHidden fills them for one viewer; unannotated rows stay
	// false/"" so every existing caller is unaffected.
	Hidden   bool
	HiddenAt string
	// Relation is how this task entered the VIEWING session's scope (E-1696):
	// the session_tasks.relation_id of the (viewer, task) row. Like Hidden it is
	// an annotation, not part of the row query, and for the same reason — the row
	// set is viewer-agnostic while relation belongs to the (session, task) pair.
	// The focal view in particular unions rows from EVERY session working the
	// focal task, so a relation baked into the query would report some other
	// session's classification as if it were yours.
	//
	// Zero (sessiontaskrelation.Relation(0), not a member of All()) means "this
	// viewer has no session_tasks row for the task" — the read-time children,
	// dependents and upstream blockers (E-1685/E-1691/E-1795), which have no
	// session_tasks row by design. Rank() puts it last, which is where an
	// unclassified row belongs. AnnotateSessionStatusRelation fills this for one
	// viewer; unannotated rows stay 0 so every existing caller is unaffected.
	Relation   sessiontaskrelation.Relation
	BlockedByN int
	BlocksN    int
	// ReplacedBy holds the ids of the tasks that supersede this one. `old
	// replaced_by new` is stored active-voice as (source=new, target=old,
	// dep_type='replaces'), so these are the source_ids of the 'replaces' rows
	// pointing AT this task.
	//
	// E-1956: a terminal status is the end of the story as the view tells it —
	// ⇥ closed says the task is finished and nothing follows — so a superseded
	// task read off this view looked abandoned unless you went and ran `task
	// show`. Part of the row query rather than an annotation (unlike Unsettled
	// and Hidden) because it is a property of the task alone: viewer-agnostic,
	// no git, no session. Empty for the overwhelming majority of rows.
	ReplacedBy []int64
	// Duplicates holds the ids of the tasks this one was a redundant filing of
	// (E-1185). Same rule as ReplacedBy — a task closed BECAUSE it duplicated
	// another reads as abandoned when the row shows only ⇥ closed — but the
	// OPPOSITE endpoint: `dupe duplicates keeper` is stored (source=dupe,
	// target=keeper) and it is the dupe that gets closed, so these are the
	// target_ids of rows pointing AWAY from this task.
	Duplicates []int64
}

// replacedByExpr is the `enr`-CTE column that collects a task's replacements as
// a comma-separated id list (NULL when there are none). Shared by both row
// queries so the two cannot drift. The live_tasks join keeps a removed
// replacement from being named.
const replacedByExpr = `
    (SELECT group_concat(d.source_id)
       FROM task_deps d JOIN live_tasks rep ON rep.id = d.source_id
      WHERE d.source_type = 'task' AND d.target_type = 'task'
        AND d.dep_type = 'replaces' AND d.target_id = b.id) AS replaced_by`

// duplicatesExpr is replacedByExpr's mirror image: same shape, swapped columns,
// because the task the annotation lands on sits on the other end of this
// relation (see SessionStatusRow.Duplicates). Reading the two together, the
// swap looks like a copy-paste slip; it is the point.
const duplicatesExpr = `
    (SELECT group_concat(d.target_id)
       FROM task_deps d JOIN live_tasks kept ON kept.id = d.target_id
      WHERE d.source_type = 'task' AND d.target_type = 'task'
        AND d.dep_type = 'duplicates' AND d.source_id = b.id) AS duplicates`

// parseRelationIDs turns a group_concat result from either expression above
// into ids. A malformed element is skipped rather than failing the whole view:
// these columns are annotations, and no row set is worth losing over one
// unparseable id.
func parseRelationIDs(s string) []int64 {
	if s == "" {
		return nil
	}
	var out []int64
	for _, part := range strings.Split(s, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

// terminalStatusSet is the canonical "done-work" status set, rendered for the
// SQL IN clauses below: these are omitted from the session-status view unless
// they are the focal/parent row or --all is passed. Kept in sync with the
// prototype spec (~/.config/endless/session-status.sql).
//
// E-1891: a status list inside a SQL string literal is invisible to every tool,
// which is why these rot longest — taskstatus.SQLList renders it from the one
// registry instead. Computed at init, not per query.
var terminalStatusSet = taskstatus.SQLList(taskstatus.Terminal)

// ResolveSessionStatusFocal resolves the focal task for the session-status/monitor
// view through the SAME pane-scoped path the tmux status line uses (GetPaneStatus),
// so the two surfaces never disagree (E-1698). It returns the resolved focal task
// id (0 when none) and the PaneStatusKind so the caller can render the matching
// hint — the identical classification the status bar shows.
//
// Only PaneStatusActive yields a focal task (the live session's active_task via
// pane→window process match). Every other kind returns focal 0:
//
//   - PaneStatusNoTask          — a session exists in this window but no active
//     task; the bar shows "claim a task".
//   - PaneStatusClaudeNoSession — Claude is in the pane but no session row exists.
//   - PaneStatusNone            — no Endless context (also the non-tmux case,
//     tmuxPane == "").
//
// This deliberately DROPS the old window-@endless_task_id and machine-wide
// most-recent fallbacks (E-1465 / ED-1523): the machine-wide one returned an
// UNRELATED task for a pane with nothing of its own, and the status line — which
// has no such fallback — already proves the hint-based behavior is correct
// (E-1698). Both claim and bind write the session's task_id, so bound
// windows still resolve via the pane-scoped path with no window-option fallback.
func ResolveSessionStatusFocal(tmuxPane string) (int64, PaneStatusKind, error) {
	ps, err := GetPaneStatus(tmuxPane)
	if err != nil {
		return 0, PaneStatusNone, err
	}
	if ps.Kind == PaneStatusActive {
		return ps.Task.TaskID, ps.Kind, nil
	}
	return 0, ps.Kind, nil
}

// ResolveSessionStatusParentSession reads the window's @endless_spawned_by marker
// and returns the spawning session's integer sessions.id, or 0 when the window
// was not spawned by `endless task spawn` or the marker is a `pid-<n>` fallback
// (a non-Claude spawner with no session row). The marker holds a session id,
// NOT a pane id — the bash prototype's pane-based lookup is wrong; the Go
// command resolves the parent's active task directly from this id in the query.
func ResolveSessionStatusParentSession(tmuxPane string) int64 {
	v := tmuxWindowOption(tmuxPane, "@endless_spawned_by")
	if v == "" {
		return 0
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// tmuxWindowOption reads a tmux window option (e.g. "@endless_task_id") for the
// given pane's window. Returns "" when not in tmux, the option is unset, or tmux
// errors. Mirrors hookcmd.tmuxTaskID/tmuxSpawnedBy, reused here so the read
// command composes the same resolution rather than re-deriving pane→session.
func tmuxWindowOption(pane, name string) string {
	if pane == "" {
		return ""
	}
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", pane, "#{"+name+"}",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SessionStatusRows returns the task rows for the session-status view of focal task
// `focal`, with `parentSession` the spawning session's id (0 if none). The row
// set, per the prototype spec (~/.config/endless/session-status.sql) plus E-1685
// and E-1691:
//
//   - every task touched (via session_tasks) by ANY live-or-dead session whose
//     task_id = focal (cross-project; robust to duplicate session rows),
//   - ∪ the focal task itself,
//   - ∪ the focal's real task-tree parent (tasks.parent_id) — the ↑ parent row,
//   - ∪ the spawning session's active task — the ↩ from row (session lineage),
//   - ∪ the focal task's DIRECT dependents — tasks T it blocks
//     (task_deps source=focal, target=T, dep_type='blocks'). These are computed
//     at read time, NOT written into session_tasks: that table is a projection
//     of the event ledger, and a dependent has no backing task.* event (E-1685).
//     A dependent carries ⊗ while the focal is open (its BlockedByN counts the
//     open focal); when the focal lands and the block clears, BlockedByN drops to
//     0 and ⊗ disappears with no special highlight. One hop only, not the
//     transitive closure.
//   - ∪ the focal task's DIRECT children — tasks T with parent_id = focal
//     (E-1691). For an epic the children ARE the work; surfacing them lets the
//     session's pane carry the subtasks. Read-time only, same invariant reason
//     as the dependents. One level only — working a child surfaces ITS children
//     in that child's own session.
//
// Done-work (terminal status) is omitted UNLESS the row is the focal, parent, or
// from (spawner) row, or includeAll is true. Returns an empty slice when focal
// is 0.
func SessionStatusRows(focal, parentSession int64, includeAll bool) ([]SessionStatusRow, error) {
	if focal == 0 {
		return nil, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}

	allFlag := 0
	if includeAll {
		allFlag = 1
	}

	q := `
WITH RECURSIVE
ftask(tid) AS (SELECT ?),
-- sfoc.stid = the SPAWNING session's active task (session lineage → ↩ from).
sfoc(stid) AS (SELECT task_id FROM sessions WHERE id = ?),
-- rpar.rpid = the focal's real task-tree parent (tasks.parent_id → ↑ parent).
rpar(rpid) AS (SELECT parent_id FROM live_tasks WHERE id = (SELECT tid FROM ftask)),
base AS (
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM session_tasks st JOIN live_tasks t ON t.id = st.task_id
   WHERE st.session_id IN (
     SELECT id FROM sessions WHERE task_id = (SELECT tid FROM ftask)
   )
  UNION
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM live_tasks t WHERE t.id = (SELECT tid FROM ftask)
  UNION
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM live_tasks t, rpar WHERE t.id = rpar.rpid
  UNION
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM live_tasks t, sfoc WHERE t.id = sfoc.stid
  UNION
  -- E-1685: the focal task's direct dependents (tasks it blocks), read-time
  -- only. Computed here rather than materialized into session_tasks so the
  -- projection-of-the-event-ledger invariant holds. The terminal-status filter
  -- in the final SELECT drops done dependents unless --all; the BlockedByN
  -- column drives their ⊗ while the focal stays open.
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM live_tasks t
   WHERE EXISTS (
     SELECT 1 FROM task_deps d
      WHERE d.source_type = 'task' AND d.target_type = 'task'
        AND d.dep_type = 'blocks'
        AND d.source_id = (SELECT tid FROM ftask)
        AND d.target_id = t.id
   )
  UNION
  -- E-1691: the focal task's DIRECT children. For an epic the children ARE the
  -- work, yet the row set above omits them. Computed at read time (not written
  -- into session_tasks) for the same projection-invariant reason as E-1685's
  -- dependents. Direct (one level) only: working a child surfaces ITS children
  -- in that child's own session, keeping each view one level deep rather than
  -- exploding the whole subtree. The terminal-status filter in the final SELECT
  -- drops done children unless --all, matching the dependent behavior.
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM live_tasks t WHERE t.parent_id = (SELECT tid FROM ftask)
),
-- E-1795: the UPSTREAM blocker chain of every task already in base, walked
-- TRANSITIVELY. Seeded from base's ids, each step adds the OPEN tasks that block
-- a task already reached (task_deps source=blocker, target=reached,
-- dep_type='blocks'). So a chain head several hops up (e.g. an unplanned epic)
-- surfaces wherever its downstream chain is already displayed. The walk is
-- restricted to non-terminal blockers to match the E-876 status-based release
-- (a done blocker no longer blocks, so its own prerequisites are irrelevant); it
-- also stops the walk from leaking past a resolved gate. UNION (not UNION ALL)
-- dedupes and terminates on cycles. Direction asymmetry is deliberate: children
-- stay one-hop (fan-out risk), blockers walk the full chain (narrow in practice).
upchain(id) AS (
  SELECT id FROM base
  UNION
  SELECT d.source_id
    FROM task_deps d
    JOIN upchain u ON d.target_id = u.id
    JOIN live_tasks blk ON blk.id = d.source_id
   WHERE d.source_type = 'task' AND d.target_type = 'task'
     AND d.dep_type = 'blocks'
     AND blk.status NOT IN (` + terminalStatusSet + `)
),
-- base ∪ the transitive upstream blockers resolved to full task rows. upchain's
-- seed rows are already in base, so the join below only adds the newly-reached
-- prerequisites; UNION dedupes the overlap.
allbase AS (
  SELECT id, project_id, title, status, phase, plan, type_id FROM base
  UNION
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM live_tasks t JOIN upchain u ON u.id = t.id
),
enr AS (
  SELECT b.id, b.project_id, b.title, b.status, b.phase,
    COALESCE((SELECT slug FROM task_types WHERE id = b.type_id), '') AS type_slug,
    (b.plan IS NOT NULL AND b.plan <> '') AS has_plan,
    (b.id = (SELECT tid FROM ftask)) AS is_focal,
    -- rpar.rpid is NULL when the focal has no parent; COALESCE keeps is_parent a
    -- real boolean rather than NULL. The focal-self guard avoids self-marking.
    COALESCE(b.id = (SELECT rpid FROM rpar), 0) AND b.id <> (SELECT tid FROM ftask) AS is_parent,
    -- sfoc.stid is NULL when there is no spawning session (or it has no active
    -- task). is_from yields to is_parent in the renderer when both are true.
    COALESCE(b.id = (SELECT stid FROM sfoc), 0) AND b.id <> (SELECT tid FROM ftask) AS is_from,
    (EXISTS(
       SELECT 1 FROM sessions s
        WHERE s.state IN (` + liveSessionStates + `) AND s.task_id = b.id
     ) AND b.id <> (SELECT tid FROM ftask)) AS in_flight,
    -- E-1693: the task's work has already merged (>=1 task_landings row). A
    -- landed non-terminal task stays visible (it still passes the terminal-status
    -- filter) but the renderer routes it to ⏚ landed rather than a fresh ▶/✎/☑
    -- (E-1750 split the old ⁇ catch-all this line used to name into ⏚ + ⁇).
    EXISTS(SELECT 1 FROM task_landings tl WHERE tl.task_id = b.id) AS landed,
    (SELECT count(*) FROM task_deps d JOIN live_tasks blk ON blk.id = d.source_id
       WHERE d.source_type = 'task' AND d.target_type = 'task'
         AND d.dep_type = 'blocks' AND d.target_id = b.id
         AND blk.status NOT IN (` + terminalStatusSet + `)) AS blocked_by_n,
    (SELECT count(*) FROM task_deps d
       WHERE d.source_type = 'task' AND d.source_id = b.id
         AND d.dep_type = 'blocks') AS blocks_n,` + replacedByExpr + `,` + duplicatesExpr + `
  FROM allbase b
)
SELECT id, project_id, title, status, phase, type_slug, has_plan,
       is_focal, is_parent, is_from, in_flight, landed, blocked_by_n, blocks_n,
       replaced_by, duplicates
  FROM enr
 WHERE (? = 1) OR is_focal OR is_parent OR is_from
       OR status NOT IN (` + terminalStatusSet + `)
`

	rows, err := db.Query(q, focal, parentSession, allFlag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSessionStatusRows(rows)
}

// scanSessionStatusRows drains a row set produced by either of the two
// session-status queries. They select the same column list in the same order —
// which is exactly why this is one function: a column added to one query and
// not the other now fails to compile rather than silently mis-scanning.
func scanSessionStatusRows(rows *sql.Rows) ([]SessionStatusRow, error) {
	var out []SessionStatusRow
	for rows.Next() {
		var r SessionStatusRow
		var replaced, duplicates sql.NullString
		if err := rows.Scan(
			&r.ID, &r.ProjectID, &r.Title, &r.Status, &r.Phase, &r.TypeSlug, &r.HasPlan,
			&r.IsFocal, &r.IsParent, &r.IsFrom, &r.InFlight, &r.Landed, &r.BlockedByN, &r.BlocksN,
			&replaced, &duplicates,
		); err != nil {
			return nil, err
		}
		r.ReplacedBy = parseRelationIDs(replaced.String)
		r.Duplicates = parseRelationIDs(duplicates.String)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SessionStatusRowsForSession returns the surfaced/revisited task rows recorded
// in session_tasks for session `sessionID` — the tasks this session filed
// (relation surfaced=2) or touched-but-did-not-claim (revisited=3). It is the
// no-goal companion to SessionStatusRows: when a session has no claimed task
// (task_id NULL) the focal-anchored projection surfaces nothing, so
// `session status` would hide the session's own work entirely (E-1802). This
// reads the already-recorded rows directly off the session and enriches them
// with the same decoration/count columns the focal view uses.
//
// The classification is authoritative and set-once by the capture executors
// (E-1462); this only READS relation_id, never reclassifies. The goal relation
// (1) is intentionally excluded here: a session with a goal takes the
// focal-anchored path, so the goal row is surfaced there, not here.
//
// is_focal/is_parent/is_from are always 0 in this view — there is no focal task
// to anchor those decorations to. in_flight, landed, blocked_by_n, and blocks_n
// are computed identically to the focal view. Done-work (terminal status) is
// omitted unless includeAll is true. Returns an empty slice when sessionID is 0.
func SessionStatusRowsForSession(sessionID int64, includeAll bool) ([]SessionStatusRow, error) {
	if sessionID == 0 {
		return nil, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}

	allFlag := 0
	if includeAll {
		allFlag = 1
	}

	// Every relation EXCEPT claimed (1). It is excluded because a session that
	// claimed a task resolves via SessionStatusRows, so a claimed row reaching
	// here would mean the anchor already took the other path. queued (5) and
	// referenced (4) are included on their merits: `session task add` promotes
	// work the session has decided on but not touched, and that is precisely the
	// case this unclaimed view exists to show. The set is built from the enum
	// rather than written as a literal so adding a relation cannot silently omit
	// it here (E-1696).
	q := `
WITH base AS (
  SELECT t.id, t.project_id, t.title, t.status, t.phase, t.plan, t.type_id
    FROM session_tasks st JOIN live_tasks t ON t.id = st.task_id
   WHERE st.session_id = ?
     AND st.relation_id IN (` + nonClaimedRelationIDs + `)
),
enr AS (
  SELECT b.id, b.project_id, b.title, b.status, b.phase,
    COALESCE((SELECT slug FROM task_types WHERE id = b.type_id), '') AS type_slug,
    (b.plan IS NOT NULL AND b.plan <> '') AS has_plan,
    0 AS is_focal,
    0 AS is_parent,
    0 AS is_from,
    EXISTS(
       SELECT 1 FROM sessions s
        WHERE s.state IN (` + liveSessionStates + `) AND s.task_id = b.id
     ) AS in_flight,
    EXISTS(SELECT 1 FROM task_landings tl WHERE tl.task_id = b.id) AS landed,
    (SELECT count(*) FROM task_deps d JOIN live_tasks blk ON blk.id = d.source_id
       WHERE d.source_type = 'task' AND d.target_type = 'task'
         AND d.dep_type = 'blocks' AND d.target_id = b.id
         AND blk.status NOT IN (` + terminalStatusSet + `)) AS blocked_by_n,
    (SELECT count(*) FROM task_deps d
       WHERE d.source_type = 'task' AND d.source_id = b.id
         AND d.dep_type = 'blocks') AS blocks_n,` + replacedByExpr + `,` + duplicatesExpr + `
  FROM base b
)
SELECT id, project_id, title, status, phase, type_slug, has_plan,
       is_focal, is_parent, is_from, in_flight, landed, blocked_by_n, blocks_n,
       replaced_by, duplicates
  FROM enr
 WHERE (? = 1) OR status NOT IN (` + terminalStatusSet + `)
`

	rows, err := db.Query(q, sessionID, allFlag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSessionStatusRows(rows)
}

// intPlaceholders renders "?,?,…" with len(ids) slots and the matching []any
// args, for a dynamic SQL `IN` clause. Returns ("", nil) for an empty set so
// callers can short-circuit (an empty `IN ()` is a SQL error).
func intPlaceholders(ids []int64) (string, []any) {
	if len(ids) == 0 {
		return "", nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// SessionStatusBlockerEdges returns the in-set blocked-by edges among `ids`: for
// each task that is blocked, the list of its blockers that are ALSO in `ids` and
// still OPEN (blocker status not terminal). This is the blocks-DAG restricted to
// the candidate set, which `session status --tree` topologically layers into
// implementation order. Result maps target (blocked task) → []source (blockers).
// Tasks with no in-set open blocker are absent from the map (they are roots).
func SessionStatusBlockerEdges(ids []int64) (map[int64][]int64, error) {
	if len(ids) == 0 {
		return map[int64][]int64{}, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	ph, args := intPlaceholders(ids)
	// Both endpoints must be in the candidate set; the blocker must be open.
	q := `
SELECT d.target_id, d.source_id
  FROM task_deps d JOIN live_tasks blk ON blk.id = d.source_id
 WHERE d.source_type = 'task' AND d.target_type = 'task'
   AND d.dep_type = 'blocks'
   AND blk.status NOT IN (` + terminalStatusSet + `)
   AND d.target_id IN (` + ph + `)
   AND d.source_id IN (` + ph + `)`
	rows, err := db.Query(q, append(append([]any{}, args...), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges := make(map[int64][]int64)
	for rows.Next() {
		var target, source int64
		if err := rows.Scan(&target, &source); err != nil {
			return nil, err
		}
		edges[target] = append(edges[target], source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return edges, nil
}

// SessionStatusDoOrder returns the per-session implementation order (E-1683's
// session_tasks.do_order) for the candidate `ids`, scoped to sessions whose
// task_id = focal — the same union scope SessionStatusRows uses. Only
// non-null do_order rows are returned; a task absent from the map has no
// explicit order. When non-empty, this OVERRIDES the DAG-derived order in
// `session status --tree`.
func SessionStatusDoOrder(focal int64, ids []int64) (map[int64]int64, error) {
	if focal == 0 || len(ids) == 0 {
		return map[int64]int64{}, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	ph, args := intPlaceholders(ids)
	q := `
SELECT st.task_id, st.do_order
  FROM session_tasks st JOIN sessions s ON s.id = st.session_id
 WHERE s.task_id = ?
   AND st.do_order IS NOT NULL
   AND st.task_id IN (` + ph + `)`
	rows, err := db.Query(q, append([]any{focal}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := make(map[int64]int64)
	for rows.Next() {
		var taskID, doOrder int64
		if err := rows.Scan(&taskID, &doOrder); err != nil {
			return nil, err
		}
		order[taskID] = doOrder
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return order, nil
}
