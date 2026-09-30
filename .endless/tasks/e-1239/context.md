Post-E-1218, .endless/worktree.json is gitignored and not tracked in main.

New worktrees created via 'git worktree add' directly (vs. 'endless task spawn' or 'endless worktree add') do not get a companion file written.

Workaround: write the companion manually before running task start.

Surfaced during E-1209 and E-1219 setup — hit it twice in one session.
