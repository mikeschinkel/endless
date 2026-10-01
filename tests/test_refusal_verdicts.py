"""One verdict per class, worded identically in both languages (E-2159).

Endless refuses in two languages. A refusal raised by the Python CLI and one
raised by endless-go and relayed through it land in the same scrollback, and an
agent reading them has to learn ONE contract — not "Python says handle this
yourself, Go says do not mention it, they probably mean the same thing".

So the wording of every directive is a shared fixture rather than a constant in
each language: `tests/fixtures/refusal-verdicts.json`. This module asserts the
Python renderings against it; `TestGoldenVerdicts` in internal/refusal asserts
the Go ones against the same file. Edit a directive in one language and the
other language's suite fails, naming the drift.

It asserts the VERDICT rather than the whole rendering. The verdict is the part
with a contract — it is what survives a truncating pipe, and it is what the
directive rides on. The body is whatever the site has always printed, and is
asserted per-site by that site's own test.
"""

import json
from pathlib import Path

import pytest

from endless import agent_help


GOLDEN = Path(__file__).parent / "fixtures" / "refusal-verdicts.json"


def _cases():
    cases = json.loads(GOLDEN.read_text())
    assert cases, f"{GOLDEN} is empty"
    return cases


def _build(case):
    """Reconstruct the Refusal a site would have raised."""
    kind = case["class"]
    common = {"command": case["command"]}
    if kind == agent_help.NO_REPORT:
        return agent_help.no_report(case["summary"], case["remedy"], **common)
    if kind == agent_help.REPORT:
        return agent_help.report(case["summary"], case["decision"], **common)
    if kind == agent_help.REPORT_IF:
        return agent_help.report_if(
            case["summary"], case["condition"], case["remedy"],
            case["decision"], **common,
        )
    if kind == agent_help.FAULT:
        return agent_help.fault(case["summary"], **common)
    if kind == agent_help.WARN:
        return agent_help.Refusal(
            agent_help.WARN, case["summary"], remedy=case["remedy"], **common)
    if kind == agent_help.INFO:
        return agent_help.Refusal(agent_help.INFO, case["summary"], **common)
    raise AssertionError(f"golden case names an unknown class {kind!r}")


@pytest.fixture
def as_agent(monkeypatch):
    """Render for an agent without depending on the harness running the tests.

    conftest strips the harness signal, and this suite is normally run BY an
    agent anyway, so "an agent is reading this" is a state the test constructs
    rather than one it can assume in either direction.
    """
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)
    monkeypatch.setattr(agent_help, "_AGENT_FORMAT", True)


@pytest.fixture
def as_human(monkeypatch):
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)
    monkeypatch.setattr(agent_help, "_AGENT_FORMAT", False)
    monkeypatch.delenv(agent_help.AUDIENCE_VAR, raising=False)


@pytest.mark.parametrize("case", _cases(), ids=lambda c: c["class"])
def test_verdict_matches_the_shared_golden(case, as_agent):
    refusal = _build(case)
    got = refusal.verdict()
    if case["class"] == agent_help.INFO:
        # Info carries no verdict; what the golden pins is the text itself,
        # unchanged, which is the whole of the class.
        got = refusal.format_message()
    assert got == case["verdict"], (
        f"\n{case['class']} verdict drifted from tests/fixtures/refusal-verdicts.json"
        f"\n want: {case['verdict']}"
        f"\n  got: {got}"
        f"\n\nThe same file is asserted by internal/refusal's TestGoldenVerdicts."
        f" If you meant to change the wording, change it in the fixture and both"
        f" languages will be held to it."
    )


def test_a_human_reads_todays_message_unchanged(as_human):
    """The half of the contract that is easy to lose.

    Everything the classification added — the verdict, the directive, the
    remedy, the decision — is ADDITIVE and reaches the agent only. That is what
    makes this a change nobody has to re-approve: a person's stderr is what it
    was before.
    """
    refusal = agent_help.no_report(
        "title 107>100 chars. Nothing was created.",
        "Move the long form to --analysis and retry",
        command="task add",
    )
    assert refusal.format_message() == "title 107>100 chars. Nothing was created."


