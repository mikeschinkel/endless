"""Tests for E-2203: the rater, which proposes complexity and risk for a
`submitted` task nobody rated.

The model call is the one thing that cannot be asserted deterministically, so
these cover everything AROUND it — parsing, writing only unset axes, the
fail-open contract, the claim, and the zero-SQLite boundary. The end-to-end
wiring (Go reads, template render, the job, the emitted event) runs against a
stubbed `claude` in `.endless/tasks/e-2203/verify.sh`.
"""

import subprocess
from pathlib import Path
from types import SimpleNamespace

import pytest

from endless import config, rater


# --- parsing ----------------------------------------------------------------

@pytest.mark.parametrize("reply,expected", [
    ("COMPLEXITY: low\nRISK: high", {"complexity": "low", "risk": "high"}),
    # Case, blank lines, a trailing period and stray emphasis are ordinary
    # model output.
    ("\n\ncomplexity: Medium.\n  Risk:LOW  ", {"complexity": "medium", "risk": "low"}),
    ("**COMPLEXITY:** high\n**RISK**: `low`", {"complexity": "high", "risk": "low"}),
    # Preamble does not cost the ratings below it.
    ("Here are my ratings:\nCOMPLEXITY: low\nRISK: low",
     {"complexity": "low", "risk": "low"}),
    # A missing or unrecognized value drops that axis, never the reply.
    ("COMPLEXITY: trivial\nRISK: medium", {"risk": "medium"}),
    # The first valid value per axis wins.
    ("RISK: low\nRISK: high", {"risk": "low"}),
    ("I cannot rate this.", {}),
    ("", {}),
    (None, {}),
])
def test_parse_ratings(reply, expected):
    assert rater.parse_ratings(reply) == expected


# --- the model call fails open ------------------------------------------------

@pytest.mark.parametrize("boom", [
    subprocess.TimeoutExpired(cmd="claude", timeout=1),
    FileNotFoundError("claude"),
    OSError("nope"),
])
def test_evaluate_fails_open_on_a_broken_call(monkeypatch, boom, isolated_env):
    from endless import internal_claude

    def raise_(*_a, **_k):
        raise boom
    monkeypatch.setattr(internal_claude, "run_internal_claude", raise_)
    proposed, failure = rater.evaluate("prompt", "sonnet")
    assert proposed == {}
    assert failure


def test_evaluate_fails_open_on_a_nonzero_exit(monkeypatch, isolated_env):
    from endless import internal_claude
    monkeypatch.setattr(
        internal_claude, "run_internal_claude",
        lambda *_a, **_k: SimpleNamespace(returncode=1, stdout="", stderr="bad"),
    )
    proposed, failure = rater.evaluate("prompt", "sonnet")
    assert proposed == {}
    assert "exited 1" in failure


def test_the_rater_model_resolves_through_internal_model(isolated_env):
    assert config.internal_model("rater") == "sonnet"


# --- the write: unset axes only ---------------------------------------------

def _capture_apply(monkeypatch, context):
    """Run apply() against a stubbed re-read, returning the emitted events."""
    from endless import event_bridge
    emitted = []
    monkeypatch.setattr(rater, "build_context", lambda _id: context)
    monkeypatch.setattr(event_bridge, "emit_event", lambda **kw: emitted.append(kw))
    return emitted


def _ctx(**over):
    base = {"status": "submitted", "project": "p", "project_root": "/p",
            "complexity": "", "risk": ""}
    base.update(over)
    return base


def test_apply_writes_both_ratings_as_the_triager(monkeypatch):
    emitted = _capture_apply(monkeypatch, _ctx())

    written = rater.apply(9, {"complexity": "low", "risk": "high"}, "sonnet")

    assert written == {"complexity": "low", "risk": "high"}
    assert [e["kind"] for e in emitted] == ["task.fields_updated"]
    assert emitted[0]["payload"]["fields"] == {"complexity": "low", "risk": "high"}
    assert emitted[0]["actor_kind"] == "triager"
    assert emitted[0]["payload"]["rater"]["job"] == "rater"
    assert emitted[0]["project_root"] == "/p", "never looked up through Python SQLite"


def test_apply_never_overwrites_a_rating_the_task_already_has(monkeypatch):
    emitted = _capture_apply(monkeypatch, _ctx(complexity="high"))

    rater.apply(9, {"complexity": "low", "risk": "medium"}, "sonnet")

    assert emitted[0]["payload"]["fields"] == {"risk": "medium"}


def test_apply_writes_nothing_once_the_task_left_submitted(monkeypatch):
    """A person approved or claimed it while the model was thinking: they win."""
    emitted = _capture_apply(monkeypatch, _ctx(status="ready"))

    assert rater.apply(9, {"complexity": "low", "risk": "low"}, "sonnet") == {}
    assert emitted == []


def test_apply_writes_nothing_when_both_got_rated_meanwhile(monkeypatch):
    emitted = _capture_apply(monkeypatch, _ctx(complexity="low", risk="low"))

    assert rater.apply(9, {"complexity": "high", "risk": "high"}, "sonnet") == {}
    assert emitted == []


# --- rate_one: claim, fail-open, faults -------------------------------------

