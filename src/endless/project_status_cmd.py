"""CLI implementation for `endless project status` / `project monitor` (E-1976,
rebuilt around tasks by E-2156).

The project-scoped counterpart to `session status` / `session monitor`: the
project's open urgent, now and next tasks, in three lists. `project status`
prints every row; `project monitor` loops the same frame until interrupted,
cutting only its third list to fit the pane; `project monitor --tmux` opens the
dedicated two-pane tmux session that loop is meant to run in.

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

from endless import agent_help


# --sort's choices: both reverse-chronological, by last update or by filing.
SORT_KEYS = ("updated", "id")
DEFAULT_SORT = "updated"


def _go_binary() -> str:
    go_bin = shutil.which("endless-go")
    if not go_bin:
        # REPORT, even though inside Endless's own checkout an agent could
        # build the binary: everywhere else this runs, endless-go is part of
        # the install and putting it on PATH is the user's machine to change.
        raise agent_help.report(
            "endless-go is not on PATH, and this view is rendered entirely by "
            "it. Nothing was shown.",
            "installing endless-go, or putting it on PATH, on their own "
            "machine",
            text="endless-go binary not found on PATH.",
        )
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
        # Stdio is inherited, so the Go side's own classified refusal has
        # already been written to this terminal. Only the status is left to
        # carry.
        agent_help.passthrough_exit(result.returncode)


def project_status_resolve(
    project: str | None,
    monitor: bool = False,
    later: bool = False,
    sort: str = DEFAULT_SORT,
    as_json: bool = False,
) -> None:
    """Render `project status` — every row, once — or `project monitor`'s live loop."""
    args = [_go_binary(), "project-status", "--sort", sort]
    if project:
        args += ["--project", project]
    if monitor:
        args.append("--monitor")
    if later:
        args.append("--later")
    if as_json:
        args.append("--json")
    _run(args)


def project_window_resolve(project: str | None, no_switch: bool = False,
                           use_existing: bool = False) -> None:
    """Open (or switch to) the dedicated two-pane tmux session for the monitor."""
    args = [_go_binary(), "project-window"]
    if project:
        args += ["--project", project]
    if no_switch:
        args.append("--no-switch")
    if use_existing:
        args.append("--use-existing")
    _run(args)
