"""Bridge to the `endless-go event` subcommand for event-sourced writes."""

import json
import os
import secrets
import shutil
import socket
import subprocess
from pathlib import Path

import click

from endless import config


# Actor kinds for which session attribution is mandatory. If the resolver
# cannot produce a session_id for these kinds, emit_event refuses to fire
# rather than silently losing attribution downstream (E-1401).
#   - "cli":    interactive commands from a Claude pane; the resolver MUST
#               find a binding or the event is unattributable.
#   - "hook":   Claude hook callbacks; same as cli — bound to a session.
#   - "system": cron / migrations / one-shot tools; no session expected.
#   - "web":    web UI; attribution is via user_id, not session.
_ATTRIBUTION_REQUIRED: frozenset[str] = frozenset({"cli", "hook"})


def _resolve_endless_go() -> str:
    """Resolve which endless-go binary to exec for a schema-mutating call.

    Under --db sandbox in a self-dev worktree, prefer <worktree>/bin/endless-go
    so the embedded schema.sql matches the sandbox DB (E-1510). Otherwise fall
    back to the PATH-resolved global. Fails loudly if --db sandbox is active
    but the worktree binary is missing — silently using main's binary would
    re-introduce the schema-baseline mismatch this routing exists to prevent.
    """
    wt_bin = config.resolved_worktree_endless_go()
    if wt_bin is not None:
        if not wt_bin.is_file() or not os.access(wt_bin, os.X_OK):
            raise click.ClickException(
                f"--db sandbox is active but the worktree's endless-go "
                f"binary is missing or not executable:\n  {wt_bin}\n"
                f"Run `just build` from the worktree."
            )
        return str(wt_bin)
    found = shutil.which("endless-go")
    if not found:
        raise click.ClickException("endless-go binary not found on PATH.")
    return found


def emit_event(
    kind: str,
    project: str,
    entity_type: str,
    entity_id: str | int,
    payload: dict,
    actor_kind: str = "cli",
    actor_id: str | None = None,
    session_id: str | None = None,
    project_root: str | None = None,
    correlation_id: str | None = None,
    prompt_verb: str | None = None,
) -> dict | None:
    """Shell out to `endless-go event emit` to write an event and execute the DB mutation.

    Returns the parsed JSON output (contains ts, kind, and
    optionally id for created tasks), or None if stdout was empty.

    `session_id` populates `actor.session_id` on the emitted event so per-
    session activity queries can later filter events by session. When None,
    the resolver `_resolve_session_id_with_prompt()` (from task_cmd) is
    called automatically — so most callers don't need to pass it. That
    resolver prompts on a tty when n>1 sibling Claude panes are alive
    and refuses loudly off-tty.

    `prompt_verb` is forwarded to the resolver so the prompt question
    can describe the action concretely, e.g. "claimed for" yields
    "Which session should this be claimed for? [ID]:". When None, the
    resolver falls back to "associated with".

    For actor_kind in {"cli", "hook"}, an unresolvable session_id is a hard
    error: emit_event raises click.ClickException with an actionable message
    rather than firing an event whose attribution will be silently dropped
    (E-1401). For actor_kind in {"system", "web"}, an empty session_id is
    fine — system has no session, web attributes via user_id.

    Raises click.ClickException on failure.
    """
    node_id = _get_or_create_node_id()

    if actor_id is None:
        actor_id = f"{os.getenv('USER', 'unknown')}@{socket.gethostname()}"

    # Track whether session_id was provided by the caller. An explicit None
    # is still "resolver-derived" — the gate only fires when both the caller
    # AND the resolver couldn't produce one.
    if session_id is None:
        try:
            from endless.task_cmd import _resolve_session_id_with_prompt
        except ImportError:
            # Early bootstrap: task_cmd not yet importable. Emit without
            # a session_id; the gate below handles refusal for cli/hook.
            pass
        else:
            try:
                eid = _resolve_session_id_with_prompt(
                    project_name=project,
                    prompt_verb=prompt_verb,
                )
                if eid is not None:
                    session_id = str(eid)
            except click.ClickException:
                # Loud refusal from the resolver (off-tty multi-sibling
                # case). Propagate so the user sees the actionable
                # message and the gate below stays silent.
                raise
            except Exception:
                # Other resolver errors (defensive): swallow per E-1401's
                # contract — the gate below handles cli/hook refusal via
                # the missing-session-id path.
                pass

    if actor_kind in _ATTRIBUTION_REQUIRED and not session_id:
        raise click.ClickException(
            "Cannot determine the Endless session for this pane.\n\n"
            "To fix, do one of:\n"
            "  - Run this command from a Claude session pane.\n"
            "  - Export ENDLESS_SESSION_ID=\"$(endless session id)\".\n"
            "  - Run `endless task bind <task-id>` from this pane to "
            "connect it to a sibling Claude session in the same tmux "
            "window.\n"
        )

    if project_root is None:
        # Look up the project's registered path so events always land in the
        # main repo, even when invoked from inside a git worktree (where cwd
        # is the worktree, not the project root). Fall back to cwd only if
        # the project isn't registered (defensive; shouldn't happen in normal
        # flow since callers always pass a known project name).
        from endless import db
        row = db.query(
            "SELECT path FROM projects WHERE name = ? LIMIT 1",
            (project,),
        )
        if row:
            project_root = str(Path(row[0]["path"]).expanduser())
        else:
            project_root = str(Path.cwd())

    # E-1429: refuse early with the friendly message if --db is required but
    # missing, then thread the resolved DB context to endless-event.
    config.require_db_context()
    event_bin = _resolve_endless_go()
    cmd = [
        event_bin, *config.go_db_context_args(), "event", "emit",
        "--kind", kind,
        "--project", project,
        "--entity-type", entity_type,
        "--entity-id", str(entity_id),
        "--actor-kind", actor_kind,
        "--actor-id", actor_id,
        "--node-id", node_id,
        "--project-root", project_root,
        "--payload", json.dumps(payload),
    ]

    if session_id:
        cmd.extend(["--session-id", session_id])

    if correlation_id:
        cmd.extend(["--cid", correlation_id])

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        error_msg = result.stderr.strip()
        raise click.ClickException(f"Event write failed: {error_msg}")

    if result.stdout.strip():
        return json.loads(result.stdout.strip())
    return None


