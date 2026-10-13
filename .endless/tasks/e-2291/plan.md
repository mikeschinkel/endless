# Plan: make task bind the planning step before claim

`session_tasks` should record how a session came to own its task. Today it learns that only through the `task.claimed` executor, and two of the three ways a session comes to own a task never reach it:
- `task bind` emits `task.claimed`, so it records `claimed` even though nothing was claimed.
- The SessionStart cwd auto-bind (`maybeCwdBind` → `monitor.BindSessionToTask`) writes `sessions.task_id` directly, with no event and no `session_tasks` row. Spawn's claim skips `task.claimed` because no session exists yet to name, so a spawned session's own task has no ownership row at all. For example, ES-1314's row for E-2283 reads `revisited`.

Separately, claim's no-plan refusal never mentions `task bind`. So a session drafting a plan with its user concludes the gate has to be loosened.

Ownership itself stays in `sessions.task_id` (`_task_claimants`, the write gate). Nothing here changes who owns a task. It changes only how that ownership is labelled and listed.

## 1. A `bound` relation (ED-1497 relations)
- Append `RelationBound = 6` to `internal/sessiontaskrelation`, with slug `bound` and label `Bound`. Never renumber. Add the `schema.sql` seed row and a migration. `VerifyIntegrity` fails closed on drift.
- Rank: directly below `claimed`, above `touched`. A later `task claim` by the same session then upgrades `bound` to `claimed`, and binding a touched task upgrades `touched` to `bound`.
- In `session status`'s display tier (`prominence` in `relation_tier.go`), `bound` counts as decided work, alongside `claimed` and `touched`.

## 1b. Rename `surfaced` to `filed`
- Relation id 2 keeps its id. Change its slug to `filed` and its label to `Filed` in `sessiontaskrelation` and in the `schema.sql` seed, which upserts the change onto existing rows on connect (the E-1659 pattern). No data migration.
- Update every reader and doc that names `surfaced`: the Go switch statements, the `session status` and `task show` text, the sessions guide's relation table, and the `touch`/`session task add` help.
- Replayed `task.imported` events, which are legacy (their commands were removed in E-2142), also land as `filed`. That is accepted.

## 1c. Retire `queued`; `touch` records `touched`
- Relation id 5 keeps its id. Change its slug from `queued` to `touched` and its label to `Touched`, through the same seed-upsert as 1b. No data migration.
- `endless touch` writes `touched` instead of `revisited`. `revisited` goes back to meaning only "the session edited it".
- `endless session task add` becomes an alias of `endless touch`: same event (`session_tasks.touched`), same relation. Retire the `session_tasks.queued` event kind for emission. The ledger holds none, so there is nothing to replay. Keep it decodable if the event registry requires that.
- Replay: the existing `session_tasks.touched` events (23 in the main ledger) rebuild as `touched`, not `revisited`, because the executor for that kind now upserts `touched`.
- `touched` keeps queued's place: it ranks just below `bound`, above `filed`. It keeps the ⊕ mark, and counts as decided work in `session status`.
- Update every reader and doc that names `queued` or calls touch `revisited`: the Go switch statements, `relation_tier.go`'s glyph comment, the help for `touch` and `session task add`, and the sessions guide's relation table and verb list.

