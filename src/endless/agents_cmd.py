"""`endless agents` — list working background agents scoped to the active epic.

A plain-text, epic-scoped supplement to Claude Code's global Agent View, which
can't filter background agents by epic (E-1621). The TUI version is E-1622.

The DB read lives in Go (no new Python DB reads, per E-1486): this module
resolves the caller's session when needed, shells out to
`endless-go session-query list-bg-agents`, and formats the returned JSON as a
table. Scope resolution:

  - ``--epic E-NNNN`` → list agents under that epic.
  - ``--all``        → list every working bg agent in the current project.
  - neither          → auto-resolve the epic from the caller's session
                       (``sessions.active_epic_id``); error with guidance when
                       no session or no active epic resolves.
"""

import json as json_mod
import subprocess

import click

from endless import config


def list_agents(epic_id: int | None = None, show_all: bool = False) -> None:
    """Render the `endless agents` listing. See module docstring for scope rules."""
    if epic_id is not None and show_all:
        raise click.ClickException("pass either --epic or --all, not both")

    args = ["session-query", "list-bg-agents"]
    if show_all:
        from endless.session_cmd import _project_root_for_cwd
        args += ["--all", "--project-root", str(_project_root_for_cwd())]
    elif epic_id is not None:
        args += ["--epic-id", str(epic_id)]
    else:
        from endless.task_cmd import _current_endless_session_id
        sid = _current_endless_session_id()
        if sid is None:
            raise click.ClickException(
                "could not resolve the current session; pass --epic E-NNNN or --all"
            )
        args += ["--session-id", str(sid)]

    result = _run_go(args)

    scope = result.get("scope")
    resolved_epic = result.get("epic_id")
    agents = result.get("agents") or []

    if scope == "epic" and resolved_epic is None:
        # Auto-resolve found no epic for the caller's session (E-1621 / Q3).
        raise click.ClickException(
            "no active epic resolved for this session; pass --epic E-NNNN or --all"
        )

    _print_table(agents, resolved_epic if scope == "epic" else None)


def _run_go(args: list[str]) -> dict:
    """Invoke `endless-go <args>` threading the resolved DB context, return JSON."""
    import shutil
    go_bin = shutil.which("endless-go")
    if not go_bin:
        raise click.ClickException("endless-go binary not found on PATH.")
    config.require_db_context()
    try:
        result = subprocess.run(
            [go_bin, *config.go_db_context_args(), *args],
            capture_output=True, text=True, timeout=5,
        )
    except (FileNotFoundError, subprocess.SubprocessError) as e:
        raise click.ClickException(f"endless-go failed: {e}")
    if result.returncode != 0:
        raise click.ClickException(result.stderr.strip() or "endless-go failed")
    try:
        return json_mod.loads(result.stdout)
    except ValueError:
        raise click.ClickException("endless-go returned malformed output")


def _print_table(agents: list[dict], epic_id: int | None) -> None:
    """Print the agents as an aligned plain-text table with a scope header."""
    scope_label = f"under E-{epic_id}" if epic_id is not None else "in this project"
    if not agents:
        click.echo(f"No background agents working {scope_label}.")
        return

    click.echo(f"Background agents working {scope_label} ({len(agents)}):")
    click.echo("")

    rows = []
    for a in agents:
        task = a.get("task_id")
        rows.append((
            str(a.get("id", "")),
            a.get("short_id") or "-",
            f"E-{task}" if task is not None else "-",
            (a.get("started_at") or "").replace("T", " "),
            a.get("title") or "",
        ))

    headers = ("ID", "SHORT", "TASK", "STARTED", "TITLE")
    widths = [
        max(len(headers[i]), max(len(r[i]) for r in rows))
        for i in range(len(headers))
    ]
    fmt = "  ".join(f"{{:<{w}}}" for w in widths)
    click.echo("  " + fmt.format(*headers))
    for r in rows:
        click.echo("  " + fmt.format(*r))
