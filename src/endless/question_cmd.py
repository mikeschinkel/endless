"""Open questions on a task — `endless question` (E-2176).

A question belongs to a task, not to the session that asked it. Questions asked
together share a series; each is answered, withdrawn, rejected (status
`invalid`: the premise is wrong) or superseded on its own, and every one of
those last three carries a required reason. The plan stays
authoritative: an answer is not in force until it is folded into the plan, and
these rows are the audit trail of how the plan got there.

**Zero Python SQLite.** Reads go through `endless-go session-query`, writes
through `endless-go event emit`. The event front door allocates series and ids,
and refuses an illegal move before it reaches the ledger, so nothing here
re-implements the lifecycle — it lives in internal/questionstatus.
"""

import json
import subprocess

import click

from endless import agent_env, agent_help, config, provenance
from endless.event_bridge import emit_event

USER = "user"


def question_id_display(qid: int) -> str:
    """Format a question id for display: EQ-42."""
    return f"EQ-{qid}"


def _go(args: list[str]) -> dict | list:
    """Run an `endless-go session-query` read and return its decoded JSON."""
    from endless.event_bridge import _child_refusal, _resolve_endless_go

    config.require_db_context()
    cmd = [_resolve_endless_go(), *config.go_db_context_args(), "session-query", *args]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    if result.returncode != 0:
        # No TSV row: `endless question` landed after the refusal audit. But
        # nothing here needs classifying anyway — endless-go classified this at
        # the site that raised it and wrote its verdict at both ends of its
        # stderr, so relaying is the whole job. event_bridge's `_child_refusal`
        # is reused rather than copied because it also owns the one case that
        # does belong to this side: a child that exits non-zero having written
        # nothing said nothing to relay and nothing to classify by, which is
        # Endless broken. That fault line replaces the old `"<verb> failed"`
        # placeholder, which named the verb and told a reader nothing else.
        raise _child_refusal(result.stderr, result.returncode,
                             f"session-query {args[0]}")
    return json.loads(result.stdout)


def _target(*, task_id: int | None = None, question_id: int | None = None) -> dict:
    """The task (and its project) a question command acts on."""
    if task_id is not None:
        return _go(["question-target", "--task", str(task_id)])
    return _go(["question-target", "--question", str(question_id)])


def ask_questions(task_id: int, questions: tuple[str, ...]) -> None:
    """Ask one series of questions on a task."""
    texts = [q.strip() for q in questions]
    if not texts or any(not q for q in texts):
        # No TSV row (post-audit command). NO-REPORT: the caller composed these
        # strings, so a blank one is its own to fix, and this runs before the
        # emit — no series was allocated and no row was written, so a retry
        # cannot duplicate anything.
        raise agent_help.no_report(
            "Every question must be non-empty.",
            "Drop the empty argument, or give it text, and retry")
    tgt = _target(task_id=task_id)
    out = emit_event(
        kind="task.questions_asked",
        project=tgt["project"],
        entity_type="task",
        entity_id=str(task_id),
        payload={"questions": [{"question": q} for q in texts]},
        prompt_verb="asked from",
    ) or {}
    ids = ", ".join(out.get("ids", []))
    click.echo(
        click.style("•", fg="cyan")
        + f" Asked series {out.get('series', '?')} on E-{task_id}: {ids}"
    )


def resolve_answerer(by: str | None) -> str:
    """Who answered: `user` or a peer's `ES-<n>`.

    From a plain shell the answerer is the person typing, so the default is
    `user`. An agent must say which it is, because only the agent knows whether
    it is relaying the user's answer or settling the question itself — and a
    default there would record every peer decision as the user's, which is
    exactly the invisibility open questions exist to end.
    """
    if by is not None:
        return by.strip()
    if agent_env.present():
        # No TSV row (post-audit command), and the clearest NO-REPORT in this
        # file: it is reachable only when an agent is the caller, and the thing
        # it asks for is the one fact the agent already has — whether it is
        # relaying the user's words or settling the question itself. Asking the
        # user which of those they are watching would be absurd. Nothing was
        # recorded, so the remedy is simply the same command with --by.
        raise agent_help.no_report(
            "--by is required when an agent answers; nothing was recorded.",
            "Retry with --by user when relaying the user's answer, or "
            "--by ES-<n> (your own session) when answering as a peer",
            text="--by is required when an agent answers. Pass --by user when "
                 "relaying the user's answer, or --by ES-<n> (your own "
                 "session) when answering as a peer.",
        )
    return USER


