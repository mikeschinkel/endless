"""The fault-triage verbs on the Python side (E-2272).

The routing, the answers and the lookup all live in Go; what Python owns, and
what these pin, is the argv it hands `endless-go errors` and the two refusals
`session goto` makes before anything runs.
"""

import pytest
from click.testing import CliRunner

from endless import jobs_cmd
from endless.cli import main


@pytest.fixture
def captured(monkeypatch):
    calls = []
    monkeypatch.setattr(jobs_cmd, "_run_go",
                        lambda subcommand, args: calls.append((subcommand, args)))
    return calls


def test_accept_passes_the_id_last(captured):
    jobs_cmd.errors_answer("accept", 12, None, None)
    assert captured == [("errors", ["accept", "12"])]


def test_accept_on_a_named_session(captured):
    jobs_cmd.errors_answer("accept", 12, 1304, None)
    assert captured == [("errors", ["accept", "--session", "1304", "12"])]


def test_decline_carries_its_reason_before_the_id(captured):
    jobs_cmd.errors_answer("decline", 12, None, "the rater raised it")
    assert captured == [("errors", ["decline", "--reason", "the rater raised it", "12"])]


def test_escalate(captured):
    jobs_cmd.errors_escalate(12)
    assert captured == [("errors", ["escalate", "12"])]


def test_decline_requires_a_reason():
    result = CliRunner().invoke(main, ["errors", "decline", "12"])
    assert result.exit_code == 2
    assert "--reason" in result.output


def test_goto_takes_a_target_or_error_fix_not_both():
    runner = CliRunner()
    both = runner.invoke(main, ["session", "goto", "ES-1", "--error-fix", "12"])
    neither = runner.invoke(main, ["session", "goto"])
    assert both.exit_code == 2
    assert neither.exit_code == 2


def test_goto_error_fix_goes_to_the_accepting_session(monkeypatch):
    seen = {}
    monkeypatch.setattr(jobs_cmd, "errors_fixer", lambda error_id: "ES-77")
    import endless.session_cmd as session_cmd
    monkeypatch.setattr(session_cmd, "session_goto",
                        lambda ref, **kw: seen.update(ref=ref, **kw))
    result = CliRunner().invoke(main, ["session", "goto", "--error-fix", "12"])
    assert result.exit_code == 0, result.output
    assert seen["ref"] == "ES-77"
    assert seen["background"] is False


def test_background_requires_resume():
    from endless import session_cmd
    with pytest.raises(Exception) as exc:
        session_cmd.session_goto("ES-1", background=True)
    assert "--background applies only with --resume" in str(exc.value)
