Answered by the user in E-2333's session on 2026-10-15:

- Starting set: the rows the table draws in bright text, meaning not spawned
  into another session, not hidden, not suppressed, and not landed. In that
  session that was the own task (E-2333) and E-2345 itself. The own task is
  included because the chain blocking it is what the user most needs to see.
- Downstream: one step, the tasks the own task directly blocks or precedes,
  so the graph also shows what finishing the task releases.
- Dimming: a spawned (in-flight) task's ROW is dimmed because the user does
  not need to spawn it. In the graph it is not dimmed, because until it lands
  in the other session the user must consider it before spawning the tasks
  that depend on it.

Using `rowIntensity` for "bright" rather than restating its conditions keeps
the graph's starting set tied to what the table shows, so a later change to
row dimming moves both together.