def answer_question(question_id: int, answer: str, by: str | None) -> None:
    """Record an answer to one open question."""
    if not answer or not answer.strip():
        # No TSV row (post-audit command). NO-REPORT, and checked before the
        # emit: the answer is an argument the caller supplies, and a blank one
        # is a malformed invocation rather than a question about the answer.
        raise agent_help.no_report(
            "The answer must be non-empty.",
            "Retry with the answer text as the argument")
    answerer = resolve_answerer(by)
    tgt = _target(question_id=question_id)
    emit_event(
        kind="task_question.resolved",
        project=tgt["project"],
        entity_type="task_question",
        entity_id=str(question_id),
        payload={"status": "answered", "answer": answer, "answered_by": answerer},
        prompt_verb="answered from",
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Answered {question_id_display(question_id)} on E-{tgt['task_id']}"
        + f" (by {answerer})"
    )


# Verb → (status written, past-tense word for the confirmation line).
RESOLUTIONS = {
    "withdraw": ("withdrawn", "Withdrew"),
    "reject": ("invalid", "Rejected"),
    "supersede": ("superseded", "Superseded"),
}


def resolve_questions(verb: str, question_ids: tuple[int, ...], reason: str) -> None:
    """Move questions to withdrawn, invalid or superseded, saying why.

    The reason is required on every one of these moves: a question closed
    without an answer and without a stated reason cannot be reviewed, and the
    asker cannot tell what to ask instead.
    """
    if not reason or not reason.strip():
        # No TSV row (post-audit command). NO-REPORT: the reason is the
        # caller's own account of why it is closing the question, so it is the
        # caller who has to supply it. Raised before the loop, so no question
        # in the batch has moved.
        raise agent_help.no_report(
            "--reason is required and may not be empty.",
            "Retry with --reason naming why the question is being closed "
            "without an answer")
    status, done = RESOLUTIONS[verb]
    for qid in question_ids:
        tgt = _target(question_id=qid)
        emit_event(
            kind="task_question.resolved",
            project=tgt["project"],
            entity_type="task_question",
            entity_id=str(qid),
            payload={"status": status, "reason": reason},
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" {done} {question_id_display(qid)} on E-{tgt['task_id']} ({status})"
        )


def list_questions(task_id: int | None, project: str | None,
                   show_all: bool, as_json: bool) -> None:
    """List open questions — for one task, one project, or everywhere."""
    args = ["task-questions"]
    if task_id is not None:
        args += ["--id", str(task_id)]
    if project:
        args += ["--project", project]
    if show_all:
        args.append("--all")
    rows = provenance.rows_of(_go(args))

    if as_json:
        click.echo(json.dumps(rows, indent=2))
        return
    if not rows:
        scope = f"E-{task_id}" if task_id is not None else (project or "any project")
        kind = "questions" if show_all else "open questions"
        click.echo(f"No {kind} on {scope}.")
        return
    click.echo(render_questions(rows))


def render_questions(rows: list[dict]) -> str:
    """Group rows by task, then series; answers indent under their question."""
    lines: list[str] = []
    task = series = None
    for r in rows:
        if r["task_id"] != task:
            if lines:
                lines.append("")
            task, series = r["task_id"], None
            lines.append(click.style(f"E-{task}", bold=True) + f"  {r['task_title']}")
        if r["series"] != series:
            series = r["series"]
            lines.append(f"  series {series}")
        status = r["status"]
        color = "yellow" if status == "open" else None
        lines.append(
            f"    {question_id_display(r['id']):<8} "
            + click.style(f"{status:<10}", fg=color)
            + f" {r['question']}"
        )
        if r.get("answer"):
            lines.append(f"    {'':<8} {'':<10} → {r['answer']} (by {r['answered_by']})")
        if r.get("reason"):
            lines.append(f"    {'':<8} {'':<10} ✕ {r['reason']}")
    return "\n".join(lines)
