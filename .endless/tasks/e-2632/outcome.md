# Synthesis: how land reports open post-land items

## The problem
Landing E-2529 printed a yellow "post-land items still open" list ending "Do them on the landed, installed result". Mike ran the land and could not tell what to do. The answer was nothing, because the claiming session owned the items. The message:
- read as a failure
- named no one to do the items
- gave statements of what will be true, not actions
- said "installed", which is Endless's own self-dev setup and wrong for most projects.

Mike's framing went further than the message. Post-land items need an owner and an action, and Endless should run the items it can.

## Where it landed
- **Owners.** Every post-land item names one of three: **cli** (Endless runs its commands), **agent** (the claiming session) or **user**. Mike chose this over guessing the owner at land time (ED-1664).
- **Actions.** Each item carries runnable commands alongside its "what will be true" statement. A command looks up values only known after the land, such as a SHA or a run ID, when it runs, so the plan never needs to hold them.
- **Order.** Post-land items are already an ordered sequence. The land runs the leading cli items and stops at a failure or at the first agent or user item. Recording that item runs the cli items after it. Running independent items in parallel is deferred until it becomes a real concern.
- **Failure.** A failed cli item stops the sequence and leaves the task `landed`. The output names the failed command and how to rerun it.
- **Pass/fail (recommendation, for Mike to confirm).** A cli item counts as done on exit 0. Its command must therefore fail when its statement isn't true. A separate check command can come later if that proves insufficient.
- **Danger.** Owner `user` covers it. Approving a plan and preferring to run a step yourself are separate things: Mike may approve a release and still want to run it himself. A separate "dangerous" flag is deferred.
- **Handoff.** Mike is rarely talking with a session when he lands its task. So the land sends agent-owned items into the claiming session's pane as a prompt that includes the reconcile step. If that session isn't running, the land says so and gives the `endless session resume …` command.
- **The report.** It says first that the land succeeded, then lists only the user's own items with their commands. It no longer says "installed".
- **User recording.** A new command, `endless task plan done E-<id> P<n>`, records a user-owned item. Reconcile's verdict file is for agents.
- **Recurring steps (PRODUCT).** Commands a project needs after every land go in project config, which may call a script. Endless adds them as leading cli items and never names an install step itself (ED-1665). Mike first floated a hook that prints the commands, which would be dynamic config. Static config is enough until a project needs the dynamic form.
- **The guide.** The post-land guide gets rewritten for any project. Today it defines post-land work by Endless's own migrations and installed build.

## Corrections of my thinking
- `landed` already exists and isn't terminal. The confusion came from reading the message, not from a missing state.
- No owner is "CI". Landing is local, and the session checks CI results.
- Self-dev examples like `just install` must not shape PRODUCT behavior.

## Follow-ups
- E-2634: Add owner and action to post-land plan items, and rewrite the guide. Every other follow-up builds on this one.
- E-2635: Add `task plan done` for user post-land items.
- E-2636: Add project config for commands run after every land.
- E-2637: Run cli-owned post-land items in plan order.
- E-2638: Rewrite the land report and hand items to their owners.
- Decisions: ED-1664 (owners and order), ED-1665 (recurring commands in config).
