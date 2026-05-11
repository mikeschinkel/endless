"""Thin Python wrapper around the endless-tmux Go binary (E-1236).

The Go binary owns all logic (DB reads, tmux config calls) so the
status-line printer stays under the latency budget. Python here is
just ergonomic surface: `endless tmux apply` / `endless tmux status-line`.
"""

import os
import shutil
import subprocess
import sys

import click


def _binary() -> str:
    """Locate the endless-tmux Go binary or raise a friendly error."""
    path = shutil.which("endless-tmux")
    if not path:
        raise click.ClickException(
            "endless-tmux binary not found on PATH. Build it: just build"
        )
    return path


def run_apply(hotkey: str, status_interval: int) -> None:
    """Shell out to `endless-tmux apply` with the given options."""
    cmd = [
        _binary(), "apply",
        "--hotkey", hotkey,
        "--status-interval", str(status_interval),
    ]
    # Inherit stdio so the user sees the tmux output / errors in real time.
    result = subprocess.run(cmd)
    if result.returncode != 0:
        sys.exit(result.returncode)


def run_status_line() -> None:
    """Shell out to `endless-tmux status-line` and pass stdout through.

    Not normally typed by users — tmux's status-format[1] invokes the Go
    binary directly. Provided for parity and manual debugging.
    """
    result = subprocess.run([_binary(), "status-line"], capture_output=True, text=True)
    if result.stdout:
        # No newline — `#()` substitution wants the raw bytes.
        click.echo(result.stdout, nl=False)
    if result.returncode != 0 and result.stderr:
        click.echo(result.stderr, err=True, nl=False)
    sys.exit(result.returncode)
