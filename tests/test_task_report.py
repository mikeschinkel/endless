"""Tests for `endless task report` (E-1771, rebuilt as a minimizer by E-1953).

Hermetic and fast: the model call is stubbed everywhere here, and the DB round
trips are stubbed at the `_run_go` seam. What that leaves provable is the
PLUMBING — that a draft reaches the minimizer whole, that only the minimized
text reaches stdout, that failures fail closed, and that the appeal is bounded.

What it deliberately does NOT cover is the minimizer's judgment, which is the
only thing a stub cannot fake. That lives in `tests/tasks/e-1953-verify.sh`,
which runs the real model against `tests/fixtures/report-draft.md` and asserts
the six properties the plan names. No stubbed test substitutes for it, and one
pretending to would be worse than none.
"""

import json
import subprocess
from pathlib import Path

import click
import pytest

from endless import report_cmd, report_prompts

FIXTURE = Path(__file__).parent / "fixtures" / "report-draft.md"


def _completed(stdout: str, code: int = 0) -> subprocess.CompletedProcess:
    return subprocess.CompletedProcess(args=["claude"], returncode=code, stdout=stdout, stderr="")


def _mock_model(monkeypatch, reply: str, code: int = 0):
    """Stub the minimizer's model call, capturing the prompt it was handed."""
    seen = {}

    def fake(prompt, **kw):
        seen["prompt"] = prompt
        seen["kw"] = kw
        return _completed(reply, code)

    monkeypatch.setattr(report_cmd.internal_claude, "run_internal_claude", fake)
    return seen


def _mock_model_raising(monkeypatch, exc):
    def fake(prompt, **kw):
        raise exc
    monkeypatch.setattr(report_cmd.internal_claude, "run_internal_claude", fake)


@pytest.fixture(autouse=True)
def _no_ledger(monkeypatch):
    """Default to an unreachable variant store: shipped prompt, no fetched
    context, no challenger.

    That is the DEGRADED path E-1975 built deliberately — the loop is an
    improvement on a prompt that already works, so a ledger it cannot reach must
    cost the user nothing beyond the improvement. Making it the default here
    keeps every pre-existing plumbing test asserting plumbing, and gives the
    loop's own behavior its own tests rather than smearing a DB dependency
    across the file.
    """
    monkeypatch.setattr(report_cmd, "_plan", lambda *a, **k: (None, None, "", ""))
    monkeypatch.setattr(report_cmd, "_task_type", lambda item_id: "")


@pytest.fixture(autouse=True)
def _no_session(monkeypatch):
    """Default to an unresolvable session: no persistence, no appeal counter.

    Tests that care about persistence opt back in explicitly. This keeps the
    common case from needing a DB, and it exercises the bare-shell path — the
    same condition under which the Stop gate fails open, since it resolves the
    session the same way.
    """
    monkeypatch.setattr(report_cmd, "_session_id", lambda: None)


# Comfortably over the default bypass threshold (256 chars). A draft shorter
# than that legitimately skips the minimizer under E-1975, so a short fixture
# would make every test below assert the bypass path while claiming to test the
# minimizer. The bypass gets its own tests further down.
_LONG_DRAFT = (
    "Here is the answer.\n\n"
    + "The parser handles nested quotes by tracking depth on a stack. "
    * 6
    + "\n"
)


def _draft(tmp_path, text=_LONG_DRAFT) -> str:
    p = tmp_path / "draft.md"
    p.write_text(text)
    return str(p)


def _mock_go(monkeypatch, handlers: dict, *, gate: bool = True):
    """Stub the Go seam. `handlers` maps subcommand -> CompletedProcess.

    `gate` is the project's report_gate. It defaults ON so the enforcement
    tests below assert enforcement rather than silently exercising the
    gate-off path — which is what they would do if the real resolver ran, since
    this repo ships the gate off.
    """
    monkeypatch.setattr(report_cmd, "_report_gate_on", lambda: gate)
    calls = []

    def fake(args, *, input_text=None):
        calls.append((args, input_text))
        return handlers.get(args[0], _completed(""))

    monkeypatch.setattr(report_cmd, "_run_go", fake)
    monkeypatch.setattr(report_cmd, "_session_id", lambda: 42)
    return calls


