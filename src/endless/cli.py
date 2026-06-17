"""Endless CLI — Click entry point."""

import os
import sys
from pathlib import Path

import click

from endless import __version__
from endless import agent_help
from endless.agent_help import AgentHelpMixin

# Subcommands that are safe to run inside an `endless-go sandbox` subshell
# (no project/global I/O — pure stdout). Anything else is refused at
# CLI entry when ENDLESS_SANDBOX is set. New subcommands inherit the
# refusal automatically; opt in here only with a one-line justification
# in the diff.
SANDBOX_SAFE_SUBCOMMANDS = frozenset({
    "shell-init",  # static stdout, no I/O
})


class TaskIDType(click.ParamType):
    """Click parameter type that accepts task IDs with optional E- prefix.

    Also routes ED-NN inputs (decision IDs) to a redirect error so users
    pointing a task verb at a decision get a clear, actionable message
    instead of an opaque "not found".
    """
    name = "task_id"

    def convert(self, value, param, ctx):
        if isinstance(value, int):
            return value
        s = str(value).strip()
        if s.upper().startswith("ED-"):
            self.fail(
                f"{value!r} is a decision ID (ED-NN); this command operates on tasks. "
                f"Use the corresponding 'decision' verb instead "
                f"(e.g. 'endless decision accept {value}').",
                param, ctx,
            )
        if s.upper().startswith("E-"):
            s = s[2:]
        try:
            return int(s)
        except ValueError:
            self.fail(f"{value!r} is not a valid task ID (expected integer or E-NNN)", param, ctx)


TASK_ID = TaskIDType()


class DecisionIDType(click.ParamType):
    """Click parameter type that accepts decision IDs with optional ED- prefix.

    E-NN inputs (task IDs) are redirected with a clear error pointing at the
    task verbs, so a user / agent that confuses the namespaces gets actionable
    output rather than a silent "not found".
    """
    name = "decision_id"

    def convert(self, value, param, ctx):
        if isinstance(value, int):
            return value
        s = str(value).strip()
        if s.upper().startswith("ED-"):
            s = s[3:]
        elif s.upper().startswith("E-"):
            self.fail(
                f"{value!r} is a task ID (E-NN); this command operates on decisions. "
                f"Use the corresponding 'task' verb instead.",
                param, ctx,
            )
        try:
            return int(s)
        except ValueError:
            self.fail(
                f"{value!r} is not a valid decision ID (expected integer or ED-NNN)",
                param, ctx,
            )


DECISION_ID = DecisionIDType()


class TaskOrDecisionIDType(click.ParamType):
    """Click type that accepts either E-NN or ED-NN and returns (kind, id).

    Used by kind-agnostic dispatchers (`task link --to <id>`, `decision link
    --to <id>`) so the verb can route on the parsed kind without re-parsing.
    """
    name = "task_or_decision_id"

    def convert(self, value, param, ctx):
        if isinstance(value, tuple):
            return value
        s = str(value).strip()
        upper = s.upper()
        if upper.startswith("ED-"):
            try:
                return ("decision", int(s[3:]))
            except ValueError:
                self.fail(f"{value!r} is not a valid decision ID", param, ctx)
        if upper.startswith("E-"):
            try:
                return ("task", int(s[2:]))
            except ValueError:
                self.fail(f"{value!r} is not a valid task ID", param, ctx)
        try:
            return ("task", int(s))
        except ValueError:
            self.fail(
                f"{value!r} is not a valid ID (expected E-NN, ED-NN, or integer)",
                param, ctx,
            )


TASK_OR_DECISION_ID = TaskOrDecisionIDType()

TASK_STATUSES = ["needs_plan", "ready", "in_progress",
                 "verify", "confirmed", "assumed", "completed",
                 "blocked", "revisit", "declined", "obsolete"]


class MultiChoice(click.ParamType):
    """Click parameter type that accepts comma-separated values from a fixed set."""
    name = "multi_choice"

    def __init__(self, choices: list[str]):
        self.choices = choices

    def convert(self, value, param, ctx):
        if value is None:
            return None
        parts = [v.strip() for v in str(value).split(",")]
        invalid = [p for p in parts if p not in self.choices]
        if invalid:
            self.fail(
                f"Invalid value(s): {', '.join(repr(v) for v in invalid)}. "
                f"Choose from: {', '.join(self.choices)}",
                param, ctx,
            )
        return parts


def _scan_db_choice(argv: list[str]) -> str | None:
    """Return the LAST `--db <val>` / `--db=<val>` value in argv, or None.

    Matches DBAwareGroup.main()'s consumption rule (last value wins) so the
    pre-Click re-exec gate (E-1513) sees the same effective --db as the
    cleaned-argv apply_db_choice call further down. Argv-agnostic helper so
    it's testable without a click.testing.CliRunner.
    """
    last: str | None = None
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--db":
            if i + 1 < len(argv):
                last = argv[i + 1]
            i += 2
            continue
        if a.startswith("--db="):
            last = a[len("--db="):]
            i += 1
            continue
        i += 1
    return last


class DBAwareGroup(click.Group):
    """Click group that accepts the global --db flag in ANY argument position.

    Click only parses group options *before* the subcommand, which forces an
    awkward `endless --db main task show` and rejects the natural
    `endless task show --db main`. This override pre-extracts `--db <val>` /
    `--db=<val>` from anywhere in argv before normal parsing (mirroring the Go
    side's monitor.ConsumeDBContextFlag), applies it via config.apply_db_choice
    (the single resolver), and hands the cleaned args to Click. Because
    click.testing.CliRunner.invoke also calls Command.main(), the test suite
    gets the same position-agnostic behavior, and there is one extraction site
    (no separate entry-point wrapper, no pyproject change). E-1476.
    """

    def main(self, args=None, **extra):
        from endless import config

        argv = list(args) if args is not None else sys.argv[1:]

        # E-1513: under `--db sandbox` inside a self-dev worktree, re-exec into
        # the worktree's Python source via `uv run --directory <worktree>
        # endless ...`. The global `endless` script is the editable install of
        # main's source (one uv tool install), so without this gate the
        # worktree's Python changes never get exercised against its sandbox
        # DB — the symmetric gap to E-1510 (Go binary self-detect).
        # worktree_python_reexec_target's source_file check is the re-entrancy
        # guard: once we've re-exec'd, this module loads from inside the
        # worktree and the helper returns None.
        if _scan_db_choice(argv) == "sandbox":
            target = config.worktree_python_reexec_target()
            if target is not None:
                os.execvp(
                    "uv",
                    ["uv", "run", "--directory", str(target), "endless", *argv],
                )

        cleaned: list[str] = []
        db_value: str | None = None
        agent_view = False
        no_session = False
        i = 0
        while i < len(argv):
            arg = argv[i]
            if arg == "--db":
                if i + 1 >= len(argv):
                    click.echo("Error: --db requires a value: main or sandbox", err=True)
                    sys.exit(2)
                db_value = argv[i + 1]
                i += 2
                continue
            if arg.startswith("--db="):
                db_value = arg[len("--db="):]
                i += 1
                continue
            # --agent-view forces the agent --help rendering in any position so a
            # human can see/debug what an agent sees. Consumed here (like --db)
            # so per-command --help doesn't reject it as an unknown option.
            if arg == "--agent-view":
                agent_view = True
                i += 1
                continue
            # --no-session (E-1444): downgrade actor.kind to system at the write
            # layer for callers with no Claude session to attribute to (plain
            # shell, cron, scripts). Consumed here so it's accepted in any
            # position and applies uniformly to every subcommand's emit_event.
            if arg == "--no-session":
                no_session = True
                i += 1
                continue
            cleaned.append(arg)
            i += 1
        if agent_view:
            agent_help.set_agent_view(True)
        if no_session:
            config.NO_SESSION = True
        if db_value is not None:
            # apply_db_choice validates the value and resolves+pins the context;
            # it raises ValueError for an unknown value or for --db sandbox
            # outside a worktree. Format like a Click usage error (exit 2).
            try:
                config.apply_db_choice(db_value)
            except ValueError as e:
                click.echo(f"Error: {e}", err=True)
                sys.exit(2)
        return super().main(args=cleaned, **extra)


class AgentAwareCommand(AgentHelpMixin, click.Command):
    """Leaf command whose --help is augmented for agents (E-1502)."""


class AgentAwareGroup(AgentHelpMixin, DBAwareGroup):
    """Group whose --help is augmented for agents, and whose children inherit
    the augmenting classes so one root `cls=` propagates across the whole tree."""


# Make the decorators on any AgentAwareGroup (including the root) mint augmented
# children, so every command and subgroup gets the agent --help treatment.
AgentAwareGroup.command_class = AgentAwareCommand
AgentAwareGroup.group_class = AgentAwareGroup


