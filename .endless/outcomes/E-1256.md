Shipped `endless task bind <id>` as a new task verb.

Symmetric to `endless task release`:
- bind sets sessions.active_task_id without changing tasks.status
- release clears sessions.active_task_id without changing tasks.status

Reuses the existing `task.claimed` event added in E-1242 (initial attempt added a duplicate `task.bound` event; rebase against main revealed E-1242 had landed the same Go infrastructure, so the duplicate was dropped). Difference between claim and bind is purely Python-side: claim ALSO emits `task.status_changed` and creates a worktree.

Target session resolution mirrors claim_item: current Endless session, else sibling Claude pane in same tmux window. Refuses on 0 matches (no session to display for) and 2+ matches (ambiguous).

The claim error message for done-status tasks now mentions `bind` as a third option alongside `--force` and "update the status first" so users discover the right verb from the friction point.

287 tests pass.

Verification deferred to land+install. Exercise on the E-1240 case: from the window's pane, run `endless task bind E-1240` and confirm the second status row starts showing `[E-1240] · endless · ... · assumed`.