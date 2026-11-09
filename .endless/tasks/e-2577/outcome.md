# What the lifecycle may demand of a user

## The rule (ED-1657)

Between "the work is done" and "it is on main", a person is asked three things
only: answer a discrepancy the session filed, say the work is acceptable
(verify), and say it goes on main (land). Nothing re-asks for any of them
unless the thing the person judged has changed. Everything else is Endless's
bookkeeping, and bookkeeping never moves a status backward.

## Why E-2486 broke the rule

Each safeguard protects something real, but four of them measured "the code
changed" in different ways, and each fired on events that changed nothing it
protects:

- The reconciliation fingerprint (`git patch-id`) hashes context lines, so a
  rebase onto a main with nearby edits staled identical verdicts.
- The sweep's staleness diff compared whole trees, so anything main gained,
  including Endless's own files, read as a change to the branch.
- `unlanded` was a stored fact, so a sweep had to keep it true, and it demoted
  the task after a failed land.
- Verify tested the worktree as it sat, not rebased onto main, so a pass could
  stand on a tree that fails once landed. The land's Step 4.2 only compiles
  endless-go, and only in Endless itself, so nothing else covered the gap.

## What was settled

1. **Verify rebases onto main before it runs.** The pass is taken on the tree
   that will land. (Mike's proposal, replacing a land-time suite run that would
   have tested the same code a third time.)
2. **Land refuses and names verify when main has moved since the pass.** It
   does not run the suite itself. In the serial case nothing re-runs.
3. **`unlanded` is computed, not stored.** Asked which design to choose with no
   legacy, Mike chose deriving it: a status records what an actor did, and a
   comparison needs no sweep if nobody stores it. The sweep's demotion goes.
4. **"The branch's own work changed" has one measure:** its added and removed
   lines, context and `.endless/` excluded. The reconciliation gate and the
   pass check both use it, so rebases and Endless's commits never stale a
   verdict.
5. **A rebase conflict in a background verify becomes session work.** Verify
   writes no pass, moves no status, and tells the owning session through the
   error-triage job's existing routes: a throwaway hook-disabled `claude -p`
   sender calling SendMessage for a live session, a background resume for an
   ended one. Those routes are pulled out of `internal/triagejob` and shared.
6. **The reconciliation gate stays at `unverified`.** With the measure fixed,
   it fires only when a session changed code after reconciling, which is the
   session's to fix.

## Left open

- A user hand-edits the branch after its session ended, so the reconciliation
  is stale with no session to refresh it. Lean: the land refuses and says to
  claim a session, rather than accepting a record nobody judged. Not decided.
- Whether a background job delivering into a session should first check that
  the session is idle is already handled by the triage routes; nothing new.

## Follow-ups (minimized per ED-1550, grouped under epic E-2261)

- **E-2263** (folded in): verify rebases onto main first; a conflict is
  delivered to the session through the shared triage routes.
- **E-2583** (new): one context-free measure of the branch's own work, used
  by the reconciliation gate and the pass check; compute whether a pass stands;
  remove `unlanded` as a stored status and the sweep's demotion; land refuses
  naming verify. (E-2584 was filed separately, then folded in and obsoleted.)
- **E-2582** is obsoleted by this design (verify's rebase makes the land's
  rebase a no-op); retiring it is Mike's call.
- E-2479 stands as is; it covers real changes to the branch's lines.
