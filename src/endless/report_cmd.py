"""`endless task report` — the enforced minimizer (E-1771, E-1953, E-1975).

The agent hands over its ENTIRE freeform draft reply. An adversarial model
rewrites it down to what the user did not already have. The output is the only
thing the agent may say, and a Stop hook holds the turn until it says exactly
that.

Why the pre-E-1953 design failed, since that one is still shaped entirely by it:
the command used to take a structured payload — `verify`, `notes`, `questions` —
and render a block the agent appended to its own prose. Two output channels
existed, so content landed in the cheap one. The agent wrote the verify command
in prose and then told `task report` there was nothing to report. Every variant
of that design fails identically, because in all of them the agent decides what
to volunteer, which means the agent is still judging its own output in the same
breath as writing it. The minimizer is a SECOND PARTY. That is the fix.

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

WHAT E-1975 ADDED, and why the command grew four steps rather than one:

  * The prompt in force is a VARIANT resolved from the ledger, not the shipped
    default. The loop promotes by pointer, so the command has to read the
    pointer.
  * Before minimizing, the variant's FETCH POLICY pulls what the user already
    has — their task plan, their session status, the replies they already read —
    and the corpus row records exactly what came back. Fetching and recording are
    one mechanism: a minimizer that fetches is nondeterministic in its inputs, so
    a later replay must serve context from the record rather than re-fetch state
    that has moved.
  * A draft under the variant's BYPASS THRESHOLD skips the model entirely. The
    row is still corpus — "we let this one through" is the evidence that axis is
    tuned on.
  * On a sampled fraction of turns the draft is minimized TWICE, by the champion
    and by a challenger, and both are emitted for the user to pick between. That
    pick is the only real counterfactual the loop ever gets: an absolute label
    says a reply was bad, and only a pick says one prompt beat another on the
    same draft — which is the exact judgment promotion requires.

Every turn, not just handoffs — the 5000-character task review that triggered
this redesign was a mid-conversation turn.

Latency is accepted. Endless is built for many parallel sessions; the requester
works elsewhere while a turn is being minimized.
"""

import json
import random
import subprocess

import click

from endless import config, internal_claude, report_prompts

# The minimizer's model and effort. Not tunable by config, on the same principle
# as the old ceremony gate: the WORDING is the lever, because that is where the
# judgment lives. A per-machine model override would make two installs of Endless
# disagree about what "minimized" means while both looked identical, and would
# make the eval corpus incomparable across machines.
#
# Sonnet rather than the Haiku the old KEEP/DROP gate used. This is a different
# job: preserving a table byte-for-byte while rewriting the paragraph beside it
# is an editing task with hard invariants, not a binary classification, and the
# invariants are exactly what a smaller model drops first.
_MODEL = "sonnet"
_EFFORT = "medium"
_TIMEOUT = 180

# How many lines of each variant are shown inline on a paired turn.
#
# A/B doubles what the user reads, and the minimizer exists to cut what they
# read. Previewing is the resolution: enough of each to tell them apart, with
# `endless session turn A|B -p` to open either in full. Rendering the shared text
# once and duplicating only the divergent spans would be better in principle and
# was rejected as substantial effort that is hard to make graspable in a
# terminal.
_PREVIEW_LINES = 12


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
            "The draft file is empty. Pass the reply you were about to send — "
            "the whole reply, not an excerpt: the Stop gate compares your final "
            "message against what this command returns."
        )
    return draft


# --- the minimizer ----------------------------------------------------------

def _minimize(draft: str, user_prompt: str, context: str, minimize_text: str) -> str:
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
    prompt = report_prompts.build_minimize_prompt(
        prompts, user_prompt, draft, context, minimize_text=minimize_text)
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

def _report_gate_on() -> bool:
    """Whether this project runs the report channel.

    Shared with the wind-down nudge and the spawn handoff rather than
    reimplemented, so the three emitters can never disagree about whether the
    channel is live — a command that refuses on a budget the handoff never
    mentioned would be worse than either behavior alone.
    """
    from endless.task_cmd import _report_gate_on as impl
    return impl()


def _optimizer_on() -> bool:
    """Whether the autoresearch loop may run for this project.

    A separate switch from the gate, because a project can reasonably want the
    minimizer without the research bill. Off means the shipped champion runs and
    nothing is paired, generated or promoted — the channel is there, frozen.

    Defaults to True on an unresolvable root, matching both the gate and the Go
    side. An unresolvable root is ignorance, not an opt-out, and the practical
    consequence is nil: pairing needs a generated challenger, and a project the
    loop cannot locate has never had a round run for it.
    """
    root = config.enclosing_project_root()
    if root is None:
        return True
    return config.project_minimizer_optimizer(root)


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


