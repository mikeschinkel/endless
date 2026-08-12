"""`endless task report <id>` — the steering-prompt reporting command (E-1771).

End-of-session reports stop being freeform prose the agent composes. Instead the
agent runs this command; the command computes the facts it can (status, type,
successors, worktree state), gates the free-text escape hatches
(notes/questions) with a per-entry Haiku check, and prints a *steering prompt*
followed by the sanctioned block the agent appends. Persisting the facts as
queryable rows is E-1777.

E-1901 made every legitimate reason to speak a FIELD. `verify` joined
notes/questions, so the verify command — previously the one thing an agent had
to write in prose — renders inside the block.

E-1911 then INVERTED the relay contract, and the shape here follows from it. The
report is no longer the agent's whole message: the agent answers organically and
appends this block after a fixed separator. The verbose half carries context,
the appended half carries the guarantee — which is why reining in verbosity is
explicitly not this command's job, and why the concise half can be molded one
response shape at a time instead of having to be right for every reply at once.
The Stop gate that enforced the old contract is parked (see
`internal/hookcmd/relay_gate.go`); the checkpoint is still recorded so reviving
it is a one-line flip.

Which is why `_render_sanctioned` and `_render_agent_notes` are separate: the
first is a contract with the user, the second is advice to the agent that must
never be relayed.

It computes more than it prints. E-1880 held the command to the bar it already
enforces on the agent: a line is emitted only if the user could not already know
it, so status/landed/parentage stay out of the output even though the query
still returns them (fetching status also proves the task exists). That is what
makes "append this block unchanged" and the handoff's "do NOT recap task status,
phase, or relationships" satisfiable at the same time.

The normal path is `endless task report <id>` with NO payload: zero free-text
entries means zero Haiku calls, and the command steers on computed facts alone.
"""

import json
import subprocess

import click

from endless import config, internal_claude, report_prompts

_NOTE_KINDS = ("anomaly", "discovery")
_QUESTION_TYPES = ("text", "integer", "real", "boolean", "choice")

# E-1953 increment 1: the payload-and-render command is OFF.
#
# It emitted `Nothing to report.` most of the time and unrequested noise the
# rest, so the PostToolUse hook was demanding that every agent append a block
# carrying no signal. The root cause is structural, not cosmetic — two output
# channels exist and content lands in the cheap one — so the four render defects
# in E-1952's seed are subsumed by the rebuild rather than patched here.
#
# Everything below stays reachable on purpose: E-1953 increment 2 rebuilds the
# command IN PLACE (a minimizer over the agent's whole draft) and reuses the
# facts query, the prompt-layering module, and the checkpoint write. Deleting it
# would mean re-deriving all three.
REPORT_DISABLED = True

_DISABLED_MESSAGE = (
    "`endless task report` is disabled (E-1953).\n"
    "\n"
    "  It is being rebuilt as a minimizer over your entire draft reply rather\n"
    "  than a renderer of fields you volunteer. Until that lands, there is no\n"
    "  block to append — answer the user in your own words.\n"
    "\n"
    "  Do NOT hand-write a block, a separator, or a `Nothing to report.` line\n"
    "  to stand in for this command's output."
)


# --- payload parsing --------------------------------------------------------

def _parse_payload(payload: str | None) -> tuple[list[dict], list[dict], str | None]:
    """Parse and validate the agent's `--json` payload into (notes, questions, verify).

    Empty/omitted payload is the normal path → ([], [], None). Rejection is
    verb-check-style: a `click.ClickException` on malformed JSON, unknown keys,
    or an out-of-set `kind`/`type`. The agent supplies genuinely non-computable
    facts, open decisions, and the one verify command; nothing else has a place
    to go — which is the point (E-1901). The Stop gate can enforce a hard
    equality check only because every legitimate reason to speak has a field
    here; without them the gate would bounce agents for saying necessary things.
    """
    if payload is None or not payload.strip():
        return [], [], None
    try:
        data = json.loads(payload)
    except json.JSONDecodeError as e:
        raise click.ClickException(f"--json payload is not valid JSON: {e}")
    if not isinstance(data, dict):
        raise click.ClickException(
            'report payload must be a JSON object, e.g. {"notes": [...], "questions": [...]}'
        )
    unknown = set(data) - {"notes", "questions", "verify"}
    if unknown:
        raise click.ClickException(
            f"unknown report field(s): {', '.join(sorted(unknown))}. "
            "Only 'notes', 'questions', and 'verify' are accepted."
        )
    notes = _parse_notes(data.get("notes", []))
    questions = _parse_questions(data.get("questions", []))
    verify = _parse_verify(data.get("verify"))
    return notes, questions, verify


