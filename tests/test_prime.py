"""E-1994: start a task's session ahead of need, so it reads in and waits.

Covers the Python half: the type-aware plan gate, the route the no-plan refusal
offers, the drafted-plan challenge, the primed handoff clause, `task prime`'s
launch, and claim starting a task its primed session already holds.
"""

import shutil
import subprocess
from pathlib import Path

import click
import pytest

from endless import db, plan_challenge, question_cmd, task_cmd


def _status(item_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))[0]["status"]


# ---------------------------------------------------------------------------
# Type-aware sufficiency
# ---------------------------------------------------------------------------

def test_a_framed_brainstorm_is_not_refused_as_planless(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Explore a thing", description="d",
                                task_type="brainstorm", context="The framing.")
    task_cmd._require_spawnable(item_id, "claim")


def test_framed_research_passes_too(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Explore a thing", description="d",
                                task_type="research", context="The framing.",
                                justification="needs a separate read")
    task_cmd._require_spawnable(item_id, "claim")


def test_an_unframed_brainstorm_is_still_planless(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Explore a thing", description="d",
                                task_type="brainstorm")
    with pytest.raises(click.ClickException, match="has no plan"):
        task_cmd._require_spawnable(item_id, "claim")


def test_a_framed_todo_still_needs_a_plan(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d",
                                task_type="todo", context="The framing.")
    with pytest.raises(click.ClickException, match="has no plan"):
        task_cmd._require_spawnable(item_id, "claim")


def test_the_no_plan_refusal_offers_prime(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(item_id, "spawn")
    assert f"endless task prime E-{item_id}" in str(exc.value)


def test_prime_relaxes_the_plan_gate_but_not_questions(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    task_cmd._require_spawnable(item_id, "prime", require_plan=False)

    question_cmd.ask_questions(item_id, ("Which way round?",))
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(item_id, "prime", require_plan=False)
    assert "parked on 1 open question" in str(exc.value)
    assert "has no plan" not in str(exc.value)


# ---------------------------------------------------------------------------
# The challenge
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("reply,passed,first", [
    ("PASS", True, None),
    ("pass\n", True, None),
    ("FAIL\n- it skips the migration\n- step 3 is vague", False, "it skips the migration"),
    ("FAIL", False, "without saying why"),
    ("", False, "empty reply"),
    ("Looks fine to me!", False, "could not be read"),
])
def test_parse_reply_fails_closed(reply, passed, first):
    v = plan_challenge.parse_reply(reply)
    assert v.passed is passed
    if first is None:
        assert v.objections == ()
    else:
        assert first in v.objections[0]


def test_challenge_fails_closed_without_claude(monkeypatch):
    from endless import internal_claude

    def boom(*a, **k):
        raise FileNotFoundError("claude")
    monkeypatch.setattr(internal_claude, "run_internal_claude", boom)
    v = plan_challenge.challenge(task_id=1, title="t", task_type="todo",
                                 description="", context="", plan="# P", model=None)
    assert not v.passed
    assert "not on PATH" in v.objections[0]


def test_build_prompt_carries_the_task_and_plan():
    p = plan_challenge.build_prompt(task_id=7, title="Do X", task_type="",
                                    description="", context="Why", plan="# Plan\nsteps")
    assert "E-7: Do X" in p and "Type: todo" in p
    assert "Why" in p and "steps" in p and "(none)" in p


def _fake_challenge(monkeypatch, verdict):
    calls = []

    def fake(**kw):
        calls.append(kw)
        return verdict
    monkeypatch.setattr(plan_challenge, "challenge", fake)
    return calls


def test_a_drafted_plan_that_fails_is_not_attached(seeded_project_at_cwd, monkeypatch):
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    monkeypatch.setattr(task_cmd, "_drafting_session_holds", lambda i: True)
    calls = _fake_challenge(monkeypatch, plan_challenge.Verdict(False, ("too vague",)))

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id, plan="# Plan\nmaybe")
    assert "did not pass its challenge" in str(exc.value)
    assert "too vague" in str(exc.value)
    assert calls and calls[0]["plan"] == "# Plan\nmaybe"
    assert not db.task_content(item_id).get("plan")
    assert _status(item_id) == "unplanned"


def test_a_drafted_plan_that_passes_reaches_submitted(seeded_project_at_cwd, monkeypatch):
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    monkeypatch.setattr(task_cmd, "_drafting_session_holds", lambda i: True)
    _fake_challenge(monkeypatch, plan_challenge.Verdict(True, ()))

    task_cmd.update_plan(item_id, plan="# Plan\nconcrete")
    assert _status(item_id) == "submitted"


def test_a_plan_attached_by_anyone_else_is_not_challenged(seeded_project_at_cwd, monkeypatch):
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    monkeypatch.setattr(task_cmd, "_drafting_session_holds", lambda i: False)
    calls = _fake_challenge(monkeypatch, plan_challenge.Verdict(False, ("no",)))

    task_cmd.update_plan(item_id, plan="# Plan\nbody")
    assert calls == []
    assert _status(item_id) == "submitted"


def test_editing_an_existing_plan_is_not_challenged(seeded_project_at_cwd, monkeypatch):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan="# Plan\nv1")
    monkeypatch.setattr(task_cmd, "_drafting_session_holds", lambda i: True)
    calls = _fake_challenge(monkeypatch, plan_challenge.Verdict(False, ("no",)))

    task_cmd.update_plan(item_id, plan="# Plan\nv2")
    assert calls == []


