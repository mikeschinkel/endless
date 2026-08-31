"""The rules that govern a project's per-task verification suites.

This module holds the text of `.endless/tasks/CLAUDE.md` and the idempotent
write that places it. It ships with every registered project — a file that
existed only in Endless's own checkout would teach nobody, and the mistakes it
exists to prevent are not Endless-specific: any project using Endless
accumulates per-task suites that look like a regression suite and rot.

It is explicitly NOT counted as a defense. It is prose, and prose is the layer
that already failed: the rule was read, understood, paraphrased into "not
required", and broken. The enforcement is the runner's own-task-only refusal and
`_guard.sh`, which reads the suite's own path and the reachability of a real
config rather than any variable a caller can set. This file is the explanation
those refusals point at, placed
where an agent working in that directory meets it before it acts instead of
having to go find it in `endless guide orchestration`.
"""

from pathlib import Path

SUITE_RULES_FILENAME = "CLAUDE.md"

SUITE_RULES = """\
# Verification suites live here

One directory per task: `.endless/tasks/e-<id>/`, holding a `verify.toml`
manifest, a `verify.sh` script, or both. `_harness.sh` is the shared shell
harness a script suite sources, and `_guard.sh` is the guard the harness
sources; both belong to no task.

## Run one with the runner, never by hand

    endless task verify             # this session's task, or the worktree you are in
    endless task verify E-<id>      # a named task

The runner is the only front door, and it is enough on its own: it resolves the
task, runs in that task's worktree, builds the isolation a suite needs (a temp
`HOME` and `XDG_CONFIG_HOME`, so a suite cannot read or pollute your real config
or the main database), and refuses a task's suite that is not yours. Executing a
script directly skips all of it — which is why every suite here refuses to run
that way.

It exports two things into a suite's environment:

| Variable | What it is |
|----------|------------|
| `ENDLESS_VERIFY_TASK` | the task being verified, `E-NNNN` |
| `ENDLESS_VERIFY_DIR`  | this suite's own directory |

Neither grants permission, and there is no variable that does. `_guard.sh`
decides what a suite may do from facts no environment can restate: the path the
running file sits at, and whether a real config is reachable from it. It refuses
a suite running from another task's worktree, and it refuses a direct run —
because a direct run has your real `HOME`, and one of them wrote into the main
database and took down session tracking.

There was once a marker variable whose presence meant "the runner started you".
One `export` satisfied it, so it enforced nothing while looking like it did.
Do not add another.

Read a file you ship beside your suite from `$ENDLESS_VERIFY_DIR`, never from a
path you type out. A hand-written path has to match a directory-casing
convention it cannot see, and gets that wrong silently on a case-insensitive
filesystem and loudly on everyone else's.

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

Every suite says so in its own first lines, naming the task it belongs to, so
you meet the rule when you open the file rather than after you have edited it.

Yours is `.endless/tasks/e-<your-task>/`, and nothing else in this tree.

## Coverage that must survive belongs elsewhere

If a behaviour is checked ONLY in a verify suite, it is unprotected the moment
that task lands. Mirror it into the project's durable test suite. Bit rot here
is expected and accepted; bit rot there is a bug.

## Writing one

A new script suite sources the harness and calls `summary` last:

    #!/usr/bin/env bash
    # ── DO NOT EDIT ─────────────────────────────────────────────────────
    # This suite belongs to E-101 and records what was true when E-101
    # landed. Edit it only if you ARE E-101. If your change breaks an
    # assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
    source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

    set -u

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
"""


def scaffold_suite_rules(project_path: Path) -> bool:
    """Idempotently place `.endless/tasks/CLAUDE.md`.

    Returns True when the file was written, False when one was already there.
    An existing file is never overwritten: a project that customized its own
    rules keeps them, and re-running registration is a no-op rather than a
    silent revert.
    """
    tasks_dir = project_path / ".endless" / "tasks"
    target = tasks_dir / SUITE_RULES_FILENAME
    if target.exists():
        return False
    tasks_dir.mkdir(parents=True, exist_ok=True)
    target.write_text(SUITE_RULES)
    return True
