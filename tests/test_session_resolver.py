"""Tests for E-1402: the session-id resolver that prompts for an
explicit choice when n>1 sibling Claude panes are alive.

Multi-pane is the common case for Mike — running 5+ Claude sessions in
sibling tmux panes is routine. A silent heuristic pick would land
attribution-less or wrongly-attributed events in the ledger, which is
worse than no row at all. So on a tty, the resolver lists the live
sessions and asks the user to pick. Off-tty, it raises loudly so the
miss is recognizable rather than silently corrupting state.
"""

import io
import json
import os
import sys
from pathlib import Path
from unittest.mock import patch

import click
import pytest

from endless import db


def _insert_session(
    *,
    pk: int,
    session_id: str,
    project_id: int,
    state: str = "working",
):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at) "
        "VALUES (?, ?, ?, 'claude', ?, '2026-05-17T00:00:00')",
        (pk, session_id, project_id, state),
    )


def _write_companion(
    sessions_dir: Path,
    *,
    eid: int,
    pid: int,
    pane_id: str,
    uuid: str,
):
    sessions_dir.mkdir(parents=True, exist_ok=True)
    data = {
        "harness": "claude",
        "harness_session_id": uuid,
        "endless_session_id": eid,
        "pane_id": pane_id,
        "cwd": str(sessions_dir.parent.parent),
        "pid": pid,
        "started_at": "2026-05-17T00:00:00Z",
    }
    (sessions_dir / f"claude-{uuid}.json").write_text(json.dumps(data))


@pytest.fixture
def project_at_cwd(seeded_project_at_cwd):
    return {
        "project_root": seeded_project_at_cwd,
        "project_id": db.query(
            "SELECT id FROM projects WHERE path = ?",
            (str(seeded_project_at_cwd),),
        )[0]["id"],
    }


@pytest.fixture
def my_pane(monkeypatch):
    """The shell pane the resolver is running from; not in sibling-id list."""
    monkeypatch.setenv("TMUX_PANE", "%999")
    monkeypatch.setenv("TMUX", "/tmp/tmux-test")
    return "%999"


@pytest.fixture(autouse=True)
def reset_cache():
    """Reset the per-process session-choice cache between tests so state
    from one test doesn't leak into the next."""
    from endless.task_cmd import _reset_session_choice_cache
    _reset_session_choice_cache()
    yield
    _reset_session_choice_cache()


def _setup_multi_sibling(project_at_cwd, eids: list[int]):
    """Insert sessions and companion files for the given eids; return the
    pane id list (including my_pane) callers should mock the tmux helper
    with."""
    project_id = project_at_cwd["project_id"]
    sessions_dir = project_at_cwd["project_root"] / ".endless" / "sessions"
    pid = os.getpid()
    panes = ["%999"]
    for i, eid in enumerate(eids, start=1):
        _insert_session(pk=eid, session_id=f"s-{eid}", project_id=project_id)
        pane = f"%{i}"
        _write_companion(
            sessions_dir, eid=eid, pid=pid, pane_id=pane, uuid=f"uuid-{eid}",
        )
        panes.append(pane)
    return panes


# ---------- _list_sibling_claude_session_eids ----------


def test_list_sibling_eids_zero_when_no_siblings(project_at_cwd, my_pane):
    from endless.task_cmd import _list_sibling_claude_session_eids
    with patch(
        "endless.session_cmd._tmux_window_pane_ids",
        return_value=[my_pane],
    ):
        assert _list_sibling_claude_session_eids() == []


def test_list_sibling_eids_returns_all_live_candidates(project_at_cwd, my_pane):
    from endless.task_cmd import _list_sibling_claude_session_eids
    panes = _setup_multi_sibling(project_at_cwd, [10, 20, 30])
    with patch(
        "endless.session_cmd._tmux_window_pane_ids",
        return_value=panes,
    ):
        assert sorted(_list_sibling_claude_session_eids()) == [10, 20, 30]


# ---------- _resolve_session_id_with_prompt: non-prompt paths ----------


def test_resolve_uses_env_var_when_set(monkeypatch):
    """Layer 1: ENDLESS_SESSION_ID wins, no prompt."""
    from endless.task_cmd import _resolve_session_id_with_prompt
    monkeypatch.setenv("ENDLESS_SESSION_ID", "777")
    assert _resolve_session_id_with_prompt() == 777


def test_resolve_returns_none_when_no_candidates(project_at_cwd, my_pane):
    """Layer 3 with 0 siblings → None, no prompt."""
    from endless.task_cmd import _resolve_session_id_with_prompt
    with patch(
        "endless.session_cmd._tmux_window_pane_ids",
        return_value=[my_pane],
    ):
        assert _resolve_session_id_with_prompt() is None


