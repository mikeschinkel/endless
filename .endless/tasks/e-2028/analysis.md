TRIGGERS. All detectable from data Endless already holds -- joins over session_tasks, tasks.status = 'underway', and sessions.state. No new capture required.

1. An inbound message arrives. Carry the reply affordance with it, not merely the content.
2. You link a task held by a live underway session (block, relate, cleans-up, replace).
3. You record a decision that governs such a task.
4. You edit, re-status or file a task a live session is holding.

EVIDENCE, from session ES-1105 on E-1944 (2026-08-21). Every one of these fired during a single brainstorm and none produced a notification.

(1) Another session sent findings from E-2010. They were folded in and one claim corrected. Replying never entered consideration -- not a judgment made and lost.
(2) E-2020 was blocked by E-1704, and E-1704 by E-1972, minutes before E-1972 was spawned into a live session.
(3) ED-1570 supplies exactly the 'verified compatible' test E-1972 was missing, and neither side was told.

DESIGN NOTE. The unit is the affected work, not the session roster: 'what you just did affects ES-NNNN, reach them with X'. A roster answers a question the session is not asking at the moment it is shown.


## The trigger list is entirely OUTBOUND (noted 2026-09-11)

All four triggers above are things YOU did to work someone else holds. The
mirror image is missing: something happened to work YOU hold or surfaced, and
nobody told you. A task you surfaced being claimed is the plain example — the
surfacing session gets `status: submitted → underway` from the field trigger and
no session id, so it learns that someone picked the task up but not who, and
cannot tell a spawn from a claim in place.

Worth folding in whenever this is planned, because the affordance differs by
direction: outbound is "what you just did affects ES-NNNN, reach them with X",
inbound is "ES-NNNN is now working E-NNNN, which you surfaced". Only the
outbound half needs E-1936 to have chosen a transport.

NOT a reason to prioritise this task. An earlier version of this note argued
that the missing inbound notice was why handoffs told Mike to spawn tasks he had
already spawned. That diagnosis was wrong: `endless task show <id> --llm`
already prints `touched_by=claimed ES-NNNN (E-NNNN) [working]`, so the data is
present in the exact command the handoff path is told to run. The defect was in
the handoff rules, not in what Endless reports, and it is fixed there. This task
remains what its title says — who to message — and is deferrable.
