## Naming

`unsettled`, not `unlanded`. ED-1540 defines unsettled as the union (modified OR
unlanded); `unlanded` names only one of the two sub-states and would be actively
wrong for a worktree that is unsettled purely because of uncommitted changes.
Under `task` (4 chars) as requested, and it pairs with the existing `task landed`.

## The predicate already exists -- explain it, don't reimplement it

`taskWorktreeDirty` (internal/monitor/reap_worktrees.go:334) is what drives the ◆:

  1. `git -C <wt> status --porcelain` non-empty            -> modified
  2. else `git -C <wt> rev-list main..HEAD --count` > 0    -> unlanded
  3. no worktree, or any git error                         -> settled (fail-open)

It considers neither remote state nor the `task_landings` rows. The new command
must report these exact signals so the marker and its explanation cannot
disagree. Build it as a diagnostic over that predicate, not a second copy.

`AnnotateSessionStatusDirty` (reap_worktrees.go:316) is the fan-out; the reaper
inverts the same two signals at reap_worktrees.go:188-207 ("never reap unsettled").

## Reuse

- `internal/monitor/worktree_anomalies.go` -- finer-grained probes
  (`WorktreeAnomaliesAt`, line 91) already split user dirt from auto-managed via
  `AutoManagedStatusGlobs` (line 37). Read `.endless/plans/E-1758.md` before
  touching the anomaly core.
- `src/endless/worktree_cmd.py:637` `_git_status_partition` -- returns
  (auto_commit_files, user_work_files); the Python mirror of the same split.
- `src/endless/worktree_cmd.py:783` `_guard_dirty_worktree` -- land-time refusal
  with separate messages for auto-managed vs user dirt; same message shapes apply.

## Where the code goes

- Register in `src/endless/cli.py` immediately after `task_landed` (~line 1393),
  same decorator set (`item_id` optional TASK_ID, --project/--all/--limit/--llm/
  --json) and the same lazy `from endless.task_cmd import ...` inside the body.
- Implement `unsettled_list` / `unsettled_item` in `src/endless/task_cmd.py`
  alongside `landed_list` (line 1551) / `landed_item` (line 1635); mirror its
  three output modes (json / llm / human table).

## Sequencing risk

E-1825 ("Replace union-sense 'dirty' with 'unsettled'") is `unverified` and
renames exactly these identifiers across exactly these files. Either sequence
this after E-1825 lands or expect to touch the same symbols.
