"""Tests for the agent --help directive block (E-1502).

The directive points the agent at a guide section; it deliberately does NOT
repeat the section's `covers` summary (that lives in the index table), but it
does keep command-specific notes (which aren't in the guide).
"""

import click

from endless import agent_help, guide_map


def _ctx(path):
    """Build a click Context chain whose command_path equals `path`."""
    ctx = None
    for name in path.split(" "):
        ctx = click.Context(click.Command(name), info_name=name, parent=ctx)
    return ctx


def test_directive_omits_covers_keeps_note(monkeypatch):
    monkeypatch.setattr(agent_help, "load_map", lambda cp: guide_map.MapEntry(
        key=cp, sections=["orchestration"],
        covers="A long covers summary that belongs in the table",
        note="The handoff is generated; you do not author it.",
    ))
    out = agent_help.agent_block(_ctx("endless task spawn"))
    assert "endless guide orchestration" in out
    assert "A long covers summary" not in out                 # covers NOT in --help
    assert "The handoff is generated" in out                  # note kept


def test_directive_multiple_sections(monkeypatch):
    monkeypatch.setattr(agent_help, "load_map", lambda cp: guide_map.MapEntry(
        key=cp, sections=["tasks", "orchestration"], covers="x"))
    out = agent_help.agent_block(_ctx("endless task chat"))
    assert "endless guide tasks" in out
    assert "endless guide orchestration" in out


def test_gap_shows_reason(monkeypatch):
    monkeypatch.setattr(agent_help, "load_map", lambda cp: guide_map.MapEntry(
        key=cp, sections=[], gap="matchers aren't covered yet."))
    out = agent_help.agent_block(_ctx("endless phrase list"))
    assert "No guide section covers this yet" in out
    assert "matchers aren't covered yet." in out


def test_unmapped_command(monkeypatch):
    monkeypatch.setattr(agent_help, "load_map", lambda cp: None)
    out = agent_help.agent_block(_ctx("endless mystery"))
    assert "No guide section is mapped" in out


def test_root_has_no_block():
    assert agent_help.agent_block(_ctx("endless")) is None
