"""CLI implementation for `endless project status` / `project monitor` (E-1976).

The project-scoped counterpart to `session status` / `session monitor`: what is
claiming your attention across every concurrent session in one project.
`project status` prints one frame, `project monitor` loops the same frame until
interrupted, and `project monitor --tmux` opens the dedicated two-pane tmux
session that loop is meant to run in.

This module performs no DB access. Everything — the query, the ranking, the
render, the redraw loop and the tmux layout — lives in Go
(`internal/projectstatuscmd`), reached through the `endless-go` binary, per
E-894's "DB access in Go" policy. Python owns the Click surface and nothing else,
which is also why the six-files-still-reading-SQLite count does not move.

Stdout and stderr are inherited rather than captured, so the Go side detects the
real terminal width and color profile, and so the live loop paints straight
through to the tty.
"""

import shutil
import subprocess

import click

from endless import rowcap


# `project status` caps PER GROUP rather than per render (see
# internal/projectstatuscmd defaultGroupCap): a single frame-wide cap with
# `unverified` ranked near the top would spend every row on the backlog and push
# the sessions the view exists to triage off the bottom. Ten rather than
# rowcap's twenty because the monitor lives in a tmux pane sized to its own
# frame.
DEFAULT_GROUP_CAP = 10

# The Click decorator for the two flags. Built from rowcap's factory so the
# mutual-exclusion error, the `--limit 0` refusal and the flag spelling stay
# identical to every other capped surface — only the number and the noun differ.
group_limit_options = rowcap.limit_options_for(
    DEFAULT_GROUP_CAP, unit="rows per group"
)


def _go_binary() -> str:
    go_bin = shutil.which("endless-go")
    if not go_bin:
        raise click.ClickException("endless-go binary not found on PATH.")
    return go_bin


def _run(args: list[str]) -> None:
    """Run one endless-go invocation, inheriting this process's stdio.

    KeyboardInterrupt is swallowed: Ctrl-C is the intended way to leave the live
    monitor, and the Go child has already restored the cursor from its own SIGINT
    handler by the time the signal reaches us.
    """
    try:
        result = subprocess.run(args)
    except KeyboardInterrupt:
        return
    if result.returncode != 0:
        raise SystemExit(result.returncode)


def project_status_resolve(
    project: str | None,
    monitor: bool = False,
    show_all: bool = False,
    limit: int | None = None,
    no_limit: bool = False,
    as_json: bool = False,
) -> None:
    """Render `project status` — one frame, or `project monitor`'s live loop.

    The cap is resolved HERE rather than in Go so `--limit` and `--no-limit`
    behave identically to every other Endless listing, errors included. What
    crosses the boundary is a resolved number: `--no-limit` when uncapped, an
    explicit `--limit N` otherwise. Go never re-derives a default it could get
    wrong.
    """
    cap = rowcap.resolve_cap(
        limit, no_limit, machine=as_json, default=DEFAULT_GROUP_CAP
    )

    args = [_go_binary(), "project-status"]
    if project:
        args += ["--project", project]
    if monitor:
        args.append("--monitor")
    if show_all:
        args.append("--all")
    if as_json:
        args.append("--json")
    if cap is None:
        args.append("--no-limit")
    else:
        args += ["--limit", str(cap)]
    _run(args)


def project_window_resolve(project: str | None, no_switch: bool = False) -> None:
    """Open (or switch to) the dedicated two-pane tmux session for the monitor."""
    args = [_go_binary(), "project-window"]
    if project:
        args += ["--project", project]
    if no_switch:
        args.append("--no-switch")
    _run(args)
