# E-1557 — Research prompt

Research Claude Code's background-session (bg / Agent View) mechanics
for the following questions. Anchor on official Anthropic Claude Code
docs, GitHub release notes (especially v2.1.139 which introduced Agent
View), and community write-ups (Reddit, HN, X, blog posts). Prefer
official > release notes > community. Goal: design how a task-tracking
tool ("endless") should integrate — specifically, how an orchestrator
session dispatches child tasks as bg agents, surfaces bg state, and
promotes a bg agent to a dedicated tmux window for focused interaction.

1. **Launch**: Current invocation to start a Claude Code session in
   background mode — `claude --bg <prompt>` (CLI), `/bg` (from inside
   an interactive session), or something else? Does background launch
   return an agent ID / session ID to the caller? What stdout/stderr
   semantics during launch? What env vars (if any) are inherited by
   the bg process from the spawner?

2. **Resume / view from inside an active session**: From within an
   active interactive Claude session, what command brings a specific
   running background agent into the current pane for viewing? Does
   viewing PROMOTE the bg agent to foreground (terminating bg
   status), or is it purely a VIEWING window where the bg keeps
   running concurrently? Can multiple sessions view the same bg
   agent?

3. **Promote to a dedicated tmux pane**: Suppose a bg agent is
   running and a user wants it in its own dedicated terminal (not
   just viewed inside another session's pane). What's the supported
   invocation — `claude --resume <bg-id>`, `claude --attach <bg-id>`,
   or something else? Does the existing bg daemon process continue,
   or does a new fg process spin up?

4. **State model and discovery**: What states does a bg agent go
   through (Working / Needs input / Ready for review / Completed)?
   Is there an external CLI or API to query state programmatically —
   `claude bg list`, `claude bg status <id>`, daemon socket,
   anything? How does a tool integrator discover the user's running
   bg agents at any moment?

5. **SessionStart hook firing in bg**: Does the SessionStart hook
   fire for bg agents the same way as for foreground sessions? Are
   env vars set by the spawner inherited by the bg agent's process?
   Are there hooks that distinguish bg from fg launch? Any hook
   fields that identify "this is a bg agent" inside the hook
   payload?

6. **Daemon lifecycle**: What process supervises bg agents? Effects
   of machine sleep, shutdown, terminal close? Recovery semantics —
   does respawn resume state or start clean?

7. **Concurrency and quota**: Does running N parallel bg agents burn
   quota at N× the rate of one? Any soft caps, throttling, or
   queueing?

8. **Version availability**: When were `--bg`, `/bg`, and Agent View
   introduced? Which Claude Code versions/builds support them
   today? Any noted regressions or open issues?

For each question, cite the source URLs. If the answer differs
between official docs and observed behavior, report both.
