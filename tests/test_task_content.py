"""E-1531: a task's typed prose is task_content rows, one per content name.

What the Python half owns, and what this pins:

  - The vocabulary comes from Go (`endless-go task-content names`). Python holds
    no list of its own, so the mirror recognizer, the `task show` headings and
    `--all-fields` all follow a name added there.
  - `reason` and `notes` are content names like any other: a CLI flag spelled
    with the token, a heading from its label, a mirror named after it.
  - Clearing content deletes the row; there are no empty rows.
"""

import types

from click.testing import CliRunner

from endless import content_names, db, doc_mirror
from endless.cli import main


def _add_task(title: str = "Refactor the cache layer", status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _no_commit(monkeypatch):
    """Keep the mirror write, skip the endless-go commit shellout."""
    monkeypatch.setattr(
        doc_mirror, "shutil", types.SimpleNamespace(which=lambda _b: None))


def _update(*args: str):
    result = CliRunner().invoke(main, ["task", "update", *args])
    assert result.exit_code == 0, result.output
    return result.output


# ─── the vocabulary ───────────────────────────────────────────────────────────


def test_the_vocabulary_comes_from_go_in_display_order():
    assert content_names.slugs() == ("context", "analysis", "plan", "outcome", "reason", "notes")
    assert content_names.label("reason") == "Reason"


def test_mirror_kinds_follow_the_vocabulary():
    """One kind per content name, each mirrored under its own token."""
    kinds = doc_mirror.task_kinds()
    assert tuple(k.name for k in kinds) == content_names.slugs()
    assert all(k.stem == k.name for k in kinds)
    assert doc_mirror.kind_for("reason").legacy_dir == ""
    assert doc_mirror.kind_for("plan").legacy_dir == "plans"


def test_reason_and_notes_mirrors_are_recognized_and_verify_files_are_not():
    assert doc_mirror.is_mirror_path(".endless/tasks/e-7/reason.md")
    assert doc_mirror.is_mirror_path(".endless/tasks/e-7/notes.md")
    assert not doc_mirror.is_mirror_path(".endless/tasks/e-7/verify.sh")
    assert not doc_mirror.is_mirror_path(".endless/tasks/e-7/verify.toml")
    assert not doc_mirror.is_mirror_path(".endless/tasks/e-7/scratch.md")
    # A kind born after consolidation never had a legacy directory.
    assert not doc_mirror.is_mirror_path(".endless/reasons/E-7.md")


# ─── the flags ────────────────────────────────────────────────────────────────


def test_update_reason_and_notes_flags_write_content_rows(seeded_project_at_cwd, monkeypatch):
    _no_commit(monkeypatch)
    tid = _add_task()
    _update(f"E-{tid}", "--reason", "why it ended", "--notes", "a note")
    assert db.task_content(tid) == {"reason": "why it ended", "notes": "a note"}
    # Each is mirrored under its own name on the main checkout.
    root = seeded_project_at_cwd
    assert (root / doc_mirror.task_doc_path(tid, "reason")).read_text() == "why it ended"
    assert (root / doc_mirror.task_doc_path(tid, "notes")).read_text() == "a note"


def test_clear_deletes_the_row(seeded_project_at_cwd, monkeypatch):
    _no_commit(monkeypatch)
    tid = _add_task()
    _update(f"E-{tid}", "--reason", "why", "--plan", "a plan")
    _update(f"E-{tid}", "--clear", "reason")
    assert db.task_content(tid) == {"plan": "a plan"}
    assert db.scalar(
        "SELECT count(*) FROM task_content WHERE task_id = ? AND content = ''", (tid,)
    ) == 0


def test_status_obsolete_via_the_cli_stores_outcome_flag_as_reason(
    seeded_project_at_cwd, monkeypatch,
):
    _no_commit(monkeypatch)
    tid = _add_task()
    _update(f"E-{tid}", "--status", "obsolete", "--outcome", "overtaken")
    assert db.task_content(tid) == {"reason": "overtaken"}


# ─── the render ───────────────────────────────────────────────────────────────


def test_all_fields_renders_every_name_under_its_label(seeded_project_at_cwd):
    tid = _add_task()
    for name in content_names.slugs():
        db.execute(
            "INSERT INTO task_content (task_id, name, content) VALUES (?, ?, ?)",
            (tid, name, f"{name} body"),
        )
    out = CliRunner().invoke(main, ["task", "show", f"E-{tid}", "--all-fields"]).output
    positions = [out.find(f"— {content_names.label(n)} —") for n in content_names.slugs()]
    assert -1 not in positions, out
    assert positions == sorted(positions), "sections must follow display order"
    for name in content_names.slugs():
        assert f"{name} body" in out


def test_a_hidden_name_collapses_to_a_placeholder_naming_its_own_flag(seeded_project_at_cwd):
    tid = _add_task()
    db.execute(
        "INSERT INTO task_content (task_id, name, content) VALUES (?, 'notes', ?)",
        (tid, "n" * 12),
    )
    out = CliRunner().invoke(main, ["task", "show", f"E-{tid}"]).output
    assert "12 chars (--notes to display)" in out
    agent = CliRunner().invoke(main, ["task", "show", f"E-{tid}", "--agent"]).output
    assert "notes_chars=12" in agent
