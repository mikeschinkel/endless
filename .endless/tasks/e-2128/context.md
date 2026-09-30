The exact 'does this branch hold work the base lacks?' probe (git range-diff, measured 584ms per worktree) is recomputed by every consumer on every tick: the reaper on five hook branches including PreToolUse and PostToolUse, and every session monitor pane re-probing every rendered row every two seconds.

Restores agreement between the reaper, the diamond marker and 'task unsettled' about what landed, which the E-2087 hotfix broke on purpose, and closes the reclamation leak it accepted.
