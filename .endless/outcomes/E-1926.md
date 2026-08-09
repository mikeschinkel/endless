# E-1926 — What FK-free task-keyed rows do when a task id is freed and reused

## The answer

They stay attached to the id, and the id is never freed.

The question as filed assumed the id would be reused and asked what the four
tables should do about it. The requester rejected the premise outright: id reuse
is the defect, not the condition to design around. Once an id is spent it stays
spent, so a row that outlives its task points at a retired number forever —
which is exactly what the four schema comments ("the row must outlive its task")
always claimed would happen. Non-reuse does not paper over the bug; it makes
those comments true instead of aspirational.

This dissolves the per-table design call the task was filed to make. There is no
policy to choose for `session_tasks` vs `session_hidden_tasks` vs
`session_notices` vs `project_next_tasks`/`_pending`. There is one id-lifecycle
fix and a read-path audit behind it.

Recorded as **ED-1547** (no reuse, removal keeps the row) and **ED-1548** (no
universal cross-entity id).

## What was decided

**No id that anything else refers to is ever reused.** Dispensed means spent,
used or not. Gaps are legal and permanent, and nothing may assume ids are dense
or contiguous.

"Refers to" means stored in another table — logically, not only where SQLite
declares a `FOREIGN KEY` — or handled by a human or agent: typed, or written
into a plan, commit subject, branch name, or path. That covers tasks, sessions
and decisions. An id that is a purely internal row handle, a surrogate primary
key nothing else stores and nobody types, may be reused freely, so junction and
child tables keep unrestricted hard deletes.

The rule was first drafted as a flat "no entity id is ever reused" and narrowed
on review. The flat version overreached: it would have bound tables whose ids
nothing holds, for no benefit. Note the declared-constraint test alone would be
insufficient — `session_tasks.task_id` has no `FOREIGN KEY` clause and still
refers to a task, and that reference is the entire bug surface this task was
filed about.

**Removal keeps the row and sets `removed=1`.** `task remove` stops issuing a
DELETE. Retaining the row makes re-minting impossible by construction rather
than by convention.

A separate spent-ids table was considered and rejected: a second table holding
the ids that another table needs has too much potential to drift from it.
Keeping the row keeps the fact in one place.

**Removed rows never render.** This is the uniform rule for all four tables and
everywhere else, and it is where the actual work is. The reads in
`session_status.go` (lines 184, 349, 480) inner-join `tasks`, so today a row
whose task was deleted silently drops out — correct behavior, obtained by
accident. Retaining the row makes those joins start matching, so the fix
*introduces* a leak unless every read path filters on `removed`. That filter is
the ongoing cost of this decision and it is re-payable by every query written
from here on.

**Pending `session_notices` are dropped, not filtered.** An undelivered notice
about a removed task is dead mail. Filtering it on read leaves it in the queue
permanently; deleting it is the honest disposal. Scope is rows with
`notified = 0` only. This is not an exception to the id rule — a notice's own id
is an internal row handle nobody holds, so deleting the row is unrestricted.

**An id is spent by the ledger, not by the table.** The SQLite DB is a
projection of `.endless/db-ledger/*.jsonl`; the `removed=1` row is the
projection's materialization of a fact the ledger already holds permanently, in
the create and remove events. The rebuild must replay removal as a retained
`removed=1` row. If it ever replayed removal as a real delete, a rebuild would
re-free the id and reuse would return on a path nobody would think to test. This
holds only while ledger compaction stays out of scope — ruled out for the
foreseeable future, since dropping create events would require finding and
fixing every reference to those ids in all content.

## What was rejected

**A universal id space** shared by tasks, sessions and decisions, such that
`12345` is unambiguous and `E-12345` precludes `ES-12345`. Raised by the
requester and rejected on three grounds: pluggable ID providers cannot honor it
(JIRA namespaces per project; GitHub shares one sequence between issues and PRs
and nothing else), so the guarantee would evaporate exactly where the E-800 /
E-1831 roadmap leads; sessions are the highest-volume entity and would inflate
the short-id space that both task and session ids are typed from by hand; and
ids are already embedded in immutable artifacts — commit subjects, branch names,
worktree and sandbox paths, and the prose of every plan and ledger entry — so
renumbering would make history lie while not renumbering would leave a permanent
split regime. Full rationale in ED-1548.

