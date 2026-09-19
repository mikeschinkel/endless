# Analysis — `session monitor` never recovers a focal task

## Mechanism (confirmed by reading, E-1851 session)

`sessionstatuscmd.Run` resolves the focal task ONCE, before entering the loop:

    focal, kind, err = monitor.ResolveSessionStatusFocal(pane)
    ...
    monitorLoop(focal, parentSession, emittingSession, ...)

`monitorLoop` takes `focal` by value and never re-resolves it — grepping the
loop body for `ResolveSessionStatusFocal|GetPaneStatus` returns 0 matches. So a
monitor started before its session exists renders the claim/bind hint forever.

## Why E-1851 exposed it

Latent while a human started `session monitor` by hand — that always happened
well AFTER Claude had registered its session. E-1851 makes `task spawn` launch
the monitor automatically, milliseconds after the window is created and seconds
before Claude's SessionStart hook writes the session row. The spawned layout's
monitor pane is therefore blank in the common case, which is most of the value
of the 3-pane layout.

## Scope of the race

`parentSession` shares it exactly: it is read from the `@endless_spawned_by`
window option, which `spawn-launch` writes immediately before its
`syscall.Exec` — the same instant the session row does not yet exist.
`emittingSession` is consulted only while `focal == 0`, so a stale 0 there
leaves the no-goal view (E-1802) empty for exactly the sessions it serves.
All three therefore move together; the plan re-anchors the set and freezes it
on the first focal hit.

## Testability constraint found while planning

`sessionstatuscmd.Run` calls `monitor.PinMainDB()` unconditionally on the
normal pane-resolved path, so `--config-dir` cannot redirect it and pane→session
resolution cannot be exercised against a seeded throwaway DB — which is what
verifying this fix requires.

Resolved in the plan by adding a test-intended `--process` flag on the precedent of
the existing `--task`/`--session` flags, NOT by gating the pin on
`HasExplicitDBContext()`. The gate was considered and rejected: it would turn a
stateless invariant into a caller-dependent convention whose failure mode is a
silently empty view, and unlike the `main.go` precedent it would be
production-reachable, since the Python CLI already translates `--db` into
`--config-dir` and is one refactor away from threading it here. Full rationale
is recorded in the plan so it is not relitigated.

## Explicitly NOT this task

The pane HEIGHT symptom is handled in E-1851: with no rows the pane holds
`monitorPaneEmptyHeight` instead of collapsing to a 2-row sliver, and it grows
to an exact fit as soon as rows resolve. This task is only about the rows
appearing at all.

Separately, a monitor whose cwd is a self_dev worktree reads the sandbox DB,
whose session rows carry no `active_task_id` — verified: the e-1851 sandbox has
1 session row, 0 with a task. That is E-698's documented cost (E-1883).
E-1851 works around it by starting the monitor pane in the project dir.
