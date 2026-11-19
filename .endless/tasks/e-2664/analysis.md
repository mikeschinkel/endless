Points agreed with Mike on 2026-11-19. They are a starting point for this task's own planning, not a plan.

1. A new `plan` task type. Filing one needs only a brief. Context and Analysis are optional, filled in when the filer already has them. The filer does no new work: no scope list and no options. Deliverable and constraints go in only if the user has already stated them.
2. `spawn` gates on type, so a `plan` task can be spawned with just a brief. There is no `--to-plan` flag and no new status. A type carries its own handoff document, which a status cannot do, and the `plan` handoff tells the session that its job is to write the plan.
3. Filing a `plan` task spawns it by default, without changing which tmux window has focus; `--no-spawn` opts out. This also lets a task be filed and spawned from the CLI alone.
4. The planning session writes the Plan field and submits it for approval. The rules now in the `/needs-plan` skill move into the `plan` handoff document, and the skill is retired.
5. The type a `plan` task becomes is proposed by the filer (optional), then proposed or changed by the planning session, and ratified by the user. It can only be a "do" type, such as todo, bugfix, docs, or a future type. Once approved, the task converts to that type and keeps its ID. Mike has ideas for injecting the do-type handoff document at that point.
6. Research and brainstorm tasks are never `plan` tasks. Mike knows their type when he files them, and they are given a brief rather than a plan: filed with a brief, and spawnable with just a brief. If Endless does not already work that way, this task makes the change.
7. `endless guide` has to explain how to file a `plan` task well enough that no skill is needed.
