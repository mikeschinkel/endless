# Medium-priority (a) feature candidates — interview roster

Continues E-1723; consumes E-1722's triage table. One-at-a-time interview; per candidate decide disposition (gate / warn / guide / leave / fold), rough shape, priority; then file or fold (--cleans-up E-1721) and note MEMORY.md prune on land.

## Candidates
- endless-schema-conventions
- global-config-exception + recovery-commits-to-main-ok (commit-on-main gate carve-outs — one surface)
- go-enum-pattern
- no-db-check-constraints
- lead-with-remaining (session status output ordering)
- no-end-of-session-status-recap
- append-only-files-check-line-count (ledger integrity check)
- companion-after-rebase (auto-recreate .endless/worktree.json)
- drive-spawned-sessions-to-closure (spawn prompt template)
- no-just-install-from-worktree
- no-mega-epic-parent (child-count warn)
- per-task-verify-script (scaffold + land-readiness gate)
- remove-worktrees-after-merge (reaper grace period)
- session-id-not-interchangeable
- test-instructions-on-verify (verify-flip guard)

## Partially-shipped residuals
- no-guessed-sqlite3-paths — extend the sqlite block gate to stray-file creation
- no-unauthorized-task-type — policy layer over the existing task_types enum

Scope: endless-scoped; generic multi-project tooling deferred.