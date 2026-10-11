# Session status ownership: right inputs, visible decisions, user wins

One cause, five symptoms. The ownership annotation decides which rows a session
sees, which wear the duplicate glyph, and which vanish — from inputs that do not
mean what the rules assume, and without ever reporting that it decided anything.
All five live in `AnnotateSessionStatusOwnership`/`liveOwnership`
(`internal/monitor/session_ownership.go`) and `applyHiddenMode` plus the footer
(`internal/sessionstatuscmd/session_status.go`).

Filed as one task per ED-1550: these are symptoms of one derivation, not five
defects, and splitting them would mean five sessions editing the same two
functions.

## 1. Ownership needs a live session actually working the task

`liveOwnership` counts any live session holding a `surfaced` or `revisited`
relation. Both are bookkeeping: `surfaced` means a session once FILED the task,
`revisited` that it once touched it. Neither says anyone is working it, and
`idle` counts as live — so a filing claim from months ago owns the row forever.

Measured: E-1993 and E-1995 are suppressed from E-1991's own monitor by session
1113, which surfaced them while working E-1989 and has been idle since. The
epic's coordinator cannot see two of its own children, and I reported them as
"the last open work" from a monitor that was omitting one of them.

Ownership means a live session holds `RelationClaimed`. Drop `surfaced` and
`revisited` from the ownership query; they stay in `session_tasks` as the
provenance they are.

## 2. The duplicate glyph goes away once the task has its own session

`◫` exists to stop two sessions working one task. Once the task has spawned and
a live session has claimed it, the warning has done its job and the user knows
where the work lives — so it becomes noise on every other monitor that happens
to list the row. Spawned tasks are already dimmed, which carries the signal.

Measured: E-2280 has its own live session (1318, claimed) and still wears `◫`
in both E-2276's and E-2279's monitors.

When a live session holds `RelationClaimed` on the task, suppress
`DuplicateWork`. With §1 in place the `ambiguous` branch stops firing on
surfaced/revisited pile-up, which is what produced this; keep the explicit
suppression anyway, so a genuine two-claim collision is the only thing left that
can raise `◫`.

## 3. An explicit hide outranks the focus pointer

`applyHiddenMode` keeps a row when `hm == hiddenOmit && r.Focused`, so a hide is
ignored on a focused row. Its stated reason — "the task the conversation is on
right now is the last row that should vanish" — describes
`sessions.task_id`. But `Focused` comes from `sessions.focus_task_id`, which is
a pointer at another task, not the one the session is on.

Measured: `session hide --task E-2280` in E-2279's session wrote its row
(session 1313, task 2280) and was then ignored, because 1313's `focus_task_id`
is 2280 while its actual task is 2279.

The rule becomes: an explicit hide outranks `focus_task_id`, and does NOT
outrank the session's own `task_id`. Going to a non-steward session and hiding a
duplicate there IS the user picking a steward, and the derivation must yield to
it; hiding the task a session is actually on remains refused, which is what the
original reason was protecting.

## 4. Suppression is reported, not silent

`applyHiddenMode` drops an owned-elsewhere row with `continue` — uncounted, no
glyph, no flag. A hide at least reports a count. So the user cannot tell "there
is nothing there" from "something was withheld", which is how §1 went unnoticed.

One unseen-rows concept, two reasons: `hidden` because the user did it,
`suppressed` because a live session owns it. The footer reports both counts
separately and names the flag for each; `--show-suppressed` reveals suppressed
rows the way `--show-hidden` reveals hidden ones, each wearing its own glyph.
`--json` keeps `owned_elsewhere` as it is.

## 5. Suppression is overridable, through the surface that already exists

An explicit `session unhide --task` on a suppressed row pins it into that
session's monitor and outranks the derivation, the same way §3 makes a hide
outrank focus. This is the only new stored state, and it is written only when the
user overrides — the derivation stays self-maintaining.

This is the half of the original design that was argued away. Deriving
suppression on the fly was chosen because it needs no maintenance, which is true
and stays true; what was left out of the trade is that an explicit command also
gives the user override and visibility. §4 and §5 add those back without
mirroring every ownership change into stored rows.

## One judgment made here, flagged

§3 splits `Focused` into two cases — `focus_task_id` yields to a hide,
`sessions.task_id` does not. Mike's reasoning authorises the first half ("if I
found a duplicate and go to a session to hide it then clearly I am picking the
other session to steward it"). The second half is mine: it preserves the
original rule's real protection, so a session cannot hide the task it is
actually on. Say so if that is wrong.

## Not here

Whether `surfaced` should confer anything at all beyond provenance, and whether
`focus_task_id` and `task_id` should remain two fields. Both are larger
questions about the session-task model; §1 and §3 only stop the display reading
them as something they are not.

## Acceptance

- A task surfaced or revisited by an idle session is NOT suppressed from a
  monitor that lists it; E-1991's monitor shows E-1993 and E-1995.
- A task claimed by a live session is suppressed from other monitors as today.
- A task with a live claiming session wears no `◫`; two live claims still do.
- `session hide --task` on a row that is only the session's focus hides it.
- `session hide --task` on the session's own task is still refused.
- `session unhide --task` on a suppressed row restores it and keeps it visible
  while the owner is still live.
- The footer reports hidden and suppressed counts separately and names the flag
  for each; `--show-suppressed` reveals them with a distinct glyph.
- `just test` and `just test-go` pass.