# --- draft input ------------------------------------------------------------

def test_missing_draft_file_is_rejected(tmp_path):
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, str(tmp_path / "nope.md"))
    assert "not found" in str(e.value)


def test_empty_draft_is_rejected(tmp_path):
    """An empty draft is the shape of an agent satisfying the gate without
    submitting anything, so the refusal says what to send instead."""
    p = tmp_path / "empty.md"
    p.write_text("   \n\n")
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, str(p))
    assert "the whole reply, not an excerpt" in str(e.value)


def test_draft_reaches_the_minimizer_whole(monkeypatch):
    """The whole point of --draft-file: nothing is summarized, tagged or escaped
    on the way in. The fixture carries a table, a fenced code block and a
    backtick-heavy line, all of which must arrive intact."""
    seen = _mock_model(monkeypatch, "minimized")
    report_cmd.report_item(None, str(FIXTURE))
    assert FIXTURE.read_text() in seen["prompt"]


def test_minimizer_runs_at_the_pinned_model_and_effort(tmp_path, monkeypatch):
    """Model and effort are not tunable — two installs that disagreed about them
    would produce incomparable corpora while looking identical."""
    seen = _mock_model(monkeypatch, "minimized")
    report_cmd.report_item(None, _draft(tmp_path))
    assert seen["kw"]["model"] == "sonnet"
    assert seen["kw"]["effort"] == "medium"


# --- output contract --------------------------------------------------------

def test_only_the_minimized_text_reaches_stdout(tmp_path, monkeypatch, capsys):
    """The agent sends stdout verbatim, so anything else printed here is
    something the USER receives. No framing, no labels, no separator."""
    _mock_model(monkeypatch, "The answer is yes.")
    report_cmd.report_item(1953, _draft(tmp_path))
    assert capsys.readouterr().out == "The answer is yes.\n"


def test_minimized_output_is_stripped(tmp_path, monkeypatch, capsys):
    _mock_model(monkeypatch, "\n\n  The answer is yes.  \n\n")
    report_cmd.report_item(1953, _draft(tmp_path))
    assert capsys.readouterr().out == "The answer is yes.\n"


# --- failing closed ---------------------------------------------------------
#
# The one place in the reporting surface that does NOT fail open. Everywhere
# else a false block costs more than a missed check; here, passing the draft
# through on failure would turn enforcement into a silent no-op that nothing
# downstream could distinguish from a draft needing no cuts.

def test_nonzero_exit_fails_closed(tmp_path, monkeypatch, capsys):
    _mock_model(monkeypatch, "", code=1)
    with pytest.raises(click.ClickException):
        report_cmd.report_item(1953, _draft(tmp_path))
    assert capsys.readouterr().out == ""


def test_empty_model_reply_fails_closed(tmp_path, monkeypatch):
    """An empty reply is a failed call, not a verdict that the draft was all
    ceremony — the prompt forbids outputting nothing."""
    _mock_model(monkeypatch, "   \n")
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, _draft(tmp_path))
    assert "returned nothing" in str(e.value)


def test_timeout_fails_closed(tmp_path, monkeypatch):
    _mock_model_raising(monkeypatch, subprocess.TimeoutExpired("claude", 180))
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, _draft(tmp_path))
    assert "timed out" in str(e.value)


def test_missing_claude_binary_fails_closed(tmp_path, monkeypatch):
    """Named explicitly because it is the failure most likely to be met with a
    'just pass the draft through' patch, which is the one fix that must never
    land: it would silently turn the whole contract into a no-op."""
    _mock_model_raising(monkeypatch, FileNotFoundError())
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, _draft(tmp_path))
    assert "no-op" in str(e.value)


