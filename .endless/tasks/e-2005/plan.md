# Notify a session when a task it holds is landed

## The gap

Landing a task changes no watched task field, so `tasks_notify_sessions` never
fires and a session is never told its own work reached `main`. E-2001 fixed the
delivery CHANNEL (injections were framed in a shape the harness discards); this
is the missing MESSAGE.

## Why it needs a schema change

`events.Actor` records `{kind, id, session_id}`. `ActorKind` names the CHANNEL —
`session`, `cli`, `hook`, `system`, `web`, `triager` — never who was at the
keyboard. A person typing `endless worktree land` and an agent shelling out to
the same command from its Bash tool both produce `kind = "cli"`, `id =
"<user>@<host>"`, and frequently the SAME `session_id`.

That last one is the trap. `_resolve_session_id_with_prompt`
(`src/endless/task_cmd.py`, the sibling-shell branch) resolves a bare shell in a
sibling pane by reading `@endless_session_uuid`, the tmux window option the
window's Claude session published. A human's command in a sibling pane is
therefore attributed to the agent sitting next to it. Attribution is working as
designed — it answers "which session is this about" — but it is the wrong axis
for "who did it", and today it is the only axis there is.

Required behaviour: an agent's own land is not announced back to it; a user's
land is announced to every session holding the task. That is inexpressible from
`session_id` alone.

## The primitive already exists

`internal/agentenv` (E-1962) answers "which agent harness is running Endless"
from the environment the harness exports to its subprocesses.
`CLAUDE_CODE_ENTRYPOINT=cli` yields `claude_cli`; a plain shell yields
`unknown`. Python mirror at `src/endless/agent_env.py`.

An agent's Bash tool exports those variables and a human's login shell does not,
and the whole chain inherits: agent Bash -> `endless` (Python) -> `endless-go
event emit`. The signal is present at emit time in the exact process that writes
the event. It was simply never recorded.

## Design

### 1. `Actor.Harness` on the event envelope

Add one field:

```go
type Actor struct {
    Kind      ActorKind `json:"kind"`
    ID        string    `json:"id"`
    SessionID string    `json:"session_id,omitempty"`
    Harness   string    `json:"harness,omitempty"`   // agentenv.ID, "" = no agent
}
```

Populated in Go at emit time from `agentenv.Detect()`, NOT passed as a flag from
the Python bridge. Two reasons: the Go process inherits the same environment, so
it is the authoritative observer; and a value the caller supplies is a value a
stale or hand-rolled caller can get wrong.

`""` (absent) means no agent harness was detected — a human at a shell. Any
`agentenv.ID` means an agent typed it. Purely additive: every historical event
stays valid unchanged, the same argument `ActorTriager` makes in its own comment.

Orthogonal to `Kind` on purpose. `hook` is always an agent, `web` never is, and
`cli` is the ambiguous one this resolves; folding agent-ness into `Kind` would
multiply the enum and break the closed set every consumer switches on.

### 2. `task_landings.landed_by_harness`

```sql
ALTER TABLE task_landings ADD COLUMN landed_by_harness TEXT;
```

Written by `execTaskLanded` from `evt.Actor.Harness`. Schema change file under
`internal/schema/changes/`, following the E-1917 ORDERING precedent (land before
installing the binary that depends on it).

### 3. `task_landings_notify_sessions` trigger

`AFTER INSERT ON task_landings`, mirroring `tasks_notify_sessions`:

- fan out to every `session_tasks` holder of `NEW.task_id`
- exclude sessions in state `ended` (undeliverable by construction)
- suppress ONLY when `NEW.landed_by_harness IS NOT NULL AND st.session_id =
  NEW.session_id` — an agent is not told about a land it performed; a human's
  land is announced to every holder, including the session the land is
  attributed to. This is the user's rule stated exactly: agent lands, suppress;
  user lands, notify.

Payload reuses `session_notices.changes` with a synthetic `landed` key:

```json
{"landed": {"before": null, "after": "<base_branch>@<sha7>"}}
```

Reusing `session_notices` rather than a parallel table is the point: one-shot
drain, the `notified` flag, `ReapNoticesForEndedSessions`, the
`.endless/logs/session-notices.jsonl` delivery log and the ordering after the
active-task line all come free, as does E-2001's framing fix without which none
of it arrives.

### 4. Renderer

`RenderNotice` gains a `landed` case ahead of `noticeFieldOrder`, since a
landing is not a field change:

```
FYI — E-2001 landed on main (1dd0006)
```

## Scope held deliberately

`tasks_notify_sessions` is NOT switched over (see E-2006) to the harness axis in this task,
even though it has the same latent flaw. That trigger is load-bearing for
E-1917's whole delivery path and its self-suppression is pinned by 21 checks;
changing it belongs in its own change with its own verification, once
`Actor.Harness` exists and has been observed working. Recorded here so the
follow-up is a decision rather than a rediscovery.

## Verification

A new `tests/tasks/e-2005-verify.sh`:

1. Fail-fast Go layer: `Actor.Harness` round-trips through emit → event JSON →
   `task_landings.landed_by_harness`, for a detected harness and for none.
2. A land with `landed_by_harness` set notifies OTHER holders and not the
   lander.
3. A land with `landed_by_harness` NULL notifies EVERY holder, the lander
   included — the 99th-percentile case, driven end to end.
4. The rendered line reads `FYI — E-<id> landed on <branch> (<sha7>)`.
5. Delivery stays one-shot: the next turn does not repeat it.
6. `tests/tasks/e-2001-verify.sh` still passes 20/20.

Each new check must be shown to FAIL against the pre-change binary, as the
current suite was.
