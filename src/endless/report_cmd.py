"""`endless task report` — the enforced minimizer (E-1771, rebuilt by E-1953).

The agent hands over its ENTIRE freeform draft reply. An adversarial model
deletes what the user did not ask for. The output is the only thing the agent
may say, and a Stop hook holds the turn until it says exactly that.

Why the previous design failed, since this one is shaped entirely by it: the
command used to take a structured payload — `verify`, `notes`, `questions` — and
render a block the agent appended to its own prose. Two output channels existed,
so content landed in the cheap one. The agent wrote the verify command in prose
and then told `task report` there was nothing to report. Every variant of that
design fails identically, because in all of them the agent decides what to
volunteer, which means the agent is still judging its own output in the same
breath as writing it. The minimizer is a SECOND PARTY. That is the fix; the four
render defects in E-1952's seed dissolve as a side effect.

Three properties follow from that and are not negotiable:

  * Input is plain markdown, whole, via `--draft-file`. Not JSON, not fields,
    not a heredoc. Any format that asks the agent to pre-structure or tag its
    draft reintroduces the restatement tax that caused the routing failure, and
    escaping failures (backticks, quotes, `$`) become retries.
  * The raw draft is PERSISTED and retrievable with `--raw`. This is what lets
    the minimizer be maximally aggressive at zero risk: nothing is destroyed,
    only hidden.
  * The appeal is bounded at one, and the appeal text goes through the minimizer
    too. An unbounded appeal is a second bite the agent will always take.

Every turn, not just handoffs — the 5000-character task review that triggered
this redesign was a mid-conversation turn.

Latency is accepted. Endless is built for many parallel sessions; the requester
works elsewhere while a turn is being minimized.
"""

import subprocess

import click

from endless import config, internal_claude, report_prompts

# The minimizer's model and effort. Not tunable, on the same principle as the
# old ceremony gate: the WORDING is the lever the user gets, because that is
# where the judgment lives. A per-machine model override would make two installs
# of Endless disagree about what "minimized" means while both looked identical,
# and would make the eval corpus incomparable across machines.
#
# Sonnet rather than the Haiku the old KEEP/DROP gate used. This is a different
# job: preserving a table byte-for-byte while deleting the paragraph beside it is
# an editing task with hard invariants, not a binary classification, and the
# invariants are exactly what a smaller model drops first.
_MODEL = "sonnet"
_EFFORT = "medium"
_TIMEOUT = 180


# --- draft I/O --------------------------------------------------------------

def _read_draft(path: str) -> str:
    """Read the agent's draft, or fail with a message that names the fix."""
    from pathlib import Path
    p = Path(path).expanduser()
    if not p.exists():
        raise click.ClickException(f"Draft file not found: {p}")
    try:
        draft = p.read_text()
    except OSError as e:
        raise click.ClickException(f"Cannot read draft file {p}: {e}")
    if not draft.strip():
        raise click.ClickException(
            "The draft file is empty. Pass the reply you were about to send, in "
            "full — the minimizer decides what survives, so there is nothing to "
            "gain by trimming it first."
        )
    return draft


# --- the minimizer ----------------------------------------------------------

def _minimize(draft: str, user_prompt: str) -> str:
    """Run the adversarial edit and return the text the agent must send.

    Fails CLOSED, and this is the one place in the reporting surface that does.
    Every other model call in Endless fails open because a false block costs more
    than a missed check; here the opposite holds. If the minimizer is unreachable
    and this returned the draft unchanged, the agent would send its unedited
    reply with the gate's blessing — the enforcement would silently become a
    no-op, and nothing downstream could tell that apart from a draft that needed
    no cuts. An error the agent must handle is honest; a green light that means
    nothing is not.
    """
    prompts = report_prompts.load_prompts()
    prompt = report_prompts.build_minimize_prompt(prompts, user_prompt, draft)
    try:
        result = internal_claude.run_internal_claude(
            prompt, model=_MODEL, effort=_EFFORT, timeout=_TIMEOUT)
    except subprocess.TimeoutExpired:
        raise click.ClickException(
            f"The minimizer timed out after {_TIMEOUT}s. Re-run the same command; "
            "if it times out again the draft is likely too long to edit in one "
            "pass — split the turn rather than sending the draft unminimized."
        )
    except FileNotFoundError:
        raise click.ClickException(
            "`claude` is not on PATH, so the draft cannot be minimized. This "
            "command cannot fall back to passing the draft through: that would "
            "turn the reporting contract into a no-op without saying so."
        )
    if result.returncode != 0:
        detail = (result.stderr or "").strip().splitlines()
        tail = f" ({detail[-1]})" if detail else ""
        raise click.ClickException(f"The minimizer failed{tail}. Re-run the command.")

    out = result.stdout.strip()
    if not out:
        raise click.ClickException(
            "The minimizer returned nothing. Re-run the command — an empty reply "
            "is never the right answer, so this is a failed call rather than a "
            "verdict that the draft was all ceremony."
        )
    return out


