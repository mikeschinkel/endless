## Decisions taken while planning (with the user, 2026-10-22)

- **claim and bind refuse an ended earlier holder, not only a present one.** Starting a task with a clean transcript must be explicitly opt-in (`--new-transcript`); a task otherwise continues from its earlier session's transcript. Spawn and prime already work this way (E-1967).
- **A lost transcript is not a reason to allow a fresh claim.** Resume fails only when the transcript file is gone, and `--new-transcript` is the explicit route on from there. E-2420 keeps transcripts from being deleted.
- **The auto-bind's present-holder case is a fault, reported to both the agent and the user.** It has been rare: 11 of 557 tasks ever had two sessions active at once, and two since September (E-2107, a two-minute overlap on 3 Sep; E-2206, ES-1339 and ES-1341 in different panes on 15 Oct). Exit 2 on SessionStart would show stderr to the user but not reach the agent, which would carry on unbound, so the hook returns JSON with `additionalContext` and `systemMessage` instead.

## Decided without asking
- The auto-bind also refuses a fresh `claude` started by hand in a task's worktree when an ended session held the task: it is the same clean-transcript start as a claim, by another route.
- A holder in the same tmux pane, and a SessionStart from resume, /clear or /compact, are the same conversation line and are not refused.
- `--new-transcript` marks its launch with an environment variable, since the plain `claude` it runs is otherwise indistinguishable from one typed by hand.

## Correction to the context as first filed
The auto-bind was first described as unguarded. It is guarded by the worktree lock; the gap is a session that claimed from the main checkout and so never took the lock.
