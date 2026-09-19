The companion file's worktree_path can go stale between hook events: it's written from active_task_id at hook time, but the underlying worktree directory can be deleted (e.g. 'git worktree remove') with no hook signal. Readers (session use, session cd) then emit a 'cd <gone>' that fails.

Hit during E-1015 verification: ran 'endless task start E-1015' which set companion worktree_path = .endless/worktrees/e-1015. Then landed E-1015 (FF-merged, removed the worktree). Companion still pointed at e-1015. From a side pane, 'esu' eval'd 'cd /.../e-1015' and got 'no such file or directory'. Next UserPromptSubmit refreshed the companion correctly, but the failure had already happened.

E-1033 (refresh-every-UserPromptSubmit) doesn't cover this — side panes read between Claude's turns. Adding more writer refresh points scales poorly (would need a hook on every git worktree operation, every rmdir, etc.).

Fix on the reader side: in _target_path (src/endless/session_cmd.py), validate worktree_path with os.path.isdir(). If it doesn't exist, ignore it and fall back to cwd. One stat per read. Companion file stays as 'intent'; reader treats it as a hint, not a guarantee.

Affects:
- session_cmd._target_path (the single place where worktree-vs-cwd is chosen for cd)
- ENDLESS_WORKTREE_PATH emission in session_use_resolve (currently emits stale path; should emit '' when stale)

Verification:
- Companion has worktree_path pointing at a directory that exists → that path is used.
- Companion has worktree_path pointing at a deleted directory → reader falls back to cwd silently. No error, no warning (the fallback is the right answer).
- Companion has no worktree_path → unchanged behavior.

Origin: E-1015 verification revealed the gap. Manual workaround: send any prompt to current Claude to force a refresh. This task removes the foot-gun entirely.