def test_plan_attach_requests_a_prime(seeded_project_at_cwd):
    filed = task_cmd.add_item(title="Fix a thing", description="d")
    task_cmd.update_plan(filed, plan="# Plan\nbody")
    with_plan = task_cmd.add_item(title="Fix another", description="d", plan="# Plan")
    bare = task_cmd.add_item(title="Fix a third", description="d")
    got = {r["id"]: r["prime_requested"] for r in db.query(
        "SELECT id, prime_requested FROM tasks WHERE id IN (?, ?, ?)",
        (filed, with_plan, bare))}
    assert got == {filed: 1, with_plan: 1, bare: 0}


# ---------------------------------------------------------------------------
# The handoff clause
# ---------------------------------------------------------------------------

def test_primed_handoff_opens_with_the_wait_clause(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan="# Plan")
    text = task_cmd.render_handoff(item_id, "Fix a thing", primed=True)
    head, _, body = text.partition("\n---\n")
    assert "Read in, then wait" in head
    assert "endless session primed" in head
    assert f"endless question ask E-{item_id}" in head
    assert "Draft one" not in head
    assert "spawned to take this task end to end" in body


def test_drafting_handoff_asks_for_a_plan(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    text = task_cmd.render_handoff(item_id, "Fix a thing", primed=True, drafting=True)
    assert "has no plan. Draft one" in text
    assert "--complexity" in text and "--risk" in text


def test_a_user_started_session_gets_no_wait_clause(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan="# Plan")
    text = task_cmd.render_handoff(item_id, "Fix a thing")
    assert "Read in, then wait" not in text
    assert "session primed" not in text


# ---------------------------------------------------------------------------
# task prime
# ---------------------------------------------------------------------------

@pytest.fixture
def prime_env(monkeypatch, tmp_path):
    """Stub the launch machinery. `arm()` installs the stubs; call it after
    seeding, because filing a task resolves endless-go and emits its event
    through the very calls stubbed here."""
    from endless import worktree_cmd
    wt = tmp_path / "wt"
    rendered = {}
    calls = []

    def fake_render(*a, **k):
        rendered.update(k)
        return "HANDOFF"

    def arm():
        monkeypatch.setattr(shutil, "which", lambda name: f"/usr/bin/{name}")
        monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
        monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "claude")
        monkeypatch.setattr(task_cmd, "_check_task_ownership", lambda *a, **k: None)
        monkeypatch.setattr(task_cmd, "_open_questions", lambda item_id: [])
        monkeypatch.setattr(task_cmd, "_branch_for_worktree", lambda p: "br")
        monkeypatch.setattr(worktree_cmd, "_project_root", lambda: tmp_path)
        monkeypatch.setattr(worktree_cmd, "create_task_worktree", lambda i, r: (wt, True))
        monkeypatch.setattr(task_cmd, "render_handoff", fake_render)
        monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: calls.append(list(cmd)))
    return calls, rendered, wt, arm