@click.group(
    cls=AgentAwareGroup,
    epilog="Inside a self-dev worktree, pass --db (accepted in any position): "
    "--db main (the real ledger) or --db sandbox (this worktree's test DB). "
    "Resolve paths with `endless db path --db=main|sandbox`.",
)
@click.version_option(__version__, prog_name="endless")
@click.pass_context
def main(ctx):
    """Project awareness system for solo developers."""
    try:
        os.getcwd()
    except FileNotFoundError:
        click.echo(
            "endless: current working directory no longer exists "
            "(was it deleted from another shell?). cd to an existing "
            "directory and retry.",
            err=True,
        )
        ctx.exit(1)
    # E-1429/E-1476: --db is consumed and applied by DBAwareGroup.main() before
    # Click parses (so it works in any position). Enforcement of "required
    # inside a worktree" happens at the DB-access choke points (db.get_db /
    # go_db_context_args), so commands that never touch the DB stay flag-free.
    sandbox = os.environ.get("ENDLESS_SANDBOX")
    if sandbox and ctx.invoked_subcommand not in SANDBOX_SAFE_SUBCOMMANDS:
        click.echo(
            f"endless: refusing to run '{ctx.invoked_subcommand}' inside "
            f"endless-go sandbox at {sandbox}",
            err=True,
        )
        click.echo(
            "    Run 'exit' to leave the sandbox subshell, "
            "or open a new terminal.",
            err=True,
        )
        ctx.exit(1)


@main.command()
@click.argument("path", default=".", type=click.Path(exists=True))
@click.option("--infer", is_flag=True, help="Auto-detect metadata, skip prompts")
@click.option("--name", default=None, help="Project identifier")
@click.option("--label", default=None, help="Display name")
@click.option("--desc", default=None, help="Description")
@click.option("--lang", default=None, help="Primary language")
@click.option("--status", default=None,
              type=click.Choice(["active", "paused", "archived", "idea"]))
def register(path, infer, name, label, desc, lang, status):
    """Register a directory as a project."""
    from endless.register import register_project
    register_project(
        Path(path).resolve(),
        name=name, label=label, description=desc,
        language=lang, status=status, infer=infer,
    )


@main.command()
@click.argument("name")
def unregister(name):
    """Unregister a project (preserves .endless config on disk)."""
    from endless.unregister import unregister_project
    unregister_project(name)


@main.command()
@click.argument("name")
def purge(name):
    """Delete .endless/ directory and add to ignore list."""
    from endless.unregister import purge_project
    purge_project(name)


@main.command("set")
@click.argument("expression")
@click.option("--path", default=None,
              help="Path segment to disambiguate duplicate names")
def set_cmd(expression, path):
    """Set a project field. Usage: endless set <name>.<field>=<value>"""
    from endless.set_cmd import set_field
    set_field(expression, path_hint=path)


@main.command()
@click.argument("old_name")
@click.argument("new_name")
@click.option("--path", default=None,
              help="Path segment to disambiguate duplicate names")
def rename(old_name, new_name, path):
    """Rename a project."""
    from endless.rename import rename_project
    rename_project(old_name, new_name, path_hint=path)


@main.command("list")
@click.option("--status", default=None,
              type=click.Choice(["active", "paused", "archived", "idea"]),
              help="Filter by status")
@click.option("--group", is_flag=True, help="Group by group name")
def list_cmd(status, group):
    """List registered projects."""
    from endless.list_cmd import list_projects
    list_projects(status_filter=status, group=group)


@main.command()
@click.argument("name", default=None, required=False)
def status(name):
    """Show detailed status of a project."""
    from endless.status import show_status
    show_status(name)


@main.command()
@click.option("--project", default=None, help="Scan a single project")
def scan(project):
    """Scan and reconcile projects."""
    from endless.scan import run_scan
    run_scan(project_name=project)


@main.command()
@click.option("--port", default=8484, help="Port to serve on")
@click.option("--watch", is_flag=True, help="Auto-restart when the binary is rebuilt")
def serve(port, watch):
    """Start the web dashboard."""
    import shutil
    import subprocess
    serve_bin = shutil.which("endless-go")
    if not serve_bin:
        raise click.ClickException(
            "endless-go binary not found on PATH."
        )
    # E-1429: require an explicit --db inside a worktree, then thread it.
    from endless import config
    config.require_db_context()
    db_args = config.go_db_context_args()
    if not watch:
        subprocess.run([serve_bin, *db_args, "serve", str(port)])
        return

    import os
    import signal
    import time

    def _get_mtime(path):
        try:
            return os.stat(path).st_mtime
        except OSError:
            return 0

    last_mtime = _get_mtime(serve_bin)
    proc = None
    try:
        while True:
            click.echo(
                click.style("•", fg="cyan")
                + f" Starting endless-go serve (watching {serve_bin} for changes)"
            )
            proc = subprocess.Popen([serve_bin, *db_args, "serve", str(port)])
            while True:
                time.sleep(1)
                if proc.poll() is not None:
                    # Process exited on its own
                    if not watch:
                        return
                    break
                current_mtime = _get_mtime(serve_bin)
                if current_mtime != last_mtime:
                    last_mtime = current_mtime
                    from datetime import datetime
                    ts = datetime.now().strftime("%-I:%M %p").lower()
                    click.echo(
                        click.style("•", fg="yellow")
                        + f" Binary changed, restarting at {ts}..."
                    )
                    proc.terminate()
                    try:
                        proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()
                    break
    except KeyboardInterrupt:
        click.echo("\n" + click.style("•", fg="cyan") + " Shutting down...")
        if proc and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()


@main.command("guide")
@click.argument("section", required=False)
@click.option("--list", "list_sections", is_flag=True,
              help="List available section slugs and exit.")
def guide(section, list_sections):
    """Output the session guide (or a specific section).

    With no argument, prints the top-level index. Pass a section slug
    (e.g. 'spawn', 'worktree') to print just that section. Use --list
    to enumerate available sections.
    """
    guide_dir = (
        Path(__file__).resolve().parent.parent.parent / "docs" / "guide"
    )
    if not guide_dir.is_dir():
        raise click.ClickException(
            f"Guide directory not found at {guide_dir}"
        )

    available = sorted(
        p.stem for p in guide_dir.glob("*.md") if p.stem != "index"
    )

    if list_sections:
        for slug in available:
            click.echo(slug)
        return

    if section is None:
        target = guide_dir / "index.md"
        if not target.exists():
            raise click.ClickException(
                f"Guide index not found at {target}"
            )
    else:
        if section == "index" or section not in available:
            raise click.ClickException(
                f"Unknown section '{section}'. Available: "
                + ", ".join(available)
            )
        target = guide_dir / f"{section}.md"

    click.echo(target.read_text())


_SHELL_INIT_SNIPPET = """\
# >>> endless shell helpers (regenerate via 'endless shell-init') >>>

# _endless_run — pick the right endless CLI for the current session.
# When ENDLESS_SESSION_ID is set, ask the global endless for the session's
# worktree path; if it returns a real directory, route the command through
# that worktree's checkout via 'uv run --directory ...' so worktree-only
# subcommands work without a global 'just install'. Otherwise fall back to
# the bare 'endless' on PATH. The lookup costs ~one subprocess per helper
# call (≈100ms), which we accept to keep ENDLESS_WORKTREE_PATH out of the
# exported environment — env vars are visible/inheritable forever, latency
# is invisible. (E-1164.)
_endless_run() {
    # ${VAR:-} expansion keeps us safe under 'set -u' (nounset) — bare
    # "$ENDLESS_SESSION_ID" would error there when the var is unset.
    if [ -n "${ENDLESS_SESSION_ID:-}" ]; then
        local wt
        wt="$(endless session cd --target worktree "$ENDLESS_SESSION_ID" 2>/dev/null)"
        if [ -n "$wt" ] && [ -d "$wt" ]; then
            uv run --directory "$wt" endless "$@"
            return $?
        fi
    fi
    endless "$@"
}

# esu — activate a Claude session in this shell (cd to its worktree
#       or cwd, plus export ENDLESS_SESSION_ID).
#   esu          → auto-resolve to sibling Claude pane in tmux
#   esu <id>     → explicit endless integer id or Claude UUID prefix
esu() {
    local out
    out="$(_endless_run session use "$@")" || return $?
    eval "$out"
}

# esp — cd into the project root of a Claude session.
#   esp          → auto-resolve to sibling Claude pane in tmux
#   esp <id>     → explicit endless integer id or Claude UUID prefix
esp() {
    if [ -z "${ENDLESS_SESSION_ID:-}" ] && [ $# -eq 0 ]; then
        echo "esp: no active session, run 'esu <id>' first" >&2
        return 1
    fi
    local target
    target="$(_endless_run session cd --target project "$@")" || return $?
    cd "$target"
}

# esf — forget the current session ref (unset ENDLESS_SESSION_ID).
#       Inverse of esu. The session itself keeps running; only this
#       shell's pointer to it is cleared. Does not cd anywhere;
#       combine with esp if you also want to return to project root.
esf() {
    if [ -z "${ENDLESS_SESSION_ID:-}" ]; then
        echo "esf: no active session" >&2
        return 1
    fi
    local out
    out="$(_endless_run session forget)" || return $?
    eval "$out"
}

# <<< endless shell helpers <<<
"""