def _record(session_id: int, item_id: int | None, checkpoint: dict, draft_path: str) -> None:
    """Persist the corpus row(s) and arm the Stop gate.

    Best-effort by design, and the asymmetry is deliberate: a report that refused
    to print because it could not arm itself would break the turn to punish
    nobody, whereas an unarmed gate merely fails open. The agent still has the
    minimized text either way.
    """
    args = ["relay-checkpoint", "--json", "--session-id", str(session_id),
            "--draft-file", draft_path]
    if item_id is not None:
        args += ["--task-id", str(item_id)]
    try:
        _run_go(args, input_text=json.dumps(checkpoint))
    except (FileNotFoundError, subprocess.SubprocessError):
        return


def _task_type(item_id: int | None) -> str:
    """The task's type slug, or "" — the bucket this turn's variant is drawn from.

    Failure is "" rather than an error. Splitting by task type is an optimization
    of the search, and a turn whose type cannot be read belongs in the untyped
    bucket rather than failing the report.
    """
    if item_id is None:
        return ""
    try:
        from endless import db
        rows = db.query(
            "SELECT COALESCE(tt.slug,'') AS slug FROM live_tasks t "
            "LEFT JOIN task_types tt ON tt.id = t.type_id WHERE t.id = ?",
            (item_id,),
        )
        return rows[0]["slug"] if rows else ""
    except Exception:  # noqa: BLE001 — see docstring
        return ""


# --- the paired presentation -------------------------------------------------

_RULE_WIDTH = 60


def _divider(label: str = "") -> str:
    if not label:
        return "─" * _RULE_WIDTH
    head = f"─[{label}]"
    return head + "─" * max(3, _RULE_WIDTH - len(head))


def _preview(text: str, slot: str) -> str:
    """The first few lines of a variant, with a pointer to the rest.

    Truncation may only ever drop PROSE. The invariants are about what reaches
    the user — a table survives byte for byte, a command the user must run
    always survives — so a presentation that cuts a fenced block in half or
    elides a verify command breaks them exactly as badly as a minimizer that
    rewrites one, and breaks them in the reply the user actually receives.

    So: if what would be cut carries protected content, nothing is cut. On a
    paired turn that means the user reads both variants in full, which is the
    doubling the design already accepts as the price of the experiment — and a
    price paid only on sampled turns, by a user who can switch pairing off.
    """
    from endless import minimizer_invariants

    lines = text.split("\n")
    if len(lines) <= _PREVIEW_LINES + 2:
        return text
    cut = "\n".join(lines[_PREVIEW_LINES:])
    if minimizer_invariants.has_protected_content(cut):
        return text
    shown = "\n".join(lines[:_PREVIEW_LINES])
    return f"{shown}\n… {len(lines) - _PREVIEW_LINES} more lines — `endless session turn {slot} -p`"


def _pair_block(a: str, b: str, notice: str) -> str:
    """The one sanctioned output of a paired turn.

    Both variants in ONE output, because the Stop hook compares the FINAL message
    and a tool call happens before it — so an agent cannot send an A/B block and
    then ask which won; the reply ends the turn. That constraint, not aesthetics,
    picks this shape. `AskUserQuestion` cannot carry it either: its labels are a
    few words and its description a sentence, and a full markdown reply rendered
    in its monospace preview box reads badly.

    The pick needs no new parser. `$A` / `$B` is already the label mechanism, and
    it composes — `$B "this sentence"` means "B wins, and that span is still
    bloat".
    """
    tail = (
        "Read either in full, with color: `endless session turn A -p` / "
        "`endless session turn B -p`.\n"
        "Reply `$A` or `$B` to pick. Add a quoted span to say what still "
        "bothers you — `$B \"this sentence\"` works.\n"
        "Want these more often or less often? `$MORE` / `$LESS`."
    )
    if notice:
        tail += f"\n(The minimizer's judge is {notice}.)"
    return "\n".join([
        _divider("Option A of B"),
        _preview(a, "A"),
        _divider("Option B of B"),
        _preview(b, "B"),
        _divider(),
        tail,
    ])


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

    # Bound the appeal BEFORE spending a model call on it — but only where the
    # channel is actually live (E-1973).
    #
    # The budget is ENFORCEMENT state. It exists so an agent cannot re-draft
    # until something it prefers survives, and that only means anything while a
    # Stop gate is holding the turn against a checkpoint. Where the gate is off
    # nothing holds the turn and nothing reads the counter, so refusing here
    # denies a command no one is enforcing on the basis of a number no one
    # consults — and it strands a session that used the minimizer voluntarily,
    # which is the one behavior a gate-off project should be encouraging.
    if session_id is not None and _report_gate_on():
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
    emitted, checkpoint = _produce(draft, user_prompt, item_id, session_id)

    click.echo(emitted)

    if session_id is not None:
        _record(session_id, item_id, checkpoint, draft_path)


