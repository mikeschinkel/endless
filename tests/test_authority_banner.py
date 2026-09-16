"""The authority banner: is this record still the one to quote? (E-2095)

A status line is read as PROVENANCE and not as a caveat on the content beneath
it, so an agent quotes an obsolete task, a replaced one, or a decision nobody
ever accepted, as though it governed. The status was on screen every time.

Two properties are under test, and they are separable. The WORDING must
distinguish "not ever" (retired, declined, rejected, replaced, superseded) from
"not yet" (a proposed decision) — a proposed decision is not a retired one. The
SHAPE is E-2097's, whose own tests pin it for refusals: one dense line, first and
last, byte-identical, so whichever end a truncating pipe leaves is sufficient
alone.

The audience gate is stricter than E-2097's and the negative tests below are the
point of it. A human sees NOTHING new: the status line and the `Replaced by:`
link are already on screen and a person reads them as the qualifiers they are.
"""

import json

import pytest
from click.testing import CliRunner

from endless import agent_env, agent_help, authority, cli, db

_TASK = 1


@pytest.fixture(autouse=True)
def _pin_agent_view(monkeypatch):
    """`--agent-view` is module state set by the CLI's argv pre-scan, so an
    invocation that passes it would leak into every later test in the process."""
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)


@pytest.fixture
def as_agent(monkeypatch):
    """Run as a Claude Code agent. conftest strips the signal; this restores it."""
    monkeypatch.setenv(agent_env.ENTRYPOINT_VAR, agent_env.CLI_ENTRYPOINT)


def _add_task(title: str, status: str) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, _TASK),
    )
    return cur.lastrowid


def _add_decision(title: str, status: str) -> int:
    cur = db.execute(
        "INSERT INTO decisions (project_id, title, status, created_at, updated_at) "
        "VALUES (1, ?, ?, datetime('now'), datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _link_replaces(new_id: int, old_id: int) -> None:
    """`old replaced_by new` is stored active-voice: source=new, target=old."""
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, "
        "dep_type) VALUES ('task', ?, 'task', ?, 'replaces')",
        (new_id, old_id),
    )


def _run(*args) -> str:
    result = CliRunner().invoke(cli.main, list(args))
    assert result.exit_code == 0, result.output
    return result.output


def _lines(output: str) -> list[str]:
    return [ln for ln in output.strip("\n").split("\n")]


# ─── the predicate, in isolation ────────────────────────────────────────────


@pytest.mark.parametrize("status", ["obsolete", "declined"])
def test_a_retired_task_is_not_ever_authoritative(status):
    caveat = authority.for_task(status, [], [])
    assert caveat is not None and caveat.kind == "not-ever"


@pytest.mark.parametrize("status", ["confirmed", "assumed", "completed",
                                    "underway", "ready", "unverified"])
def test_a_live_or_finished_task_carries_no_caveat(status):
    assert authority.for_task(status, [], []) is None


def test_a_replaced_task_triggers_whatever_its_status():
    """`task replace` deliberately holds shipped work at the status it earned,
    so a replaced task can read `confirmed` and still not be the record to
    quote. Keying the banner on status alone would miss all 84 of them."""
    caveat = authority.for_task("confirmed", ["E-456"], [])
    assert caveat is not None
    assert caveat.see == ["E-456"]
    assert "Read E-456 instead." in caveat.summary("E-123")


def test_a_duplicate_points_at_the_record_that_was_kept():
    caveat = authority.for_task("obsolete", [], ["E-456"])
    assert "duplicates E-456" in caveat.reason
    assert caveat.see == ["E-456"]


def test_a_proposed_decision_is_not_yet_rather_than_not_ever():
    """The distinction the wording exists to carry. A decision nobody has
    accepted has not been rejected; quoting it as settled asserts an agreement
    nobody made."""
    caveat = authority.for_decision("proposed", [])
    assert caveat.kind == "not-yet"
    summary = caveat.summary("ED-56")
    assert "NOT YET authoritative" in summary
    assert "Do not quote it as settled." in summary


@pytest.mark.parametrize("status", ["rejected", "superseded"])
def test_a_retired_decision_is_not_ever_authoritative(status):
    assert authority.for_decision(status, []).kind == "not-ever"


def test_an_accepted_decision_carries_no_caveat():
    assert authority.for_decision("accepted", []) is None


