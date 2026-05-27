"""Tests for the guide-map primitives (E-1502).

The semantic mapping (which section explains a command) is authored by the
/regenerate-guide slash command; these tests cover the deterministic machinery
around it: command-path filenames, map-file parsing, inheritance, coverage
validation, and index-block assembly. One live test guards that the committed
mapping stays complete as commands are added.
"""

import pytest

from endless import guide_map


@pytest.fixture
def guide_tree(tmp_path, monkeypatch):
    """Redirect guide_map at a temp guide/ + help/ tree."""
    gdir = tmp_path / "guide"
    hdir = gdir / "help"
    hdir.mkdir(parents=True)
    (gdir / "tasks.md").write_text("# Tasks\n\n## Adding tasks\n\n## Viewing tasks\n")
    (gdir / "orchestration.md").write_text("# Orchestration\n\n## Worktrees\n")
    (gdir / "index.md").write_text(
        f"intro text\n\n{guide_map.BEGIN_MARKER}\n{guide_map.END_MARKER}\n\nfooter\n"
    )
    monkeypatch.setattr(guide_map, "GUIDE_DIR", gdir)
    monkeypatch.setattr(guide_map, "HELP_DIR", hdir)
    monkeypatch.setattr(guide_map, "INDEX_FILE", gdir / "index.md")
    monkeypatch.setattr(guide_map, "TOPICS_FILE", hdir / "_topics.md")
    return hdir


def _write(hdir, stem, text):
    (hdir / f"{stem}.md").write_text(text)


# --------------------------------------------------------------------------- #
# filenames and parsing
# --------------------------------------------------------------------------- #

def test_command_path_to_filename():
    assert guide_map.command_path_to_filename("task spawn") == "task-spawn"
    assert guide_map.command_path_to_filename("shell-init") == "shell-init"
    assert guide_map.command_path_to_filename("task clear tier") == "task-clear-tier"


def test_parse_entry_section_covers_note():
    p = guide_map._parse_entry("section: orchestration\ncovers: A thing\n\nA note line.\n")
    assert p.sections == ["orchestration"]
    assert p.covers == "A thing"
    assert p.note == "A note line."
    assert p.gap == ""


def test_parse_entry_multi_section_and_gap():
    p = guide_map._parse_entry("section: tasks, orchestration\ncovers: X\n")
    assert p.sections == ["tasks", "orchestration"]
    g = guide_map._parse_entry("gap: not covered yet.\n")
    assert g.gap == "not covered yet." and g.sections == []


def test_guide_sections_headers(guide_tree):
    secs = guide_map.guide_sections()
    assert set(secs) == {"tasks", "orchestration"}  # index excluded
    assert secs["tasks"] == ["Adding tasks", "Viewing tasks"]


# --------------------------------------------------------------------------- #
# inheritance
# --------------------------------------------------------------------------- #

def test_load_map_inheritance(guide_tree):
    _write(guide_tree, "task", "section: tasks\ncovers: task stuff\n")
    _write(guide_tree, "task-spawn", "section: orchestration\ncovers: spawning\n")

    own = guide_map.load_map("task spawn")
    assert own.sections == ["orchestration"] and own.inherited_from == ""

    inherited = guide_map.load_map("task add")
    assert inherited.sections == ["tasks"] and inherited.inherited_from == "task"

    deep = guide_map.load_map("task clear tier")
    assert deep.sections == ["tasks"] and deep.inherited_from == "task"

    assert guide_map.load_map("nonexistent cmd") is None


# --------------------------------------------------------------------------- #
# validation
# --------------------------------------------------------------------------- #

def test_validate_clean(guide_tree, monkeypatch):
    monkeypatch.setattr(guide_map, "walk_commands", lambda: ["task", "task add", "worktree"])
    _write(guide_tree, "task", "section: tasks\ncovers: task stuff\n")
    _write(guide_tree, "worktree", "section: orchestration\ncovers: worktrees\n")
    guide_map.update_index_block()
    rep = guide_map.validate()
    assert rep.ok(), rep.render()


def test_validate_missing_command(guide_tree, monkeypatch):
    monkeypatch.setattr(guide_map, "walk_commands", lambda: ["task", "loner"])
    _write(guide_tree, "task", "section: tasks\ncovers: x\n")
    rep = guide_map.validate()
    assert "loner" in rep.missing
    assert not rep.ok()


def test_validate_gap_is_ok_but_reported(guide_tree, monkeypatch):
    monkeypatch.setattr(guide_map, "walk_commands", lambda: ["phrase", "phrase list"])
    _write(guide_tree, "phrase", "gap: matchers not covered yet.\n")
    guide_map.update_index_block()
    rep = guide_map.validate()
    assert rep.ok(), rep.render()          # gap does not fail the gate
    assert "phrase" in rep.gaps            # but is surfaced
    assert "phrase list" not in rep.gaps   # inherited gap is not double-counted


def test_validate_bad_section_and_missing_fields(guide_tree, monkeypatch):
    monkeypatch.setattr(guide_map, "walk_commands", lambda: ["a", "b"])
    _write(guide_tree, "a", "section: nope\ncovers: x\n")   # bad slug
    _write(guide_tree, "b", "covers: only covers\n")        # no section, no gap
    rep = guide_map.validate()
    assert any("nope" in s for s in rep.bad_section)
    assert "b" in rep.missing_fields
    assert not rep.ok()


def test_validate_orphan_file(guide_tree, monkeypatch):
    monkeypatch.setattr(guide_map, "walk_commands", lambda: ["task"])
    _write(guide_tree, "task", "section: tasks\ncovers: x\n")
    _write(guide_tree, "ghost", "section: tasks\ncovers: removed command\n")
    rep = guide_map.validate()
    assert "ghost" in rep.orphan_files
    assert not rep.ok()


# --------------------------------------------------------------------------- #
# index block
# --------------------------------------------------------------------------- #

def test_index_block_idempotent_and_stale_detection(guide_tree, monkeypatch):
    monkeypatch.setattr(guide_map, "walk_commands", lambda: ["task"])
    _write(guide_tree, "task", "section: tasks\ncovers: task stuff\n")

    assert guide_map.update_index_block() is True       # first write changes
    assert guide_map.update_index_block() is False      # idempotent
    assert guide_map._index_in_sync() is True

    block = guide_map._current_index_block(guide_map.INDEX_FILE.read_text())
    assert "`endless task`" in block
    assert "footer" in guide_map.INDEX_FILE.read_text()  # surrounding text preserved

    _write(guide_tree, "task", "section: orchestration\ncovers: changed\n")
    assert guide_map._index_in_sync() is False           # drift detected


def test_topics_feed_index(guide_tree, monkeypatch):
    monkeypatch.setattr(guide_map, "walk_commands", lambda: [])
    guide_map.TOPICS_FILE.write_text(
        "topic: who am I\nsection: sessions\ncovers: current session\n"
    )
    block = guide_map.assemble_index_block()
    assert "_topic:_ who am I" in block


# --------------------------------------------------------------------------- #
# live regression gate
# --------------------------------------------------------------------------- #

def test_live_mapping_is_complete():
    """The committed map must cover every real command (the pre-land gate).

    Fails when a command is added without a docs/guide/help/ file, or a map
    file points at a renamed/removed section. Acknowledged gaps are allowed.
    """
    rep = guide_map.validate()
    assert rep.ok(), "guide map drift:\n" + rep.render()
