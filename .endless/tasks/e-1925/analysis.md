# Context and constraints for `session recap`

## Why now

E-1914's reworked `session list` made task-less sessions visible for the first
time (they used to carry a discussion summary; now the columns are the active
task's id and title, so they render blank). Seeing one immediately raises "what
was that session about, and is it worth resuming?" — and there is currently no
way to answer it.

E-1918 auto-creates a task titled `Auto-resumed task for session ES-NNN` on
resume precisely BECAUSE the user cannot name it. `session recap` is the
companion that lets them find out: additional to E-1918, not an alternative.
Expected to be reached for rarely — only when someone is triaging a task-less
session out of `session list`.

## HARD REQUIREMENT — do not re-open E-1470

Headless `claude -p` calls must scrub `TMUX_PANE` (or set the hook-skip marker)
before spawning. E-1470 recorded this exact failure:

> Headless `claude -p` calls run `subprocess.run` with no env scrub, inheriting
> the caller `TMUX_PANE`. The headless call gets a new session UUID and fires a
> hook → `TouchSession(new_uuid, inherited_pane)`; the collision UPDATE then sets
> `state=ended` on the CALLING live session (same pane, different uuid).

The observed damage was that `endless session id` and `endless task add` broke
until the next Stop hook, because the resolver filters `state != ended` and the
caller had just been marked ended by its own subprocess. **Recap was one of the
two callers that hit it.** Reintroducing a `claude -p` call without the scrub
reintroduces the bug.

## This does NOT re-open E-1906

E-1906 ("Remove the vestigial session-recap flag now that one session maps to one
task") removed an **automatic, background** recap flag, on the stated premise
that *"one session now maps to one task and `endless task report` covers the
handoff."*

A task-less session is exactly the case where that premise does not hold, so an
**on-demand, user-invoked** recap sits in the gap E-1906's reasoning leaves open
rather than contradicting it. The distinction to preserve: nothing may generate
recaps on its own schedule, for sessions nobody asked about.

E-1906 dropped the old recap columns via a change file, so caching needs a **new**
column with its own migration — not a resurrection of the dropped one.

## Shape

```
endless session recap <session-id|task-id>
```

- Accepts a session ref (`ES-NNN`, integer, UUID prefix) or a task id (`E-NNN`),
  resolving a task to its most-recent session. Take `ES-` from the start; E-1918
  is fixing the entry points that lack it, and a new command must not add another.
- Shells out to `claude -p` with haiku or sonnet over the session's transcript.
- Caches into the new `sessions` column so repeat calls are free.
- **Invalidates** — TTL, or cleared on the session's next turn — so a later call
  reflects the current transcript rather than a stale summary. A recap that
  silently describes a conversation as it was ten turns ago is worse than none.

## Open questions for planning

- Model choice: haiku (cheap, likely sufficient for "what was this about") vs
  sonnet. Possibly a flag.
- Invalidation mechanism: a TTL is simplest; clearing on the next recorded turn
  is more correct but needs a hook touchpoint.
- Behavior for an ended session whose transcript file is gone.

## Settled: `session list` may show a CACHED recap, never generate one

A task-less row in `session list` has nothing in its Task/Title columns, so it may
fall back to this session's recap — **but only if `session recap` has already
cached one.** The listing must never generate a recap on read: it renders up to
`--limit` rows, so a generate-on-read fallback would fan out into one `claude -p`
per blank row, which is precisely the automatic-background behavior E-1906 removed
and E-1470's env-scrub bug lived in.

So the rule is: read the cache if present, render blank if not, never fill it.

**Knock-on for E-1914's omit-by-default rule.** E-1914 omits task-less sessions
from `session list` until `--all`, on the grounds that they have nothing to show. A
cached recap gives such a row content, which partly undoes that premise. Decide at
planning time between:

  - leave the omission as-is, and surface cached recaps only under `--all`
    (simplest; the rows a user has bothered to recap are the ones they were
    already inspecting with `--all`), or
  - un-omit a task-less session *once it has a cached recap*, since it now has
    something to say (more useful, but makes row inclusion depend on cache state,
    which is a surprising thing for a listing to do).

The first is the safer default. Either way this must not silently re-establish the
Summary column E-1914 removed.
