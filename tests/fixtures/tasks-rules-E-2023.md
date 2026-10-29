# Verification suites live here

One directory per task: `.endless/tasks/e-<id>/`, holding a `verify.toml`
manifest, a `verify.sh` script, or both. `_harness.sh` is the shared shell
harness a script suite sources; it belongs to no task.

## Run one with the runner, never by hand

    endless task verify E-<id>      # or, with no id, this session's own task

The runner is the only front door. It builds the isolation a suite needs (a
temp `HOME` and `XDG_CONFIG_HOME`, so a suite cannot read or pollute your real
config or the main database), it knows which task it is running, and it refuses
a task's suite that is not yours. Executing a script directly skips all three —
which is why a suite that sources `_harness.sh` refuses to run that way.

## Do not run another task's suite

A suite is a **land-time gate for one task at one moment**, not a regression
suite and not a smoke test. Its fixtures and assertions were pinned to the tree
that existed when that task landed. After the land, whether it still runs — or
passes — is undefined, and nothing re-runs it for you.

So a landed suite's result says nothing about your work, in either direction. A
failure in it is not evidence of a bug; acting on one means changing working
code to satisfy a check that no longer describes it. That has happened, more
than once, and it is the reason the runner refuses rather than warns.

A glob over this directory is the same mistake at scale. There is no
"project-wide verify"; running every suite is running two hundred other
people's land-time gates.

## Do not edit a landed task's suite

It records what was true when that task landed. Retrofitting it to a later
change rewrites that history. If your change alters a string or a behaviour a
landed suite asserted, leave the suite alone — its owner's own run will tell
them, on their schedule, with their context.

Yours is `.endless/tasks/e-<your-task>/`, and nothing else in this tree.

## Coverage that must survive belongs elsewhere

If a behaviour is checked ONLY in a verify suite, it is unprotected the moment
that task lands. Mirror it into the project's durable test suite. Bit rot here
is expected and accepted; bit rot there is a bug.

## Writing one

A new script suite sources the harness and calls `summary` last:

    #!/usr/bin/env bash
    set -u
    source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

    section "What this proves"
    assert_eq "the thing does the thing" "expected" "$(the-thing)"

    summary

The harness gives you `section`, `assert_eq`, `assert_contains`,
`assert_not_contains`, `report_pass`, `report_fail`, `report_skip`,
`setup_error` (exit 2) and `summary` (exit 0 all-passed, 1 on any failure). It
emits TAP to the runner as a side effect, so each assertion lands in the run's
report; you write assertions, not plumbing.

Fold the task's own unit tests in as a first, fail-fast check, so the one suite
is a complete proof for that task at land time.