def _parse_verify(raw) -> str | None:
    """Validate the single verify command, or None when absent.

    Deliberately NOT Haiku-gated, unlike notes and questions. Those gates exist
    to catch ceremony dressed as content — a judgment call about prose. A command
    is not prose: it either is the one thing the user runs or it is not, and
    asking a model whether a shell command "reads as ceremony" would invent a
    failure mode rather than catch one.
    """
    if raw is None:
        return None
    if not isinstance(raw, str) or not raw.strip():
        raise click.ClickException(
            "'verify' must be a non-empty string — the single command the user "
            "runs to verify this task."
        )
    if "\n" in raw.strip():
        raise click.ClickException(
            "'verify' must be ONE command on one line. If verification takes "
            "several steps, fold them into a single script or a && chain — "
            "handing the user a checklist is the ceremony this replaces."
        )
    return raw.strip()


def _parse_notes(raw) -> list[dict]:
    if not isinstance(raw, list):
        raise click.ClickException("'notes' must be a list.")
    out = []
    for i, n in enumerate(raw):
        if not isinstance(n, dict):
            raise click.ClickException(f"note #{i + 1} must be an object.")
        unknown = set(n) - {"kind", "text"}
        if unknown:
            raise click.ClickException(
                f"note #{i + 1} has unknown field(s): {', '.join(sorted(unknown))}."
            )
        kind = n.get("kind")
        text = n.get("text")
        if kind not in _NOTE_KINDS:
            raise click.ClickException(
                f"note #{i + 1} kind must be one of {', '.join(_NOTE_KINDS)}; got {kind!r}."
            )
        if not isinstance(text, str) or not text.strip():
            raise click.ClickException(f"note #{i + 1} needs non-empty 'text'.")
        out.append({"kind": kind, "text": text.strip()})
    return out


def _parse_questions(raw) -> list[dict]:
    if not isinstance(raw, list):
        raise click.ClickException("'questions' must be a list.")
    out = []
    for i, q in enumerate(raw):
        if not isinstance(q, dict):
            raise click.ClickException(f"question #{i + 1} must be an object.")
        unknown = set(q) - {"text", "type", "style"}
        if unknown:
            raise click.ClickException(
                f"question #{i + 1} has unknown field(s): {', '.join(sorted(unknown))}."
            )
        text = q.get("text")
        qtype = q.get("type", "text")
        style = q.get("style")
        if not isinstance(text, str) or not text.strip():
            raise click.ClickException(f"question #{i + 1} needs non-empty 'text'.")
        if qtype not in _QUESTION_TYPES:
            raise click.ClickException(
                f"question #{i + 1} type must be one of {', '.join(_QUESTION_TYPES)}; got {qtype!r}."
            )
        entry = {"text": text.strip(), "type": qtype}
        if isinstance(style, str) and style.strip():
            entry["style"] = style.strip()
        out.append(entry)
    return out


# --- per-entry Haiku gate ---------------------------------------------------

def _classify(prompt_text: str, entry_text: str) -> tuple[bool, str | None]:
    """Ask Haiku to KEEP or DROP one free-text entry.

    Returns (keep, reason). Fail-open: any failure (timeout, missing binary,
    non-zero exit, unparseable reply) returns (True, None) so an infra outage
    can never block a genuine handoff — the gate is a best-effort quality
    filter, not a hard blocker (diverges from the fail-closed verb-gate because
    a false block here is far costlier).
    """
    prompt = prompt_text.format(text=entry_text)
    try:
        result = internal_claude.run_internal_claude(prompt, model="haiku", timeout=30)
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return True, None
    if result.returncode != 0:
        return True, None
    response = result.stdout.strip()
    upper = response.upper()
    if upper.startswith("KEEP"):
        return True, None
    if upper.startswith("DROP"):
        reason = response[len("DROP"):].lstrip(": ").strip()
        return False, (reason or None)
    # Unparseable → fail-open.
    return True, None


def _running_under_agent() -> bool:
    from endless.task_cmd import _running_under_agent as impl
    return impl()


def _bounce_note(text: str, reason: str | None) -> None:
    detail = f"\n  Check: {reason}" if reason else ""
    if _running_under_agent():
        raise click.ClickException(
            f"This note reads as ceremony, not a genuine non-computable fact:\n"
            f'    "{text}"{detail}\n'
            f"\n"
            f"  A handoff note must state something the reviewer could NOT compute\n"
            f"  from git or the task tracker. Remove it, or replace it with the\n"
            f"  actual out-of-band fact. Do not reword ceremony to pass this check."
        )
    raise click.ClickException(
        f'This note reads as ceremony rather than a real anomaly:\n    "{text}"{detail}\n'
        f"  Drop it or replace it with the genuine out-of-band fact."
    )


