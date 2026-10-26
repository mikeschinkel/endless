# Synthesis: recording a post-land item's verdict

## The problem
A plan item whose work needs the landed result (E-2428's P9: a row in a table its own migration creates) can only be reconciled `blocked` before the land, and reconcile refuses the task after it. Recording the truth took revisit → underway → reconcile → verify → land with no code changed.

## Plan item, not follow-up task (ED-1636)
The test: does a post-land item need mechanisms that duplicate a task's lifecycle? It does not. A follow-up task brings claim, worktree, plan approval, verify, land and a backlog entry the user must close (ED-1550's cost). A post-land item needs only a deferral mark, one new parent status, and reconcile accepting that status. Its work also stays in the session that landed it, which ED-1550 already exempts.

**Dividing line:** a post-land item produces no code. Work that would produce code is a follow-up task, because code needs its own verify and land.

**Vocabulary (PRODUCT):** "land" is Endless's term, meaning merge into the primary branch plus the project's migrations. It is not git merge and not deploy; Endless has no deploy concept. A post-land item is work that needs the landed, migrated, locally installed result, which for most projects is on the user's own machine.

## The `landed` status
- The land goes `unlanded → landed` when deferred items remain, and to `assumed` as today otherwise.
- `landed → assumed` happens on the reconcile that records the last deferred verdict.
- `landed → revisit` when the shipped work is wrong; `declined`/`obsolete` by the user.
- There is no `landed → confirmed`. That replaces the seed's Q5 confirm gate: "items still undone" is now a status, not a check.
- The name: `landed`, chosen over `unassumed` as the more honest one. Whether to rename the `un*` statuses before v1.0 is a separate, open question.

## Deferral and verdicts (the seed's Q1–Q4)
- **Q1, both entry points:** items are marked post-land while planning (approved with the plan), or found at reconcile, where a `blocked` item's answer is the structured choice "do it after land".
- **Q2:** reconcile stays the only writer, accepting a `landed` task for its deferred items only.
- **Q3:** the agent records the verdict, attributed to its session.
- **Q4:** the verdict carries a note, and the item's `verify:` line names the check.
- **Failure:** a failed verdict keeps the task `landed`, and a later reconcile records the pass. Anything more elaborate would reinvent a task.

## Getting agents to follow it
The rule goes wherever the agent is at the moment it acts. The land runs in the user's pane, so the agent never sees the land's output.
1. **Gate:** reconcile on a `landed` task refuses if the branch has commits past the land, and names the remedy (a `--cleans-up` follow-up). This enforces the code/no-code line instead of teaching it.
2. **Reconcile messages:** at deferral time, say what happens after the land; on a failed verdict, say the task stays `landed`.
3. **Per-prompt hook context** lists a `landed` task's open post-land items.
4. **`task show` and the task list** show them too.
5. **The guide** is the reference the messages point to. The content goes in the existing tasks section, as the subsection `tasks/post-land` (ED-1637), not a new top-level section.

Handoff templates are left unchanged, since a copy there would drift.

## Upkeep
Adding `landed` means editing internal/taskstatus/transitions.go and running `just lifecycle-index` to regenerate the Mermaid status diagram and its copies; `just test` fails on drift.

## Follow-ups
- E-2443: guide subsections addressed as `section/sub` (implements ED-1637).
- E-2444: post-land items, the `landed` status, reconcile, the gate, and the agent-facing surfaces (implements ED-1636; blocked by E-2443 for the guide subsection).
- ED-1636 and ED-1637: proposed decisions, for your review.
