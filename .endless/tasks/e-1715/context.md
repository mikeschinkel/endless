The E-1710 audit found E-1209 landed on main (commit 6671bca9, an ancestor of main) yet has no task_landings row, because task.landed event recording (E-1392) postdates it; only 3 sub-1209 tasks ever got backfilled landing rows.

This masks whether old tasks actually landed (E-1223/1224 were filed to verify an already-shipped E-1209).

Goal: stop guessing whether a task landed.
