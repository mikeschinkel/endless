`task update --status` accepts any status from any status, from any actor, regardless of whether a session ever claimed the task.

Observed 2026-08-20: a session holding E-1817 ran `endless task update E-2015 --status unverified` on a task that was `unplanned`, had never been claimed, had no worktree and no session — so `session goto E-2015` found nothing to go to.

The lifecycle is byte-synced across three docs by a test while nothing checks the lifecycle itself.
