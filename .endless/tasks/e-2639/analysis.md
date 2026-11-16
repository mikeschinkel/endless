# Where Endless stores commit SHAs today

Survey of the endless source (paths relative to the endless repo). "Fails
closed" means a missing commit makes Endless refuse or recompute rather than
give a wrong answer.

| Stored SHA | Used in a git operation? | After a rewrite |
|---|---|---|
| `task_landings.merge_commit_sha`; ledger `task.landed` payload | Yes: `session resume --review/--reopen` passes it unchecked to `git worktree add` (`LandedSHA` in `internal/monitor/resume.go`, then the `.landed` base in `src/endless/session_cmd.py`) | Resume fails until a base is passed by hand; `task landed` / `task show` display dead SHAs |
| `tasks.verified_sha`; ledger `task.status_changed` payload | Yes: `git diff --quiet <sha> task/<id>` (`PassIsStale` in `internal/worktreecmd/verify_state.go`) | Fails closed: the task drops to `unverified` and needs a new verify |
| `task_reconciliations.head` | Yes, informational: files changed since the last reconcile (`internal/sessionquerycmd/plan_items.go`) | Shows "could not list changed files"; the land gate uses a patch-id fingerprint, not this |
| `task_post_land_entries.head` | No (never read) | Harmless |
| Verify report names `verify-<ts>-<sha>.ctrf.json` | No | Label goes stale |
| Project identity = root commit SHA, in `.endless/config.json` and `projects.identity` | No, once stored | Harmless while stored; a project registered without a stored identity would re-derive a new one |
| `session_statuses.commits` | No | Display only |
| Per-clone caches in `.git/info/endless/` | Yes, with an ancestry check | Rebuild themselves |

Other facts that bear on the questions:

- Ledger entries are not hash-chained and carry no commit SHA in their
  envelope; SHAs appear only inside payloads. Voids and corrections address a
  ledger line by segment name and byte offset (`internal/events/void.go`).
- The database cannot yet be rebuilt from the ledger: `event rebuild-db
  --confirm` is refused until E-799 lands; only `event validate-db` compares.
- `endless correct` fixes one row at a time and covers `verified_sha`,
  `merge_commit_sha` and reconciliation `head`, not post-land `head`. There is
  no bulk remap.
- Endless decides which main commit it may amend by subject and paths, then
  by live ref checks, not by a stored SHA, so amending keeps working.
- `git filter-repo` writes an old→new commit map to `.git/filter-repo/commit-map`
  and by default rewrites abbreviated SHAs inside commit messages, but not
  inside file contents (the ledger `.jsonl`, `config.json`, report names).
- A task branch forked before a rewrite and not rewritten with it can bring the
  removed file back when it lands, because its commits no longer match main's.
- Unconfirmed: a worktree rebuilt by `session resume --reopen` may record the
  landed SHA as `base_branch` in `.endless/worktree.json`
  (`_bootstrap_task_worktree` in `src/endless/worktree_cmd.py`); what `worktree land` does with that after
  a rewrite has not been traced.
