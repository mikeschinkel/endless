A session that claims a task while running in the project's main checkout (no `.endless/worktrees/e-NNNN`, no `task/NNNN` branch) cannot be resumed by `endless session resume`.

Observed on go-cliutil E-2289 (session ES-1316, transcript cwd = project root on `main` for every entry, task status `unplanned`, never landed):

- `endless session resume E-2289 --rebind` → "E-2289's worktree is gone (it was dropped after landing)… retry with --review / --reopen".
- `--tmux-session NAME` / `--all-tmux-sessions` make the same inference and type `session resume E-2289 --rebind --review` into the window.
- `endless session resume E-2289 --rebind --review` → "E-2289 never landed and its branch task/2289 is gone, so there is no base commit to rebuild from."

The two messages contradict each other: the missing-worktree branch assumes "landed" without checking, and the --review branch then proves it never landed. Neither handles "never had a worktree; the session ran in the project root", where the correct resume is simply cd to the root and `claude --resume <uuid>`.

Also: `--dry-run --review=<ref>` created a real worktree (`.endless/worktrees/e-2289`, detached) despite --dry-run. The help says dry-run "resolves (and creates)" what the resume needs, so this is documented, but surprising.

Workaround used: `cd <project root> && claude --resume <uuid>`.
