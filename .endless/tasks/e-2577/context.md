Mike, landing E-2486 on 2026-11-08: "This is becoming a big time waster. Maybe we need a brainstorm task to determine how to deal with our new process requirements, ESPECIALLY reconciliation that has evolved to become something too complex for a user to do."

What the lifecycle demanded of one task, E-2486, between "the work is done" and "it is on main":

- Reconciliation R1 filed a discrepancy question; answering it changed the code, so R2 was needed before `unverified`.
- Main was merged into the branch to pick up E-2010. The merge commit carried main's ledger commits, which the land refuses, so the branch was rebased. Each head change made the last reconciliation stale, and a stale reconciliation cannot be refreshed at `unverified`: the task had to be reopened through `revisit` and `underway`, reconciled, and set back to `unverified`. That dance ran three times (R3, R4, R5), with identical verdicts each time.
- The land itself rebased the branch onto a main that had moved. When the land then failed on a database lock, the sweep judged the verify pass stale, because the staleness diff excludes only the ledger globs and the rebase had brought in LESSONS.md, another task's items.toml and the CTRF report of this task's own verify run. The status fell back to `unverified`, which made `just land` refuse ("not unlanded") and `task verify` refuse ("code changed since the last reconciliation"), and the only way out was the same reopen-and-reconcile dance again.
- The agent twice handed Mike commands the gates would refuse, because the gate path is long enough that even the agent lost track of it (lesson recorded).

Questions to settle:

- Which of these steps protect something, and which exist only because an earlier step exists? Reconciliation protects the plan's truth; what does re-reconciling identical verdicts after a rebase protect?
- Should a reconciliation be keyed to the tree's content rather than the head commit, so a rebase or an Endless-only commit leaves it current?
- Should the staleness diff for a verify pass, and the reconcile currency check, ignore everything Endless itself commits (mirrors, lessons, CTRF reports), the way it already ignores the ledger? Related: E-2479 asks the refusal to at least say how to recover.
- Should a verified task survive a land's own rebase, or should the land not rebase a branch that is already content-identical to what passed?
- Where is the user in this? Which steps are the user's decisions (answering discrepancies, verifying, landing) and which should be the system's, so that the user runs at most verify and land?
