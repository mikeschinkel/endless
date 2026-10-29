"""The rules that govern a project's per-task verification suites.

This module holds the text of `.endless/tasks/CLAUDE.md` and the idempotent
write that places it. It ships with every registered project — a file that
existed only in Endless's own checkout would teach nobody, and the mistakes it
exists to prevent are not Endless-specific: any project using Endless
accumulates per-task suites that look like a regression suite and rot.

It is explicitly NOT counted as a defense. It is prose, and prose is the layer
that already failed: the rule was read, understood, paraphrased into "not
required", and broken. The enforcement is the runner's own-task-only refusal,
which runs before anything in a suite does. This file is the explanation that
refusal points at, placed where an agent working in that directory meets it
before it acts instead of having to go find it in `endless guide orchestration`.

It teaches the manifest form (`verify.toml`) alone, because that is the only
form every project can run (E-2419). The endless repo's legacy script suites,
their `_harness.sh` and its `_guard.sh` exist in that repo only, and are
documented in `_harness.sh`'s own header rather than here.
"""

from pathlib import Path

SUITE_RULES_FILENAME = "CLAUDE.md"

SUITE_RULES = """\
# A task's own directory lives here

One directory per task: `.endless/tasks/e-<id>/`.

## Three kinds of file, and only one of them is yours

| In `e-<id>/`                                  | Whose          | Written by                     |
|-----------------------------------------------|----------------|--------------------------------|
| `verify.toml`, `land.toml`                    | the task's     | you, on the task branch        |
| `justification.md`, `context.md`, `plan.md`, `analysis.md`, `outcome.md`, `reason.md`, `notes.md` | the database's | `endless task update`, on main |
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
or the main database, with a `tmux.socket_dir` of the run's own in that `HOME`,
so a suite cannot reach your Endless tmux servers), and refuses a task's suite
that is not yours. Running a suite's commands by hand skips all of it.

Every run is refused, before anything executes, while the worktree has
uncommitted changes (Endless's own `.endless/verbs.jsonl` and
`.endless/db-ledger/` aside): a recorded run names the commit it tested, so
commit first. A passing run's report is moved onto main and committed there,
and the `CTRF:` line names it. A failing run's report stays in the user cache,
where the `CTRF:` line names it, until the task passes — then the task's
failed reports are deleted.

It exports these into a suite's environment:

| Variable | What it is |
|----------|------------|
| `ENDLESS_VERIFY_TASK` | the task being verified, `E-NNNN` |
| `ENDLESS_VERIFY_DIR`  | this suite's own directory |
| `TMUX_TMPDIR` | the run's private tmux server |

None of them grants permission, and there is no variable that does: a check
that tests for one proves nothing, because one `export` satisfies it.

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
  says `as = "agent"`; the person's needs nothing:

      [[check]]
      name         = "a person sees no agent hint"
      sh           = "the-thing 2>&1"
      not_contains = "<agent-only text>"

      [[check]]
      name     = "an agent sees it"
      as       = "agent"
      sh       = "the-thing 2>&1"
      contains = "<agent-only text>"

- **A check that needs tmux** says `tmux = true`, which runs it in a fixture
  pane on the private server. The two combine.
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

A suite is a manifest, `.endless/tasks/e-<id>/verify.toml`: a list of
`[[check]]` entries the runner runs in order and merges into one report.

    # ── DO NOT EDIT ─────────────────────────────────────────────────────
    # This suite belongs to E-101 and records what was true when E-101
    # landed. Edit it only if you ARE E-101. If your change breaks a check
    # here, leave it alone — see .endless/tasks/CLAUDE.md.
    schema = 1
    task   = "E-101"

    [[check]]
    name   = "unit tests"
    runner = "gotest"
    paths  = ["./internal/thing/..."]

    [[check]]
    name     = "--help says enabled defaults off"
    sh       = "the-thing --help"
    contains = "enabled defaults OFF"

    [[check]]
    name = "the old config file is gone"
    sh   = "test ! -e old.conf"

Put `schema` and `task` before the first `[[check]]`; TOML binds a key that
follows one to that check.

**Native tests first.** Fold the task's own tests in as the first, fail-fast
check, so the one suite is a complete proof for that task at land time. A
`gotest` or `pytest` (or `pytest/uv`) check names `tests`, optionally scoped by
`paths`, and Endless runs the tool and reads its results. Any other runner takes
a literal `command` whose stdout is TAP.

**Tests in a nested module or subproject.** `dir` names the directory a
check runs in, relative to the worktree root — a subdirectory with its own
`go.mod`, `pyproject.toml` or `package.json`. It works on every check, `sh`
included; `paths` resolve against it, and a `pytest` check's report names its
tests with `dir` in front. A `dir` that is absolute, leaves the root, or does
not exist refuses the suite. Setup and teardown still run at the root.

    [[check]]
    name   = "dtx unit tests"
    runner = "gotest"
    dir    = "dtx"
    paths  = ["./..."]

**`sh` checks for the rest.** A fact no native test covers is one shell
command, run with `sh -c` at the worktree root (or the check's `dir`): exit 0
passes, anything else fails. It stands alone — `runner`, `command`, `tests`,
`paths` and `format` are refused beside it — and may add matchers, read against
its stdout:

| Key | Passes when stdout |
|-----|--------------------|
| `contains` | contains this text |
| `not_contains` | does not contain it |
| `equals` | equals it, trailing newlines ignored (`equals = ""` is no output) |

Add `2>&1` to match stderr. A failing `sh` check reports why — the exit code,
or the expected value — with the command's stdout and stderr.

`name` labels any check: its line in the summary, and `labels.check` on every
test it reports. An unnamed `sh` check is named by its command.

A directory that also holds `_harness.sh` has legacy script suites
(`verify.sh`); that file's header documents them. Write no new one.

## A second file of yours: `land.toml`

`.endless/tasks/e-<id>/land.toml` is where a branch tells `endless worktree
land` how it lands — per-task like `verify.toml`, written and
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
