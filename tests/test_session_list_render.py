"""Tests for the reworked `session list` output (E-1914).

Four changes, all asserted here: the Summary column is gone and the active task
id + its title took its place; state renders as a one-column glyph with a legend
(the fix for the ragged table `needs_input` used to cause); the listing defaults
to the project enclosing cwd, with --all-projects / --project around it; and the
Project column appears only when the output actually spans more than one project.
"""

import json

import pytest
from click.testing import CliRunner

from endless import db
from endless.cli import main
from endless.session_cmd import SESSION_STATE_ICONS


def _project(project_id, name):
    db.execute(
        "INSERT INTO projects (id, name, path) VALUES (?, ?, ?)",
        (project_id, name, f"/{name}"),
    )


def _task(task_id, project_id, title):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, phase) "
        "VALUES (?, ?, ?, 'ready', 'now')",
        (task_id, project_id, title),
    )


def _session(session_id, project_id, state, active_task_id=None, messages=1):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state, kind_id, "
        "active_task_id) VALUES (?, ?, ?, ?, 1, ?)",
        (session_id, f"uuid-{session_id}", project_id, state, active_task_id),
    )
    for n in range(messages):
        db.execute(
            "INSERT INTO session_messages (session_id, role, content, created_at) "
            "VALUES (?, 'user', ?, '2026-08-07T00:00:00')",
            (f"uuid-{session_id}", f"msg {n}"),
        )


def _run(*args):
    return CliRunner().invoke(main, list(args))


def _body(output):
    """The data rows: everything between the ─── separator and the legend."""
    lines = output.splitlines()
    start = next(i for i, ln in enumerate(lines) if ln.startswith("─")) + 1
    rows = []
    for line in lines[start:]:
        if not line.strip() or line.startswith("⟳"):
            break
        rows.append(line)
    return rows


@pytest.fixture
def one_project():
    _project(1, "probe")
    _task(1596, 1, "Fix worktree bootstrap fallback")
    _task(1902, 1, "Session store audit")
    _session(982, 1, "working", 1596)
    _session(963, 1, "needs_input", 1902)


# --- columns ----------------------------------------------------------------


def test_task_id_and_title_replace_the_summary(one_project):
    db.execute("UPDATE sessions SET summary = 'a discussion recap' WHERE id = 982")

    result = _run("session", "list", "--project", "probe")

    assert result.exit_code == 0, result.output
    assert "Summary" not in result.output
    assert "a discussion recap" not in result.output
    assert "E-1596" in result.output
    assert "Fix worktree bootstrap fallback" in result.output


def test_state_renders_as_a_glyph_with_a_legend(one_project):
    result = _run("session", "list", "--project", "probe")

    assert "needs_input" not in result.output
    assert "working" not in "\n".join(_body(result.output))
    assert SESSION_STATE_ICONS["working"] in result.output
    assert SESSION_STATE_ICONS["needs_input"] in result.output
    # The legend is static, so all four states are always documented.
    for icon in SESSION_STATE_ICONS.values():
        assert icon in result.output


def test_needs_input_row_does_not_skew_the_table(one_project):
    """The ragged-output bug: `needs_input` is 11 chars against `idle`'s 4, so
    printing the raw word shoved every following column right on that row only.
    Asserted on rendered offsets, not by eye."""
    result = _run("session", "list", "--project", "probe")

    rows = _body(result.output)
    assert len(rows) == 2
    offsets = {row.index("E-"): row for row in rows}
    assert len(offsets) == 1, f"Task column is ragged: {rows}"

    # ...and the title column lines up too.
    titles = {982: "Fix worktree bootstrap fallback", 963: "Session store audit"}
    title_offsets = {
        row.index(titles[int(row.split()[0])]) for row in rows
    }
    assert len(title_offsets) == 1, f"Title column is ragged: {rows}"