def test_prime_launches_detached_and_claims_nothing(seeded_project_at_cwd, prime_env):
    calls, rendered, wt, arm = prime_env
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan="# Plan")
    arm()
    task_cmd.prime_task(item_id, auto=True, target_session="$3")

    cmd = [c for c in calls if "spawn-window" in c][0]
    assert "--no-refocus" in cmd and "--auto" not in cmd
    assert cmd[cmd.index("--placement") + 1] == "first"
    assert "--prime-draft" not in cmd, "a planned task is read, not drafted"
    assert cmd[cmd.index("--cwd") + 1] == str(wt)
    assert cmd[cmd.index("--target-session") + 1] == "$3"
    assert cmd[cmd.index("--spawned-by") + 1] == "prime"
    assert rendered["primed"] is True and rendered["drafting"] is False
    assert _status(item_id) == "submitted", "priming never moves status"


def test_prime_drafts_for_a_planless_task(seeded_project_at_cwd, prime_env):
    calls, rendered, _, arm = prime_env
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    arm()
    task_cmd.prime_task(item_id)
    assert rendered["drafting"] is True
    cmd = [c for c in calls if "spawn-window" in c][0]
    assert "--prime-draft" in cmd, "a drafting window is marked for the challenge"
    assert _status(item_id) == "unplanned"


def test_prime_does_not_draft_for_a_framed_brainstorm(seeded_project_at_cwd, prime_env):
    _, rendered, _, arm = prime_env
    item_id = task_cmd.add_item(title="Explore a thing", description="d",
                                task_type="brainstorm", context="Framing.")
    arm()
    task_cmd.prime_task(item_id)
    assert rendered["drafting"] is False


def test_prime_refuses_started_work(seeded_project_at_cwd, prime_env):
    item_id = task_cmd.add_item(title="Fix a thing", description="d",
                                plan="# Plan", status="underway")
    prime_env[3]()
    with pytest.raises(click.ClickException, match="nobody has started"):
        task_cmd.prime_task(item_id)


# ---------------------------------------------------------------------------
# Claim from a primed session starts the task
# ---------------------------------------------------------------------------

def test_claim_by_the_holding_session_starts_a_pre_work_task(seeded_project_at_cwd, monkeypatch):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan="# Plan")
    monkeypatch.setattr(task_cmd, "_check_task_ownership", lambda *a, **k: True)
    monkeypatch.setattr(task_cmd, "_echo_claim_next_step", lambda *a, **k: None)
    from endless import worktree_cmd
    monkeypatch.setattr(worktree_cmd, "create_task_worktree",
                        lambda i, r: (Path("/wt"), True))
    task_cmd.claim_item(item_id, unattended=True)
    assert _status(item_id) == "underway"


def test_a_bound_session_without_the_draft_marker_is_not_the_drafter(
        seeded_project_at_cwd, monkeypatch):
    """`task bind` can bind any session to a planless task; only the window
    `task prime` marked makes it the drafter."""
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    monkeypatch.setattr(task_cmd, "_current_endless_session_id", lambda: 99)
    monkeypatch.setattr(task_cmd.db, "query",
                        lambda sql, params=(): [{"task_id": item_id}])
    monkeypatch.setattr(task_cmd, "_prime_draft_window_task", lambda: None)
    assert not task_cmd._drafting_session_holds(item_id)
    monkeypatch.setattr(task_cmd, "_prime_draft_window_task", lambda: str(item_id + 1))
    assert not task_cmd._drafting_session_holds(item_id)
    monkeypatch.setattr(task_cmd, "_prime_draft_window_task", lambda: str(item_id))
    assert task_cmd._drafting_session_holds(item_id)


def test_submit_after_a_held_attach_requests_a_prime(seeded_project_at_cwd):
    """An agent's unrated attach holds the status (E-2203); its later
    `task submit` is then the move to `submitted`, and must request the prime
    the held attach could not."""
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    task_cmd.update_plan(item_id, plan="# Plan\nbody", keep_status=True)
    assert db.query("SELECT prime_requested FROM tasks WHERE id = ?",
                    (item_id,))[0]["prime_requested"] == 0
    task_cmd.submit_item(item_id, complexity="low", risk="low")
    row = db.query("SELECT status, prime_requested FROM tasks WHERE id = ?", (item_id,))[0]
    assert (row["status"], row["prime_requested"]) == ("submitted", 1)
