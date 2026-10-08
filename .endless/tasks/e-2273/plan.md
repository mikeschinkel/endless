Root cause: `canAmend` (internal/events/commit.go) refuses to amend a commit any other ref contains — origin/main included — but it is a check-then-act with no coordination against main-sync's push (internal/mainsyncjob, `git push <remote> refs/heads/main:<mergeRef>`). The check passes before origin/main moves; the push publishes the tip; the amend then rewrites it.

Fix options (pick in review):

A. A shared lock. commitPaths holds a project-scoped lock across canAmend + commit; main-sync takes the same lock across its push and the remote-tracking ref update. An amend can then never straddle a push. Cost: a lock both packages must agree on, and a push that blocks ledger commits for its duration.

B. main-sync never pushes an amendable tip. If HEAD's subject is the ledger subject (or any subject commitPaths amends), push HEAD~ — or skip this run — so the tip that may still be amended stays local until a non-amendable commit lands on top of it. Cost: the newest ledger entry reaches origin one run later.

C. Stop amending once published, re-checked after the commit. After amending, verify the pre-amend SHA is not on the remote-tracking ref; if it is, undo the amend into a new commit. Cost: detects instead of prevents, and still races the push.

Recommendation: B — single-package change, no cross-process lock, and it removes the window by construction; or A if B's one-run delay matters.

Tests: a fake-git or temp-repo test where a push lands between canAmend and the commit (A), or where main-sync is run with an amendable ledger tip and must not publish it (B); plus a regression that a normal non-ledger tip still pushes.



## Decided in review (2026-10-08)

- Option B. "Amendable" means any subject that starts with `Endless: ` (`events.MayAmend`), because every subject commitPaths is given carries that prefix.
- Found while reproducing: the push named `refs/heads/main`, so git resolved the branch again at push time and could publish a ledger commit added after the check. main-sync now resolves the tip once and pushes that SHA. Without this, B alone would not close the race.
- When main's only unpushed commit is amendable, the run reports a new `held back` action and pushes nothing.
