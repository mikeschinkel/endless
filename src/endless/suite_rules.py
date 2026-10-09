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
# A task's own directory lives here

One directory per task: `.endless/tasks/e-<id>/`. `_harness.sh` is the shared
shell harness a script suite sources, and `_guard.sh` is the guard the harness
sources; both belong to no task.

## Three kinds of file, and only one of them is yours

| In `e-<id>/`                                  | Whose          | Written by                     |
|-----------------------------------------------|----------------|--------------------------------|
| `verify.toml`, `verify.sh`, `land.toml`       | the task's     | you, on the task branch        |
| `context.md`, `plan.md`, `analysis.md`, `outcome.md`, `reason.md`, `notes.md` | the database's | `endless task update`, on main |
| `verify-<UTC timestamp>-<sha>.ctrf.json`      | the runner's   | `endless task verify`, on main |

Those `.md` files are **document mirrors**: each is a projection of one piece
of the task's content in the database, and is named after it. The database is
the source of truth; the file exists so a human can read it on a Git host
without a database.

**Never hand-edit one, and never `git add` one.** A direct edit leaves the
database stale and is overwritten without warning by the `doc-mirrors` sweep,
which rewrites any mirror whose bytes differ from the database. Write the
content under `.endless/tmp/` and load it with the flag named after it:

    endless task update E-<id> --context-file .endless/tmp/<file>.md
    endless task update E-<id> --plan-file .endless/tmp/<file>.md
    endless task update E-<id> --analysis-file .endless/tmp/<file>.md
    endless task update E-<id> --outcome-file .endless/tmp/<file>.md
    endless task update E-<id> --reason-file .endless/tmp/<file>.md
    endless task update E-<id> --notes-file .endless/tmp/<file>.md

A Claude hook refuses a Write/Edit of those names, so you meet this rule before
you break it rather than after.

Your worktree may already contain them, and that is not a second home for the
content: they are on main, your branch was cut from main, so git checked them
out like any other tracked file. They go stale as main moves on, exactly as
`.endless/db-ledger/` already does. Nothing reads them, and nothing writes them
here.

The `.ctrf.json` files are **verify-run reports**: one per passing run of the
task's suite, in the CTRF standard, named for when the run happened and the
commit it tested. `endless task verify` moves each one onto main and commits it
there as the run passes. They are a record, kept forever: never hand-edit one,
never `git add` one, never delete one.

Decisions (`.endless/decisions/ED-<id>.md`) are mirrors under the same rules,
and they do not live under `.endless/tasks/` because a decision belongs to no
task.

## Run one with the runner, never by hand

    endless task verify             # this session's task, or the worktree you are in
    endless task verify E-<id>      # a named task

The runner is the only front door, and it is enough on its own: it resolves the
task, runs in that task's worktree, builds the isolation a suite needs (a temp
`HOME` and `XDG_CONFIG_HOME`, so a suite cannot read or pollute your real config
or the main database), and refuses a task's suite that is not yours. Executing a
script directly skips all of it — which is why every suite here refuses to run
that way.

Every run is refused, before anything executes, while the worktree has
uncommitted changes (Endless's own `.endless/verbs.jsonl` and
`.endless/db-ledger/` aside): a recorded run names the commit it tested, so
commit first. A passing run's report is moved onto main and committed there,
and the `CTRF:` line names it. A failing run's report stays in the user cache,
where the `CTRF:` line names it, until the task passes — then the task's
failed reports are deleted.

It runs a copy of `verify.sh` taken when the run starts, so editing the suite
mid-run cannot break somebody else's run.

It exports these into a suite's environment:

| Variable | What it is |
|----------|------------|
| `ENDLESS_VERIFY_TASK` | the task being verified, `E-NNNN` |
| `ENDLESS_VERIFY_DIR`  | this suite's own directory (the real one, not the copy) |
| `ENDLESS_VERIFY_AGENT_ENV` | the agent environment `as_agent` layers on |
| `TMUX_TMPDIR` | the run's private tmux server, which `with_tmux` uses |

None of them grants permission, and there is no variable that does. `_guard.sh`
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

## Every check runs as a person

Whoever starts the run — you, or the user — every check runs as a **person**.
The runner strips the caller's identity before the suite starts: the agent
harness's variables (`CLAUDECODE`, `CLAUDE_*`, `AI_AGENT`,
`__CFBundleIdentifier`), `ENDLESS_AUDIENCE`, `ENDLESS_SESSION_ID`, `TMUX` and
`TMUX_PANE`. It points `TMUX_TMPDIR` at a private, empty tmux server, so no
check can reach the user's live sessions, and kills that server after the run.
So a suite gives the same verdict to everyone who runs it.

- **Test both sides where they differ.** A check of the agent's experience
  wraps its command in `as_agent`; the person's needs no wrapper:

      assert_not_contains "a person reads no directive" "Handle this yourself" \\
          "$(endless task show E-99999999 --db sandbox 2>&1)"
      assert_contains "an agent reads one" "Handle this yourself" \\
          "$(as_agent endless task show E-99999999 --db sandbox 2>&1)"

- **A check that needs tmux** wraps its command in `with_tmux`, which runs it
  in a fixture pane on the private server. The two combine:
  `with_tmux as_agent endless session id --db sandbox`.
- In a `verify.toml`, a check says the same with `as = "agent"` and
  `tmux = true`.
- **Never read identity from the caller**, and never set these variables by
  hand to "simulate" a person: the runner already did, and a hand-rolled strip
  misses one. In zsh, `env $VARS cmd` does not word-split, so a strip built
  that way silently does nothing — use an array or bash.

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
landed suite asserted, leave the suite alone — that task's own run will tell
whoever works it, on their schedule, with their context.

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
`setup_error` (exit 2), `summary` (exit 0 all-passed, 1 on any failure), and
`as_agent` / `with_tmux` (see **Every check runs as a person**). It
emits TAP to the runner as a side effect, so each assertion lands in the run's
report; you write assertions, not plumbing.

Fold the task's own unit tests in as a first, fail-fast check, so the one suite
is a complete proof for that task at land time.

## A second file of yours: `land.toml`

`.endless/tasks/e-<id>/land.toml` is where a branch tells `endless worktree
land` how it lands — per-task like `verify.toml` and `verify.sh`, written and
committed on the task branch, and read from the landing branch at land time. It
is optional: without one, the land uses its defaults. Settings live in tables,
never at the top level, and a table or key the land does not understand refuses
the land before the merge, naming it. Which settings apply to this project, if
any, is in **Landing the work** in `endless guide orchestration`.
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