_SQL_READ_PREFIXES = ("select", "with", "explain")


def _is_read_only_sql(sql: str) -> bool:
    stripped = sql.strip()
    while stripped.startswith("--"):
        nl = stripped.find("\n")
        if nl == -1:
            stripped = ""
            break
        stripped = stripped[nl + 1:].lstrip()
    while stripped.startswith("/*"):
        end = stripped.find("*/")
        if end == -1:
            stripped = ""
            break
        stripped = stripped[end + 2:].lstrip()
    head = stripped[:7].lower()
    return any(head.startswith(p) for p in _SQL_READ_PREFIXES)


@main.command("sql")
@click.argument("query")
@click.option("--write", is_flag=True,
              help="Allow mutating statements (INSERT/UPDATE/DELETE/PRAGMA/etc.). "
                   "Default is read-only — only SELECT/WITH/EXPLAIN are accepted.")
@click.option("--tsv", is_flag=True,
              help="Tab-separated output (no header). Useful for piping.")
def sql_query(query, write, tsv):
    """Run a SQL query against the Endless DB.

    Resolves the DB path internally (no need to know where it lives).
    Read-only by default — pass --write for mutations. Replaces the
    agent instinct to reach for sqlite3 against speculative paths
    under .endless/, which silently creates ghost DB files.
    """
    from endless import db
    import sqlite3

    if not write and not _is_read_only_sql(query):
        raise click.ClickException(
            "Refusing to run a non-read-only query without --write. "
            "Allowed prefixes: SELECT, WITH, EXPLAIN.\n"
            "If you need to mutate, pass --write explicitly."
        )

    try:
        conn = db.get_db()
        cursor = conn.execute(query)
        rows = cursor.fetchall()
        # sqlite3's default isolation_level is "deferred" — an implicit
        # BEGIN opens on first mutation, and without an explicit commit()
        # the transaction rolls back on connection close. cursor.rowcount
        # reports affected rows even when uncommitted, which is what made
        # writes appear to succeed while silently being lost.
        if write:
            conn.commit()
    except sqlite3.Error as e:
        raise click.ClickException(f"SQL error: {e}")

    headers = [c[0] for c in cursor.description] if cursor.description else []
    if not rows:
        if write:
            click.echo(f"OK ({cursor.rowcount} rows affected)")
        return
    if tsv:
        if not headers:
            for r in rows:
                click.echo("\t".join(str(c) for c in r))
            return
        for r in rows:
            click.echo("\t".join(str(r[h]) for h in headers))
        return

    from tabulate import tabulate
    table = [[r[h] for h in headers] for r in rows]
    click.echo(tabulate(table, headers=headers, tablefmt="simple"))


@main.command("shell-init")
def shell_init():
    """Print shell helper functions for bash/zsh.

    Wraps 'endless session use', 'session cd --target project', and
    'session forget' with short functions (esu, esp, esf). One-time
    setup:

      endless shell-init >> ~/.zshrc        # or ~/.bashrc

    Re-running replaces nothing automatically — find the marker
    block ('endless shell helpers') in your rc file and replace
    it manually if the snippet changes.
    """
    click.echo(_SHELL_INIT_SNIPPET, nl=False)


@main.group("session")
def session_cmd():
    """View and manage session conversation history."""
    pass


@session_cmd.command("show")
@click.argument("session_ref", required=False, default=None)
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_show(session_ref, as_json):
    """Show details for a Claude session — current by default.

    With no arg, in tmux: auto-resolves to the sole sibling Claude pane in
    the current window. Otherwise an endless integer id or Claude UUID
    prefix is required.
    """
    from endless.session_cmd import session_show_resolve
    session_show_resolve(session_ref, as_json=as_json)


@session_cmd.command("activity")
@click.argument("session_ref", required=False, default=None)
@click.option("--kinds", default=None,
              help="Comma-separated event kinds to include "
                   "(e.g. task.created,task.claimed). Default: all.")
@click.option("--pane", "pane", default=None,
              help="Override $TMUX_PANE for session resolution. "
                   "Used by the tmux menu binding to bypass the popup's "
                   "own pane id (which has no Endless session).")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_activity(session_ref, kinds, pane, as_json):
    """Report what this session (or another) did, projected from the event ledger.

    Filters events by `actor.session_id` (populated since E-1284) and
    groups by kind: Filed, Decisions, Claimed, Shipped → verify,
    Confirmed, etc. Default session is the current one (resolved via
    the same 3-layer fallback as task claim — env, pane-direct,
    single-sibling Claude pane).
    """
    from endless.session_activity import session_activity as run_activity
    kinds_list = (
        [k.strip() for k in kinds.split(",") if k.strip()]
        if kinds else None
    )
    run_activity(
        session_ref=session_ref,
        kinds_filter=kinds_list,
        as_json=as_json,
        pane=pane,
    )


@session_cmd.command("history")
@click.argument("session_id", required=False, default=None)
@click.option("--tools", "show_tools", default=None, flag_value="truncated",
              help="Include tool calls (truncated)")
@click.option("--tools-full", "show_tools", flag_value="full",
              help="Include full tool call content")
@click.option("--timestamps", is_flag=True,
              help="Show timestamps on each message")
@click.option("--limit", default=20, type=int,
              help="Max messages (default: 20)")
