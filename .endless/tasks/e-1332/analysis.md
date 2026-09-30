Revised approach (not immediate destroy): keep the sandbox in place briefly after the worktree is gone (allows recovery/inspection of dev artifacts mid-stream), then reap on a timer.

Open design item for planning: pick the grace period (24 hours, 7 days, etc.) and the reaper mechanism (periodic job, on-demand 'endless-sandbox prune --aged').
