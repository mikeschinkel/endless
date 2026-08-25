"""Thin Python wrapper around the `endless-go tmux` subcommand (E-1236).

The Go binary owns all logic (DB reads, tmux config calls) so the
status-line printer stays under the latency budget. Python here is
just ergonomic surface: `endless tmux apply` / `endless tmux status-line`.

`run_active_id` is the exception to "tmux surface": it backs the
user-facing `endless task id` (and its `endless tmux task` alias), which
is a session-orientation verb rather than a tmux one. It lives here
because the thing it wraps is `endless-go tmux active-id`, and one
module owning that shellout beats two spellings of it. E-1302.
"""

import os
import shutil
import subprocess
import sys

import click


def _binary() -> str:
    """Locate the endless-go Go binary or raise a friendly error."""
    path = shutil.which("endless-go")
    if not path:
        raise click.ClickException(
            "endless-go binary not found on PATH."
        )
    return path


def run_apply(hotkey: str, status_interval: int) -> None:
    """Shell out to `endless-go tmux apply` with the given options."""
    cmd = [
        _binary(), "tmux", "apply",
        "--hotkey", hotkey,
        "--status-interval", str(status_interval),
    ]
    # Inherit stdio so the user sees the tmux output / errors in real time.
    result = subprocess.run(cmd)
    if result.returncode != 0:
        sys.exit(result.returncode)


def run_init(hotkey: str, status_interval: int) -> None:
    """Shell out to `endless-go tmux init` with the given options.

    Self-gates via @server_uuid: first call after a tmux server start
    runs reset + apply and stamps a UUID; subsequent calls no-op.
    """
    cmd = [
        _binary(), "tmux", "init",
        "--hotkey", hotkey,
        "--status-interval", str(status_interval),
    ]
    result = subprocess.run(cmd)
    if result.returncode != 0:
        sys.exit(result.returncode)


def run_status_line() -> None:
    """Shell out to `endless-go tmux status-line` and pass stdout through.

    Not normally typed by users — tmux's status-format[1] invokes the Go
    binary directly. Provided for parity and manual debugging.
    """
    result = subprocess.run([_binary(), "tmux", "status-line"], capture_output=True, text=True)
    if result.stdout:
        # No newline — `#()` substitution wants the raw bytes.
        click.echo(result.stdout, nl=False)
    if result.returncode != 0 and result.stderr:
        click.echo(result.stderr, err=True, nl=False)
    sys.exit(result.returncode)


def _no_task_message(pane: str | None, invoked_as: str) -> str:
    """Diagnostic for the no-active-task exit, phrased for how we got here.

    A session's task binding is keyed by the tmux pane it runs in, so "no
    task" has two very different causes and two different fixes. Saying
    which one applies is the whole value of the message — the Go side exits
    silently because tmux redraws it many times a minute.
    """
    if not (pane or os.environ.get("TMUX_PANE")):
        return (
            f"{invoked_as}: no active task — a session's task is bound to the "
            f"tmux pane it runs in, and this shell is not inside tmux."
        )
    return (
        f"{invoked_as}: no active task for this session. "
        f"Claim one with `endless task claim E-NNNN`."
    )


def run_active_id(pane: str | None, invoked_as: str) -> None:
    """Print the current session's task as one bare `E-NNNN` line.

    Backs both `endless task id` and its `endless tmux task` alias. The
    binding lives in the database (`sessions.task_id`, resolved from the
    pane), so the Go binary owns the read and Python never touches SQLite.

    stdout stays a single bare id so `$(endless task id)` captures
    something usable; every diagnostic goes to stderr.
    """
    cmd = [_binary(), "tmux", "active-id"]
    if pane:
        cmd += ["--pane", pane]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode == 0:
        click.echo(result.stdout.strip())
        return
    # active-id exits 1 both for "no task" (silent) and for a real DB failure
    # (which it explains). Relay its explanation when there is one rather than
    # overwriting a genuine error with a guess about panes.
    stderr = result.stderr.strip()
    click.echo(stderr or _no_task_message(pane, invoked_as), err=True)
    sys.exit(result.returncode or 1)