@click.option("--sort", "sort_order", default="desc",
              type=click.Choice(["asc", "desc"]),
              help="Sort order (default: desc, newest first)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_history(session_id, show_tools, timestamps, limit, sort_order, as_json):
    """Show conversation history for a session.

    With no arg, defaults to the current session (same auto-resolution as
    session show).
    """
    from endless.session_cmd import show_history
    show_history(session_id, show_tools=show_tools,
                 show_timestamps=timestamps, limit=limit,
                 sort_asc=(sort_order == "asc"), as_json=as_json)


@session_cmd.command("list")
@click.option("--project", default=None, help="Filter by project")
@click.option("--state", default=None,
              type=click.Choice(["working", "idle", "needs_input", "ended"]),
              help="Filter by state")
@click.option("--sort", "sort_by", default=None,
              type=click.Choice(["id", "project", "state", "count"]),
              help="Sort by column (default: state priority)")
@click.option("--all", "show_all", is_flag=True,
              help="Include hidden and empty sessions")
@click.option("--hidden", "show_hidden", is_flag=True,
              help="Show only hidden sessions")
@click.option("--empty", "show_empty", is_flag=True,
              help="Include empty/short sessions (<=2 messages)")
@click.option("--limit", default=20, type=int,
              help="Max sessions (default: 20)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_list(project, state, sort_by, show_all, show_hidden, show_empty, limit, as_json):
    """List recent sessions."""
    from endless.session_cmd import list_sessions
    list_sessions(project_name=project, show_all=show_all,
                  show_hidden=show_hidden, show_empty=show_empty,
                  state_filter=state, sort_by=sort_by,
                  limit=limit, as_json=as_json)


@session_cmd.command("search")
@click.argument("query")
@click.option("--project", default=None, help="Filter by project")
@click.option("--limit", default=20, type=int,
              help="Max results (default: 20)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_search(query, project, limit, as_json):
    """Search across all session messages."""
    from endless.session_cmd import search_sessions
    search_sessions(query, project_name=project, limit=limit, as_json=as_json)


@session_cmd.command("use")
@click.argument("session_ref", required=False, default=None)
def session_use(session_ref):
    """Print shell-evaluable activation for a Claude session.

    Designed for `eval "$(endless session use)"`. Emits a minimal block —
    cd to the session's worktree (if its directory exists) or cwd, plus
    ENDLESS_SESSION_ID. Then runs .endless/extensions/use.sh (if present)
    and appends its stdout. With no arg, in tmux: auto-resolves to the
    sole sibling Claude pane in the current window.

    Standard env vars exported:
      ENDLESS_SESSION_ID    endless integer id

    Other session fields (harness, project root, worktree path, etc.)
    are looked up on demand via 'endless session show $ENDLESS_SESSION_ID
    --json' so they're never stale (E-1038 supersedes E-1014's original
    five-var contract).
    """
    from endless.session_cmd import session_use_resolve
    session_use_resolve(session_ref)


@session_cmd.command("forget")
def session_forget():
    """Print shell-evaluable lines that unset session-use env vars.

    Designed for `eval "$(endless session forget)"`. Inverse of
    'session use': emits one 'unset' line per env var that 'session use'
    is documented to export. Makes the current shell forget its session
    reference; the session itself is unaffected. Does not cd anywhere.

    See `endless shell-init` for the esf wrapper function.
    """
    from endless.session_cmd import session_forget_resolve
    session_forget_resolve()


@session_cmd.command("cd")
@click.argument("session_ref", required=False, default=None)
@click.option("--all", "show_all", is_flag=True,
              help="List all live Claude sessions in this project")
@click.option("--target", "target",
              type=click.Choice(["auto", "worktree", "project", "cwd"]),
              default="auto",
              help="Which path to print: auto (default; worktree else cwd), "
                   "worktree (errors if none), project (project root), cwd")
def session_cd(session_ref, show_all, target):
    """Print a path for a Claude session, for `cd $(...)` wrapping.

    With no session-ref, in tmux: auto-resolves to the sole sibling Claude
    pane in the current window. Use --all to list candidates. Provide an
    endless integer id or a Claude UUID prefix to disambiguate.

    See `endless shell-init` for esu/esp wrapper functions.
    """
    from endless.session_cmd import session_cd_resolve
    session_cd_resolve(session_ref, show_all=show_all, target=target)


@session_cmd.command("id")
def session_id():
    """Print the current Endless session's integer id to stdout.

    Designed for shell substitution: `ENDLESS_SESSION_ID="$(endless session id)"`.
    Resolves via the same 3-layer logic as cli/hook event attribution:
    ENDLESS_SESSION_ID env var, TMUX_PANE companion match, or a single
    sibling Claude pane in the current tmux window. On ambiguity or no
    match, exits non-zero with a diagnostic on stderr (stdout stays empty).
    """
    from endless.session_cmd import session_id_resolve
    session_id_resolve()


@session_cmd.command("reimport")
@click.argument("session_id", required=False, default=None)
def session_reimport(session_id):
    """Reimport transcript data from JSONL files."""
    from endless.session_cmd import reimport_sessions
    reimport_sessions(session_value=session_id)


@session_cmd.command("recap")
@click.argument("session_id", required=False, default=None)
@click.option("--force", is_flag=True,
              help="Generate even if < 10 new messages")
def session_recap(session_id, force):
    """Generate recap summaries for sessions using Claude."""
    from endless.session_cmd import recap_session
    recap_session(session_value=session_id, force=force)


@session_cmd.command("hide")
@click.argument("session_ids", nargs=-1, required=True)
def session_hide(session_ids):
    """Hide sessions from the list."""
    from endless.session_cmd import hide_sessions
    hide_sessions(list(session_ids))


@session_cmd.command("unhide")
@click.argument("session_ids", nargs=-1, required=True)
def session_unhide(session_ids):
    """Unhide sessions."""
    from endless.session_cmd import unhide_sessions
    unhide_sessions(list(session_ids))


@session_cmd.group("status")
def session_status_cmd():
    """Record and query session status snapshots (E-1312)."""
    pass


@session_status_cmd.command("add")
@click.argument("input_file", required=False,
                type=click.Path(exists=True, dir_okay=False))
@click.option("--session-id", "session_id_override", type=int, default=None,
              help="Override tmux-pane session lookup (reserved; not "
                   "implemented in v1).")
def session_status_add(input_file, session_id_override):
    """Record a session status snapshot from XML on stdin or a file path.

    Reads XML matching the <session-status>/<task>/<decision>/<commit>/
    <entry> schema, validates strictly, and emits a
    session_status.recorded event. The Go-side handler dedups against
    the latest row for this session and renders markdown for chat.

    Example:

      \b
      endless session status add <<'EOF'
      <session-status>
        <headline>E-1312 v1 landed.</headline>
        <resolved>
          <task id="E-1312" status="verify">CLI + Go handler + tests</task>
        </resolved>
      </session-status>
      EOF
    """
    from endless.session_status_cmd import session_status_add as impl
    impl(input_file, session_id_override)


@main.group("task")
def task_cmd():
    """Manage project tasks."""
    pass


@task_cmd.command("import")
@click.argument("file", default=None, required=False)
@click.option("--from-claude", is_flag=True,
              help="Import from ~/.claude/plans/")
@click.option("--json", "json_file", default=None,
              help="Import from JSON file")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--replace", is_flag=True,
              help="Replace items from same source file under same parent")
@click.option("--parent", type=TASK_ID, default=None,
              help="Parent goal ID to import under")
def task_import(file, from_claude, json_file, project, replace, parent):
    """Import a plan file into the DB."""
    if json_file:
        import json as json_mod
        from pathlib import Path
        from endless.task_cmd import import_json
        p = Path(json_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        data = json_mod.loads(p.read_text())
        import_json(data, project_name=project, clear=replace)
    else:
        from endless.task_cmd import import_plan
        import_plan(
            file_path=file, from_claude=from_claude,
            project_name=project, replace=replace,
            parent_id=parent,
        )


@task_cmd.command("list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include confirmed items")
@click.option("--status", default=None,
              type=MultiChoice(TASK_STATUSES),
              help="Filter by status (comma-separated, e.g. needs_plan,ready)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Filter by phase")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
@click.option("--related-to", "--relates-to", "related_to_id", type=TASK_ID, default=None,
              help="Filter to tasks related to this task ID")
@click.option("--rel-type", "rel_type", default=None,
              help="Narrow --related-to by relation type (blocks, implements, informs, ...)")
@click.option("--sort", default=None,
              type=click.Choice(["id", "status", "phase", "tier", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--tree", "as_tree", is_flag=True,
              help="Show as indented tree instead of flat table")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_list(project, show_all, status, phase, tier, parent_id, related_to_id, rel_type,
              sort, as_tree, llm, as_json):
    """List tasks for a project."""
    from endless.task_cmd import show_plan, parse_tier_filter, parse_parent_filter
    tier_val = parse_tier_filter(tier) if tier else None
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    show_plan(project_name=project, show_all=show_all,
              status_filter=status, phase_filter=phase,
              tier_filter=tier_val, parent_id=parent_val,
              related_to_id=related_to_id, rel_type=rel_type,
              sort_by=sort, tree=as_tree, llm=llm, as_json=as_json)


@task_cmd.command("show")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--no-description", is_flag=True,
              help="Hide description")
@click.option("--text", "show_text", is_flag=True,
              help="Show text field")
@click.option("--children", "show_children", is_flag=True,
              help="Show direct children")
@click.option("--outcome", "show_outcome", is_flag=True,
              help="Show outcome field (always shown for declined tasks)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_show(item_ids, no_description, show_text,
              show_children, show_outcome, llm, as_json):
    """Show detail for one or more tasks."""
    from endless.task_cmd import detail_item
    for item_id in item_ids:
        detail_item(item_id, show_description=not no_description,
                    show_text=show_text,
                    show_children=show_children, show_outcome=show_outcome,
                    llm=llm, as_json=as_json)


task_cmd.add_command(task_show, name="detail")


@task_cmd.group("next", invoke_without_command=True)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--limit", default=10, type=int,
              help="Max items to show (default: 10)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Filter by phase")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
@click.pass_context
def task_next(ctx, project, show_all, limit, llm, as_json, tier, phase, parent_id):
    """Show top actionable tasks, ranked by priority."""
    # `next` is a group so it can host `revise` (and future `move`/`briefing`),
    # but bare `endless task next` keeps its heuristic-list behavior.
    if ctx.invoked_subcommand is not None:
        return
    from endless.task_cmd import next_tasks, parse_tier_filter, parse_parent_filter
    tier_val = parse_tier_filter(tier) if tier else None
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    next_tasks(project_name=project, show_all=show_all,
               limit=limit, llm=llm, as_json=as_json, tier=tier_val,
               phase_filter=phase, parent_id=parent_val)


@task_next.command("revise")
@click.option("--file", "file_path", required=True,
              help="Path to a JSON file holding the full new curated list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--json", "as_json", is_flag=True,
              help="Emit the resulting list as JSON")
def task_next_revise(file_path, project, as_json):
    """Replace the curated 'next' list from a JSON file (full rewrite)."""
    from endless.task_cmd import revise_next_list
    revise_next_list(file_path=file_path, project_name=project, as_json=as_json)


@task_cmd.command("active")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
def task_active(project, show_all, llm, as_json, parent_id):
    """Show in-progress and verify tasks."""
    from endless.task_cmd import active_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    active_tasks(project_name=project, show_all=show_all,
                 llm=llm, as_json=as_json, parent_id=parent_val)


@task_cmd.command("recent")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--limit", default=10, type=int,
              help="Max items to show (default: 10)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
def task_recent(project, show_all, limit, llm, as_json, parent_id):
    """Show most recently updated tasks."""
    from endless.task_cmd import recent_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    recent_tasks(project_name=project, show_all=show_all,
                 limit=limit, llm=llm, as_json=as_json, parent_id=parent_val)


@task_cmd.command("search")
@click.argument("query")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include confirmed/assumed/declined items")
@click.option("--status", default=None,
              type=MultiChoice(TASK_STATUSES),
              help="Filter by status (comma-separated, e.g. needs_plan,ready)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Filter by phase")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
@click.option("--text", "search_text", is_flag=True,
              help="Also search in text field")
@click.option("--limit", default=20, type=int,
              help="Max results (default: 20)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_search(query, project, show_all, status, phase, parent_id,
                search_text, limit, llm, as_json):
    """Search tasks by query string."""
    from endless.task_cmd import search_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    search_tasks(query, project_name=project, show_all=show_all,
                 status_filter=status, phase_filter=phase,
                 parent_id=parent_val,
                 search_text=search_text,
                 limit=limit, llm=llm, as_json=as_json)


@task_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the task")
@click.option("--text", "text_file", default=None,
              help="Load full task text from file")
@click.option("--phase", default="now",
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Phase: urgent, now, next, later, maybe (default: now)")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--parent", type=TASK_ID, default=None,
              help="Parent task ID to add under")
@click.option("--after", type=TASK_ID, default=None,
              help="Insert after this task ID")
@click.option("--type", "task_type", default=None,
              type=click.Choice(["task", "bug", "research", "epic"]),
              help="Task type (default: task)")
@click.option("--status", default=None,
              type=click.Choice(TASK_STATUSES),
              help="Initial status (default: needs_plan)")
@click.option("--tier", default=None,
              help="Tier (1-4 or auto/quick/deep/discuss)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
@click.option("--justification", default=None,
              help="Justification text for --type research (stored under '## Justification' in notes). "
                   "Required for --type research unless --parent is an in-progress epic.")
@click.option("--blocks", "blocks_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new task blocks (repeatable)")
@click.option("--blocked-by", "blocked_by_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that block this new task (repeatable)")
@click.option("--relates-to", "relates_to_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) related to this new task (repeatable)")
@click.option("--implements", "implements_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that this new task implements (repeatable)")
@click.option("--cleans-up", "cleans_up_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that this new task cleans up after (repeatable)")
@click.option("--cleaned-up-by", "cleaned_up_by_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that clean up after this new task (repeatable)")
def task_add(title, description, text_file, phase, project, parent, after, task_type, status, tier, force,
             justification,
             blocks_ids, blocked_by_ids, relates_to_ids, implements_ids,
             cleans_up_ids, cleaned_up_by_ids):
    """Add a task."""
    from endless.task_cmd import add_item, parse_tier, link_tasks
    tier_val = parse_tier(tier) if tier else None
    new_id = add_item(title, description=description, text_file=text_file,
                      phase=phase, project_name=project, after=after, parent_id=parent,
                      task_type=task_type, status=status, tier=tier_val, force=force,
                      justification=justification)
    if new_id is None:
        return
    for tid in blocks_ids:
        link_tasks(new_id, tid, "blocks")
    for tid in blocked_by_ids:
        link_tasks(new_id, tid, "blocked_by")
    for tid in relates_to_ids:
        link_tasks(new_id, tid, "relates_to")
    for tid in implements_ids:
        link_tasks(new_id, tid, "implements")
    for tid in cleans_up_ids:
        link_tasks(new_id, tid, "cleans_up")
    for tid in cleaned_up_by_ids:
        link_tasks(new_id, tid, "cleaned_up_by")


@task_cmd.command("update")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--status", default=None,
              help="Status: needs_plan, ready, in_progress, verify, confirmed, assumed, blocked, revisit, declined, obsolete")
@click.option("--title", default=None,
              help="New title")
@click.option("--description", default=None,
              help="New description")
@click.option("--text", "text_file", default=None,
              help="Load full task text from file")
@click.option("--parent", type=TASK_ID, default=None,
              help="Set parent task ID (0 to make root)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Phase: urgent, now, next, later, maybe")
@click.option("--tier", default=None,
              help="Tier (0=n/a, 1-4 or auto/quick/deep/discuss, none=clear)")
@click.option("--type", "task_type", default=None,
              type=click.Choice(["task", "bug", "research", "epic"]),
              help="Task type — closes the prior gap that forced direct SQL writes (E-1329)")
@click.option("--analysis", "analysis_text", default=None,
              help="Analysis content (string, or @path/to/file to load from file). Closes the prior gap that forced direct SQL writes (E-1329)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
@click.option("--outcome", default=None,
              help="Outcome / reason for status (required if status=declined)")
@click.option("--justification", default=None,
              help="Justification text when setting --type research (stored under '## Justification' in notes). "
                   "Required unless the effective parent is an in-progress epic.")
def task_update(item_ids, status, title, description, text_file, parent, phase, tier,
                task_type, analysis_text, force, outcome, justification):
    """Update fields on one or more tasks."""
    from endless.task_cmd import update_plan, parse_tier
    tier_val = parse_tier(tier) if tier else None
    # Support `--analysis @path/to/file` for content too long for the shell.
    if analysis_text is not None and analysis_text.startswith("@"):
        from pathlib import Path
        p = Path(analysis_text[1:]).expanduser()
        if not p.exists():
            raise click.ClickException(f"Analysis file not found: {p}")
        analysis_text = p.read_text()
    for item_id in item_ids:
        update_plan(item_id, status=status, title=title,
                    description=description, text_file=text_file,
                    parent_id=parent,
                    phase=phase, tier=tier_val, task_type=task_type,
                    analysis=analysis_text,
                    outcome=outcome, force=force,
                    justification=justification)


@task_cmd.command("remove")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also remove all descendants")
def task_remove(item_ids, cascade):
    """Remove one or more tasks."""
    from endless.task_cmd import remove_item
    for item_id in item_ids:
        remove_item(item_id, cascade=cascade)


@task_cmd.group("clear")
def task_clear():
    """Clear a field on a task."""
    pass


@task_clear.command("tier")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
def task_clear_tier(item_ids):
    """Clear tier on one or more tasks (set to NULL/untriaged)."""
    from endless.task_cmd import update_plan, TIER_CLEAR
    for item_id in item_ids:
        update_plan(item_id, tier=TIER_CLEAR)


@task_cmd.command("confirm")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also confirm all descendants")
@click.option("--outcome", default=None,
              help="Outcome — what was confirmed (applies to root only on cascade)")
def task_complete(item_ids, cascade, outcome):
    """Confirm one or more tasks."""
    from endless.task_cmd import complete_item
    for item_id in item_ids:
        complete_item(item_id, cascade=cascade, outcome=outcome)


@task_cmd.command("assume")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also assume all descendants")
@click.option("--outcome", default=None,
              help="Outcome — what was assumed (applies to root only on cascade)")
def task_assume(item_ids, cascade, outcome):
    """Assume one or more tasks (believed complete, not yet verified)."""
    from endless.task_cmd import assume_item
    for item_id in item_ids:
        assume_item(item_id, cascade=cascade, outcome=outcome)


@task_cmd.command("decline")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--reason", required=True,
              help="Why this task is being declined (stored as outcome)")
def task_decline(item_ids, reason):
    """Decline one or more tasks (sets status=declined; reason required)."""
    from endless.task_cmd import decline_item
    for item_id in item_ids:
        decline_item(item_id, reason=reason)


@task_cmd.command("complete")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--outcome", required=True,
              help="Findings / deliverable text (required — IS the deliverable)")
def task_complete_cmd(item_ids, outcome):
    """Mark one or more tasks as `completed` (E-1240).

    For findings-as-deliverable tasks (audits, research, reviews, etc.)
    whose deliverable is the outcome text itself, not behavior. Gated:
    the task's title's lead verb must be marked `completable: true` in
    verbs.json. For implementation tasks, use `task confirm` / `task assume`.
    """
    from endless.task_cmd import mark_completed_item
    for item_id in item_ids:
        mark_completed_item(item_id, outcome=outcome)


@task_cmd.command("claim")
@click.argument("item_id", type=TASK_ID)
@click.option("--force", is_flag=True,
              help="Re-claim even when the task is in a done-ish status "
                   "(verify, confirmed, declined, obsolete, assumed) — "
                   "demotes it back to in_progress.")
def task_claim(item_id, force):
    """Claim ownership of a task for this session."""
    from endless.task_cmd import claim_item
    claim_item(item_id, force=force)


@task_cmd.command("release")
@click.argument("item_id", type=TASK_ID, required=False, default=None)
@click.option("--ignore-missing", is_flag=True,
              help="When releasing a specific task ID, succeed with an info "
                   "message instead of erroring if no session has it claimed.")
def task_release(item_id, ignore_missing):
    """Release a session's claim on a task (defaults to current session's active task)."""
    from endless.task_cmd import release_item
    release_item(item_id, ignore_missing=ignore_missing)


@task_cmd.command("bind")
@click.argument("item_id", type=TASK_ID)
def task_bind(item_id):
    """Bind this session to a task for status-bar display only.

    Unlike `claim`, `bind` does not change the task's status or create
    a worktree — it just sets sessions.active_task_id so the second
    tmux status row shows this task. Use when the task is already in
    `assumed` / `confirmed` / `verify` and you want the bar to keep
    showing it as context. Symmetric counterpart to `release`.
    """
    from endless.task_cmd import bind_item
    bind_item(item_id)


@task_cmd.command("start", hidden=True)
@click.argument("item_id", type=TASK_ID, required=False, default=None)
def task_start_deprecated(item_id):
    """Deprecated stub for `task claim` (E-1232 rename).

    Refuses to execute — prints the rename note and exits non-zero so the
    caller (agent or human) switches to the new verb instead of being
    silently enabled to keep using the old one.
    """
    raise click.ClickException(
        "`task start` was renamed to `task claim`.\n"
        "Run: endless task claim "
        + (f"E-{item_id}" if item_id is not None else "<id>")
    )


@task_cmd.command("move")
@click.argument("item_id", type=TASK_ID, required=False, default=None)
@click.option("--parent", type=TASK_ID, default=None,
              help="Target parent task ID to move under")
@click.option("--root", is_flag=True,
              help="Move to root (no parent)")
@click.option("--with-children", is_flag=True,
              help="Move the task and all its descendants")
@click.option("--children-of", type=TASK_ID, default=None,
              help="Move children of this task instead")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def task_move(item_id, parent, root, with_children, children_of, project):
    """Move a task to a new parent or to root."""
    from endless.task_cmd import move_task
    move_task(item_id=item_id, parent=parent, root=root,
              with_children=with_children, children_of=children_of,
              project_name=project)


@task_cmd.command("handoff")
@click.argument("item_id", type=TASK_ID)
def task_handoff(item_id):
    """Render the spawn handoff for a task (the text spawn pastes)."""
    from endless.task_cmd import show_handoff
    show_handoff(item_id)


@task_cmd.command("spawn")
@click.argument("item_id", type=TASK_ID)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--no-plan", is_flag=True,
              help="Skip plan mode, send the handoff directly")
@click.option("--worktree", default=None,
              help="cd to this path (e.g. a git worktree) before launching "
                   "claude, instead of the spawn-created task worktree. The "
                   "spawned session reads .claude/settings.json from this "
                   "directory, so a worktree-local hook override (see "
                   "'just claude-settings-init') applies.")
@click.option("--force", is_flag=True,
              help="Allow spawn on a task in a done-ish status "
                   "(verify/confirmed/declined/obsolete/assumed/completed); "
                   "demotes it back to in_progress. Mirrors `claim --force`.")
@click.option("--reopen", is_flag=True,
              help="Reopen an assumed/confirmed/completed target before "
                   "spawning (status → ready/needs_plan based on text "
                   "presence). Use for handoff to a fresh session; "
                   "mutually exclusive with --force.")
def task_spawn(item_id, project, no_plan, worktree, force, reopen):
    """Spawn a new tmux window with Claude working on a task.

    Pastes the handoff generated from the template (no stored prompt — E-1469).
    """
    from endless.task_cmd import spawn_plan
    spawn_plan(item_id, project_name=project, no_plan=no_plan,
               worktree=worktree, force=force, reopen=reopen)


@task_cmd.command("reopen")
@click.argument("item_id", type=TASK_ID)
def task_reopen(item_id):
    """Reopen a terminal-status task back to actionable state.

    Flips assumed/confirmed/completed → ready (if a plan is attached)
    or needs_plan (if not). Metadata-only: no worktree creation, no
    session binding. Caller chooses the next step (spawn, claim, or
    hand-back).
    """
    from endless.task_cmd import reopen_item
    reopen_item(item_id)


@task_cmd.command("chat")
def task_chat():
    """Start a chat-only session (no task tracking)."""
    from endless.task_cmd import start_chat
    start_chat()


@task_cmd.command("link")
@click.argument("source_id", type=TASK_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "dep_type", required=True,
              help="Relation type — legal set depends on target kind "
                   "(task→task: blocks, blocked_by, implements, implemented_by, "
                   "replaces, replaced_by, documents, documented_by, "
                   "cleans_up, cleaned_up_by, relates_to; "
                   "task→decision: implements, cleans_up, documents, relates_to)")
def task_link(source_id, target, dep_type):
    """Create a typed relation from a task to a task or decision."""
    target_kind, target_id = target
    if target_kind == "decision":
        from endless.decision_cmd import link_task_to_decision
        link_task_to_decision(source_id, target_id, dep_type)
    else:
        from endless.task_cmd import link_tasks
        link_tasks(source_id, target_id, dep_type)


@task_cmd.command("unlink")
@click.argument("source_id", type=TASK_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "dep_type", default=None,
              help="Relation type to remove (omit to auto-detect when unambiguous)")
def task_unlink(source_id, target, dep_type):
    """Remove a typed relation from a task to a task or decision."""
    target_kind, target_id = target
    if target_kind == "decision":
        from endless.decision_cmd import unlink_task_from_decision
        unlink_task_from_decision(source_id, target_id, dep_type)
    else:
        from endless.task_cmd import unlink_tasks
        unlink_tasks(source_id, target_id, dep_type)


@task_cmd.command("block")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "blocker_id", type=TASK_ID, required=True,
              help="Task ID that blocks this task")
