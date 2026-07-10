RECOMMENDATION: REMOVE the named-alternate worktree capability; collapse to one
canonical worktree+sandbox per task (e-<id>). Model multi-checkout needs (A/B,
run-while-edit, bisect/snapshot-compare, ad-hoc testing) as CHILD TASKS.

(Supersedes the initial "warn" recommendation. Warn assumed the slug-tolerance
was load-bearing forward-compat plumbing; Mike confirmed the named-alternate was
a capability he has never used and is willing to drop, which makes full removal
the cleaner answer. Retracting "warn".)

=== What the capability actually is ===
There is NO command that creates a named alternate. `create_task_worktree`
(src/endless/worktree_cmd.py:1055-1121) has only ever produced bare `e-<id>`
(single commit in its history; no --slug/--name/--alt ever existed). What exists
is deliberate SLUG-TOLERANCE in the readers, making a hand-created
`git worktree add .endless/worktrees/e-<id>-<slug>` a *supported convention*:
  - internal/monitor/worktree.go:15-54  (globs e-<id> AND e-<id>-*, picks
    lexicographically-first on multiple matches)
  - path regexes accepting optional -slug: src/endless/worktree_cmd.py:183-185,
    internal/monitor/worktree_lock.go:213-236
  - sandbox naming follows the worktree basename 1-to-1:
    src/endless/config.py:203-228
  - comment: internal/monitor/db.go:187 ("e-NNN[-slug]")

=== Why remove (not keep, not warn) ===
- Unused for the entire life of the feature (Mike: never needed it). YAGNI.
- It is a TRAP, not dormant optionality: the recognition is half-wired, so it
  cannot be used safely today —
    * reaper regex is anchored e-(\d+)$ (reap_worktrees.go:55-57) -> a named alt
      is invisible to cleanup and leaks forever;
    * land/drop/for-task silently pick the lexicographically-first match -> can
      target the WRONG checkout (orphan branches, data loss).
- It directly fights the one-worktree-per-task invariant Mike now wants; every
  -slug-tolerant reader is a site that must reason about multiplicity.
- The legitimate need is served BETTER by child tasks: a child gets its own
  clean e-<childid> worktree+sandbox+lock+land+reap and shows in the ledger,
  instead of an untracked sibling dir.
- The invariant is what makes GATES workable: a gate is only robust when the
  thing it guards (the task's worktree) is single-valued.
- Re-adding later (if a concrete need surfaces) is cheap and would be done RIGHT
  (creation command + reaper + land/drop disambiguation), unlike today's
  half-build, so keeping the buggy version buys ~nothing.

=== Precondition (confirmed) ===
No `e-<id>-slug` worktree dirs exist in the wild (Mike checked). So dropping
recognition strands nothing.

=== Code touch-points for the removal (follow-on task) ===
- internal/monitor/worktree.go:15-54 -- drop the e-<id>-* glob; resolve only
  bare e-<id> (collapses to a single Stat).
- src/endless/worktree_cmd.py:183-185 -- drop optional -slug from the path regex;
  remove any "pick first of many" land/drop logic.
- internal/monitor/worktree_lock.go:213-236 -- drop optional -slug from the
  path regex.
- src/endless/config.py:203-228 -- collapse sandbox naming to e-<id>;
  worktree_task_id and worktree_dir_name become equivalent.
- internal/monitor/reap_worktrees.go:55-57 -- regex already anchored e-(\d+)$;
  now consistent (update/remove the now-moot slug comment).
- internal/monitor/db.go:187 -- comment "e-NNN[-slug]" -> "e-NNN".
- Docs: CLAUDE.md ("~/.cache/.../e-NNN[-slug]/", "endless-sandbox destroy
  e-NNN[-slug]") -> e-NNN; `endless guide orchestration` -- document the
  child-task model for multi-checkout needs.
- Tests: update any asserting slug-tolerance.

A non-canonical hand-created dir is then simply NOT recognized as the task's
worktree (inert/foreign) -- we do NOT add an active block on raw `git worktree
add`; create_task_worktree already raises on a foreign path collision.

=== Migration note ===
NONE. No named-alternate dirs exist; single user, no shipped product. The work
is subtractive (remove slug-tolerance) plus a docs pass -- zero user-facing
migration.
