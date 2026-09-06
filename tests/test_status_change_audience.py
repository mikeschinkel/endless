"""E-2120: `task update` narrates a status change to a human, not to an agent.

Every status-affecting update printed two things addressed to the agent: the
`• Status: <old> -> <new>` field render, and an advisory naming `--keep-status`.
Agents relayed both to their user as if a completed, correct transition were
news — spending the scarcest resource in the loop on something nobody could act
on. Rewording the advisory was tried (E-1859, which recorded the loop "observed
four times in one session") and did not take: the stimulus is the PRESENCE of
agent-addressed text about a status, not its phrasing.

So the surface is audience-gated instead. A human sees byte-for-byte what it
always printed. An agent sees the fields it ASKED to change — an explicit
`--status` still renders, a status inferred from some other edit does not, and
no advisory fires. The transition itself is unaffected in either case; what
changes is who is told about it.

The gate is `agent_help.agent_facing()`, opened here through the harness signal
rather than by stubbing that function — it is where detection and `--agent-view`
compose (E-2097), and a stub would assert against the stub.
"""

import pytest

from endless import agent_help, db, task_cmd


@pytest.fixture(autouse=True)
def _agent_view_off(monkeypatch):
    """Pin `--agent-view` off so ambient CLI state cannot open the gate.

    conftest already strips the harness env vars, so a test that does not call
    `_as_agent` is a human.
    """
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)


def _as_agent(monkeypatch) -> None:
    monkeypatch.setenv("CLAUDE_CODE_ENTRYPOINT", "cli")


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


def _ready_task() -> int:
    """A `ready` task, built as a human so no gate is open during setup."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    task_cmd.update_plan(item_id=item_id, status="ready")
    return item_id


# ─── the inferred status change: shown to a human, withheld from an agent ────

def test_an_agent_is_not_told_about_a_status_it_did_not_ask_for(
    capsys, monkeypatch, seeded_project_at_cwd
):
    item_id = _ready_task()
    capsys.readouterr()
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, description="a materially different spec")

    out = capsys.readouterr().out
    assert "Description:" in out, "the field it DID ask for still renders"
    assert "Status:" not in out, out
    assert "re-triage" not in out, "the advisory is human-only"
    assert _status_of(item_id) == "untriaged", "the reset still happened"


def test_a_human_still_sees_the_status_line_and_the_advisory(
    capsys, seeded_project_at_cwd
):
    item_id = _ready_task()
    capsys.readouterr()

    task_cmd.update_plan(item_id=item_id, description="a materially different spec")

    out = capsys.readouterr().out
    assert "Status:" in out, out
    assert "re-triage" in out, out
    assert _status_of(item_id) == "untriaged"


def test_an_agent_still_sees_a_status_it_named(
    capsys, monkeypatch, seeded_project_at_cwd
):
    """An explicit `--status` is a field the agent asked for. It renders."""
    item_id = _ready_task()
    capsys.readouterr()
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, status="revisit")

    out = capsys.readouterr().out
    assert "Status:" in out, out
    assert "revisit" in out, out


def test_an_agent_is_not_told_about_the_tier_1_advance(
    capsys, monkeypatch, seeded_project_at_cwd
):
    """The other inference on this path, held to the same rule."""
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    capsys.readouterr()
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, tier=1)

    out = capsys.readouterr().out
    assert "Tier:" in out, "the field it DID ask for still renders"
    assert "Status:" not in out, out
    assert _status_of(item_id) == "ready", "the advance still happened"


def test_the_agent_render_is_never_left_empty(
    capsys, monkeypatch, seeded_project_at_cwd
):
    """Filtering the status entry cannot swallow the whole report.

    Every inferred status change rides along with the edit that produced it, so
    the field the agent asked for is always still there to print.
    """
    item_id = _ready_task()
    capsys.readouterr()
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, description="a materially different spec")

    out = capsys.readouterr().out
    assert f"Updated E-{item_id}" in out, out
    assert "•" in out, "at least one field bullet survives the filter"