def task_block(item_id, blocker_id):
    """Record that a task is blocked by another task. Shortcut for `link --type blocked_by`."""
    from endless.task_cmd import link_tasks
    link_tasks(item_id, blocker_id, "blocked_by")


@task_cmd.command("replace")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "replacement_id", type=TASK_ID, required=True,
              help="Task ID that replaces this task")
@click.option("--status", "new_status", default="obsolete",
              type=click.Choice(["obsolete", "declined", "confirmed", "assumed", "completed"]),
              help="Status to set on the replaced task (default: obsolete)")
@click.option("--outcome", default=None,
              help="Outcome — why this was replaced (required if --status=declined)")
def task_replace(item_id, replacement_id, new_status, outcome):
    """Mark a task as replaced by another task (sets status, default 'obsolete')."""
    from endless.task_cmd import replace_task
    replace_task(item_id, replacement_id, status=new_status, outcome=outcome)


@task_cmd.command("unblock")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "blocker_id", type=TASK_ID, required=True,
              help="Task ID to remove as blocker")
def task_unblock(item_id, blocker_id):
    """Remove a blocking dependency between tasks."""
    from endless.task_cmd import unlink_tasks
    unlink_tasks(item_id, blocker_id, "blocked_by")


@task_cmd.command("deps")
@click.argument("item_id", type=TASK_ID)
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
def task_deps(item_id, llm):
    """Show all relations for a task. Alias of `task relations`."""
    from endless.task_cmd import show_relations
    show_relations(item_id, llm=llm)


