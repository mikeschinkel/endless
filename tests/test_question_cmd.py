"""Tests for `endless question` (E-2176).

question_cmd holds no SQL: reads go through `endless-go session-query`, writes
through `event emit`. These tests stub both seams and pin what the Python layer
owns — argument shaping, the answerer rule, and rendering. The Go side (series
and id allocation, the lifecycle guard, replay) is covered by its own tests.
"""

import click
import pytest
from click.testing import CliRunner

from endless import question_cmd
from endless.cli import QUESTION_ID, main


@pytest.fixture
def seams(monkeypatch):
    """Stub the Go read and the event emit; record every emit."""
    emitted: list[dict] = []

    def fake_go(args):
        if args[0] == "question-target":
            if args[1] == "--task":
                return {"task_id": int(args[2]), "project": "proj"}
            return {"task_id": 7, "project": "proj", "status": "open"}
        raise AssertionError(f"unexpected read {args}")

    def fake_emit(**kw):
        emitted.append(kw)
        if kw["kind"] == "task.questions_asked":
            n = len(kw["payload"]["questions"])
            return {"series": 2, "ids": [f"EQ-{i}" for i in range(10, 10 + n)]}
        return {}

    monkeypatch.setattr(question_cmd, "_go", fake_go)
    monkeypatch.setattr(question_cmd, "emit_event", fake_emit)
    return emitted


def test_question_id_type_accepts_prefixed_and_bare():
    assert QUESTION_ID.convert("EQ-12", None, None) == 12
    assert QUESTION_ID.convert("eq-12", None, None) == 12
    assert QUESTION_ID.convert("12", None, None) == 12


def test_question_id_type_redirects_a_task_id():
    with pytest.raises(click.exceptions.BadParameter, match="question list E-7"):
        QUESTION_ID.convert("E-7", None, None)


def test_ask_sends_one_series_of_texts_only(seams):
    result = CliRunner().invoke(main, ["question", "ask", "E-7", "One?", " Two? "])
    assert result.exit_code == 0, result.output
    assert len(seams) == 1
    evt = seams[0]
    assert evt["kind"] == "task.questions_asked"
    assert evt["entity_type"] == "task" and evt["entity_id"] == "7"
    assert evt["project"] == "proj"
    # No series, no ids: those are allocated by the Go front door.
    assert evt["payload"] == {"questions": [{"question": "One?"}, {"question": "Two?"}]}
    assert "Asked series 2 on E-7: EQ-10, EQ-11" in result.output


def test_ask_refuses_an_empty_question(seams):
    result = CliRunner().invoke(main, ["question", "ask", "E-7", "One?", "  "])
    assert result.exit_code != 0
    assert "non-empty" in result.output
    assert seams == []


def test_answer_from_a_shell_defaults_to_user(seams):
    result = CliRunner().invoke(main, ["question", "answer", "EQ-3", "Sixty"])
    assert result.exit_code == 0, result.output
    assert seams[0]["kind"] == "task_question.resolved"
    assert seams[0]["entity_type"] == "task_question" and seams[0]["entity_id"] == "3"
    assert seams[0]["payload"] == {
        "status": "answered", "answer": "Sixty", "answered_by": "user",
    }


def test_answer_from_an_agent_requires_by(seams, monkeypatch):
    monkeypatch.setattr(question_cmd.agent_env, "present", lambda: True)
    result = CliRunner().invoke(main, ["question", "answer", "EQ-3", "Sixty"])
    assert result.exit_code != 0
    assert "--by is required" in result.output
    assert seams == []

    result = CliRunner().invoke(
        main, ["question", "answer", "EQ-3", "Sixty", "--by", "ES-1236"])
    assert result.exit_code == 0, result.output
    assert seams[0]["payload"]["answered_by"] == "ES-1236"


@pytest.mark.parametrize("verb,status", [
    ("withdraw", "withdrawn"),
    ("reject", "invalid"),
    ("supersede", "superseded"),
])
def test_resolution_verbs_write_their_status(seams, verb, status):
    result = CliRunner().invoke(main, ["question", verb, "EQ-3", "4", "--reason", "moot"])
    assert result.exit_code == 0, result.output
    assert [e["entity_id"] for e in seams] == ["3", "4"]
    assert all(e["payload"] == {"status": status, "reason": "moot"} for e in seams)


