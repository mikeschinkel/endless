# Open questions

Terms used throughout. A **reconciliation** is the session's recorded verdict per plan item, stored with the commit it was made at and a **fingerprint**: the `git patch-id` of the branch's own diff against its merge-base with main, `.endless/` excluded. The **gate** refuses `unverified` and `verify` unless the latest reconciliation's fingerprint equals the current one and every discrepancy question is answered. A **pass** is a `task verify` run by a person that succeeded, recorded with the commit it tested; `unlanded` means a pass stands at the branch's current code. The **staleness diff** is `git diff <pass commit> <branch>` excluding only the ledger and verb files; when it is non-empty the sweep moves the task from `unlanded` back to `unverified`.

Facts these questions rest on, checked on 2026-11-08:

- The fingerprint is already content-keyed, but not the way its comment claims. `git patch-id` hashes the hunks including their context lines, so a rebase onto a main that changed lines near the branch's edits changes the fingerprint with no change to the branch's own work. Reproduced in a three-commit repository: the id changed after a clean rebase. That is why E-2486 needed R3, R4 and R5 with identical verdicts.
- The staleness diff compares two trees. After a rebase the branch's tree holds everything main gained, so any file main changed outside the ledger globs reads as a change to the branch. In E-2486 those were LESSONS.md, another task's items.toml and this task's own CTRF report.
- `worktree land` always rebases the branch onto main (Step 4), by design: "rather than gating on a proxy for staleness, the land removes the staleness" (E-1941). A land that then fails leaves the branch rebased, and the next sweep applies the staleness diff to it.
- E-2479, now underway, lets `task reconcile` be re-recorded at `unverified` when the fingerprint is stale, and makes the gate's refusal say the recovery. It does not change what makes a reconciliation stale.
- The actors are fixed in the transition table: reconciling and setting `unverified` are the session's; verifying and landing are the user's; `unverified → revisit` is the user's.

## Q1. What should make a reconciliation stale?

A reconciliation records that the work matched the plan at some code. The question is which later changes to the code should void that record.

- **A. The branch's own diff, hashed without context lines.** Compute the fingerprint from the added and removed lines only, so a rebase that changes surrounding lines leaves it alone and an edit to the branch's work changes it.
  Pros: a rebase, clean or with context drift, never re-asks for verdicts; an edit still does.
  Cons: a conflict resolved by changing the branch's own lines still changes it, which is correct, but a conflict resolved by keeping main's version of a line the branch had edited removes a line from the diff and so changes it too, even though the agent may regard the verdicts as unchanged; `git patch-id` cannot do this, so it is a small hash routine of Endless's own.
- **B. The set of files the branch touches, plus their content on the branch.** Hash the branch-side blobs of every file in the merge-base diff.
  Pros: simple to state and compute from `git ls-tree`.
  Cons: a rebase that brings main's change into a file the branch also touched changes the blob and so the fingerprint, with no change to the branch's work; this is the common case in a shared file.
- **C. Keep the current fingerprint and rely on E-2479's cheaper re-reconcile.**
  Pros: nothing to build here.
  Cons: identical verdicts are still re-recorded after every rebase that drifts context, which is the cost that opened this task; each re-record is the session's work, so a task whose session has ended cannot be landed after a rebase without a new claim.

Recommendation: A.

## Q2. What should the staleness diff for a pass ignore?

- **A. Compare the branch's own diff against main at the pass and now**, the same measure Q1 picks for reconciliations, rather than the two trees.
  Pros: a rebase, including the land's own, leaves a pass standing; only a change to the branch's work voids it; one definition of "the code changed" serves both the reconciliation and the pass.
  Cons: a pass taken before a rebase did not run against main's newer code, so a build break or test failure that only appears on the rebased tree is caught by the land's own rebuild (Step 4.2) and not by the pass.
