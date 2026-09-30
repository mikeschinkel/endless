# A stale bin/endless-go hides real Python test failures

## The symptom, and how it was found

Seven tests in `tests/test_report_reminder.py` fail against current source and
had been failing for about a month without anyone seeing it. They were found by
accident: a `just build` run for an unrelated reason replaced a stale
`bin/endless-go`, and the next `just test` went red.

## The cause is two independent things

**The test bug.** `_add_task` in that file inserts a task row directly with
`status='underway'` and never records a session. Every wind-down test then
drives it to `unverified`. E-2018's lifecycle guard refuses exactly that — a
task no session ever claimed would be reporting implementation nobody did — so
the guard is right and the fixture is wrong.

**The reason nobody noticed.** `just test` runs Python against whatever
`bin/endless-go` happens to be sitting in the worktree. Nothing asserts the two
are in step. A binary predating E-2018 has no such guard, so the fixture passed
against it and failed against source, and which one you got depended on when you
last built.

The second is the one worth fixing. The first is a one-line fixture change; the
second is why a real failure stayed invisible for a month, and it will hide the
next one the same way.

## The fixture fix

`_add_task` also inserts an ended session bound to the task. `taskEverClaimed`
counts `sessions.task_id`, so an ended session satisfies the guard without
making the fixtures look like a live claim:

```python
    task_id = cur.lastrowid
    db.execute(
        "INSERT INTO sessions (session_id, project_id, state, task_id, started_at) "
        "VALUES (?, 1, 'ended', ?, '2026-08-01T00:00:00')",
        (f"uuid-report-reminder-{task_id}", task_id),
    )
    return task_id
```

Verified: that alone takes the file to 32 passed, and the full suite green.

## The drift fix is the actual work

**`just test` gains `build` as a prerequisite.** The suite then cannot run
against a binary older than the source that produced it.

That is the whole change. The Go build is incremental, so on a run where nothing
changed the added cost is near zero, and on a run where something did change the
build was required for the result to mean anything.

Both alternatives were considered and rejected. A staleness check that refuses
to run is cheaper per invocation, but it adds a mechanism that can itself be
wrong, and mtime comparison carries edge cases a build does not. A single test
asserting the binary is current is smaller still, but by the time it fires the
other several thousand tests have already run against the stale binary — it
reports the problem rather than preventing it.

Give `just test-go` the same prerequisite if it has the same exposure.

## Acceptance

- `tests/test_report_reminder.py` passes against a freshly built binary.
- `just test` builds before it runs, so it cannot exercise a stale
  `bin/endless-go`.
- `just test` and `just test-go` pass.



## Scope grown during implementation (Mike chose option B)

The premise above did not reproduce: a fresh build of current source passed
all 32 tests (and the full suite, 3772). The reason is in the guard itself.
conftest stubs every emit's session id to 1, no sessions row 1 exists, and
`ValidateStatusActor` treated the unreadable row as "return nil" — skipping the
never-claimed rule along with the held-task rule. So any session id without a
row could move a never-claimed task to `unverified`.

- **Guard.** An unreadable session row now skips only the held-task rule; the
  never-claimed rule still runs. Two Go tests pin both halves.
- **Fixture.** With the guard fixed, the seven tests fail exactly as reported.
  `_add_task` now records the claim by the session the tests act as
  (conftest's stubbed id), not an auto-increment id that happens to be 1.
- **Drift.** `just test` depends on `build`. `just test-go` does not need it —
  `go test` compiles from source and no Go test runs bin/endless-go.

What turned the finder's run red is still unexplained: no branch in any
worktree has a guard without the early return.
