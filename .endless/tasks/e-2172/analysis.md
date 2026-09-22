# The gap E-1881 cannot close

E-1881 syncs endless-managed commits up and rebases the worktree down. Its hard
constraint rules out the entire population this task is about:

> Only worktrees with NO live session and no process holding cwd inside, via the
> same `WorktreeInUse` predicate the reaper shares with the other destructive
> path (E-1947). A rebase rewrites history underneath whoever is sitting in the
> directory. This is not a tunable.

That is correct and must not be relaxed. Its consequence is that a worktree with
an agent working in it can NEVER be rebased by the job — and a worktree with an
agent working in it is precisely the one drifting fastest, because it is the one
in use. The job reaches every worktree except the ones that need it most.

So the only actor who can rebase a live worktree is the agent inside it (or the
user). Today nothing asks it to, nothing tells it how far behind it is, and
nothing makes the cost visible until something breaks.

# Measured consequence, from E-1969's session (2026-09-22)

That session's worktree sat ~200 commits behind main for its whole life. Two
concrete costs, neither of which announced itself as staleness:

- **It could not read the code it was reasoning about.** Investigating a
  `doc-mirrors` job failure meant reading `internal/events/commit.go`, whose
  relevant half (`stageablePaths`, `CommitDoc`, the E-2137 rewrite) landed after
  the worktree's HEAD. The worktree's copy was silently a different program.
- **The workaround became a habit that then leaked.** Reading main's path
  directly is the correct move when the worktree is stale, but it is
  indistinguishable from reaching for main's path out of habit, and the two got
  mixed. A worktree that tracked main would have made the question moot.

E-2152 records the same class one layer down: "89 Python tests failed in a
worktree after a rebase because its binary predated the rebase", and nineteen
monitor processes running a landed fix that appeared not to work. Both invert
trust — correct code appears broken, so the search goes to the code rather than
to what is executing it. Staleness is expensive precisely because it is silent.

# What has to be decided

The shapes differ in kind, not degree, and the right answer may combine them:

1. **Tell the agent.** A staleness line in the per-turn hook context, the way
   the active-task line already ships. Cheapest, and it is information the agent
   currently has no way to obtain without asking git.
2. **Give the agent a verb.** A `worktree sync` (or similar) that does E-1881's
   two steps for THIS worktree, from inside it, where the live-session
   constraint does not apply because the live session is the one invoking it.
   Without this, "rebase frequently" means hand-rolled git, which is exactly
   what an agent should not improvise in a worktree holding uncommitted work.
3. **Pick the moment.** Rebasing mid-edit is worse than not rebasing. The
   natural points are a clean tree at session start, after a commit, and before
   a land. A nudge that fires with a dirty tree will be ignored and will teach
   the agent to ignore the next one.
4. **Make it mandatory past a threshold.** A worktree far enough behind is not
   merely inconvenient; its reads are unreliable. Whether that warrants a gate,
   and at what distance, is the sharpest question here — and the E-1669
   precedent ("a warning, NOT a refuse") applies.

# Constraints any answer must respect

- **Never rebase a dirty tree unprompted.** The agent's uncommitted work is the
  thing most easily destroyed, and an auto-rebase that stashes is the shape that
  has caused real damage before.
- **The binary follows the source.** E-2152's measured failure was a rebase
  WITHOUT a rebuild. Whatever ships must either rebuild or say loudly that the
  worktree binary now predates the source.
- **Do not duplicate E-1881.** The up-sync half is its. This task is only about
  the live-worktree case its constraint excludes, and should reuse E-1881's
  algorithm rather than restate it.

# Explicitly out of scope

The agent-habit half of the `cd` observation. That was a session failing to
check its own next command after acknowledging a correction; it is recorded in
LESSONS.md and is not a product defect. Only the staleness that made the
workaround necessary belongs here.
