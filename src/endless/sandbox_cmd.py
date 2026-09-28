"""CLI implementation for `endless sandbox reset` (E-1608).

Thin wrapper around `endless-go sandbox reset`, which clears the current
worktree's sandbox, adds Endless's standard contents, and runs the project's
.endless/hooks/seed-sandbox.sh. All of that lives in Go
(internal/sandboxcmd/reset.go); here we only exec it, so worktree creation, a
user, and `endless task verify` all go through the one implementation.
"""

import subprocess
from pathlib import Path

import click

from endless.event_bridge import _resolve_endless_go


def run_reset() -> None:
    """Entry point bound by cli.py: reset the sandbox of the worktree at cwd."""
    result = subprocess.run([_resolve_endless_go(), "sandbox", "reset"])
    raise SystemExit(result.returncode)


def reset_after_create(worktree_path: Path) -> None:
    """Seed a freshly created worktree's sandbox via `sandbox reset`.

    Non-fatal and loud, like the post-worktree-create hook it follows: the
    worktree is kept, and the warning names the command that finishes the job.
    """
    rerun = f"cd {worktree_path} && endless sandbox reset"
    try:
        binary = _resolve_endless_go()
    except click.ClickException as e:
        click.echo(
            click.style("⚠ could not seed the worktree's sandbox", fg="yellow")
            + f"\n    {e.format_message()}\n    Re-run after fixing:\n        {rerun}",
            err=True,
        )
        return
    result = subprocess.run(
        [binary, "sandbox", "reset"],
        cwd=str(worktree_path),
        stdout=subprocess.DEVNULL,
    )
    if result.returncode != 0:
        click.echo(
            click.style(
                f"⚠ sandbox reset exited {result.returncode}", fg="yellow"
            )
            + f"\n    worktree: {worktree_path}\n"
            f"    The worktree was KEPT. Finish seeding its sandbox with:\n"
            f"        {rerun}",
            err=True,
        )