def apply_change(path: str) -> dict:
    """Shell out to `endless-go event apply-change <path>` and return parsed JSON.

    Applies one per-ticket schema-change file (internal/schema/changes/<name>)
    and records it in _schema_version. Returns {"name", "status"[, "reason"]}.
    Raises click.ClickException on failure (binary missing or non-zero exit).
    """
    config.require_db_context()  # E-1429
    event_bin = _resolve_endless_go()
    cmd = [event_bin, *config.go_db_context_args(), "event", "apply-change", str(path)]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        # endless-event prints JSON with an "error" field on failure.
        try:
            payload = json.loads(result.stdout.strip()) if result.stdout.strip() else {}
        except json.JSONDecodeError:
            payload = {}
        msg = payload.get("error") or result.stderr.strip() or "apply-change failed"
        raise click.ClickException(f"apply-change failed: {msg}")

    if not result.stdout.strip():
        return {}
    return json.loads(result.stdout.strip())


def backup_db() -> dict:
    """Shell out to `endless-go event backup` (VACUUM INTO a timestamped copy).

    Raises click.ClickException on failure (binary missing or non-zero exit).
    """
    config.require_db_context()  # E-1429
    event_bin = _resolve_endless_go()
    result = subprocess.run(
        [event_bin, *config.go_db_context_args(), "event", "backup"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        msg = result.stderr.strip() or "backup failed"
        raise click.ClickException(f"backup failed: {msg}")

    if not result.stdout.strip():
        return {"status": "ok"}
    return json.loads(result.stdout.strip())


def _get_or_create_node_id() -> str:
    """Read node_id from config.json, or generate and persist one."""
    config_path = config.CONFIG_FILE
    if not config_path.exists():
        raise click.ClickException(
            f"Config not found at {config_path}. Run 'endless scan' first."
        )

    data = json.loads(config_path.read_text())
    if "node_id" not in data:
        data["node_id"] = secrets.token_hex(2)  # 4 hex chars
        config_path.write_text(json.dumps(data, indent=2) + "\n")

    return data["node_id"]