# --- the bounded appeal -----------------------------------------------------

def test_appeal_is_bounded_at_one(tmp_path, monkeypatch):
    """Two runs is the whole budget: the report, then one appeal. A third is
    refused — an agent that can re-run freely will re-draft until something it
    prefers survives, which is the self-judgment the minimizer replaced."""
    _mock_go(monkeypatch, {"report-runs": _completed("2\n")}, gate=True)
    seen = _mock_model(monkeypatch, "minimized")
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, _draft(tmp_path))
    assert "already used this turn's one appeal" in str(e.value)
    # Refused BEFORE the model call — the bound must not cost a round trip.
    assert seen == {}


def test_appeal_budget_is_not_enforced_when_the_gate_is_off(tmp_path, monkeypatch, capsys):
    """E-1973: the budget is ENFORCEMENT state, so it must not refuse where
    nothing enforces.

    Where `report_gate` is off no Stop gate holds the turn and nothing reads the
    counter, so refusing here denies a command no one is enforcing on the basis
    of a number no one consults — and it strands a session that reached for the
    minimizer voluntarily, which is the one behavior a gate-off project should
    be encouraging.

    The run count is deliberately well past the limit: the assertion is that the
    budget is not consulted at all, not that it is generously sized.
    """
    _mock_go(monkeypatch, {"report-runs": _completed("7\n")}, gate=False)
    _mock_model(monkeypatch, "minimized")
    report_cmd.report_item(1973, _draft(tmp_path))
    assert capsys.readouterr().out == "minimized\n"


def test_second_run_is_allowed(tmp_path, monkeypatch, capsys):
    _mock_go(monkeypatch, {"report-runs": _completed("1\n")})
    _mock_model(monkeypatch, "the appeal, minimized")
    report_cmd.report_item(1953, _draft(tmp_path))
    assert capsys.readouterr().out == "the appeal, minimized\n"


def test_checkpoint_carries_the_draft_and_task(tmp_path, monkeypatch):
    """The corpus row and the gate are armed in one call, and the raw draft goes
    with it — that is what makes an over-cut recoverable rather than lost."""
    calls = _mock_go(monkeypatch, {"report-runs": _completed("0\n")})
    _mock_model(monkeypatch, "minimized")
    draft = _draft(tmp_path)
    report_cmd.report_item(1953, draft)

    checkpoint = [c for c in calls if c[0][0] == "relay-checkpoint"]
    assert len(checkpoint) == 1
    args, stdin = checkpoint[0]
    payload = json.loads(stdin)
    assert payload["emitted"] == "minimized"
    assert [v["sanctioned"] for v in payload["variants"]] == ["minimized"]
    assert "--draft-file" in args and draft in args
    assert "--task-id" in args and "1953" in args


def test_id_less_report_omits_the_task(tmp_path, monkeypatch):
    """An unclaimed quick-question session is exactly where sprawl happens, so
    the id is optional and the corpus keys on the session instead."""
    calls = _mock_go(monkeypatch, {"report-runs": _completed("0\n")})
    _mock_model(monkeypatch, "minimized")
    report_cmd.report_item(None, _draft(tmp_path))

    args, _ = [c for c in calls if c[0][0] == "relay-checkpoint"][0]
    assert "--task-id" not in args


def test_user_prompt_is_read_from_the_session_not_the_agent(tmp_path, monkeypatch):
    """Asking the agent for the prompt would collect paraphrases, and would hand
    it a lever over how its own draft is judged — it could describe the user as
    having asked for exactly what it wrote."""
    _mock_go(monkeypatch, {
        "report-runs": _completed("0\n"),
        "report-prompt": _completed("does the parser handle nested quotes?"),
    })
    seen = _mock_model(monkeypatch, "minimized")
    report_cmd.report_item(None, _draft(tmp_path))
    assert "does the parser handle nested quotes?" in seen["prompt"]