@pytest.mark.parametrize("verb", ["withdraw", "reject", "supersede"])
def test_resolution_verbs_require_a_reason(seams, verb):
    result = CliRunner().invoke(main, ["question", verb, "EQ-3"])
    assert result.exit_code != 0
    assert "--reason" in result.output
    result = CliRunner().invoke(main, ["question", verb, "EQ-3", "--reason", "  "])
    assert result.exit_code != 0
    assert "--reason is required" in result.output
    assert seams == []


def test_render_groups_by_task_then_series():
    rows = [
        {"id": 1, "task_id": 7, "task_title": "Seven", "series": 1, "question": "a?",
         "status": "answered", "answer": "yes", "answered_by": "ES-9"},
        {"id": 2, "task_id": 7, "task_title": "Seven", "series": 2, "question": "b?",
         "status": "open", "answer": None, "answered_by": None},
        {"id": 3, "task_id": 8, "task_title": "Eight", "series": 1, "question": "c?",
         "status": "open", "answer": None, "answered_by": None},
        {"id": 4, "task_id": 8, "task_title": "Eight", "series": 1, "question": "d?",
         "status": "withdrawn", "answer": None, "answered_by": None, "reason": "moot"},
    ]
    out = click.unstyle(question_cmd.render_questions(rows))
    lines = out.splitlines()
    assert lines[0] == "E-7  Seven"
    assert lines[1] == "  series 1"
    assert "EQ-1" in lines[2] and "answered" in lines[2] and "a?" in lines[2]
    assert lines[3].strip() == "→ yes (by ES-9)"
    assert lines[4] == "  series 2"
    assert lines[6] == ""
    assert lines[7] == "E-8  Eight"
    assert lines[-1].strip() == "✕ moot"


# --- end to end, through the real CLI and the real Go executors ---------------


@pytest.fixture
def real_task(seeded_project_at_cwd, monkeypatch):
    from endless import config, db
    # `--no-session` sets config.NO_SESSION process-wide and nothing resets it;
    # registering it with monkeypatch restores it after the test, so later tests
    # do not silently emit as the system actor.
    monkeypatch.setattr(config, "NO_SESSION", False)
    project_id = db.query("SELECT id FROM projects")[0]["id"]
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, phase) "
        "VALUES (600, ?, 'asked of', 'ready', 'now')",
        (project_id,),
    )
    return 600


def _run(*args):
    return CliRunner().invoke(main, [*args, "--no-session"])


def test_end_to_end_ask_answer_supersede_list(real_task):
    from endless import db

    r = _run("question", "ask", "E-600", "One?", "Two?")
    assert r.exit_code == 0, r.output
    assert "Asked series 1 on E-600: EQ-1, EQ-2" in r.output
    r = _run("question", "ask", "E-600", "Three?")
    assert "Asked series 2 on E-600: EQ-3" in r.output, r.output

    assert _run("question", "answer", "EQ-1", "Yes").exit_code == 0
    assert _run("question", "answer", "EQ-2", "No", "--by", "ES-5").exit_code == 0
    assert _run("question", "supersede", "EQ-1", "--reason", "in the plan").exit_code == 0
    assert _run("question", "reject", "EQ-3", "--reason", "wrong premise").exit_code == 0

    rows = db.query(
        "SELECT id, series, status, answer, answered_by, reason FROM task_questions ORDER BY id")
    assert [tuple(r[k] for k in ("id", "series", "status", "answer", "answered_by", "reason"))
            for r in rows] == [
        (1, 1, "superseded", "Yes", "user", "in the plan"),
        (2, 1, "answered", "No", "ES-5", None),
        (3, 2, "invalid", None, None, "wrong premise"),
    ]

    r = _run("question", "list", "E-600")
    assert r.exit_code == 0 and "No open questions on E-600." in r.output, r.output
    r = _run("question", "list", "E-600", "--all")
    assert "→ No (by ES-5)" in r.output, r.output
    assert "✕ wrong premise" in r.output, r.output


def test_end_to_end_refused_move_changes_nothing(real_task):
    from endless import db

    assert _run("question", "ask", "E-600", "One?").exit_code == 0
    assert _run("question", "withdraw", "EQ-1", "--reason", "moot").exit_code == 0
    r = _run("question", "answer", "EQ-1", "late")
    assert r.exit_code != 0
    assert "cannot become answered" in r.output
    assert db.query("SELECT status FROM task_questions")[0]["status"] == "withdrawn"


def test_end_to_end_unknown_task_and_question(real_task):
    r = _run("question", "ask", "E-9999", "One?")
    assert r.exit_code != 0 and "no task E-9999" in r.output
    r = _run("question", "answer", "EQ-42", "x")
    assert r.exit_code != 0 and "no question EQ-42" in r.output
