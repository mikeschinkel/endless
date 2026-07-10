"""CLI implementation for `endless verify` (E-1603).

Thin wrapper around the `endless-go verify` subcommand — the Tier-0
verification runner. This module holds no orchestration logic: discovery,
per-run isolation (temp working dir + temp HOME/XDG_CONFIG_HOME), running the
suite's checks, normalizing to CTRF, and the pass/fail exit code all live in Go
(internal/verifycmd). Here we only resolve which task to verify and which
endless-go binary to exec, then pass stdout/stderr and the exit code straight
through.

Resolving "the cwd's task" (when no id is given) is intentionally the Python
side's job: it owns session/worktree context. The id is then handed to
endless-go explicitly, so the Go runner stays a pure function of a task id.
"""

import subprocess

import click

from endless.event_bridge import _resolve_endless_go
from endless.task_cmd import _current_session_active_task_id


def run_verify(item_id: int | None, keep: bool) -> None:
    """Entry point bound by cli.py.

    `item_id` is the E- stripped task number (via the TASK_ID click type), or
    None to verify the current session's active task. Shells to
    `endless-go verify [--keep] E-<id>` and exits with its return code.
    """
    resolved = item_id
    if resolved is None:
        resolved = _current_session_active_task_id()
    if resolved is None:
        raise click.ClickException(
            "no task id given and no active task for this session; "
            "pass an explicit task id, e.g. `endless verify E-1234`."
        )

    task_id = f"E-{resolved}"
    binary = _resolve_endless_go()

    cmd = [binary, "verify"]
    if keep:
        cmd.append("--keep")
    cmd.append(task_id)

    result = subprocess.run(cmd)
    raise SystemExit(result.returncode)
