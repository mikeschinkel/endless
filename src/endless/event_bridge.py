"""Bridge to the `endless-go event` subcommand for event-sourced writes."""

import json
import os
import secrets
import shutil
import socket
import subprocess
from pathlib import Path

import click

from endless import agent_help
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


def _blank_line_for_humans() -> None:
    """The blank spacer line that has always preceded the two refusals below.

    It is kept for a person: it separates the refusal from whatever the land was
    printing when it hit, and this module must not change what a human reads.
    It is NOT written for an agent, because a leading blank line is the one
    thing the bracketed verdict cannot survive — `head -1` would return the
    blank line instead of the verdict the bracket exists to guarantee.
    """
    if not agent_help.agent_facing():
        agent_help.info("", err=True)


def _child_refusal(text: str, returncode: int, what: str):
    """The refusal to raise for a non-zero `endless-go` run. RAISE the result.

    Go classifies its own refusals: `internal/refusal` decides the class at the
    site that raises it and writes the verdict line at BOTH ends of the message.
    So the only correct thing to do with that text here is pass it through.
    The prefixes this used to add — "Event write failed: ", "backup failed: ",
    "schema initialization failed: " — put Python's voice in front of Go's
    sentinel, flattened three different Go classes under one Python wording, and
    left an agent two directives to reconcile, one of which nobody had chosen.

    `text` is Go's stderr — Go's own words.

    Silence is the one case that belongs to this side. A child that exits
    non-zero having written nothing said nothing to relay and gave nothing to
    classify by, which is Endless broken rather than Endless refusing — so it
    becomes a fault naming the call that produced it, not an empty relay.
    """
    text = (text or "").rstrip("\n")
    if text.strip():
        return agent_help.relay(text, exit_code=returncode or 1)
    return agent_help.fault(
        f"`endless-go {what}` exited {returncode} without writing a reason.",
        exit_code=returncode or 1,
    )


def _display_path(p: str) -> str:
    """Render an absolute path ~-relative for user-facing messages."""
    home = str(Path.home())
    if p.startswith(home):
        return "~" + p[len(home):]
    return p