@pytest.fixture
def harness(monkeypatch):
    """Stub every Go round-trip rate_one makes, and record what it did."""
    calls = SimpleNamespace(claimed=[], released=[], faults=[], applied=[],
                            evaluated=0, claim_wins=True, reply={},
                            failure="", context=_ctx())

    def claim(task_id):
        calls.claimed.append(task_id)
        return calls.claim_wins

    def evaluate(_prompt, _model):
        calls.evaluated += 1
        return dict(calls.reply), calls.failure

    def apply(task_id, proposed, _model):
        calls.applied.append((task_id, proposed))
        return dict(proposed)

    monkeypatch.setattr(rater, "build_context", lambda _id: calls.context)
    monkeypatch.setattr(rater, "claim", claim)
    monkeypatch.setattr(rater, "release", lambda t: calls.released.append(t))
    monkeypatch.setattr(rater, "render_prompt", lambda _c: "prompt")
    monkeypatch.setattr(rater, "_model", lambda _c: "sonnet")
    monkeypatch.setattr(rater, "evaluate", evaluate)
    monkeypatch.setattr(rater, "apply", apply)
    monkeypatch.setattr(rater, "report_failure",
                        lambda t, d: calls.faults.append((t, d)))
    return calls


def test_rate_one_rates_and_releases_its_claim(harness):
    harness.reply = {"complexity": "medium", "risk": "low"}

    result = rater.rate_one(7)

    assert result["outcome"] == "rated"
    assert result["ratings"] == {"complexity": "medium", "risk": "low"}
    assert harness.claimed == [7] and harness.released == [7]
    assert harness.faults == []


def test_rate_one_claims_before_the_model_call(harness):
    """Losing the claim means someone else is mid-call: no second spend."""
    harness.claim_wins = False

    result = rater.rate_one(7)

    assert result["outcome"] == "skipped"
    assert harness.evaluated == 0
    assert harness.applied == []
    assert harness.released == [], "a claim this process never held is not released"


def test_rate_one_fails_open_on_no_verdict_and_records_a_fault(harness):
    harness.reply = {}

    result = rater.rate_one(7)

    assert result["outcome"] == "failed"
    assert harness.applied == [], "no verdict writes nothing"
    assert len(harness.faults) == 1 and harness.faults[0][0] == 7
    assert harness.released == [7], "the claim is released on failure too"


def test_rate_one_fails_open_on_a_broken_call(harness):
    harness.failure = "`claude` was not found on PATH"

    result = rater.rate_one(7)

    assert result["outcome"] == "failed"
    assert harness.applied == []
    assert harness.faults == [(7, "`claude` was not found on PATH")]


def test_rate_one_writes_only_the_unset_axis(harness):
    harness.context = _ctx(complexity="high")
    harness.reply = {"complexity": "low", "risk": "medium"}

    result = rater.rate_one(7)

    assert result["outcome"] == "rated"
    assert harness.applied == [(7, {"risk": "medium"})]


def test_rate_one_partial_reply_writes_what_it_has_and_records_a_fault(harness):
    harness.reply = {"risk": "low"}

    result = rater.rate_one(7)

    assert result["outcome"] == "partial"
    assert harness.applied == [(7, {"risk": "low"})]
    assert len(harness.faults) == 1


@pytest.mark.parametrize("context", [
    _ctx(status="ready"),
    _ctx(complexity="low", risk="low"),
])
def test_rate_one_skips_without_a_claim_or_a_call(harness, context):
    harness.context = context

    assert rater.rate_one(7)["outcome"] == "skipped"
    assert harness.claimed == [] and harness.evaluated == 0


def test_rate_one_dry_run_writes_nothing(harness):
    harness.reply = {"complexity": "low", "risk": "low"}

    result = rater.rate_one(7, dry_run=True)

    assert result["outcome"] == "dry-run"
    assert harness.applied == []


def test_rate_one_fails_open_when_the_context_read_fails(harness, monkeypatch):
    def broken(_id):
        raise rater.RaterError("endless-go exploded")
    monkeypatch.setattr(rater, "build_context", broken)

    result = rater.rate_one(7)

    assert result["outcome"] == "failed"
    assert harness.claimed == []
    assert harness.faults == [(7, "endless-go exploded")]


def test_rate_batch_fails_open_when_the_queue_read_fails(monkeypatch):
    faults = []

    def broken(**_k):
        raise rater.RaterError("no queue")
    monkeypatch.setattr(rater, "select_unrated", broken)
    monkeypatch.setattr(rater, "report_failure", lambda t, d: faults.append((t, d)))

    results = rater.rate_batch()

    assert results[0]["outcome"] == "failed"
    assert faults == [(0, "no queue")]


def test_the_tally_is_the_last_line(harness, capsys):
    """internal/raterjob records the final line as the job's note."""
    harness.reply = {"complexity": "low", "risk": "low"}
    rater.render_results([rater.rate_one(7)], dry_run=False)
    out = capsys.readouterr().out.strip().splitlines()
    assert out[-1] == "rated 1 of 1"


# --- the boundary -----------------------------------------------------------

def test_the_rater_never_touches_sqlite():
    """Every read goes through endless-go; CLAUDE.md forbids a sixth Python
    file reading SQLite."""
    import ast
    tree = ast.parse(Path(rater.__file__).read_text())
    imported = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            imported |= {a.name for a in node.names}
        elif isinstance(node, ast.ImportFrom):
            imported |= {f"{node.module}.{a.name}" for a in node.names}
    assert "sqlite3" not in imported
    assert "endless.db" not in imported