def test_unresolvable_session_still_prints(tmp_path, monkeypatch, capsys):
    """A bare shell has no session, so nothing persists and no gate is armed.
    The command must still work: the Stop gate resolves the session the same
    way and fails open for the same reason, so the two agree by construction."""
    _mock_model(monkeypatch, "minimized")
    report_cmd.report_item(None, _draft(tmp_path))
    assert capsys.readouterr().out == "minimized\n"


# --- --raw ------------------------------------------------------------------

def test_raw_round_trips_the_draft(monkeypatch, capsys):
    """Byte-for-byte, no re-wrapping: its whole purpose is to prove nothing the
    minimizer cut was lost."""
    original = FIXTURE.read_text()
    _mock_go(monkeypatch, {"report-draft": _completed(original)})
    report_cmd.show_raw()
    assert capsys.readouterr().out == original


def test_raw_without_a_persisted_draft_says_so(monkeypatch):
    _mock_go(monkeypatch, {"report-draft": _completed("", code=1)})
    with pytest.raises(click.ClickException) as e:
        report_cmd.show_raw()
    assert "--draft-file" in str(e.value)


def test_raw_without_a_session_says_so(monkeypatch):
    with pytest.raises(click.ClickException) as e:
        report_cmd.show_raw()
    assert "No resolvable Endless session" in str(e.value)


# --- the prompt as a shipped asset ------------------------------------------

def test_prompt_splices_without_str_format():
    """A draft containing braces — JSON, an f-string, a shell brace expansion —
    must not make the splice raise or interpolate the agent's own text. The
    minimizer must never fail on the CONTENT of what it is minimizing."""
    prompts = report_prompts.load_prompts()
    hostile = 'Config is {"a": 1} and ${HOME} and {unclosed'
    built = report_prompts.build_minimize_prompt(prompts, "q?", hostile)
    assert hostile in built


def test_prompt_carries_the_invariants():
    """These four are the contract the golden test measures against. They are
    guidance to an adversarial reader, not regex gates — a gate would mangle a
    legitimate quotation of a banned phrase."""
    text = report_prompts.DEFAULTS[report_prompts.MINIMIZE]
    for want in ("TABLES survive byte for byte", "FENCED CODE BLOCKS survive byte for byte",
                 "meant to RUN always survives", "DIRECT QUESTION GETS ITS DIRECT ANSWER"):
        assert want in text, want


def test_objective_is_deletion_then_deduplication():
    """ED-1557 replaced a deletion-only objective with two ordered ones.

    E-1953 stated the first as an explicit negation ("YOUR OBJECTIVE IS NOT TO
    MAKE IT SHORT"), and that sentence is deliberately GONE — under a rewriting
    objective it argued against the thing the prompt now asks for. What replaces
    it is the same protection stated positively: a discussion the user asked for
    survives at whatever length it takes.
    """
    text = report_prompts.DEFAULTS[report_prompts.MINIMIZE]
    assert "NOT TO MAKE IT SHORT" not in text
    assert "DELETE WHAT THE USER DID NOT ASK FOR" in text
    assert "a long discussion is correct" in text
    assert "DO NOT MAKE THE USER READ THE SAME THING TWICE" in text


def test_rewriting_is_licensed_and_fabrication_is_not():
    """The two halves of ED-1557 that must travel together.

    Deletion-only was safe by construction: an editor that can only remove cannot
    assert. Licensing a rewrite removes that guarantee, so the anti-fabrication
    invariant is not a nicety — it is the thing that makes the licence
    survivable, and dropping it would let the loop optimise toward fluent
    fiction.
    """
    text = report_prompts.DEFAULTS[report_prompts.MINIMIZE]
    assert "You may REWRITE, not only delete" in text
    assert "NEVER INVENT" in text
    assert "deleting, not rewriting" not in text


