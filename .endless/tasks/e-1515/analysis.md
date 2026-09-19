Recurred 2026-08-20 landing E-2011, which renamed endless.project_path.normalize
to resolved: main advanced, `_record_landing`'s lazy `from endless.event_bridge
import emit_event` loaded post-merge code against the pre-merge
endless.project_path still in sys.modules, and the land died with `cannot import
name 'resolved' from 'endless.project_path'`. No task_landings row; re-running
`just land` recorded it. Same function, same import, same mechanism as the
2026-05-29 occurrence on E-1510. Filed again as E-2013 before this task was
found; E-2013 is declined as a duplicate and carries a plan whose design is the
in-product fork this task's sibling E-1516 objects to.

Two things have changed since this was filed, and both bear on the chosen remedy.

1. It now fires on the CANONICAL path, not the bare one. The `just land` recipe
   makes a single `endless worktree land` call — E-1941 deliberately moved the
   schema-apply step OUT of the Justfile and INTO the command, so that the apply
   happens after the ff-merge. So the single-process path this task treats as
   "the canonical path fixed by Justfile orchestration" is the only path there
   is, and E-1516's stated promotion trigger ("if the bare path becomes more
   commonly used") has effectively fired.

2. The `--no-record` / `--record-only` split under-covers. It isolates Step 6
   only. Everything E-1941 and E-1799/E-1800 have since moved after the ff-merge
   runs in that same exposed process: `_apply_branch_schema_changes` (5.5),
   `_run_post_land_script`, `_check_post_land_residue`, `_reap_stale_worktrees`.
   Whichever of them imports a module for the first time after the merge is
   equally exposed, and which one that is depends on what the process happened to
   touch earlier — so the failure is order-dependent and will not reproduce.
   The loud case is an ImportError; the quiet case is a newly-imported module
   reading an already-cached module's changed constant, which raises nothing.

Consequence for the decision this task is blocked on: the Justfile-is-the-dev-only
-layer escape hatch got narrower when E-1941 moved work inward. Isolating the
whole post-merge tail — not just the recording — is what the fix has to do,
whether it is done by the recipe or inside the command.
