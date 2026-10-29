"""The verification-suite rules ship with every registered project (E-2023).

A file that existed only in Endless's own checkout would teach nobody, and the
mistakes it exists to prevent are not Endless-specific. So registration places
it, the same way it places `.gitignore` entries and `.endless/tmp/`.

Re-running registration brings an unedited copy up to the rules this Endless
ships, and leaves an edited one alone (E-2421). The text, every shipped
version's hash and that decision live in Go (internal/shipped, which also tests
the text itself); these tests cover what a registration does with them.
"""

import hashlib
import json
from pathlib import Path

from endless import db, event_bridge
from endless.register import register_project

from conftest import init_git_repo

REPO = Path(__file__).resolve().parents[1]
RULES = ".endless/tasks/CLAUDE.md"
# The repo's own copy is the shipped text (internal/shipped asserts it).
SHIPPED = (REPO / RULES).read_text()
# The first version Endless shipped (53c53f8a8, E-2023), from before any record.
FIRST_SHIPPED = (REPO / "tests" / "fixtures" / "tasks-rules-E-2023.md").read_text()


def _sha(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()


def _project(isolated_env, name: str) -> Path:
    project_dir = isolated_env["projects_root"] / name
    init_git_repo(project_dir)
    return project_dir


def _recorded(name: str) -> str | None:
    rows = db.query(
        "SELECT w.sha256 FROM shipped_file_writes w "
        "JOIN projects p ON p.id = w.project_id WHERE p.name = ? AND w.path = ?",
        (name, RULES),
    )
    return rows[0]["sha256"] if rows else None


def _ledger_writes(project_dir: Path) -> list[dict]:
    lines = []
    for seg in sorted((project_dir / ".endless" / "db-ledger").glob("*.jsonl")):
        lines += [json.loads(line) for line in seg.read_text().splitlines()]
    return [e for e in lines if e["kind"] == "shipped_file.written"]


def test_registration_places_the_rules_and_records_them(isolated_env):
    project_dir = _project(isolated_env, "fresh-proj")

    register_project(project_dir, infer=True)

    rules = project_dir / RULES
    assert rules.read_text() == SHIPPED
    assert _recorded("fresh-proj") == _sha(SHIPPED)
    # The record lives in the project's own ledger, so a clone knows it too.
    writes = _ledger_writes(project_dir)
    assert [(w["entity"]["id"], w["payload"]["sha256"]) for w in writes] == [
        (RULES, _sha(SHIPPED))
    ]


def test_a_rerun_with_nothing_to_do_records_nothing(isolated_env, capsys):
    project_dir = _project(isolated_env, "steady-proj")
    register_project(project_dir, infer=True)
    capsys.readouterr()

    register_project(project_dir, infer=True)

    assert len(_ledger_writes(project_dir)) == 1
    assert "Verification-suite rules" not in capsys.readouterr().out


def test_a_rerun_updates_what_init_last_wrote(isolated_env, capsys):
    """P1: the copy is exactly what init last wrote, so it is replaced."""
    project_dir = _project(isolated_env, "recorded-proj")
    register_project(project_dir, infer=True)
    older = "# The rules as an older Endless wrote them\n"
    (project_dir / RULES).write_text(older)
    event_bridge.emit_event(
        "shipped_file.written", project="recorded-proj",
        entity_type="shipped_file", entity_id=RULES,
        payload={"kind": "tasks-rules", "sha256": _sha(older)},
        actor_kind="system", project_root=str(project_dir),
    )
    capsys.readouterr()

    register_project(project_dir, infer=True)

    assert (project_dir / RULES).read_text() == SHIPPED
    assert _recorded("recorded-proj") == _sha(SHIPPED)
    assert "Verification-suite rules updated in" in capsys.readouterr().out


def test_a_rerun_without_a_record_updates_a_shipped_version(isolated_env, capsys):
    """P2: a copy from before the record existed is replaced only if Endless
    shipped exactly it."""
    project_dir = _project(isolated_env, "legacy-proj")
    (project_dir / ".endless" / "tasks").mkdir(parents=True)
    (project_dir / RULES).write_text(FIRST_SHIPPED)

    register_project(project_dir, infer=True)

    assert (project_dir / RULES).read_text() == SHIPPED
    assert _recorded("legacy-proj") == _sha(SHIPPED)
    assert "Verification-suite rules updated in" in capsys.readouterr().out


def test_a_rerun_leaves_an_edited_copy_alone_and_says_so(isolated_env, capsys):
    """P3, with a record: the copy is not what init last wrote."""
    project_dir = _project(isolated_env, "customized-proj")
    register_project(project_dir, infer=True)
    (project_dir / RULES).write_text("# Our own rules\n")
    capsys.readouterr()

    register_project(project_dir, infer=True)

    assert (project_dir / RULES).read_text() == "# Our own rules\n"
    assert _recorded("customized-proj") == _sha(SHIPPED)
    out = capsys.readouterr().out
    assert f"{RULES} was edited locally and not updated" in out


def test_without_a_record_an_edited_shipped_version_is_left_alone(isolated_env, capsys):
    """P3, without a record: one changed byte makes it not a shipped version."""
    project_dir = _project(isolated_env, "edited-legacy-proj")
    (project_dir / ".endless" / "tasks").mkdir(parents=True)
    edited = FIRST_SHIPPED + "\nOne line of our own.\n"
    (project_dir / RULES).write_text(edited)

    register_project(project_dir, infer=True)

    assert (project_dir / RULES).read_text() == edited
    assert _recorded("edited-legacy-proj") is None
    assert "was edited locally and not updated" in capsys.readouterr().out