def test_deduplication_targets_what_will_be_reviewed_anyway():
    """What the second objective is FOR, and the thing it is not.

    The point is that the user should not read the same thing twice: session
    status, a task plan, a decision are all material they will open anyway, so
    restating it here buys them a second reading and nothing else.

    An earlier chat message is categorically not that. Chat is ephemeral, the
    user is reading the current reply, and nothing sends them back through the
    transcript. Stating the rule as "what you already sent" instead — and
    feeding prior replies in as evidence — deleted the table, the code block and
    the verify command together in 2 of 8 live runs, leaving one sentence.

    That was never two rules competing with the invariants. It was one rule
    written wrong, and it was briefly "fixed" by declaring a precedence over the
    invariants, which is the patch this test exists to keep out.
    """
    text = report_prompts.DEFAULTS[report_prompts.MINIMIZE]
    assert "DO NOT MAKE THE USER READ THE SAME THING TWICE" in text
    assert "review ANYWAY" in text
    assert "chat is ephemeral" in text
    # The mis-statement and its patch must both stay gone.
    assert "the replies you already sent them" not in text
    assert "OBJECTIVE TWO NEVER OUTRANKS" not in text
    assert "outrank both objectives" not in text


def test_prior_replies_are_not_a_fetch_source():
    """Removed from the declared set, not merely from the default policy.

    The optimizer writes fetch policies and can only choose from what is
    declared, so leaving the source in place would let it re-introduce the
    defect on its own.
    """
    from endless import minimizer_fetch
    assert "recent_replies" not in minimizer_fetch.SOURCES
    assert all(
        f["source"] != "recent_replies"
        for f in report_prompts.DEFAULT_FETCH_POLICY["fetches"]
    )


def test_denylist_sits_under_a_generative_rule():
    """A phrase list alone never converges — ban 'load-bearing', get 'does the
    heavy lifting'. The list is anchors beneath a rule, not a substitute."""
    text = report_prompts.DEFAULTS[report_prompts.MINIMIZE]
    assert "CHARACTERIZES THE REASONING OR NARRATES THE ANALYSIS" in text
    assert "anchors for it, not a checklist" in text


def test_denylist_is_spliced_into_the_prompt():
    prompts = report_prompts.load_prompts()
    built = report_prompts.build_minimize_prompt(prompts, "q?", "draft")
    assert "load-bearing" in built
    assert "{denylist}" not in built


# --- tunable config surface -------------------------------------------------

def test_prompts_default_to_embedded(isolated_env):
    assert report_prompts.load_prompts()[report_prompts.MINIMIZE] == \
        report_prompts.DEFAULTS[report_prompts.MINIMIZE]


def test_machine_layer_overrides_embedded(isolated_env):
    """Editing an override needs no task and no land — the wording will take
    substantial tuning, and routing every adjustment through ceremony means it
    never gets tuned."""
    path = report_prompts._machine_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text('{"name": "minimize", "text": "cut it: {draft}"}\n')
    assert report_prompts.load_prompts()[report_prompts.MINIMIZE] == "cut it: {draft}"


def test_denylist_is_separately_overridable(isolated_env):
    """Kept separate from the instruction so `$BLOAT "<phrase>"` can append to
    it directly, turning an annoyance into config without a round trip."""
    path = report_prompts._machine_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text('{"name": "denylist", "text": "  \\"synergize\\""}\n')
    prompts = report_prompts.load_prompts()
    assert prompts[report_prompts.DENYLIST] == '  "synergize"'
    # The instruction is untouched, and the override is what gets spliced.
    built = report_prompts.build_minimize_prompt(prompts, "q?", "d")
    assert "synergize" in built
    assert "load-bearing" not in built


def test_unknown_name_ignored(isolated_env):
    path = report_prompts._machine_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text('{"name": "not-a-real-prompt", "text": "x"}\n')
    assert report_prompts.load_prompts()[report_prompts.MINIMIZE] == \
        report_prompts.DEFAULTS[report_prompts.MINIMIZE]
