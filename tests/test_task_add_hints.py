"""Tests for the `task add` file-time hints (E-1889).

Three advisories fire after a task is filed: the discovery may be a bug in
work this session just landed, the backlog already costs N open tasks, and
this session has filed others that may share a root cause with it.

Every one is a HINT. The invariants worth pinning are therefore (a) each fires
exactly when its condition holds and stays silent otherwise, and (b) none of
them can ever cost the caller the add — a hint that blows up is swallowed.
"""

from unittest.mock import patch

import pytest
from click.testing import CliRunner

from endless import db


def _insert_session(pk: int, project_id: int) -> None:
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at) VALUES (?, ?, ?, 'claude', 'working', "
        "'2026-08-01T00:00:00')",
        (pk, f"uuid-{pk}", project_id),
    )


def _insert_task(pk: int, project_id: int, *, status: str = "untriaged",
                 title: str = "a task") -> None:
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
        (pk, project_id, title, status),
    )


def _insert_landing(task_id: int, session_id: int | None,
                    hours_ago: float) -> None:
    db.execute(
        "INSERT INTO task_landings (task_id, session_id, branch, "
        "merge_commit_sha, landed_at) VALUES (?, ?, 'b', 'sha', "
        "strftime('%Y-%m-%dT%H:%M:%S', 'now', ?))",
        (task_id, session_id, f"-{hours_ago} hours"),
    )


def _insert_surfaced(session_id: int, task_id: int) -> None:
    """Record that `session_id` filed `task_id` (relation 2 = 'surfaced')."""
    db.execute(
        "INSERT INTO session_tasks (session_id, task_id, created_at, "
        "updated_at, relation_id) VALUES (?, ?, '2026-08-01T00:00:00', "
        "'2026-08-01T00:00:00', 2)",
        (session_id, task_id),
    )


@pytest.fixture
def project(seeded_project_at_cwd):
    pid = db.query(
        "SELECT id FROM projects WHERE path = ?",
        (str(seeded_project_at_cwd),),
    )[0]["id"]
    _insert_session(1, pid)  # matches conftest's stubbed session id
    return pid


# ── hint 1: recently landed ──────────────────────────────────────────────────

def test_recently_landed_fires_for_this_sessions_own_landing(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2000, project)          # the landed task
    _insert_task(2001, project)          # the one just filed against it
    _insert_landing(2000, session_id=1, hours_ago=3)

    print_add_hints(2001, (2000,))

    out = capsys.readouterr().out
    assert "E-2000 was landed by this session 3h ago" in out
    assert "endless task update E-2000 --status revisit" in out


def test_recently_landed_silent_for_another_sessions_landing(project, capsys):
    """Another session's landed work is not something THIS session broke."""
    from endless.task_cmd import print_add_hints

    _insert_session(2, project)
    _insert_task(2010, project)
    _insert_task(2011, project)
    _insert_landing(2010, session_id=2, hours_ago=3)

    print_add_hints(2011, (2010,))

    assert "was landed by this session" not in capsys.readouterr().out


def test_recently_landed_silent_outside_the_window(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2020, project)
    _insert_task(2021, project)
    _insert_landing(2020, session_id=1, hours_ago=30)

    print_add_hints(2021, (2020,))

    assert "was landed by this session" not in capsys.readouterr().out


def test_recently_landed_silent_without_cleans_up(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2030, project)
    _insert_task(2031, project)
    _insert_landing(2030, session_id=1, hours_ago=1)

    print_add_hints(2031, ())

    assert "was landed by this session" not in capsys.readouterr().out


def test_recently_landed_fires_once_per_cleans_up_target(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2040, project)
    _insert_task(2041, project)
    _insert_task(2042, project)
    _insert_landing(2040, session_id=1, hours_ago=2)
    _insert_landing(2041, session_id=1, hours_ago=4)

    print_add_hints(2042, (2040, 2041))

    out = capsys.readouterr().out
    assert "E-2040 was landed by this session 2h ago" in out
    assert "E-2041 was landed by this session 4h ago" in out


# ── hint 2: backlog pressure ─────────────────────────────────────────────────

def test_backlog_pressure_counts_open_tasks(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2100, project, status="untriaged")
    _insert_task(2101, project, status="unplanned")
    _insert_task(2102, project, status="confirmed")   # not open
    _insert_task(2103, project, status="underway")    # not open

    print_add_hints(2100, ())

    out = capsys.readouterr().out
    assert "test now carries 2 open tasks (untriaged/unplanned)" in out


