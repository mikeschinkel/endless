`endless session resume` assumes that a session bound to a task has a
worktree for that task. That held when binding happened only through `task
claim`, which creates the worktree. Since then a session can bind to a task
while still planning, before any claim, so it runs in the project root with no
worktree. `.endless/work/` (worktree OR empty dir) will widen this further.

In `_resolve_resume` (src/endless/session_cmd.py), when the task has no
worktree directory the code falls through to the "dropped after landing"
branch. A bare resume therefore says the worktree is "gone (dropped after
landing)" even though none ever existed. `--reopen` then refuses because there
is no `task/<id>` branch and no landed commit to rebuild from. No flag gets
past it.

Seen on 2026-10-14 when moving session ES-1316 (E-2289, project go-cliutil,
bound but never claimed, cwd the project root) to the Endless tasks server.
All three of these failed: `endless session resume 2289 --rebind`,
`endless session resume 2289` and `endless session resume 2289 --reopen`.
`session show ES-1316` also prints an empty Cwd. The fix should not depend on
it.

Resume should tell "never had a worktree" apart from "worktree dropped after
landing", and resume the former in the directory the session ran in (the
project root for a bound-not-claimed task, or its `.endless/work/` dir once
that exists). The same resolver backs `session goto --resume`.
