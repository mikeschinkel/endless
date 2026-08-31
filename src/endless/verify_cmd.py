"""CLI implementation for `endless task verify` (E-1603).

Thin wrapper around the `endless-go verify` subcommand — the verification
runner. This module holds no verification logic: discovery, per-run isolation
(temp working dir + temp HOME/XDG_CONFIG_HOME), the own-task-only refusal,
running the suite's checks, normalizing to CTRF, and the pass/fail exit code all
live in Go (internal/verifycmd). Here we only resolve WHICH task to verify and
WHERE to run it, then pass stdout/stderr and the exit code straight through.

Both resolutions are deliberately the Python side's job: it owns
session/worktree context. The Go runner stays a pure function of a task id and
a working directory, so nothing about sessions, worktrees or projects has to
exist inside it.
"""

import re
import subprocess
from pathlib import Path

import click

from endless.event_bridge import _resolve_endless_go
from endless.task_cmd import _current_session_task_id

_WORKTREE_RE = re.compile(r"/\.endless/worktrees/e-(\d+)(?:/|$)")


def run_verify(item_id: int | None, keep: bool) -> None:
    """Entry point bound by cli.py.

    `item_id` is the E- stripped task number (via the TASK_ID click type), or
    None to resolve the task from context. Shells to
    `endless-go verify [--keep] E-<id>` and exits with its return code.
    """
    resolved = item_id
    if resolved is None:
        resolved = _resolve_task_id()
    if resolved is None:
        raise click.ClickException(
            "no task id given, and neither this session nor the current "
            "directory names one; pass an explicit task id, e.g. "
            "`endless task verify E-101`."
        )

    task_id = f"E-{resolved}"
    binary = _resolve_endless_go()

    cmd = [binary, "verify"]
    if keep:
        cmd.append("--keep")
    cmd.append(task_id)

    result = subprocess.run(cmd, cwd=_run_dir(resolved))
    raise SystemExit(result.returncode)


def _resolve_task_id() -> int | None:
    """The task to verify when the caller named none.

    Two sources, in order:

      1. The current session's active task. This is the primary one and the
         documented meaning of a bare `endless task verify`: verify what you
         are working on. It reads the database, so it is best-effort — a
         self-dev worktree with no `--db` refuses that read, and a refusal must
         not become the answer to a question the cwd can also answer.
      2. The task whose worktree cwd is inside. Structural, needs no database,
         and correct by construction: a suite is a proof about a checkout, and
         a task worktree's path names its task.

    Same two sources, same order, as `just land` — minus the tmux hop, because
    Python resolves the session directly rather than shelling out for it.
    """
    task_id = _session_task_id()
    if task_id is not None:
        return task_id
    return _cwd_task_id()


def _session_task_id() -> int | None:
    """The current session's active task, or None if it cannot be read.

    Every failure is None, not an exception: this is one of two ways to answer
    the question, and a database that refuses to open is a reason to try the
    other one rather than to give up.
    """
    try:
        return _current_session_task_id()
    except Exception:
        return None


def _cwd_task_id(cwd: Path | None = None) -> int | None:
    """The task whose worktree contains cwd, from the path alone."""
    match = _WORKTREE_RE.search(str(cwd if cwd is not None else Path.cwd()))
    return int(match.group(1)) if match else None


def _run_dir(task_id: int) -> str | None:
    """Where to run the suite: that task's worktree when it exists, else cwd.

    A suite is a pre-land gate. It proves the CANDIDATE tree, so it has to run
    against the candidate tree — asking for a task from the main checkout would
    otherwise discover main's copy of that suite and run it against code the
    task has not landed yet. Doing this here rather than making the caller `cd`
    first is what makes one command sufficient from anywhere in the project.

    Returns None (meaning "inherit cwd") when the worktree does not exist: a
    task whose worktree was reaped, or a project not using worktrees at all,
    still verifies from wherever the caller is standing.
    """
    root = _main_checkout()
    if root is None:
        return None
    worktree = root / ".endless" / "worktrees" / f"e-{task_id}"
    return str(worktree) if worktree.is_dir() else None


def _main_checkout(cwd: Path | None = None) -> Path | None:
    """The main checkout above cwd, whether cwd is in it or in a worktree.

    Keyed on the path shape rather than git, because the caller may not be
    inside a repository at all and because the worktree layout is Endless's own
    convention: <root>/.endless/worktrees/e-NNN. Falls back to walking up for a
    .endless directory when cwd is not inside a worktree.
    """
    start = (cwd if cwd is not None else Path.cwd()).resolve()
    match = _WORKTREE_RE.search(str(start))
    if match:
        return Path(str(start)[: match.start()])
    for candidate in [start, *start.parents]:
        if (candidate / ".endless").is_dir():
            return candidate
    return None