def _bounce_question(text: str, reason: str | None) -> None:
    detail = f"\n  Check: {reason}" if reason else ""
    if _running_under_agent():
        raise click.ClickException(
            f"This question restates settled state rather than asking for a real decision:\n"
            f'    "{text}"{detail}\n'
            f"\n"
            f"  A handoff question must be a decision the user has to make before\n"
            f"  the work can proceed. Remove it, or replace it with the actual open\n"
            f"  decision. Do not invent a decision to pass this check."
        )
    raise click.ClickException(
        f'This question restates settled state rather than asking a real decision:\n    "{text}"{detail}\n'
        f"  Drop it or replace it with the genuine open decision."
    )


def _gate(notes: list[dict], questions: list[dict], prompts: dict[str, str]) -> None:
    """Bounce any ceremonial note/question. Fires only when entries exist."""
    for n in notes:
        keep, reason = _classify(prompts[report_prompts.NOTE_CHECK], n["text"])
        if not keep:
            _bounce_note(n["text"], reason)
    for q in questions:
        keep, reason = _classify(prompts[report_prompts.QUESTION_CHECK], q["text"])
        if not keep:
            _bounce_question(q["text"], reason)


# --- computed facts ---------------------------------------------------------

def _compute_facts(item_id: int) -> dict:
    """Fetch the computed report facts from Go (status, type, landed,
    successors). Not all of them are rendered — see `_render_sanctioned`."""
    from endless.event_bridge import _resolve_endless_go
    go_bin = _resolve_endless_go()
    config.require_db_context()
    try:
        result = subprocess.run(
            [go_bin, *config.go_db_context_args(),
             "session-query", "task-report", "--id", str(item_id)],
            capture_output=True, text=True, timeout=10,
        )
    except (FileNotFoundError, subprocess.SubprocessError) as e:
        raise click.ClickException(f"endless-go failed: {e}")
    if result.returncode != 0:
        raise click.ClickException(result.stderr.strip() or "endless-go failed")
    try:
        return json.loads(result.stdout)
    except ValueError:
        raise click.ClickException("endless-go returned malformed report facts")


