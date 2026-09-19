# Add `--resume` to `session goto` — resume a non-live session in a new window — PLAN

## Context
`endless session goto <ref>` switches tmux focus to a task's or session's LIVE
pane; if none exists it errors ("No live session matches '<ref>' …; run `session
list`"). `endless session resume <ref>` relaunches a lost session in the CURRENT
tmux pane (clobbers it). Neither covers the common case: "take me to session/task
X, and if it isn't live, resume it in a FRESH window and focus it." Today that
means manually finding the session in the DB, creating a tmux window, and running
resume there (observed with `session goto 1748`).

## Change — `src/endless/session_cmd.py`
1. Add a `--resume` flag to `session goto` (the `session_goto` entrypoint, ~L2015).
   When the ref resolves to a known session/task that has NO live pane, instead of
   erroring, resume it in a NEW tmux window and focus it — the manual steps,
   automated. When a live pane exists, `--resume` is a no-op (normal goto).
2. Improve the not-live error (`_resolve_goto_target` ~L1989 / `_goto_session`
   ~L1942): when the ref matches a KNOWN-but-not-live session/task (as opposed to
   an unknown ref), name the `--resume` option in the message, so the user learns
   it exactly when they need it — not just "run `session list`".
3. Reuse, don't duplicate: factor the relaunch mechanics out of `resume_session`
   (~L137, `_resume_target` ~L106) so `goto --resume` can target a NEW window while
   `session resume` keeps targeting the current pane. One resume core, two window
   strategies.

## Scope
- Works for both a task ref and a session ref (the reported case, `1748`, is a task
  id whose session was dead).
- Explicit flag, NOT default: bare `goto` stays side-effect-free (resume spawns a
  process + window); `--resume` opts into that.

## Deliverable — verify script
Generate `tests/tasks/e-<id>-verify.sh` (shape per `tests/tasks/e-1577-verify.sh`).
Scriptable (no tmux/interactive needed): bare `goto` on a known-but-dead session/
task errors with a message NAMING `--resume` (not just "session list"); `goto` on
an unknown ref keeps the old error; the target-resolution helper reports "known but
not live" vs "unknown". MANUAL (interactive/tmux — can't be scripted cleanly):
`goto --resume <dead>` opens a new window, resumes the session, and focuses it;
`goto --resume <live>` behaves like plain `goto`. On completion set `unverified`
and hand off exactly: `esu && ./tests/tasks/e-<id>-verify.sh`, plus the manual
tmux steps.