## 1d. Rename `task show`'s "Touched by:" header to "Sessions:"
- After 1c, "touched" names one relation, so the header that lists every `session_tasks` relation can no longer use the word. Rename it to `Sessions:`. Each row's label already says how that session is involved.
- The header is printed in one place, `_echo_touched_by_section` in `src/endless/task_cmd.py`. Rename the function to match.
- Update the comments, docstrings and help that cite "Touched by:": `task_cmd.py`, `session_cmd.py`, `session_task_cmd.py`, `cli.py` (`session goto`, `session task remove`, `session hide`), and the comment in `internal/events/session_task_membership.go`.
- Update the guide: `docs/guide/tasks.md` (the block's section and its example), `docs/guide/sessions.md` (`remove`), and `docs/guide/appendix-a.md` (`session goto`).
- Update `tests/test_task_show_sessions.py` to assert `Sessions:`.
- Old task plans and analyses under `.endless/tasks/` stay as written.

## 2. Which relation each path writes
- `task claim` → `claimed`. No change.
- `task bind` → `bound`, whatever the task's status (Q1 a).
- The SessionStart cwd auto-bind → decided by the task's status at bind time (auto-bind a):
  - `underway` (spawn claimed it for this session) → `claimed`
  - pre-work (`unplanned`/`submitted`/`ready`, i.e. a primed session, ED-1621) → `bound`
  - any other status → `bound`
  
  It writes the row in the same transaction as the `sessions` upsert in `BindSessionToTask`.

## 3. Bind gets its own event (Q3 a)
- A new kind, `task.bound`, emitted by `bind_item`. Its executor shares the `sessions` UPDATE (task_id, revive an ended row, epic_id) with the `task.claimed` executor through one helper, so the two are not copies. It upserts `RelationBound`.
- Old bind events in the ledger stay `task.claimed`, so they rebuild as `claimed`. They cannot be told apart after the fact, and that is accepted.
- Check other readers of `KindTaskClaimed` (for example the `claim-event` reason in `usermachinelog.go`) and give `task.bound` its own reason where they log one.

## 4. Readers that special-case `claimed`
- **`session status`:** `nonClaimedRelationIDs` becomes "every relation except the anchor relations". The anchor relations are a named set, `{claimed, bound}`, defined in `sessiontaskrelation`, so a bound task is the session's anchor and not a second row (Q4 a).
- **`session task remove`:** refuses a `bound` task exactly as it refuses a `claimed` one, and points to `session hide --task` (Q6 a). `sessions.task_id` stays set either way, so allowing it would leave the list out of step with ownership.
- **`touch` / `session task add`:** touching your own bound task leaves it `bound`, reported rather than raised as an error, as it already does for `claimed`.

## 5. Claim's no-plan refusal points to bind (Q8 a)
- In `_require_spawnable`, when the verb is `claim` and there is no plan, add a conditional route: "If you and your user are working out the plan together now: `endless task bind <id>`. Claim once the plan is attached." Do not add it for `spawn` or `prime`.
- Update the `task bind` section of `endless guide tasks` with the same wording, and update `task bind`'s help and docstring. Bind's documented use is no longer only already-shipped tasks.

## 6. Writes before a plan stay refused (Q7 a)
- No new rule. A bound session passes `sessionMayWrite`, but bind creates no worktree, and the worktree gate (`enforceWorktreeGate`) refuses edits in main.
- Add a hook test that pins this: a session bound to an unplanned task, with no worktree, has its Write in main refused.

## Tests
- Go:
  - the relation enum, rank and seed integrity
  - the `task.bound` executor writes `sessions.task_id` and `bound`
  - a claim after a bind upgrades the row to `claimed`
  - binding a touched task upgrades it to `bound`
  - `touch` and `session task add` both write `touched`, and a replayed `session_tasks.touched` event rebuilds as `touched`
  - the cwd auto-bind writes `claimed` for an `underway` task and `bound` for a pre-work one
  - `session status` shows a bound task only as the anchor
  - `session task remove` refuses a bound task
  - the hook test in section 6
  - the slug rename (`filed`) passes `VerifyIntegrity`
  - the backfill writes `claimed` or `bound` per the status rule and leaves stronger rows alone
- Python:
  - `task bind` emits `task.bound`
  - claim's no-plan refusal includes the bind line, and spawn's does not
  - `task show` prints `Sessions:` and no longer prints `Touched by:`

## 7. Backfill existing ownership rows
- Today 563 of the 574 sessions that hold a `sessions.task_id` have no `claimed` row for it, mostly spawned sessions, through the auto-bind gap.
- A migration upserts one row per session that holds a `sessions.task_id`, using the same status rule as the auto-bind: `claimed` if the task has reached `underway` or later, `bound` if it is still pre-work.
- It is upgrade-only through the normal ladder, so an existing stronger row is untouched. Upgrading `revisited` or `filed` to `claimed`/`bound` is the point.
- The migration has to be safe to run again: a rebuild from the ledger must reach the same rows. Either the backfill is derived at projection time, or the migration runs after replay. Decide while implementing, and say which in the commit.
