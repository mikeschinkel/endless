"""`endless correct` — a user-only correcting entry for ledger state (E-2486).

The ledger is append-only, and until this the only way to put a wrong row right
was to replay ordinary transitions through their gates — which can deadlock
when a gate was skipped on the way in, and leaves transitions in the history
that describe no real event. Accounting solves the same problem with a
correcting journal entry: never erase, post an entry that says what it corrects
and why. That is what this command records, through `endless-go correct`:

- a `ledger.row_changed` entry — the row, the operation and the values, which
  the executor and the projector both apply and which triggers none of an
  existing kind's effects;
- a `ledger.corrected` entry — the reason, the before values, whether gates
  were skipped, and the byte offset of each change entry in the segment both
  were written to. With no change it is an annotation.

Both are written under the ledger lock as adjacent lines and committed in one
ledger commit whose subject names the correction, so `git log` is where
corrections are read.

This module performs no DB access of its own: everything that reads or writes
rows — the before values, the gates, the invariants, the apply — is Go's. What
Python owns is the one thing it must decide before anything runs: that a
PERSON is asking. An agent is refused outright (REPORT: the ledger being wrong
is the user's to judge), and the Go half refuses it again from its own
environment, so neither side relies on the other.
"""

from __future__ import annotations

import json
import subprocess

import click

from endless import agent_env, agent_help, config, event_bridge


def refuse_agent() -> None:
    """Refuse an agent before anything else runs. The same words Go uses."""
    if not agent_env.present():
        return
    raise agent_help.report(
        "`endless correct` is the user's command: an agent does not correct "
        "the ledger. Nothing was recorded.",
        "whether the recorded state is wrong, and what the correction and its "
        "reason should be",
    )


# The targets, as Go corrects them (internal/events/correction_targets.go).
# cli.py builds one subcommand group per name; a test holds this list equal
# to `endless-go correct --list-targets`, so a target added in Go without a
# subcommand here fails the suite rather than going unreachable.
TARGETS: tuple[str, ...] = (
    "task", "task_dep", "project", "session", "session_status",
    "session_tasks", "note", "decision", "decision_relation", "task_question",
    "shipped_file", "plan_item", "session_hidden_task", "session_pinned_task",
    "session_notice", "task_landing", "task_reconciliation",
    "task_reconciliation_entry", "task_post_land_entry",
    "conversation", "message", "project_next",
)

# Entity types with no table: they take annotations only.
ANNOTATION_ONLY: frozenset[str] = frozenset({"conversation", "message", "project_next"})

OPS: tuple[str, ...] = ("update", "insert", "delete", "annotate")


def correct(target: str, op: str, key: str | None, pairs: tuple[str, ...],
            reason: str, skip_gates: bool, project: str | None) -> None:
    """Entry point bound by cli.py for `endless correct <target> <op> [KEY]`.

    `op` is one of OPS; `pairs` are the col=value arguments of an update or an
    insert and are empty for a delete or an annotation. The Go command still
    takes the operation as flags (--set, --insert, --delete); this maps the
    subcommand onto them.
    """
    from endless.task_cmd import _resolve_project

    refuse_agent()
    if op not in OPS:  # pragma: no cover - cli.py builds the subcommands
        raise agent_help.no_report(f"correct: unknown operation {op!r}",
                                   "Use update, insert, delete or annotate")
    if not reason or not reason.strip():
        raise agent_help.no_report(
            "correct: --reason is required; a correction records why. "
            "Nothing was recorded.",
            "Retry with --reason <text>",
        )

    _, project_name = _resolve_project(project)
    node_id = event_bridge._get_or_create_node_id()
    actor_kind, actor_id, session_id = event_bridge.resolve_actor(
        "cli", None, None, project_name, prompt_verb="corrected for")
    project_root = event_bridge.project_root_for(project_name)

    config.require_db_context()  # E-1429
    cmd = [
        event_bridge._resolve_endless_go(), *config.go_db_context_args(),
        "correct",
        "--target", target,
        "--reason", reason,
        "--project", project_name,
        "--project-root", project_root,
        "--node-id", node_id,
        "--actor-kind", actor_kind,
        "--actor-id", actor_id,
    ]
    if key:
        cmd += ["--key", key]
    if op == "update":
        for pair in pairs:
            cmd += ["--set", pair]
    elif op == "insert":
        for pair in pairs:
            cmd += ["--insert", pair]
    elif op == "delete":
        cmd.append("--delete")
    if skip_gates:
        cmd.append("--skip-gates")
    if session_id:
        cmd += ["--session-id", session_id]

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise event_bridge._child_refusal(result.stderr, result.returncode, "correct")

    out = json.loads(result.stdout.strip() or "{}")
    _render(out, reason, skip_gates)


def go_targets() -> dict:
    """The targets as the Go binary reports them (`--list-targets`); for the
    parity test and nothing else at runtime."""
    result = subprocess.run(
        [event_bridge._resolve_endless_go(), "correct", "--list-targets"],
        capture_output=True, text=True, check=True)
    return json.loads(result.stdout)


def _render(out: dict, reason: str, skip_gates: bool) -> None:
    """One line for what changed, one for the record, one per note."""
    click.echo(click.style("•", fg="cyan") + " " + out.get("summary", "Corrected"))
    refs = out.get("refs") or []
    record = (f"Recorded as ledger.corrected in {out.get('segment', 'the ledger')}"
              if out.get("segment") else "Recorded as ledger.corrected")
    if refs:
        record += f", covering {len(refs)} change entr{'y' if len(refs) == 1 else 'ies'}"
    else:
        record += " (an annotation)"
    if skip_gates:
        record += ", gates skipped"
    click.echo(f"  {record}. Reason: {reason}")
    for note in out.get("notes") or []:
        click.echo(click.style("  ↳ ", fg="yellow") + note)
