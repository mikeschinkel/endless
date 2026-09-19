## Three flags, one candidate name

Two existing flags are in play, they control unrelated machinery, and BOTH are
candidates to be replaced by `--no-agent`. They cannot both take the name, and
deciding that is part of this brainstorm rather than a detail to settle later.

- `claim --unattended` (E-2093) governs BINDING. With it, a task is claimed and
  `sessions.task_id` is left unset, so the work has no session record. Its half
  of the old `claim --force` was "claim with no Claude session bound".
- `--no-session` (E-1444) is GLOBAL, accepted in any position, and governs
  ATTRIBUTION. It sets a module flag the event write layer reads, which
  downgrades `actor.kind` to system and NULLs the envelope's session id. It
  exists, in its own words, for callers with no Claude session to attribute to:
  plain shell, cron, scripts. It does NOT skip session resolution — run it from
  a Claude pane and the claim still binds normally.

## Why the name collides

If the answer to the central question is "a session record always exists, and
what varies is whether an agent drives it", then both flags are misnamed by the
same reasoning, because in both cases the absent thing is the agent.

But the third possibility is more interesting than renaming either: if an
agent-less claim mints a session record, then `--no-session` may not need a new
name because it may not need to exist. Its whole purpose is to have somewhere to
put attribution when there is no session to credit. Give it a session to credit
and events can be attributed to that row instead of downgraded to system, and
the flag collapses rather than being renamed.

So the ordering is: settle whether an agent-less claim mints a session; then see
how many of these two flags survive; then name what is left. Renaming first
would be churn, which is the same reason the rename does not lead this task.