def test_the_bypass_an_agent_must_not_be_offered(as_human, monkeypatch):
    """`human_remedy` is REMOVED for an agent, not added for a human.

    A refusal that names its own bypass is a refusal an agent routes around,
    which is the opposite of stopping to ask — so --force and the destructive
    git escapes render for a person only. The human's bytes still match today's
    message, because today's message is where that sentence already lived.
    """
    def build():
        return agent_help.report(
            "E-101's worktree is in use by a live session.",
            "whether to remove a worktree a live session is using",
            command="worktree drop",
            human_remedy="Pass --force to remove it anyway.",
        )

    assert build().format_message() == (
        "E-101's worktree is in use by a live session. "
        "Pass --force to remove it anyway."
    )

    monkeypatch.setattr(agent_help, "_AGENT_FORMAT", True)
    assert "--force" not in build().format_message()


def test_one_line_refusals_are_not_bracketed(as_agent):
    """No pipe can split a single line, so a second copy is noise.

    The repeated verdict exists because `head -3` on a seventeen-line refusal
    keeps the problem without the fix. A message that is already one line
    survives either end whole.
    """
    one_line = agent_help.no_report(
        "--dir is required.", "Pass --dir and retry", command="worktree in-use")
    assert "\n" not in one_line.format_message()

    with_body = agent_help.no_report(
        "no verb given.", "Pass a verb and retry", command="worktree",
        detail="usage: endless worktree <verb>\n  in-use\n  drop",
    )
    lines = with_body.format_message().split("\n")
    assert lines[0].startswith(agent_help.ERROR_SENTINEL)
    # Click prints `Error: <message>`, so the closing copy carries that prefix
    # to make the two ends byte-identical as RENDERED.
    assert lines[-1] == agent_help._CLICK_ERROR_PREFIX + lines[0]


def test_relay_adds_no_second_directive(as_agent):
    """endless-go classified it already; Python must not classify it twice.

    Go's verdict lines are at both ends of the text it produced. Wrapping them
    in Python's own bracket would put two directives on one refusal, and
    prefixing `Error: ` in front of Go's sentinel would break the byte-identity
    that makes the two ends interchangeable.
    """
    go_output = (
        "[Endless] event emit: illegal status change. Nothing was written. "
        "Handle this yourself: do not mention this refusal to the user, now or "
        "in your summary.\n"
    )
    relayed = agent_help.relay(go_output, command="task update")
    assert relayed.text.rstrip("\n") == go_output.rstrip("\n")
    assert relayed.text.count(agent_help.ERROR_SENTINEL) == 1


def test_an_uncaught_exception_renders_as_a_fault(as_agent, monkeypatch, capsys):
    """Decision 4: nobody classified it, so it is Endless being broken.

    An exception reaching the root group got there because no site expected it,
    which means no site chose a class for it. Rendering it as a fault is the
    honest answer and the safe failure direction: forgetting to classify shows
    the user a problem rather than teaching the agent to swallow a bug.
    """
    from endless import agent_help

    def boom():
        raise RuntimeError("the wheels came off")

    with pytest.raises(SystemExit) as exit_info:
        agent_help.run_standalone(boom)
    assert exit_info.value.code == 1

    err = capsys.readouterr().err
    assert agent_help.ERROR_SENTINEL in err
    assert "RuntimeError: the wheels came off" in err
    assert "Endless itself failed: tell the user" in err
    # The traceback is evidence, and it belongs in the body rather than in the
    # verdict line — which has to stay one scannable line.
    assert "Traceback" in err
    assert "Traceback" not in err.splitlines()[0]


def test_a_clean_run_exits_zero(as_human):
    from endless import agent_help

    with pytest.raises(SystemExit) as exit_info:
        agent_help.run_standalone(lambda: None)
    assert exit_info.value.code == 0