def _produce(
    draft: str,
    user_prompt: str,
    item_id: int | None,
    session_id: int | None,
) -> tuple[str, dict]:
    """Resolve the variant, fetch, minimize (once or twice) and build the output.

    Degrades to the shipped default whenever the ledger is unreachable. That is
    the right failure: the loop is an improvement on a prompt that already works,
    so a broken loop must cost the user nothing beyond the improvement. Only the
    MODEL call fails closed here, because only that one can silently void the
    enforcement.
    """
    task_type = _task_type(item_id)
    variant, challenger, context, record = _plan(task_type, item_id, session_id)
    minimize_text = variant["prompt_text"] if variant else None

    if minimize_text is None:
        minimize_text = report_prompts.load_prompts()[report_prompts.MINIMIZE]

    threshold = variant["bypass_threshold"] if variant else report_prompts.DEFAULT_BYPASS_THRESHOLD
    variant_hash = variant["hash"] if variant else ""

    # The bypass. A draft this short costs more in latency to minimize than the
    # minimization can save the reader, and the row is still corpus — the whole
    # reason the threshold is an optimizer axis is that nobody yet knows where it
    # belongs, because in the corpus at design time nothing had ever been under
    # one.
    if len(draft.strip()) < threshold:
        text = draft.strip()
        return text, {
            "emitted": text,
            "task_type": task_type,
            "context": record,
            "variants": [{"sanctioned": text, "slot": "",
                          "variant_hash": variant_hash, "bypassed": True}],
        }

    primary = _minimize(draft, user_prompt, context, minimize_text)
    if challenger is None:
        return primary, {
            "emitted": primary,
            "task_type": task_type,
            "context": record,
            "variants": [{"sanctioned": primary, "slot": "",
                          "variant_hash": variant_hash, "bypassed": False}],
        }

    secondary = _minimize(draft, user_prompt, context, challenger["prompt_text"])
    emitted = _pair_block(primary, secondary, _calibration_notice())
    return emitted, {
        "emitted": emitted,
        "task_type": task_type,
        "context": record,
        "variants": [
            {"sanctioned": primary, "slot": "A",
             "variant_hash": variant_hash, "bypassed": False},
            {"sanctioned": secondary, "slot": "B",
             "variant_hash": challenger["hash"], "bypassed": False},
        ],
    }


def _plan(task_type: str, item_id: int | None, session_id: int | None):
    """(variant, challenger_or_None, context_text, context_record).

    Every failure here degrades rather than raises: no ledger means the shipped
    prompt with no fetched context, which is exactly E-1953's behavior.
    """
    try:
        from endless import minimizer_fetch, minimizer_store
        variant = minimizer_store.champion(task_type)
        context, record = minimizer_fetch.run_policy(
            variant["fetch_policy"], task_id=item_id, session_id=session_id)
        challenger = _pick_challenger(task_type, variant)
        return variant, challenger, context, record
    except Exception:  # noqa: BLE001 — see docstring
        return None, None, "", ""


def _pick_challenger(task_type: str, variant: dict):
    """The B side of a paired turn, or None.

    Three independent reasons to return None, and each is a real state rather
    than an error: the project has the optimizer off, this turn did not fall in
    the sample, or nothing has been generated to compare against yet. The last
    one is why A/B is naturally silent on a fresh install — a pair needs a
    counterfactual, and the champion against itself is not one.
    """
    from endless import minimizer_store
    if not _optimizer_on():
        return None
    if random.random() >= minimizer_store.ab_rate():
        return None
    pending = minimizer_store.untried_challengers(task_type, variant["hash"], limit=1)
    return pending[0] if pending else None


def _calibration_notice() -> str:
    """The in-band note about why pairs are appearing more often, or "".

    ED-1556 says low judge agreement raises the sample rate and is announced in
    band, never halting promotion silently. In band means HERE — in the reply the
    user is already reading — not in a log they would have to go looking for.
    """
    try:
        from endless import minimizer_store
        return minimizer_store.state_get("calibration_notice", "") or ""
    except Exception:  # noqa: BLE001
        return ""


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
