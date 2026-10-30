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
import subprocess
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


def test_a_current_copy_with_a_stale_record_is_recorded_again(isolated_env):
    """P12: a copy already current whose record says otherwise gets its record
    corrected, or the next shipped version would read it as edited."""
    project_dir = _project(isolated_env, "stale-record-proj")
    register_project(project_dir, infer=True)
    event_bridge.emit_event(
        "shipped_file.written", project="stale-record-proj",
        entity_type="shipped_file", entity_id=RULES,
        payload={"kind": "tasks-rules", "sha256": _sha(FIRST_SHIPPED)},
        actor_kind="system", project_root=str(project_dir),
    )

    register_project(project_dir, infer=True)

    assert _recorded("stale-record-proj") == _sha(SHIPPED)


def _git(path: Path, *args: str) -> str:
    return subprocess.run(
        ["git", "-c", "user.email=test@example.com", "-c", "user.name=Test",
         *args], cwd=str(path), check=True, capture_output=True, text=True,
    ).stdout


def _worktree(project_dir: Path, name: str, rules: str | None) -> Path:
    """A task worktree whose branch committed `rules` as its copy."""
    wt = project_dir / ".endless" / "worktrees" / name
    _git(project_dir, "worktree", "add", "-q", "-b", f"task/{name}", str(wt))
    if rules is not None:
        (wt / RULES).write_text(rules)
        _git(wt, "commit", "-q", "-am", f"{name}: its own copy")
    return wt


def test_init_brings_each_worktree_up_to_the_rules(isolated_env, capsys):
    """P11: a rules change applies to work in flight now, committed on each
    worktree's branch; the land's rebase then drops that commit."""
    project_dir = _project(isolated_env, "wt-proj")
    (project_dir / ".endless" / "tasks").mkdir(parents=True)
    (project_dir / RULES).write_text(FIRST_SHIPPED)
    _git(project_dir, "add", RULES)
    _git(project_dir, "commit", "-q", "-m", "the rules an older Endless shipped")
    old = _worktree(project_dir, "old", None)
    (old / "work.txt").write_text("task work\n")
    _git(old, "add", "work.txt")
    _git(old, "commit", "-q", "-m", "old: task work")
    edited = _worktree(project_dir, "edited", "# Our own rules\n")
    current = _worktree(project_dir, "current", SHIPPED)
    detached = project_dir / ".endless" / "worktrees" / "detached"
    _git(project_dir, "worktree", "add", "-q", "--detach", str(detached))
    heads = {wt: _git(wt, "rev-parse", "HEAD") for wt in (edited, current)}

    register_project(project_dir, infer=True)

    out = capsys.readouterr().out
    assert (project_dir / RULES).read_text() == SHIPPED
    assert (old / RULES).read_text() == SHIPPED
    assert _git(old, "status", "--porcelain") == ""
    assert _git(old, "log", "-1", "--format=%s").strip() == (
        "Endless: update the verification-suite rules")
    assert "committed on task/old" in out
    assert (edited / RULES).read_text() == "# Our own rules\n"
    assert "was edited on task/edited and not updated" in out
    assert (detached / RULES).read_text() == FIRST_SHIPPED
    assert "it is not on a branch" in out
    for wt, head in heads.items():
        assert _git(wt, "rev-parse", "HEAD") == head
    # Nothing in the worktrees is recorded: the record is the default branch's.
    assert len(_ledger_writes(project_dir)) == 1

    # Land rebases the branch onto the default branch, which has the same file.
    _git(old, "rebase", "-q", "main")
    assert _git(old, "log", "--format=%s", "main..HEAD").splitlines() == [
        "old: task work"]
