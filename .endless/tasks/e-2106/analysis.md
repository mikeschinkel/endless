Endless resolves a resume target out of `sessions.session_id` and hands the UUID
to `claude --resume` without ever checking that the transcript still exists, so
a session whose transcript is gone fails differently at each entry point and
never routes the user anywhere. Found while trying to pick E-1934 back up: its
claiming session ended after thirty seconds and its transcript is absent from
disk and from every Time Machine backup taken after it ran.

(a) `session goto <ref> --resume` appears to do nothing. `_open_resume_window`
passes the `claude --resume` command line to `tmux new-window`, which succeeds
because the window really was created, so Endless reports `(new window)` and
returns a pane id. The claude process inside then exits on the missing
transcript and the window closes. No error reaches the caller.

(b) `session resume <ref>` reports only Claude's own error. `resume_session`
execs `claude --resume` directly, so nothing says what Endless resolved or what
to do instead. `--dry-run` against the same ref resolves cleanly and prints a
worktree, which makes the failure look unrelated to the transcript.

Both should stat the transcript before launching and refuse with a route rather
than a dead end. The route exists: a task whose claimant has ended is still
claimable, so a fresh Claude session plus `task claim` picks the work up, and
`task bind` attaches ownership without moving status. Neither is discoverable
from the failure today.

(c) `claim_item` prints a "To work on this task, choose one:" block after every
claim, and both options are wrong for the case that reaches it most often — a
re-claim onto a task whose worktree already exists. Option 1 offers
`task spawn`, which `_check_prior_claim` refuses whenever the task has any prior
claimant, which a re-claim always does. Option 2 spells out `/cd`, `shell-init`
and `eswt`.

## What claim should print instead, by caller

Replace the block with one outcome per caller, decided from how the bound
session resolved. `_resolve_session_id_with_prompt` returns a bare id today and
does not report which layer produced it, so it needs to report provenance —
env/pane, sibling pane, or nothing — before this can branch.

1. Claim ran INSIDE the Claude session being bound (env or direct pane match).
   Print `/cd <worktree>` and nothing else. The session is already here; it only
   needs its working directory moved. Mike would almost never claim this way,
   but another user might, and it should work rather than fail.

2. Claim ran from a shell, no tmux. Refuse: tmux is required.

3. Claim ran from a shell inside a tmux window holding one pane. Do the work:
   split the window into two sibling PANES — horizontal running `esm`
   (`endless session monitor`), vertical running nothing. Confirmed by Mike.

4. Claim resolved a Claude session in a SIBLING pane. Refuse and say to run
   `session goto --resume` instead. Deliberately not handled: covering it means
   deciding what happens to every other pane and window in play, and tmux panes
   are cheap enough that the complexity is not worth the benefit.

5. `--unattended`. Scope here is only the printed line: there is no bound
   Claude session, so there is nothing to resume. Print the worktree and stop.
   Everything else about this flag — whether an agent-less claim should mint a
   session record at all, and whether the flag should be renamed --no-agent —
   is E-2110, deliberately out of scope for this task.

Verify by claiming a task whose worktree already exists from each of the five
callers above, and by pointing both resume paths at a session whose transcript
file has been removed.
