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

Decide how `just test` should relate to the binary it exercises. The options, in
rough order of cost:

1. **`just test` depends on `just build`.** Simplest, and makes the suite always
   test what the source says. Costs a build on every test run.
2. **A staleness check that fails loudly.** Compare the built binary against the
   source tree and refuse to run rather than silently testing yesterday's code.
   Cheaper per run; one more thing to keep correct.
3. **A single test that asserts the binary is current.** Smallest change, and it
   turns a silent wrong answer into one named failure.

Not settled here — pick one while implementing, and say why in the commit. What
is settled is that the current behaviour, where the answer depends on when you
last built, is not acceptable.

Worth checking whether the same exposure exists for `just test-go`, and whether
a worktree's binary can be stale in ways the main checkout's cannot.

## Acceptance

- `tests/test_report_reminder.py` passes against a freshly built binary.
- `just test` either cannot run against a stale `bin/endless-go`, or fails
  visibly and by name when it is stale.
- `just test` and `just test-go` pass.