def test_backlog_pressure_singular_for_one_open_task(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2110, project, status="untriaged")

    print_add_hints(2110, ())

    assert "carries 1 open task (untriaged/unplanned)" in capsys.readouterr().out


def test_backlog_pressure_scoped_to_the_tasks_own_project(project, capsys):
    from endless.task_cmd import print_add_hints

    db.execute(
        "INSERT INTO projects (id, name, path, status, created_at, updated_at) "
        "VALUES (900, 'other', '/tmp/other-proj', 'active', datetime('now'), "
        "datetime('now'))"
    )
    _insert_task(2120, project, status="untriaged")
    _insert_task(2121, 900, status="untriaged")
    _insert_task(2122, 900, status="unplanned")

    print_add_hints(2120, ())

    assert "test now carries 1 open task" in capsys.readouterr().out


# ── hint 3: same-session root cause ──────────────────────────────────────────

def test_root_cause_lists_this_sessions_prior_filings(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2200, project, title="Status list drifted in cli.py")
    _insert_task(2201, project, title="Status list drifted in the web view")
    _insert_surfaced(1, 2200)
    _insert_surfaced(1, 2201)

    print_add_hints(2201, ())

    out = capsys.readouterr().out
    assert "This session has already filed 1 task:" in out
    assert "E-2200  Status list drifted in cli.py" in out
    assert "File the cause, not each symptom." in out
    # The task just filed is never listed against itself.
    assert "E-2201" not in out


def test_root_cause_silent_on_the_sessions_first_filing(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_task(2210, project)
    _insert_surfaced(1, 2210)

    print_add_hints(2210, ())

    assert "already filed" not in capsys.readouterr().out


def test_root_cause_ignores_another_sessions_filings(project, capsys):
    from endless.task_cmd import print_add_hints

    _insert_session(2, project)
    _insert_task(2220, project)
    _insert_task(2221, project)
    _insert_surfaced(2, 2220)
    _insert_surfaced(1, 2221)

    print_add_hints(2221, ())

    assert "already filed" not in capsys.readouterr().out


def test_root_cause_ignores_non_surfaced_touches(project, capsys):
    """A task this session *claimed* or *revisited* was not filed by it."""
    from endless.task_cmd import print_add_hints

    _insert_task(2230, project)
    _insert_task(2231, project)
    db.execute(
        "INSERT INTO session_tasks (session_id, task_id, created_at, "
        "updated_at, relation_id) VALUES (1, 2230, '2026-08-01T00:00:00', "
        "'2026-08-01T00:00:00', 1)"  # 1 = 'claimed'
    )
    _insert_surfaced(1, 2231)

    print_add_hints(2231, ())

    assert "already filed" not in capsys.readouterr().out


# ── the never-block invariant ────────────────────────────────────────────────

def test_hints_never_raise_when_a_query_fails(project, capsys):
    from endless import task_cmd

    _insert_task(2300, project)

    with patch.object(task_cmd.db, "query", side_effect=RuntimeError("boom")):
        task_cmd.print_add_hints(2300, ())   # must not raise

    assert capsys.readouterr().out == ""


def test_hints_never_raise_without_a_session(project, capsys):
    """Filing from a plain shell (no bound session) silences hints 1 and 3
    rather than erroring; the backlog count still applies."""
    from endless import task_cmd

    _insert_task(2310, project, status="untriaged")

    with patch.object(task_cmd, "_current_endless_session_id", return_value=None):
        task_cmd.print_add_hints(2310, (2310,))

    out = capsys.readouterr().out
    assert "was landed by this session" not in out
    assert "already filed" not in out
    assert "open task" in out


def test_task_add_still_succeeds_when_a_hint_blows_up(project):
    """End to end: a hint failure cannot cost the caller the task."""
    from endless import task_cmd
    from endless.cli import main

    with patch.object(task_cmd, "print_add_hints",
                      side_effect=RuntimeError("boom")):
        result = CliRunner().invoke(main, ["task", "add", "Fix the widget"])

    # The add itself is what must survive: the row exists and the id was
    # printed before the hint ran.
    assert "Added E-" in result.output
    rows = db.query("SELECT id FROM tasks WHERE title = 'Fix the widget'")
    assert len(rows) == 1


def test_task_add_prints_hints_through_the_cli(project):
    from endless.cli import main

    _insert_task(2400, project, status="untriaged")

    result = CliRunner().invoke(main, ["task", "add", "Fix the gadget"])

    assert result.exit_code == 0, result.output
    assert "open task" in result.output
