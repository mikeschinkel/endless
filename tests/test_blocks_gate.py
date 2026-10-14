"""E-2296: a task with a live blocker refuses its own start.

`task claim`, `task spawn` and `task prime` refuse while a task that blocks this
one is not terminal, naming each blocker and pointing at `task unblock`. Unlike
E-2270's `precedes` refusal there is no override: `--out-of-order` does not
cover it, and no other flag does either.
"""

import shutil

import click
import pytest

from endless import db, task_cmd


def _status(item_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))[0]["status"]


def _set_status(item_id: int, status: str) -> None:
    """A raw write: the setup needs a task sitting IN a status, not a test of
    the lifecycle guard that `task update --status` would run."""
    db.execute("UPDATE tasks SET status = ? WHERE id = ?", (status, item_id))


def _pair(blocker_status: str = "submitted") -> tuple[int, int]:
    """(blocker, dependent): both planned, the first linked as blocking."""
    blocker = task_cmd.add_item(title="Fix each fault", description="d", plan="# Plan")
    dependent = task_cmd.add_item(title="Fix each error", description="d", plan="# Plan")
    task_cmd.link_tasks(blocker, dependent, "blocks")
    if blocker_status != "submitted":
        _set_status(blocker, blocker_status)
    return blocker, dependent


# ---------------------------------------------------------------------------
# The helper
# ---------------------------------------------------------------------------

def test_a_live_blocker_is_listed(seeded_project_at_cwd):
    blocker, dependent = _pair()
    rows = task_cmd._live_blockers(dependent)
    assert [(r["id"], r["status"], r["title"]) for r in rows] == [
        (blocker, "submitted", "Fix each fault")]


def test_the_blocker_side_is_not_blocked(seeded_project_at_cwd):
    blocker, _ = _pair()
    assert task_cmd._live_blockers(blocker) == []


@pytest.mark.parametrize("status", ["confirmed", "assumed", "superseded"])
def test_a_terminal_blocker_does_not_refuse(seeded_project_at_cwd, status):
    _, dependent = _pair(status)
    assert task_cmd._live_blockers(dependent) == []
    for verb in ("claim", "spawn", "prime"):
        task_cmd._require_spawnable(dependent, verb)


def test_every_live_blocker_is_named(seeded_project_at_cwd):
    """More than three: the refusal names them all, where the status line's
    GetActiveBlockers stops at three."""
    dependent = task_cmd.add_item(title="Fix each error", description="d", plan="# Plan")
    blockers = [task_cmd.add_item(title=f"Fix fault {n}", description="d", plan="# Plan")
                for n in range(4)]
    for b in blockers:
        task_cmd.link_tasks(b, dependent, "blocks")
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(dependent, "claim")
    for b in blockers:
        assert f"E-{b}" in str(exc.value)


# ---------------------------------------------------------------------------
# The refusal, and no override
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("verb", ["claim", "spawn", "prime"])
def test_each_verb_refuses_naming_the_blocker(seeded_project_at_cwd, verb):
    blocker, dependent = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(dependent, verb)
    msg = str(exc.value)
    assert f"Cannot {verb} E-{dependent}" in msg
    assert f"E-{blocker}" in msg and "submitted" in msg
    assert f"endless task unblock E-{dependent} --by E-{blocker}" in msg
    assert "--out-of-order" not in msg


@pytest.mark.parametrize("verb", ["claim", "spawn", "prime"])
def test_out_of_order_does_not_override_a_block(seeded_project_at_cwd, verb):
    blocker, dependent = _pair()
    with pytest.raises(click.ClickException, match=f"E-{blocker}"):
        task_cmd._require_spawnable(dependent, verb, out_of_order=True)


def test_unblocking_releases_the_task(seeded_project_at_cwd):
    blocker, dependent = _pair()
    task_cmd.unlink_tasks(blocker, dependent, "blocks")
    task_cmd._require_spawnable(dependent, "claim")


# ---------------------------------------------------------------------------
# Through each command
# ---------------------------------------------------------------------------

def test_claim_refuses_and_changes_nothing(seeded_project_at_cwd):
    blocker, dependent = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd.claim_item(dependent, unattended=True, out_of_order=True)
    assert f"E-{blocker}" in str(exc.value)
    assert _status(dependent) == "submitted", "a refused claim changes nothing"


@pytest.fixture
def in_tmux(monkeypatch):
    real_which = shutil.which
    monkeypatch.setattr(shutil, "which",
                        lambda name: "/usr/bin/tmux" if name == "tmux" else real_which(name))
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    monkeypatch.setattr(task_cmd, "_check_task_ownership", lambda *a, **k: None)


def test_spawn_refuses_and_changes_nothing(seeded_project_at_cwd, in_tmux):
    blocker, dependent = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd.spawn_plan(dependent, out_of_order=True)
    assert f"Cannot spawn E-{dependent}" in str(exc.value)
    assert f"E-{blocker}" in str(exc.value)
    assert _status(dependent) == "submitted"


def test_prime_refuses_and_changes_nothing(seeded_project_at_cwd, in_tmux):
    blocker, dependent = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd.prime_task(dependent, out_of_order=True)
    assert f"Cannot prime E-{dependent}" in str(exc.value)
    assert f"E-{blocker}" in str(exc.value)
    assert _status(dependent) == "submitted"