def test_a_superseded_decision_names_its_successor():
    caveat = authority.for_decision("superseded", ["ED-78"])
    assert "Read ED-78 instead." in caveat.summary("ED-56")


# ─── the audience gate ──────────────────────────────────────────────────────


def test_a_human_sees_nothing():
    """Stricter than E-2097's split, deliberately. There the underlying message
    is a refusal a human must see; here there is nothing a human is owed."""
    assert authority.banner(authority.for_task("obsolete", [], []), "E-1") is None


def test_agent_view_lets_a_human_see_what_an_agent_sees(monkeypatch):
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", True)
    line = authority.banner(authority.for_task("obsolete", [], []), "E-1")
    assert line is not None and line.startswith(agent_help.ERROR_SENTINEL)


# ─── the shape, through the real CLI ────────────────────────────────────────


def test_task_show_brackets_the_record_for_an_agent(seeded_project_at_cwd, as_agent):
    task_id = _add_task("Add the retired thing", "obsolete")
    lines = _lines(_run("task", "show", f"E-{task_id}"))
    assert lines[0] == lines[-1], "\n".join(lines)
    assert lines[0].startswith(f"{agent_help.ERROR_SENTINEL} task show: E-{task_id}")


def test_head_and_tail_each_keep_the_caveat(seeded_project_at_cwd, as_agent):
    """Measured the way the failure was: a small window over the output, from
    either side."""
    task_id = _add_task("Add the retired thing", "declined")
    lines = _lines(_run("task", "show", f"E-{task_id}"))
    assert any(agent_help.ERROR_SENTINEL in ln for ln in lines[:3]), lines[:3]
    assert any(agent_help.ERROR_SENTINEL in ln for ln in lines[-3:]), lines[-3:]


def test_agent_mode_brackets_it_too(seeded_project_at_cwd, as_agent):
    task_id = _add_task("Add the retired thing", "obsolete")
    lines = _lines(_run("task", "show", f"E-{task_id}", "--agent"))
    assert lines[0] == lines[-1], "\n".join(lines)


def test_a_current_task_gets_no_banner(seeded_project_at_cwd, as_agent):
    task_id = _add_task("Add the current thing", "confirmed")
    out = _run("task", "show", f"E-{task_id}")
    assert agent_help.ERROR_SENTINEL not in out


def test_a_human_running_task_show_sees_no_banner(seeded_project_at_cwd):
    task_id = _add_task("Add the retired thing", "obsolete")
    out = _run("task", "show", f"E-{task_id}")
    assert agent_help.ERROR_SENTINEL not in out


def test_task_show_json_carries_the_fact_as_a_key(seeded_project_at_cwd):
    """Ungated, and in every format: nothing truncates JSON by lines, so the
    repetition would buy nothing — but a consumer still has to be able to ask."""
    keeper = _add_task("Add the thing that is kept", "underway")
    old = _add_task("Add the superseded thing", "confirmed")
    _link_replaces(keeper, old)
    out = json.loads(_run("task", "show", f"E-{old}", "--json"))
    assert out["authority"]["authoritative"] is False
    assert out["authority"]["see"] == [f"E-{keeper}"]


def test_task_show_json_says_so_when_the_record_is_authoritative(seeded_project_at_cwd):
    task_id = _add_task("Add the current thing", "assumed")
    out = json.loads(_run("task", "show", f"E-{task_id}", "--json"))
    assert out["authority"] == {"authoritative": True, "kind": None,
                                "reason": "", "see": [], "summary": ""}


def test_decision_show_brackets_a_proposed_decision(seeded_project_at_cwd, as_agent):
    decision_id = _add_decision("Decide the undecided thing", "proposed")
    lines = _lines(_run("decision", "show", f"ED-{decision_id}"))
    assert lines[0] == lines[-1], "\n".join(lines)
    assert "NOT YET authoritative" in lines[0]


def test_decision_show_leaves_an_accepted_decision_alone(seeded_project_at_cwd,
                                                         as_agent):
    decision_id = _add_decision("Decide the settled thing", "accepted")
    out = _run("decision", "show", f"ED-{decision_id}")
    assert agent_help.ERROR_SENTINEL not in out


def test_decision_show_json_carries_the_fact_as_a_key(seeded_project_at_cwd):
    decision_id = _add_decision("Decide the undecided thing", "proposed")
    out = json.loads(_run("decision", "show", f"ED-{decision_id}", "--json"))
    assert out["authority"]["kind"] == "not-yet"