@task_cmd.command("relations")
@click.argument("item_id", type=TASK_ID)
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
def task_relations(item_id, llm):
    """Show all of a task's relations under a single 'Links:' section."""
    from endless.task_cmd import show_relations
    show_relations(item_id, llm=llm)


@main.command("plan", context_settings={"ignore_unknown_options": True})
@click.argument("args", nargs=-1, type=click.UNPROCESSED)
def plan_redirect(args):
    """Renamed to 'task'. Prints corrected command."""
    corrected = " ".join(["endless", "task"] + list(args))
    click.echo(
        click.style("The 'plan' command has been renamed to 'task'.", fg="yellow")
    )
    click.echo()
    click.echo(f"  Use: {click.style(corrected, bold=True)}")
    click.echo()
    click.echo(
        "If recording a Claude plan (from ~/.claude/plans/), "
        "use --type=plan with 'task add'."
    )
    raise SystemExit(1)


@main.group("decision")
def decision_cmd():
    """Manage project decisions."""
    pass


@decision_cmd.command("list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show decisions from all projects")
@click.option("--sort", default=None,
              type=click.Choice(["id", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def decision_list(project, show_all, sort, llm, as_json):
    """List decisions for a project."""
    from endless.decision_cmd import list_decisions
    list_decisions(project_name=project, show_all=show_all,
                   sort_by=sort, llm=llm, as_json=as_json)


@decision_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the decision")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--about", "about_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this decision documents (repeatable; soft link)")
@click.option("--decides", "decides_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that implement this decision (repeatable; hard link)")
def decision_add(title, description, project, about_ids, decides_ids):
    """Record a decision (starts as `proposed`)."""
    from endless.decision_cmd import add_decision
    add_decision(
        title,
        description=description,
        project_name=project,
        about_task_ids=tuple(about_ids),
        decides_task_ids=tuple(decides_ids),
    )


@decision_cmd.command("show")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def decision_show(item_ids, llm, as_json):
    """Show detail for one or more decisions."""
    from endless.decision_cmd import detail_decision
    for item_id in item_ids:
        detail_decision(item_id, llm=llm, as_json=as_json)


@decision_cmd.command("accept")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
def decision_accept(item_ids):
    """Accept one or more decisions (proposed → accepted)."""
    from endless.decision_cmd import accept_decision
    for item_id in item_ids:
        accept_decision(item_id)


@decision_cmd.command("reject")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
@click.option("--reason", required=True,
              help="Why this decision is being rejected (stored on the row)")
def decision_reject(item_ids, reason):
    """Reject one or more decisions (proposed → rejected) with a stored reason."""
    from endless.decision_cmd import reject_decision
    for item_id in item_ids:
        reject_decision(item_id, reason)


@decision_cmd.command("link")
@click.argument("source_id", type=DECISION_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "relation_type", required=True,
              help="Relation type — legal set depends on the pair "
                   "(see 'Relation-type vocabulary by pair' in the plan)")
def decision_link(source_id, target, relation_type):
    """Link a decision to a task or another decision."""
    from endless.decision_cmd import link_decision
    target_kind, target_id = target
    link_decision(source_id, target_kind, target_id, relation_type)


@decision_cmd.command("unlink")
@click.argument("source_id", type=DECISION_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "relation_type", default=None,
              help="Relation type to remove (omit to auto-detect when unambiguous)")
def decision_unlink(source_id, target, relation_type):
    """Remove a decision-sourced relation."""
    from endless.decision_cmd import unlink_decision
    target_kind, target_id = target
    unlink_decision(source_id, target_kind, target_id, relation_type)


@main.group("channel")
def channel_cmd():
    """Inter-session messaging. Worker session beacons, human session connects."""
    pass


@channel_cmd.command("beacon")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def channel_beacon(project):
    """Announce this session as available for messaging (run in worker session)."""
    from endless.channel_cmd import beacon
    beacon(project_name=project)


@channel_cmd.command("connect")
@click.argument("channel_id", default=None, required=False)
def channel_connect(channel_id):
    """Connect to a beaconing session (auto-finds if only one beacon)."""
    from endless.channel_cmd import connect
    connect(channel_id)


@channel_cmd.command("send")
@click.argument("message")
def channel_send(message):
    """Send a message to the connected session."""
    from endless.channel_cmd import send
    send(message)


@channel_cmd.command("inbox")
def channel_inbox():
    """Show pending messages."""
    from endless.channel_cmd import inbox
    inbox()


@channel_cmd.command("list")
@click.option("--project", default=None,
              help="Project name")
def channel_list(project):
    """List active beacons."""
    from endless.channel_cmd import list_beacons
    list_beacons(project_name=project)


@channel_cmd.command("close")
def channel_close():
    """Close the active channel."""
    from endless.channel_cmd import close
    close()


@main.group("worktree")
def worktree_cmd():
    """Inspect git worktrees managed by endless (E-971 foundation, read-only)."""
    pass


@worktree_cmd.command("list")
@click.option("--state", "state_filter", default=None,
              type=click.Choice(["main", "active", "foreign"]),
              help="Filter by lifecycle state")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_list(state_filter, as_json):
    """List worktrees for the current project."""
    from endless.worktree_cmd import list_worktrees
    list_worktrees(state_filter, as_json)


@worktree_cmd.command("current")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_current(as_json):
    """Show the worktree for the current cwd."""
    from endless.worktree_cmd import current_worktree
    current_worktree(as_json)


@worktree_cmd.command("show")
@click.argument("name_or_path")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_show(name_or_path, as_json):
    """Show detail for one worktree (by trailing path segment or absolute path)."""
    from endless.worktree_cmd import show_worktree
    show_worktree(name_or_path, as_json)


@worktree_cmd.command("for-task")
@click.argument("task_id")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_for_task(task_id, as_json):
    """Resolve a task ID to its worktree path (or report none)."""
    from endless.worktree_cmd import for_task
    for_task(task_id, as_json)


@worktree_cmd.command("land")
@click.argument("task_id")
@click.option("--dry-run", is_flag=True,
              help="Show what would happen without making changes")
def worktree_land(task_id, dry_run):
    """Auto-commit endless-managed dirt, rebase, ff-merge, remove worktree (E-987)."""
    from endless.worktree_cmd import land_worktree
    land_worktree(task_id, dry_run)


@worktree_cmd.command("drop")
@click.argument("name_or_path")
@click.option("--force", is_flag=True,
              help="Drop even if dirty/unmerged/foreign")
def worktree_drop(name_or_path, force):
    """Remove a worktree (refuses dirty/unmerged/foreign without --force)."""
    from endless.worktree_cmd import drop_worktree
    drop_worktree(name_or_path, force)


@worktree_cmd.command("reap")
def worktree_reap():
    """Sweep stale landed worktrees (E-1337).

    Removes worktree directories whose owning task has at least one row
    in task_landings older than worktree_ttl (.endless/config.json,
    default 14d) AND has no live process holding cwd inside. Pre-existing
    orphan directories without landing records are skipped.
    """
    from endless.worktree_cmd import _project_root, _reap_stale_worktrees
    _reap_stale_worktrees(_project_root())


@main.group("verb")
def verb_cmd():
    """Manage verbs — the registered actions that can start task titles."""
    pass


@verb_cmd.command("add")
@click.argument("value")
@click.option("--definition", default=None,
              help="Short 'to ___' definition (required, e.g., 'to deliberate over')")
@click.option("--machine-only", is_flag=True,
              help="Skip the project config write (machine layer only)")
def verb_add(value, definition, machine_only):
    """Register a new verb."""
    from endless.verb_cmd import add_verb
    add_verb(value, definition, machine_only)


@verb_cmd.command("list")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def verb_list(as_json):
    """List registered verbs from project + machine layers."""
    from endless.verb_cmd import list_verbs
    list_verbs(as_json)


@verb_cmd.command("remove")
@click.argument("value")
@click.option("--machine-only", is_flag=True,
              help="Remove from machine layer only")
def verb_remove(value, machine_only):
    """Remove a verb."""
    from endless.verb_cmd import remove_verb
    remove_verb(value, machine_only)


@main.group("phrase")
def phrase_cmd():
    """Manage matchers (action regexes) in config files."""
    pass


@phrase_cmd.command("add")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None,
              help="Optional scope qualifier (e.g., 'task', 'channel')")
@click.option("--method", default=None,
              type=click.Choice(["exact", "substring", "regex"]),
              help="Match algorithm (default: regex)")
@click.option("--case-sensitive", is_flag=True,
              help="Match exact case (default: case-insensitive)")
@click.option("--machine-only", is_flag=True,
              help="Skip the project config write (machine layer only)")
def phrase_add(type_, value, scope, method, case_sensitive, machine_only):
    """Add a matcher: TYPE VALUE [--scope ...] [--method ...] [--case-sensitive] [--machine-only]."""
    from endless.phrase_cmd import add_phrase
    add_phrase(type_, value, scope, method, case_sensitive, machine_only)


@phrase_cmd.command("list")
@click.option("--type", "type_filter", default=None,
              help="Filter to one type")
@click.option("--scope", "scope_filter", default=None,
              help="Filter to one scope")
@click.option("--all", "show_disabled", is_flag=True,
              help="Include disabled matchers")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def phrase_list(type_filter, scope_filter, show_disabled, as_json):
    """List matchers from project + machine config layers, merged."""
    from endless.phrase_cmd import list_phrases
    list_phrases(type_filter, scope_filter, show_disabled, as_json)


@phrase_cmd.command("disable")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
@click.option("--machine-only", is_flag=True,
              help="Operate on machine layer only")
def phrase_disable(type_, value, scope, machine_only):
    """Disable a matcher value (it stops matching but isn't removed)."""
    from endless.phrase_cmd import disable_phrase
    disable_phrase(type_, value, scope, machine_only)


@phrase_cmd.command("enable")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
def phrase_enable(type_, value, scope):
    """Re-enable a previously disabled matcher value."""
    from endless.phrase_cmd import enable_phrase
    enable_phrase(type_, value, scope)


@phrase_cmd.command("remove")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
@click.option("--machine-only", is_flag=True,
              help="Remove from machine layer only")
def phrase_remove(type_, value, scope, machine_only):
    """Remove a matcher value (use disable for reversible silencing)."""
    from endless.phrase_cmd import remove_phrase
    remove_phrase(type_, value, scope, machine_only)


@main.command("docs")
@click.argument("name", default=None, required=False)
@click.option("--type", "type_filter", default=None,
              help="Filter by document type")
def docs_cmd(name, type_filter):
    """List tracked documents for a project (temporarily disabled)."""
    click.echo(
        click.style("•", fg="yellow")
        + " Document tracking is temporarily disabled."
        + " It will return with shadow git repos."
    )


@main.command("notes")
@click.argument("name", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include resolved notes")
def notes_cmd(name, show_all):
    """Show pending notes for a project."""
    from endless.notes_cmd import list_notes
    list_notes(name=name, show_all=show_all)


@main.group("note")
def note_cmd():
    """Manage notes."""
    pass


@note_cmd.command("add")
@click.argument("message")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def note_add(message, project):
    """Add a note to a project."""
    from endless.notes_cmd import add_note
    add_note(name=project, message=message)


@note_cmd.command("resolve")
@click.argument("note_id", type=int)
def note_resolve(note_id):
    """Mark a note as resolved."""
    from endless.notes_cmd import resolve_note
    resolve_note(note_id=note_id)



@main.command()
@click.argument("path", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include dormant projects in review")
@click.option("--reset", is_flag=True,
              help="Forget prior decisions, re-evaluate all directories")
def discover(path, show_all, reset):
    """Find and register unregistered projects."""
    from endless.discover import run_discover
    run_discover(discover_path=path, show_all=show_all, reset=reset)


@main.group()
def setup():
    """Install hooks and integrations."""
    pass


@setup.command("prompt-hook")
def setup_prompt_hook():
    """Install the ZSH prompt hook for activity monitoring."""
    from endless.setup import setup_prompt_hook
    setup_prompt_hook()


@setup.command("remove-prompt-hook")
def setup_remove_prompt_hook():
    """Remove the ZSH prompt hook."""
    from endless.setup import remove_prompt_hook
    remove_prompt_hook()


@setup.command("claude-hook")
def setup_claude_hook_cmd():
    """Install the Claude Code hook for activity monitoring."""
    from endless.setup import setup_claude_hook
    setup_claude_hook()


@setup.command("remove-claude-hook")
def setup_remove_claude_hook():
    """Remove the Claude Code hook."""
    from endless.setup import remove_claude_hook
    remove_claude_hook()


@setup.command("channel-plugin")
def setup_channel_plugin_cmd():
    """Register the MCP channel plugin for inter-session messaging."""
    from endless.setup import setup_channel_plugin
    setup_channel_plugin()


@setup.command("remove-channel-plugin")
def setup_remove_channel_plugin_cmd():
    """Remove the MCP channel plugin."""
    from endless.setup import remove_channel_plugin
    remove_channel_plugin()


# tmux integration command group (E-1236)
@main.group("tmux")
def tmux_cmd():
    """Endless tmux integration (ephemeral status line + popup menus)."""
    pass


@tmux_cmd.command("apply")
@click.option("--hotkey", default="e",
              help="Prefix-table key to bind for the popup menu (default: e)")
@click.option("--status-interval", type=int, default=2,
              help="tmux status-interval seconds (default: 2)")
def tmux_apply(hotkey, status_interval):
    """Configure the running tmux server for Endless (ephemeral).

    Enables a second status row, wires it to the printer, installs hotkey
    and right-click popup menus. No files are touched; reverses when the
    tmux server exits. Run once per tmux server start.
    """
    from endless.tmux_cmd import run_apply
    run_apply(hotkey, status_interval)


@tmux_cmd.command("status-line")
def tmux_status_line():
    """Print one styled line for tmux's status-format[1].

    Normally invoked by tmux on each refresh, not by humans. The tmux
    config calls the `endless-go tmux` Go binary directly to stay under the
    latency budget; this Python wrapper exists for parity and debugging.
    """
    from endless.tmux_cmd import run_status_line
    run_status_line()


@tmux_cmd.command("init")
@click.option("--hotkey", default="e",
              help="Prefix-table key to bind for the popup menu (default: e)")
@click.option("--status-interval", type=int, default=2,
              help="tmux status-interval seconds (default: 2)")
def tmux_init(hotkey, status_interval):
    """Init the current tmux server for Endless (gated by @server_uuid).

    Target for `set-hook -g session-created "run-shell 'endless tmux init'"`
    in ~/.tmux.conf. First call after a tmux server start runs reset +
    apply and stamps a fresh @server_uuid; subsequent calls no-op.
    """
    from endless.tmux_cmd import run_init
    run_init(hotkey, status_interval)


@tmux_cmd.command("reset")
def tmux_reset():
    """Mark dead-pane session rows ended for the current project.

    Wraps `endless-go tmux reset`. Useful for debugging when sessions
    survive a tmux server restart and won't get reaped naturally.
    """
    from endless.tmux_cmd import run_reset
    run_reset()


# Suggestions command group (E-918) — AI-agent rule-relaxation suggestions
@main.group("suggestions")
def suggestions_cmd():
    """Review AI-agent suggestions for relaxing enforcement rules."""
    pass


@suggestions_cmd.command("list")
@click.option("--project", default=None, help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True, help="Include accepted suggestions")
@click.option("--source", default=None, help="Filter by source (e.g. drift_detection)")
def suggestions_list(project, show_all, source):
    """List open suggestions (default) or all suggestions with --all."""
    from endless.suggestions_cmd import list_suggestions
    list_suggestions(project, show_all, source)


@suggestions_cmd.command("show")
@click.argument("suggestion_id", type=int)
def suggestions_show(suggestion_id):
    """Show details of a single suggestion."""
    from endless.suggestions_cmd import show_suggestion
    show_suggestion(suggestion_id)


@suggestions_cmd.command("accept")
@click.argument("suggestion_id", type=int)
@click.option("--type", "task_type",
              type=click.Choice(["task", "chore", "bug", "spike", "research"]),
              default="chore",
              help="Type of task to create (default: chore)")
@click.option("--parent", type=TASK_ID, default=None, help="Parent task ID")
@click.option("--project", default=None, help="Project name (default: from suggestion or cwd)")
def suggestions_accept(suggestion_id, task_type, parent, project):
    """Create a task from a suggestion and link them."""
    from endless.suggestions_cmd import accept_suggestion
    accept_suggestion(suggestion_id, task_type, parent, project)


@main.group("db")
def db_cmd():
    """Database administration."""
    pass


@db_cmd.command("apply-change")
@click.argument("path", type=click.Path(exists=True))
def db_apply_change(path):
    """Apply one per-ticket schema-change file (internal/schema/changes/<name>).

    Records the change in _schema_version; re-applying an already-applied change
    is a no-op. Driven by `just land`, one file per invocation.
    """
    from endless.event_bridge import apply_change
    result = apply_change(path)
    name = result.get("name") or path
    status = result.get("status") or "applied"
    if status == "skipped":
        click.echo(f"Change {name}: already applied (skipped).")
    else:
        click.echo(f"Change {name}: applied.")


@db_cmd.command("backup")
def db_backup():
    """Back up the database (VACUUM INTO a timestamped copy under backups/)."""
    from endless.event_bridge import backup_db
    backup_db()
    click.echo("Database backed up.")


@db_cmd.command("path")
def db_path():
    """Print the absolute path to the database selected by the global --db.

    For SQL-client debugging or scripting, and referenced by the --db gate's
    refusal message. Uses the single global --db (E-1476): run
    `endless db path --db=main` or `endless db path --db=sandbox`. Resolving
    --db=sandbox requires running from inside a self-dev worktree (the global
    --db handler errors otherwise). This command does not open the DB, so it is
    not subject to the worktree gate.
    """
    from endless import config

    if config.RESOLVED_CONFIG_DIR is None:
        raise click.ClickException(
            "db path needs an explicit --db value: "
            "'endless db path --db=main' or 'endless db path --db=sandbox'."
        )
    click.echo(str(config.DB_PATH))


# Hidden `endless internal` group: debug surfaces that shell through to
# endless-go internals. Hidden so they don't clutter the main help; reachable
# by name for debugging (E-1565).
@main.group("internal", hidden=True)
def internal_cmd():
    """Hidden helpers that shell through to endless-go internals."""
    pass


@internal_cmd.group("template")
def internal_template_cmd():
    """Render templates via the embedded Go renderer (E-1565)."""
    pass


@internal_template_cmd.command("render")
@click.argument("name")
@click.option("--project", default=None,
              help="Registered project name (overrides cwd-based resolution)")
def internal_template_render(name, project):
    """Render <name> from embedded templates; reads JSON vars on stdin.

    Lookup order at render time: <project_root>/.endless/templates/<name>.local.tmpl,
    then <name>.tmpl, then embedded. The committed `.tmpl` is materialized
    from embed on first render so users can customize it on disk.
    """
    import subprocess
    import sys

    from endless.event_bridge import _resolve_endless_go

    binary = _resolve_endless_go()
    args = [binary, "template", "render"]
    if project:
        args.extend(["--project", project])
    args.append(name)
    result = subprocess.run(
        args,
        input=sys.stdin.read(),
        capture_output=True, text=True, check=False,
    )
    sys.stdout.write(result.stdout)
    if result.stderr:
        sys.stderr.write(result.stderr)
    sys.exit(result.returncode)