def test_session_without_an_active_task_is_omitted_by_default(one_project):
    """Once Summary gave way to the task id + title, a session that never claimed
    a task has nothing to put in either column — on a real DB that is most of the
    roster. --all reveals them, as it already does for hidden and empty ones."""
    _session(551, 1, "idle", None)

    default = _run("session", "list", "--project", "probe")
    assert default.exit_code == 0, default.output
    assert [r for r in _body(default.output) if r.startswith("551")] == []
    assert [r for r in _body(default.output) if r.startswith("982")] != []

    shown = _run("session", "list", "--project", "probe", "--all")
    assert shown.exit_code == 0, shown.output
    rows = [r for r in _body(shown.output) if r.startswith("551")]
    assert len(rows) == 1
    assert "E-" not in rows[0]


def test_list_survives_a_row_with_invalid_utf8(one_project):
    """A byte-truncated string left by a long-dead writer must not take down the
    whole command. CAST(x'..' AS TEXT) stores the half-encoded codepoint as TEXT,
    exactly as the damaged production row holds it."""
    db.execute("UPDATE sessions SET summary = CAST(x'496ee2' AS TEXT) WHERE id = 982")

    result = _run("session", "list", "--project", "probe")

    assert result.exit_code == 0, result.output
    assert "Fix worktree bootstrap fallback" in result.output
    # The damage is marked, not silently dropped.
    assert db.query("SELECT summary FROM sessions WHERE id = 982")[0]["summary"] == "In�"


# --- project scoping --------------------------------------------------------


def test_defaults_to_the_current_project(monkeypatch, tmp_path):
    _project(1, "probe")
    _project(2, "other")
    _task(1596, 1, "Probe work")
    _task(2000, 2, "Other work")
    _session(982, 1, "working", 1596)
    _session(700, 2, "working", 2000)

    proj = tmp_path / "probe"
    (proj / ".endless").mkdir(parents=True)
    (proj / ".endless" / "config.json").write_text('{"name": "probe"}')
    db.execute("UPDATE projects SET path = ? WHERE id = 1", (str(proj),))
    monkeypatch.chdir(proj)

    result = _run("session", "list")

    assert result.exit_code == 0, result.output
    assert "Probe work" in result.output
    assert "Other work" not in result.output
    assert "project: probe" in result.output


def test_all_projects_spans_everything_and_adds_the_column():
    _project(1, "probe")
    _project(2, "other")
    _task(1596, 1, "Probe work")
    _task(2000, 2, "Other work")
    _session(982, 1, "working", 1596)
    _session(700, 2, "working", 2000)

    result = _run("session", "list", "--all-projects")

    assert result.exit_code == 0, result.output
    assert "Probe work" in result.output and "Other work" in result.output
    assert "Project" in result.output
    # With a Project column the header must NOT also claim a single project.
    assert "project: " not in result.output


def test_project_column_absent_for_single_project_output(one_project):
    result = _run("session", "list", "--project", "probe")

    assert "Project" not in result.output
    assert "project: probe" in result.output


def test_project_and_all_projects_together_is_an_error(one_project):
    result = _run("session", "list", "--project", "probe", "--all-projects")

    assert result.exit_code != 0
    assert "mutually exclusive" in result.output


def test_outside_a_registered_project_points_at_the_escape_hatches(monkeypatch, tmp_path):
    _project(1, "probe")
    _session(982, 1, "working", None)
    monkeypatch.chdir(tmp_path)

    result = _run("session", "list")

    assert result.exit_code != 0
    assert "--all-projects" in result.output
    assert "--project" in result.output


# --- json -------------------------------------------------------------------


def test_json_gains_task_id_and_keeps_the_raw_state(one_project):
    result = _run("session", "list", "--project", "probe", "--json")

    assert result.exit_code == 0, result.output
    rows = {r["id"]: r for r in json.loads(result.output)}
    assert rows[982]["task_id"] == 1596
    assert rows[963]["task_id"] == 1902
    # No icon substitution in JSON: consumers must not have to learn the glyphs.
    assert rows[963]["state"] == "needs_input"
    # Shape is otherwise unchanged.
    assert "summary" in rows[982] and "messages" in rows[982]