def _compute_anomalies() -> list[str]:
    """Genuine git/worktree anomaly lines for the current worktree, or [].

    Reuses the DB-free `session-query worktree-anomalies` probe. When cwd is not
    inside an endless worktree there is nothing to inspect → []. Anomalies are an
    AGENT concern; the steering prompt tells the agent to surface them to the
    user only when unexpected.
    """
    import shutil
    from endless.worktree_cmd import worktree_root_for_cwd
    root = worktree_root_for_cwd()
    if root is None:
        return []
    binary = shutil.which("endless-go")
    if not binary:
        return []
    main_root = str(root.parents[2])
    try:
        result = subprocess.run(
            [binary, "session-query", "worktree-anomalies",
             "--worktree-path", str(root), "--project-root", main_root],
            capture_output=True, text=True, timeout=10,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return []
    return [ln for ln in result.stdout.splitlines() if ln.strip()]


# --- render -----------------------------------------------------------------

def _ref_line(refs: list[dict]) -> str:
    return ", ".join(f"E-{r['id']} [{r['status']}]" for r in refs)


def _render_sanctioned(facts: dict, notes: list[dict], questions: list[dict],
                       verify: str | None) -> str:
    """Assemble the SANCTIONED block — the exact text the user is to receive.

    Everything in here the agent appends unchanged, so a line that does not
    belong in the user's hands must not appear here. That is why the worktree
    anomalies moved out to `_render_agent_notes`.

    The command holds itself to the bar it already enforces on the agent
    (E-1880): `note-check` DROPs any agent note that "restates something already
    visible in git/task state", so the command must not emit such a line either.
    That rules out `Task:` (the user typed the id), `Status:` (`session status`
    renders it — the flip *is* the contract) and `Landed:` (computable from
    `task show`), all of which the handoff simultaneously tells the session NOT
    to recap. E-1911 retires `Children:` on the same grounds, one bar later: it
    was kept because the epic handoff asked the session to lead with the state
    of its children, so the two surfaces each justified the other while
    duplicating `session status`. Both are gone. What survives is what a query
    would not have told them: the verify command, the follow-ups this session
    filed, and the gated free text.

    Empty categories are omitted; an all-empty block is legitimate and
    `report_item` substitutes the `nothing-to-report` text.
    """
    lines: list[str] = []

    # Verify leads: it is the one line the user acts on. Backticked so an agent
    # copying the block verbatim reproduces the formatting rather than adding it
    # and tripping the gate.
    if verify:
        lines.append(f"Verify: `{verify}`")

    successors = facts.get("successors") or []
    if successors:
        lines.append(f"Follow-ups you filed: {_ref_line(successors)}")

    if notes:
        lines.append("Notes to relay:")
        for n in notes:
            lines.append(f"  - [{n['kind']}] {n['text']}")

    if questions:
        lines.append("Questions for the user:")
        for q in questions:
            style = f"/{q['style']}" if q.get("style") else ""
            lines.append(f"  - {q['text']}  ({q['type']}{style})")

    return "\n".join(lines)


def _render_agent_notes(anomalies: list[str]) -> str:
    """Assemble the AGENT-facing addendum — read by the agent, never relayed.

    Worktree anomalies are conditional by nature: the agent is told to surface
    them only if unexpected. That makes them the one thing that cannot live in
    the sanctioned block, which is unconditional by construction — including them
    would force the relay of noise.

    So they render BEFORE the separator, with the escape route named: an anomaly
    worth the user's attention is re-run material (`--json` anomaly note), which
    puts it back inside the block. Their position is not cosmetic — the block has
    an opening separator and no closing one, so anything printed after the
    separator is part of the block by definition (E-1911).
    """
    if not anomalies:
        return ""
    lines = [
        "",
        "NOT part of the report — for you, not the user. Uncommitted/worktree "
        "state; surface it ONLY if unexpected (e.g. a change you did not make), "
        "and if it is, re-run `endless task report` with a --json anomaly note "
        "so it lands inside the block rather than in prose beside it:",
    ]
    for a in anomalies:
        lines.append(f"  - {a}")
    return "\n".join(lines)


def _record_checkpoint(sanctioned: str) -> None:
    """Record the sanctioned text as this session's relay checkpoint.

    The gate that consumed it is parked (E-1911), so this currently records for
    a reader that is not listening. Kept anyway, and deliberately: the checkpoint
    is the only per-turn record of what the block actually said, and reviving the
    gate under the append contract is a one-line flip that would otherwise also
    need this write restored and re-proven.

    Best-effort by design. An unresolved session (no tmux, no CLAUDECODE, a bare
    shell) means no checkpoint — the report still prints and is still correct. A
    report that refused to run because it could not arm itself would break
    handoffs to punish nobody.
    """
    from endless.task_cmd import _current_endless_session_id
    from endless.event_bridge import _resolve_endless_go

    session_id = _current_endless_session_id()
    if session_id is None:
        return
    try:
        subprocess.run(
            [_resolve_endless_go(), *config.go_db_context_args(),
             "session-query", "relay-checkpoint", "--session-id", str(session_id)],
            input=sanctioned, capture_output=True, text=True, timeout=10,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return


def report_item(item_id: int, payload: str | None) -> None:
    """Compute facts, gate the free-text payload, print the steer, arm the gate.

    One path, not two. Before E-1901 an empty fact block took a separate branch
    that told the agent to compose its own one-line sign-off — freehand prose,
    and the exact latitude the computed half exists to remove. Now the empty case
    has a sanctioned string of its own (`nothing-to-report`), so both cases
    render and print identically.

    The separator ALWAYS prints, empty case included. That is what makes a
    swallowed block detectable: an absent block is ambiguous between "there were
    no facts" and "the block failed to render", and the user cannot tell those
    apart without going and checking by hand — which is the cost this whole
    surface exists to remove. `Nothing to report.` under the separator states the
    null result instead of leaving it to be inferred from silence.

    Print order is load-bearing: steer, then the agent-facing addendum, then the
    separator and the block LAST. The block has no closing marker, so everything
    after the separator is the block.

    Refuses outright while `REPORT_DISABLED` holds (E-1953). The refusal is the
    FIRST statement so no model call, DB read, or checkpoint write happens on the
    way to it — a disabled command that still costs a Haiku round-trip is not
    disabled.
    """
    if REPORT_DISABLED:
        raise click.ClickException(_DISABLED_MESSAGE)

    notes, questions, verify = _parse_payload(payload)
    prompts = report_prompts.load_prompts()
    _gate(notes, questions, prompts)  # raises on a bounced entry
    facts = _compute_facts(item_id)
    anomalies = _compute_anomalies()

    sanctioned = _render_sanctioned(facts, notes, questions, verify).strip()
    if not sanctioned:
        sanctioned = prompts[report_prompts.NOTHING_TO_REPORT].strip()

    click.echo(prompts[report_prompts.STEER])
    agent_notes = _render_agent_notes(anomalies)
    if agent_notes:
        click.echo(agent_notes)
    click.echo()
    click.echo(report_prompts.SEPARATOR)
    click.echo(sanctioned)
    _record_checkpoint(sanctioned)