def _resolve_endless_go(override: str | None = None) -> str:
    """Resolve which endless-go binary to exec for a schema-mutating call.

    When `override` is given, it is an explicit, caller-pinned binary path that
    wins over every other resolution: validate it exists and is executable, then
    return it. A self_dev land passes the worktree binary here (E-1664) — during
    a land that binary is the only one whose embedded schema/enums match the rows
    the land just applied, so it is an invariant of the operation, not a choice.
    A set-but-invalid override fails loudly rather than silently sliding to the
    stale global (which is the exact bug class this exists to prevent).

    With no override: under --db sandbox in a self-dev worktree, prefer
    <worktree>/bin/endless-go so the embedded schema.sql matches the sandbox DB
    (E-1510). Otherwise fall back to the PATH-resolved global. Fails loudly if
    --db sandbox is active but the worktree binary is missing — silently using
    main's binary would re-introduce the schema-baseline mismatch this routing
    exists to prevent.
    """
    if override is not None:
        ov = Path(override)
        if not ov.is_file() or not os.access(ov, os.X_OK):
            _blank_line_for_humans()
            # The remedy deliberately does not name a build command. This branch
            # is only reachable in a self-dev checkout, but shipped code cannot
            # know what that checkout builds with, and the path is the one thing
            # that identifies what has to exist.
            raise agent_help.no_report(
                f"The pinned endless-go binary is missing or not executable: "
                f"{_display_path(str(ov))}. Nothing ran.",
                "Rebuild the binary at that path and retry",
                text=f"The pinned endless-go binary is missing or not "
                     f"executable:\n\n    {_display_path(str(ov))}\n",
            )
        return str(ov)
    wt_bin = config.resolved_worktree_endless_go()
    if wt_bin is not None:
        if not wt_bin.is_file() or not os.access(wt_bin, os.X_OK):
            _blank_line_for_humans()
            raise agent_help.no_report(
                f"The worktree's endless-go binary is missing or not "
                f"executable: {_display_path(str(wt_bin))}. Nothing ran.",
                "Rebuild the binary at that path and retry",
                text=f"The worktree's endless-go binary is missing or "
                     f"not executable:\n\n    {_display_path(str(wt_bin))}\n",
            )
        return str(wt_bin)
    found = shutil.which("endless-go")
    if not found:
        # REPORT rather than NO-REPORT: every other resolution has already been
        # tried, so there is no second way for the agent to run this command.
        # Installing the binary or putting it on PATH happens outside the
        # session entirely.
        raise agent_help.report(
            "endless-go binary not found on PATH. Nothing ran.",
            "installing endless-go, or putting it on the PATH Endless's "
            "subprocesses inherit, is theirs to do",
            text="endless-go binary not found on PATH.",
        )
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
    endless_go_bin: str | None = None,
    ts: str | None = None,
) -> dict | None:
    """Shell out to `endless-go event emit` to write an event and execute the DB mutation.

    Returns the parsed JSON output (contains ts, kind, and
    optionally id for created tasks), or None if stdout was empty.

    `session_id` populates `actor.session_id` on the emitted event so
    events can be attributed to the session that caused them. When None,
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

    `endless_go_bin` pins the exact endless-go binary to exec, bypassing the
    default PATH/sandbox resolution. A self_dev land passes the worktree binary
    here so the task.landed emit uses the build whose schema matches the rows it
    just applied (E-1664). None ⇒ default resolution.

    `ts` sets the event timestamp to a historical RFC3339 instant instead of
    now(). A record-only landing backfill (E-1719) passes the merge commit's
    date so `landed_at` records when the work actually landed. None ⇒ now().

    Raises click.ClickException on failure.
    """
    node_id = _get_or_create_node_id()

    if actor_id is None:
        actor_id = f"{os.getenv('USER', 'unknown')}@{socket.gethostname()}"

    # E-1444: position-anywhere --no-session flag (set by DBAwareGroup.main)
    # downgrades cli/hook callers to system, the explicit escape hatch for
    # plain-shell triage filings, cron, and scripts. system is already exempt
    # from the gate, so the rest of this function proceeds unchanged.
    if config.NO_SESSION and actor_kind in _ATTRIBUTION_REQUIRED:
        actor_kind = "system"
        session_id = None
    elif session_id is None and actor_kind in _ATTRIBUTION_REQUIRED:
        # Only cli/hook auto-resolve a session. A system actor (e.g. the E-1719
        # record-only backfill) deliberately records no session, so it must not
        # pick up whatever live pane happens to be running.
        # Track whether session_id was provided by the caller. An explicit None
        # is still "resolver-derived" — the gate only fires when both the caller
        # AND the resolver couldn't produce one.
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
        # The --no-session bullet moves to `human_remedy`, which is shown to a
        # person and withheld from an agent. It is a bypass, and the thing it
        # bypasses is the attribution this gate exists to keep (E-1401): an
        # agent handed that bullet takes it, files the event as actor.kind=system
        # and the refusal has achieved nothing. A person choosing to file
        # without a session binding — cron, a script, a plain-shell triage
        # filing — is the case the flag was added for, and they still read it.
        raise agent_help.no_report(
            "Cannot determine the Endless session for this pane. No event was "
            "written.",
            'Export ENDLESS_SESSION_ID="$(endless session id)", or run '
            "`endless task bind <task-id>` from this pane, and retry",
            text="Cannot determine the Endless session for this pane.\n\n"
                 "To fix, do one of:\n"
                 "  - Run this command from a Claude session pane.\n"
                 "  - Export ENDLESS_SESSION_ID=\"$(endless session id)\".\n"
                 "  - Run `endless task bind <task-id>` from this pane to "
                 "connect it to a sibling Claude session in the same tmux "
                 "window.",
            human_remedy="\n  - Pass --no-session (accepted in any position) to "
                         "file without a Claude session binding "
                         "(actor.kind=system; for cron, scripts, plain-shell "
                         "triage filings).\n",
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
            from endless.project_path import resolved
            project_root = str(resolved(row[0]["path"]))
        else:
            project_root = str(Path.cwd())

    # E-1429: refuse early with the friendly message if --db is required but
    # missing, then thread the resolved DB context to endless-event.
    config.require_db_context()
    event_bin = _resolve_endless_go(override=endless_go_bin)
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

    # E-1719: a record-only/historical backfill passes the real commit date so
    # landed_at reflects when the work landed, not now().
    if ts:
        cmd.extend(["--ts", ts])

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise _child_refusal(result.stderr, result.returncode, "event emit")

    if result.stdout.strip():
        return json.loads(result.stdout.strip())
    return None


def project_registry(verb: str, *args: str):
    """Shell out to `endless-go project <verb> <args...>` and return its answer.

    The Go owner of the projects registry's "not a project" state (E-2251):
    `resolve` answers whether directories are projects, ignored or neither;
    `ignore`, `activate` and `clear` write the status; `list-ignored` lists it.
    Machine-local registry writes, so no event and no ledger line.

    A list payload comes back unwrapped (provenance.rows_of), an object as-is.
    A non-zero exit relays Go's own refusal.
    """
    from endless import provenance

    config.require_db_context()  # E-1429
    cmd = [_resolve_endless_go(), *config.go_db_context_args(), "project", verb, *args]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise _child_refusal(result.stderr, result.returncode, f"project {verb}")
    payload = json.loads(result.stdout) if result.stdout.strip() else {}
    if isinstance(payload, dict) and "rows" in payload:
        return provenance.rows_of(payload)
    return payload


def init_schema(endless_go_bin: str | None = None) -> dict:
    """Shell out to `endless-go event migrate` to build or update the schema.

    Python does not own a migration runner. It used to: db.py read
    internal/schema/schema.sql off disk and executescript() it, which made the
    Python CLI a second applier of the schema that nothing would remember to
    tell about a new migration, and which only worked when endless was installed
    from a source checkout -- the file it reached for is not shipped with the
    tool. Go embeds the migration set in the binary, so it always has one.

    Returns {"status", "db", "version", "latest"}. Raises click.ClickException
    on failure (binary missing or non-zero exit), like backup_db, whose shape
    this follows.
    """
    config.require_db_context()  # E-1429
    event_bin = _resolve_endless_go(override=endless_go_bin)
    result = subprocess.run(
        [event_bin, *config.go_db_context_args(), "event", "migrate"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise _child_refusal(result.stderr, result.returncode, "event migrate")

    if not result.stdout.strip():
        return {"status": "ok"}
    return json.loads(result.stdout.strip())


def upgrade_db() -> dict:
    """Shell out to `endless-go event upgrade`: back up, migrate forward, reseed.

    The recovery path (E-2020). The Go side opens the database FILE rather than
    through the application's connect, so it works while every ordinary command
    is refusing the database — a version mismatch, or an enum mirror drifted far
    enough to fail-close the gates. It still refuses a worktree-built binary
    aimed at the main database (ED-1601), and under `--db main` this resolves
    the installed binary anyway.

    Returns {"status", "from", "to", "db", "backup", "backup_skipped"}. Raises
    click.ClickException on failure.
    """
    config.require_db_context()  # E-1429
    event_bin = _resolve_endless_go()
    result = subprocess.run(
        [event_bin, *config.go_db_context_args(), "event", "upgrade"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        # The TSV has no row for this one, but its four siblings in this file
        # all do, all say CONDITIONAL relay, and this is the same seam: Go
        # chose the class at the site that raised it. So the prefix goes the
        # way the others' did — `"database upgrade failed: "` in front of Go's
        # own sentinel was Python's voice over a verdict Python did not choose,
        # and `_child_refusal` is the one place that decides what to do with a
        # child's words, silence included.
        raise _child_refusal(result.stderr, result.returncode, "event upgrade")
    return json.loads(result.stdout.strip())


def backup_db(endless_go_bin: str | None = None) -> dict:
    """Shell out to `endless-go event backup` (VACUUM INTO a timestamped copy).

    Raises click.ClickException on failure (binary missing or non-zero exit).

    endless_go_bin pins the binary, exactly as `emit_event` does (E-1664). A
    backup is a VACUUM INTO
    of the file and never goes through the application's connect, so which
    build takes it does not matter to the snapshot.
    """
    config.require_db_context()  # E-1429
    event_bin = _resolve_endless_go(override=endless_go_bin)
    result = subprocess.run(
        [event_bin, *config.go_db_context_args(), "event", "backup"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise _child_refusal(result.stderr, result.returncode, "event backup")

    if not result.stdout.strip():
        return {"status": "ok"}
    return json.loads(result.stdout.strip())


def clear_land_schema_faults(
    db_version: int, binary_version: int, since: str, by: str,
    endless_go_bin: str | None = None,
) -> int:
    """Shell out to `endless-go errors clear-land-schema` and return how many
    incidents it cleared (E-2205).

    Clears the open ERR-0020 "database is at schema v<db_version>, endless-go
    carries v<binary_version>" incident first seen at or after `since` (the
    errors table's '%Y-%m-%dT%H:%M:%S' UTC form) — the one a self_dev land's own
    migration caused. The fingerprint is built Go-side, beside the code that
    records it, so the two cannot drift. endless_go_bin pins the binary, exactly
    as `emit_event` does (E-1664).

    Raises click.ClickException on failure (binary missing or non-zero exit).
    """
    config.require_db_context()  # E-1429
    event_bin = _resolve_endless_go(override=endless_go_bin)
    result = subprocess.run(
        [event_bin, *config.go_db_context_args(), "errors", "clear-land-schema",
         "--db-version", str(db_version),
         "--binary-version", str(binary_version),
         "--since", since, "--by", by],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise _child_refusal(result.stderr, result.returncode,
                             "errors clear-land-schema")
    return int(json.loads(result.stdout.strip() or "{}").get("cleared", 0))


def _get_or_create_node_id() -> str:
    """Read node_id from config.json, or generate and persist one."""
    config_path = config.CONFIG_FILE
    if not config_path.exists():
        raise agent_help.no_report(
            f"Config not found at {config_path}. Nothing was written.",
            "Run `endless project scan`, then retry",
            text=f"Config not found at {config_path}. Run 'endless project "
                 f"scan' first.",
        )

    data = json.loads(config_path.read_text())
    if "node_id" not in data:
        data["node_id"] = secrets.token_hex(2)  # 4 hex chars
        config_path.write_text(json.dumps(data, indent=2) + "\n")

    return data["node_id"]