def test_resolve_auto_picks_single_sibling(project_at_cwd, my_pane):
    """Layer 3 with 1 sibling → auto-pick, no prompt."""
    from endless.task_cmd import _resolve_session_id_with_prompt
    panes = _setup_multi_sibling(project_at_cwd, [42])
    with patch(
        "endless.session_cmd._tmux_window_pane_ids",
        return_value=panes,
    ):
        assert _resolve_session_id_with_prompt() == 42


# ---------- _resolve_session_id_with_prompt: tty prompt path ----------


def test_resolve_prompts_on_tty_multi_sibling(project_at_cwd, my_pane, capsys):
    """n>1 siblings + tty → prints list, prompts, returns the chosen id."""
    from endless.task_cmd import _resolve_session_id_with_prompt

    panes = _setup_multi_sibling(project_at_cwd, [10, 20, 30])
    with patch("endless.session_cmd._tmux_window_pane_ids", return_value=panes), \
         patch("sys.stdin.isatty", return_value=True), \
         patch("click.prompt", return_value=20):
        result = _resolve_session_id_with_prompt(prompt_verb="claimed for")
    assert result == 20
    out = capsys.readouterr().out
    assert "There are multiple Claude sessions in this tmux window:" in out


def test_resolve_rejects_invalid_id_then_accepts_valid(
    project_at_cwd, my_pane, capsys,
):
    """Invalid id input → re-prompt; second (valid) input is accepted."""
    from endless.task_cmd import _resolve_session_id_with_prompt

    panes = _setup_multi_sibling(project_at_cwd, [10, 20])
    inputs = iter([99, 20])  # 99 isn't a candidate; 20 is

    with patch("endless.session_cmd._tmux_window_pane_ids", return_value=panes), \
         patch("sys.stdin.isatty", return_value=True), \
         patch("click.prompt", side_effect=lambda *a, **kw: next(inputs)):
        result = _resolve_session_id_with_prompt()
    assert result == 20
    out = capsys.readouterr().out
    assert "is not in this window's candidate set" in out


def test_resolve_caches_choice_for_process_lifetime(
    project_at_cwd, my_pane,
):
    """A second call within the same process returns the cached choice
    without prompting again."""
    from endless.task_cmd import _resolve_session_id_with_prompt

    panes = _setup_multi_sibling(project_at_cwd, [10, 20])
    prompt_calls = {"n": 0}

    def _fake_prompt(*a, **kw):
        prompt_calls["n"] += 1
        return 10

    with patch("endless.session_cmd._tmux_window_pane_ids", return_value=panes), \
         patch("sys.stdin.isatty", return_value=True), \
         patch("click.prompt", side_effect=_fake_prompt):
        first = _resolve_session_id_with_prompt()
        second = _resolve_session_id_with_prompt()

    assert first == 10
    assert second == 10
    assert prompt_calls["n"] == 1


def test_resolve_uses_prompt_verb_in_question(project_at_cwd, my_pane):
    """The supplied prompt_verb appears in the question string passed
    to click.prompt — that's how the call site shapes the wording."""
    from endless.task_cmd import _resolve_session_id_with_prompt

    panes = _setup_multi_sibling(project_at_cwd, [10, 20])
    captured = {}

    def _fake_prompt(question, *a, **kw):
        captured["question"] = question
        return 10

    with patch("endless.session_cmd._tmux_window_pane_ids", return_value=panes), \
         patch("sys.stdin.isatty", return_value=True), \
         patch("click.prompt", side_effect=_fake_prompt):
        _resolve_session_id_with_prompt(prompt_verb="claimed for")

    assert "claimed for" in captured["question"]
    assert "[ID]" in captured["question"]


def test_resolve_default_wording_when_no_verb_passed(project_at_cwd, my_pane):
    from endless.task_cmd import _resolve_session_id_with_prompt

    panes = _setup_multi_sibling(project_at_cwd, [10, 20])
    captured = {}

    def _fake_prompt(question, *a, **kw):
        captured["question"] = question
        return 10

    with patch("endless.session_cmd._tmux_window_pane_ids", return_value=panes), \
         patch("sys.stdin.isatty", return_value=True), \
         patch("click.prompt", side_effect=_fake_prompt):
        _resolve_session_id_with_prompt()

    assert "associated with" in captured["question"]


# ---------- _resolve_session_id_with_prompt: non-tty refusal ----------


def test_resolve_raises_loudly_when_non_tty_and_ambiguous(
    project_at_cwd, my_pane,
):
    """n>1 siblings + no tty → loud error so the miss is recognizable.
    Set ENDLESS_SESSION_ID or run interactively is the fix."""
    from endless.task_cmd import _resolve_session_id_with_prompt

    panes = _setup_multi_sibling(project_at_cwd, [10, 20, 30])
    with patch("endless.session_cmd._tmux_window_pane_ids", return_value=panes), \
         patch("sys.stdin.isatty", return_value=False):
        with pytest.raises(click.ClickException) as exc:
            _resolve_session_id_with_prompt()
    msg = str(exc.value)
    assert "3 live Claude sessions" in msg
    assert "ENDLESS_SESSION_ID" in msg
