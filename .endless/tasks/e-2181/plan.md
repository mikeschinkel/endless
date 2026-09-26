# Name a spawned session after its task

## The change

`spawn_plan()` in `src/endless/task_cmd.py` takes `name: str | None = None` and
passes it through only when set. Nothing sets it for a task spawn, so it is
always `None` and `claude --name` is never passed.

Default it to the task id when the caller gives nothing:

```python
if name is None:
    name = f"e-{item_id}"
```

That is the change. The rest of the path already exists and is exercised today
by the explicit `endless task spawn --name` flag:

`task_spawn` (cli.py, the `--name`/`session_name` option) → `spawn_plan`
(task_cmd.py) → the `spawn_cmd += ["--name", name]` line in the same function
→ `spawn-window`'s own `--name` flag (internal/spawnlaunchcmd/spawn_window.go)
→ `buildClaudeArgv` (internal/spawnlaunchcmd/spawn_launch.go), which appends it
when `spec.Name` is non-empty.

`grep -rn 'name' src/endless/task_cmd.py | grep spawn` finds the Python end;
`grep -rn 'spec.Name' internal/spawnlaunchcmd` finds the Go end.

An explicit `--name` keeps winning; this only fills the empty case.

## Why the id and nothing else

Claude Code derives an unnamed session's name from the working directory's
basename plus a two-character disambiguator — documented in
`docs/arch-2026-08-14-cross-session-messaging.md` §2. A task session lands in
`.endless/worktrees/e-NNNN`, so today it shows as `e-1983-f2`: right identity,
two characters of noise.

`tmux_window_name()` in task_cmd.py already decided this exact question
for tmux windows under E-2102 — "the task id, and nothing else" — on the
argument that a narrow label spends none of its width on something the user
already knows. A session name is read in the same places and under the same
constraint. This makes the two agree instead of one carrying a suffix the other
does not.

## Why it is worth doing at all

A session name is becoming an address, not a label. `ListAgents` lists peers by
name and `SendMessage` addresses them by name, which is the transport E-1995
builds its dispute resolution on. `e-1983` is an address a person and an agent
can both derive from a task id without a lookup; `e-1983-f2` has to be read off
a listing first.

## Collisions

Claude Code keeps a contested name with the session that already holds it and
renames the newcomer. Two live sessions on one task is already the case
`task claim --force` exists to make rare, so the collision path stays a
fallback rather than something to design around.

## Not in scope

Naming sessions that Endless did not spawn. A hand-started `claude` in the main
checkout still shows as `endless-NN`, because nothing there knows a task. That
is the separate discoverability question — Endless records no cwd for a session
and creates no process row for a non-tmux one — and it is not this task.

## Acceptance

- `endless task spawn E-NNNN` produces a session listed as `e-NNNN`.
- `endless task spawn E-NNNN --name foo` still produces `foo`.
- `session goto --resume` is unaffected.
- `just test` passes, with a test covering the defaulted and explicit cases.
