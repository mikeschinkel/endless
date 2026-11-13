# Open questions: adopting edits made outside any task

Terms used below. **Stray edits**: changes a user made in a main checkout's working tree that belong to no task. **Holding branch**: a branch that carries stray edits as commits until a task consumes them. **Consuming task**: the task whose spawned session brings those commits into its worktree and lands them. **Branch-and-reset**: committing the stray edits to the holding branch and restoring main's working tree to clean.

Settled already, from Mike's words on 2026-11-13: a worktree is never created without a session behind it, so no worktree is created ahead of a spawn for stray edits. From the code: the holding branch is never the task branch, because worktree creation refuses a pre-existing task branch carrying real work. Removing the unattended claim is its own task.

## Q1. Who performs branch-and-reset?

**A. The user, by hand, following a documented git recipe.**
Pros: the user is the only one who knows which uncommitted changes in main are theirs and which task each belongs to.
Cons: the recipe has several steps that are easy to get wrong, such as sweeping Endless's mirror files into the commit or resetting a path that was not part of the edit; nothing records that it happened.

**B. An Endless verb the user runs.**
Pros: one command; it can exclude `.endless/` mirrors, refuse when main is clean or when the named task already has a worktree, and record a ledger event so the adoption is visible.
Cons: the verb has to handle a partial selection, some files or hunks for one task and the rest for another, which a hand recipe handles by the user's judgment.

**C. An Endless verb that an agent may also run.**
Pros: the planning session that discovers the stray edits can finish the job in the same turn.
Cons: it puts an agent's hands on the main checkout's working tree, which the orchestration guide forbids today ("never in the main checkout's working tree"); deciding which of the user's edits belong to which task is the user's judgment, and an agent guessing it is how the four unattended claims happened.

Recommendation: B.

## Q2. How is the holding branch named?

**A. `wip/E-NNNN`, one per consuming task.**
Pros: the name says which task consumes it, so spawn can find it by convention with no new field.
Cons: the task must be filed before branch-and-reset can run, so clearing main waits on a `task add`; a batch of edits in one repo that spans several tasks needs the user to split the edits up front.

**B. `wip/<slug>`, one per repo per batch; tasks name it in their context.**
Pros: clear main first, file tasks afterwards; one branch per repo.
Cons: no mechanical link from task to branch, so nothing can check it or act on it.

**C. Either: park on `wip/<slug>`, rename to `wip/E-NNNN` once the task exists.**
Pros: no ordering constraint on clearing main.
Cons: a rename step something must perform; two branch shapes to document and detect.

Recommendation: A. If filing first proves too heavy in practice, C.

## Q3. How does the consuming task record what it consumes?

**A. By the naming convention alone: a `wip/E-NNNN` branch exists, so the task consumes it.**
Pros: nothing to type; spawn and the handoff can detect it.
Cons: a branch given that name by mistake is consumed silently; the convention cannot say "only these commits".

**B. In the task's context prose.**
Pros: none beyond needing no change.
Cons: the handoff cannot act on prose, so every spawned session parses it differently.

**C. A structured field or relation on the task, set when branch-and-reset runs.**
Pros: explicit and checkable; `task show` can print it; it can name a commit range, not just a branch.
Cons: a schema and CLI change that duplicates what the branch name already says in the common case.

Recommendation: A, with the handoff stating what it found. C only if a real case needs a commit range.

## Q4. How do the commits enter the task worktree?

**A. Spawn creates the task branch from the holding branch's tip instead of the default branch.**
Pros: no git steps for anyone; authorship and the commit message are preserved.
Cons: the worktree starts from whatever default-branch commit the holding branch was cut from, so it can be stale on day one and any drift surfaces only at land.

**B. Spawn creates the task branch from the default branch as today, then cherry-picks the holding branch's commits onto it before launching.**
Pros: the worktree starts at the current default branch; authorship preserved; drift surfaces at spawn.
Cons: a cherry-pick can conflict during the pre-claim step, when no session exists yet to resolve it, so spawn needs a rule for that case (refuse, or launch and let the session resolve).

**C. The spawned session does it, following the handoff.**
Pros: a session is present to resolve any conflict; no change to spawn.
Cons: every session repeats git steps an automation could do, and each may choose a different method (apply, cherry-pick, merge).

Recommendation: B, with a conflict left for the session: spawn launches, and the handoff names the conflicted cherry-pick as the first thing to resolve.

## Q5. What happens to the holding branch after the task lands?

**A. `worktree land` deletes it when the consuming task lands.**
Pros: nothing left behind.
Cons: if the task consumed only some of the branch's commits, the rest are lost with it.

**B. The reaper deletes it once the default branch holds everything on it, the same test it applies to settled worktrees.**
Pros: never deletes work the default branch lacks.
Cons: the branch lingers until the sweep runs.

**C. The user deletes it.**
Pros: none.
Cons: holding branches accumulate.

Recommendation: B.

## Q6. Must a task exist before stray edits leave main?

**A. Yes: branch-and-reset requires a task id, and the branch is `wip/E-NNNN`.**
Pros: everything Endless carries has a task; there is no parked work that nothing tracks.
Cons: an exploratory edit the user has not decided about cannot be cleared from main without filing a task for it.

**B. No: a `wip/<slug>` branch may hold edits with no task, to be adopted or discarded later.**
Pros: main can be cleared immediately.
Cons: parked branches with no task are invisible to Endless, which is the orphan problem in a new shape.

Recommendation: A, consistent with the rule that a worktree never exists without a session.

## Q7. One small change across several repos becomes several tasks and plans. What reduces that?

**A. Accept it: one task per repo, because each repo lands and releases separately.**
Pros: matches how land and release already work.
Cons: one idea produces several plans that all say the same thing.

**B. One epic in the originating project with per-repo children, the reasoning written once on the epic and the children carrying minimal plans.**
Pros: the reasoning lives in one place.
Cons: depends on a child being allowed a parent in another project, which needs verifying before this is chosen.

**C. A handoff or plan template for "move the user's edit unchanged", so each child task's plan is a few lines.**
Pros: the four tasks of 2026-11-02 all had the same plan; a template removes the typing.
Cons: one more template to keep current.

Recommendation: A plus C now; B if cross-project parenting is supported.

## Decided without asking

- No worktree is ever created without a session behind it (Mike, 2026-11-13).
- The holding branch is never the task branch; worktree creation refuses a task branch carrying real work (from the code).
- Removing `task claim --unattended` is the separate task already filed, not this one.
- Branch-and-reset changes main's working tree only; the default branch itself is still changed only by `worktree land`.