# --- session plumbing -------------------------------------------------------

def _session_id() -> int | None:
    """This session's `sessions.id`, or None outside a resolvable session.

    None is not an error. A bare shell running the command by hand has no
    session, so nothing can be persisted and no checkpoint can be armed — and
    the Stop gate, which resolves the session the same way, will fail open for
    exactly the same reason. The two agree by construction.
    """
    from endless.task_cmd import _current_endless_session_id
    return _current_endless_session_id()


def _go_bin() -> str:
    from endless.event_bridge import _resolve_endless_go
    return _resolve_endless_go()


def _run_go(args: list[str], *, input_text: str | None = None) -> subprocess.CompletedProcess:
    config.require_db_context()
    return subprocess.run(
        [_go_bin(), *config.go_db_context_args(), "session-query", *args],
        input=input_text, capture_output=True, text=True, timeout=15,
    )


def _runs_this_turn(session_id: int) -> int:
    """How many times this turn has already produced minimized output."""
    result = _run_go(["report-runs", "--session-id", str(session_id)])
    if result.returncode != 0:
        return 0
    try:
        return int(result.stdout.strip())
    except ValueError:
        return 0


def _record(session_id: int, item_id: int | None, minimized: str, draft_path: str) -> None:
    """Persist the corpus row and arm the Stop gate.

    Best-effort by design, and the asymmetry is deliberate: a report that refused
    to print because it could not arm itself would break the turn to punish
    nobody, whereas an unarmed gate merely fails open. The agent still has the
    minimized text either way.
    """
    args = ["relay-checkpoint", "--session-id", str(session_id), "--draft-file", draft_path]
    if item_id is not None:
        args += ["--task-id", str(item_id)]
    try:
        _run_go(args, input_text=minimized)
    except (FileNotFoundError, subprocess.SubprocessError):
        return


# --- entry points -----------------------------------------------------------

def show_raw() -> None:
    """Print the raw draft of this session's most recent report, unchanged.

    The safety net under an aggressive minimizer. It round-trips the draft
    byte-for-byte — no re-wrapping, no trailing newline normalization — because
    its whole purpose is to prove that nothing the minimizer cut was lost.
    """
    session_id = _session_id()
    if session_id is None:
        raise click.ClickException(
            "No resolvable Endless session, so no draft was persisted to show."
        )
    result = _run_go(["report-draft", "--session-id", str(session_id)])
    if result.returncode != 0:
        raise click.ClickException(
            "No draft persisted for this session yet — run `endless task report "
            "--draft-file <path>` first."
        )
    click.echo(result.stdout, nl=False)


def report_item(item_id: int | None, draft_path: str) -> None:
    """Minimize the draft, print the result, persist it, and arm the gate.

    Print order: the minimized text is the ONLY thing on stdout. Nothing frames
    it, labels it, or follows it — the agent is about to send stdout verbatim,
    so anything this command prints is something the user receives.

    Which is also why the appeal refusal and every failure go to stderr as a
    ClickException rather than to stdout.
    """
    draft = _read_draft(draft_path)
    session_id = _session_id()

    # Bound the appeal BEFORE spending a model call on it.
    if session_id is not None:
        runs = _runs_this_turn(session_id)
        if runs >= 2:
            raise click.ClickException(
                "You have already used this turn's one appeal.\n"
                "\n"
                "  Send the output you were given, verbatim. If the minimizer "
                "genuinely dropped something the user needs, say so in your NEXT "
                "turn — after they have replied — rather than re-drafting until "
                "something you prefer survives."
            )

    user_prompt = _user_prompt(session_id)
    minimized = _minimize(draft, user_prompt)

    click.echo(minimized)

    if session_id is not None:
        _record(session_id, item_id, minimized, draft_path)


def _user_prompt(session_id: int | None) -> str:
    """The message that prompted this turn, or "" when unavailable.

    Read from the session row (staged there by the UserPromptSubmit hook) rather
    than asked of the agent. Asking the agent to supply it would collect a corpus
    of paraphrases instead of prompts, and would hand the agent a lever over how
    its own draft is judged — it could describe the user as having asked for
    exactly what it wrote.
    """
    if session_id is None:
        return ""
    try:
        result = _run_go(["report-prompt", "--session-id", str(session_id)])
    except (FileNotFoundError, subprocess.SubprocessError):
        return ""
    if result.returncode != 0:
        return ""
    return result.stdout