- **B. Keep the tree comparison and widen the exclusion to the whole `.endless/` directory.**
  Pros: the three files that bit E-2486 were all under `.endless/`; the land already treats that directory as Endless's bookkeeping when it counts unlanded commits.
  Cons: a rebase that brings in any change to project source outside `.endless/` still voids the pass, so a pass rarely survives a land on a busy main; the exclusion also hides a real edit to a verify suite under `.endless/tasks/`.
- **C. Keep the tree comparison and the current globs.**
  Pros: none.
  Cons: every land on a moved main voids the pass it depends on if the land fails after Step 4; E-2486's third round of the dance came from exactly this.

Recommendation: A, with B's `.endless/` exclusion applied to the pass as well, so a mirror or CTRF commit on the branch never counts.

## Q3. Should a land that fails after its rebase leave the branch rebased?

- **A. Yes, as today.** The rebase is real progress; the next attempt starts from it.
  Pros: a second land has less to do, and a conflict is met once.
  Cons: with the current staleness diff the rebased branch voids the pass, so the user's next command is refused; this con disappears if Q2 takes A.
- **B. Restore the pre-rebase tip when a later step fails.** The land already records the pre-rebase tip for its abort path.
  Pros: a failed land leaves the branch byte-identical to what passed, whatever Q2 decides.
  Cons: a failure after Step 5's fast-forward cannot be undone this way, because main already holds the rebased commits, so the restore applies only to failures between Step 4 and Step 5; a conflict the user resolved during the rebase is thrown away.

Recommendation: A, conditional on Q2 taking A; otherwise B.

## Q4. Which steps are the user's, and which should the system do unasked?

Today the user runs `verify` and `land`, answers discrepancy questions, and reopens a task with `revisit`. The session reconciles and sets `unverified`. The question is whether anything else should move on its own.

- **A. Nothing more moves on its own.** The user runs verify and land; every other transition stays the session's, prompted by the gate's refusal text (E-2479).
  Pros: every status change is traceable to a person or a session that chose it.
  Cons: a task whose session has ended, and whose reconciliation is made stale by whatever Q1 leaves in, cannot proceed until someone claims it.
- **B. The sweep re-records a reconciliation itself when the fingerprint changed but the verdicts cannot have:** that is, when the branch's own diff without context lines is unchanged (Q1 A's measure) and only the recorded fingerprint differs. The new record copies the last verdicts and names the reason.
  Pros: the dance vanishes even for an ended session.
  Cons: if Q1 takes A there is no such case left, since the fingerprint would not have changed; if Q1 takes B or C this writes verdicts no agent judged, under the session's name or a system actor, into a record whose purpose is judged verdicts.
- **C. A successful `verify` by the user counts as reconciliation when the gate's only complaint is a stale fingerprint.**
  Pros: the user's two commands are enough on their own, always.
  Cons: a pass proves the suite, not the plan; an item the agent would now mark `differently` is recorded `as_planned` by a person who never read the items.

Recommendation: A, with Q1 A removing the case B and C exist for.

## Q5. Should `unverified` be reached by the session, or by the gate passing?

Today the session runs `task update --status unverified` after reconciling, and the gate checks that call.

- **A. Keep the explicit call.**
  Pros: the session states that the work is done, as distinct from the record being complete.
  Cons: one more command whose refusals the agent has to learn.
- **B. A recorded reconciliation with every item settled moves the task to `unverified` itself**, as a post-land reconcile that leaves nothing open already moves `landed` to `assumed`.
  Pros: one fewer step and one fewer refusal; the same rule already exists for the post-land case.
  Cons: a session that reconciles early, to see what questions it would file, moves the task to `unverified` before it meant to, unless the move waits for a flag.

Recommendation: B, with the move taken only when the reconciliation files no discrepancy.

## Decided without asking

- E-2479's re-reconcile at `unverified` stands whatever Q1 decides; it covers the cases Q1 leaves.
- The measure chosen in Q1 is computed in one Go function and used by the gate, the staleness diff (if Q2 takes A) and `task show`'s landedness, so "the code changed" means one thing.
- The brainstorm's outcome is filed as accepted decisions, one per question answered, and as todo tasks for the changes, each citing the decision.