**Splitting storage identity from display identity** — an internal never-reused
integer for joins, with the human-facing id a separate, possibly delegated
concern. Proposed during the session and rejected by the requester with the
decisive argument: agents write ids into plans, ledger entries and commit
messages constantly, so the id in prose *is* the identity. On a team, a
locally-minted `E-1926` names one developer's task and not their teammate's, and
no amount of internal cleanliness fixes that. This is a real requirement, it is
about cross-*developer* uniqueness rather than cross-*entity* uniqueness, and it
belongs to E-1831 — not here.

**Block allocation as an availability hedge** for a remote ID provider. Raised
and dismissed as YAGNI: Endless is open source, a team's provider will almost
certainly be a service they control (possibly on their own LAN), and the
failure mode is not worth designing against before it appears.

## What went upstream to E-1831

E-1831 ("Choose a collision-free distributed task-ID scheme") is the home for
distributed ids and is still `submitted`, not answered. Three things from this
session were written into its plan text rather than duplicated here:

**A fourth candidate — git-as-CAS dispenser.** The requester's idea: to mint an
id, read the id file from a dedicated repo or Gist, increment, commit, push. A
successful push *is* the allocation; a rejected non-fast-forward push means
someone else took that number, so pull, take the next, retry. Compare-and-swap
with the git host providing serialization. Zero new infrastructure, availability
equal to the git host the team already cannot work without, ids stay short bare
integers, and it is self-hostable end to end. Same shape as candidate #3 from
the caller's side — both are an `allocate()` behind the provider interface — so
it is arguably the reference implementation of #3 that ships without a server.

**Two constraints every candidate must satisfy:** no scheme may permit
re-minting a spent id, and any scheme minting from local state must mint against
the ledger high-water mark rather than `MAX(id)` over live rows.

**A note that cross-entity unification is settled and rejected**, so E-1831 does
not reopen it.

## Follow-ups filed

Filed as four, merged to two on review. If each task reliably spawns more than
one, the backlog cannot converge, so the split has to earn itself.

- **E-1929** — Replace task, session and decision deletion with a `removed` flag
  filtered from every read path. Covers the schema change for all three covered
  entities, the read-path audit, the pending-notice drop, and the
  projector/rebuild check. Absorbed **E-1930** (extending the policy beyond
  tasks, which the narrowed rule shrank to three known entities) and **E-1931**
  (dropping pending notices, a few lines inside the same removal path). Both
  closed `obsolete`.
- **E-1932** — Purge rows already resurrected against a reused id. The
  policy is prospective, so pre-existing damage stays wrong; it is detectable
  because all four tables carry a timestamp (`created_at`, `hidden_at`,
  `changed_at`, `added_at`), and a row predating `tasks.created_at` of the id's
  current occupant provably belongs to a previous occupant. Kept separate from
  E-1929 because folding a one-shot data repair into a behavior change makes one
  task that can half-fail.

## Measured, not inferred

Carried forward from the task's analysis: after `endless task remove 8001` in an
isolated DB, `session_tasks` held 2 rows for 8001, `session_hidden_tasks` 1,
`session_notices` 1, `project_next_tasks` 1. For contrast `task_landings` (FK ON
DELETE CASCADE), `sessions.active_task_id` and `session_statuses.active_task_id`
(FK ON DELETE SET NULL) all cleared correctly, and `session_task_relations` is
the relation-kind vocabulary table, not task-keyed at all.

Confirmed this session: all four tables carry a timestamp column, which is what
makes E-1932's detector possible; `session_notices.notified` is the pending flag
that scopes E-1931; and `project_next_tasks.task_id` is TEXT where the other
three are INTEGER, so any cross-table sweep needs a cast.
