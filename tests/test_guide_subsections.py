"""`endless guide <section>/<sub>` reads a subsection.

A subsection is `docs/guide/<section>/<sub>.md`, one level deep, beside its
parent section's file. These tests drive the command over a temp guide tree,
so they hold whatever the real guide contains.
"""

import click
import pytest
from click.testing import CliRunner

from endless import agent_help, cli, guide_map

ON = "Gate on: run the report."
OFF = "Gate off: nothing to run."


@pytest.fixture
def guide_tree(tmp_path, monkeypatch):
    gdir = tmp_path / "guide"
    (gdir / "_help").mkdir(parents=True)
    (gdir / "_help" / "_topics.md").write_text("")
    (gdir / "index.md").write_text("# Index\n")
    (gdir / "tasks.md").write_text(
        "# Tasks\n\nParent body. See `endless guide tasks/post-land`.\n")
    (gdir / "orchestration.md").write_text("# Orchestration\n")
    (gdir / "tasks").mkdir()
    (gdir / "tasks" / "post-land.md").write_text(
        f"# After landing\n\n{{{{if .report_gate}}}}{ON}{{{{else}}}}{OFF}{{{{end}}}}\n")
    (gdir / "tasks" / "triage.md").write_text("# Triage\n")
    monkeypatch.setattr(guide_map, "GUIDE_DIR", gdir)
    monkeypatch.setattr(guide_map, "HELP_DIR", gdir / "_help")
    monkeypatch.setattr(guide_map, "INDEX_FILE", gdir / "index.md")
    monkeypatch.setattr(guide_map, "TOPICS_FILE", gdir / "_help" / "_topics.md")
    return gdir


def _guide(monkeypatch, *args, report_gate=True):
    monkeypatch.setattr(cli, "guide_conditions",
                        lambda: {"report_gate": report_gate, "self_dev": False})
    return CliRunner().invoke(cli.main, ["guide", *args])


@pytest.mark.parametrize("gate,shown,hidden", [(True, ON, OFF), (False, OFF, ON)],
                         ids=["gate-on", "gate-off"])
def test_subsection_renders_with_conditionals_resolved(
        guide_tree, monkeypatch, gate, shown, hidden):
    result = _guide(monkeypatch, "tasks/post-land", report_gate=gate)
    assert result.exit_code == 0, result.output
    assert "# After landing" in result.output
    assert shown in result.output
    assert hidden not in result.output
    assert "{{" not in result.output


def test_section_prints_only_its_own_file(guide_tree, monkeypatch):
    result = _guide(monkeypatch, "tasks")
    assert result.exit_code == 0, result.output
    assert "Parent body." in result.output
    assert "# After landing" not in result.output
    assert "# Triage" not in result.output


def test_list_shows_sections_then_their_subsections(guide_tree, monkeypatch):
    result = _guide(monkeypatch, "--list")
    assert result.exit_code == 0, result.output
    assert result.output.splitlines() == [
        "orchestration", "tasks", "tasks/post-land", "tasks/triage",
    ]


def test_unknown_subsection_names_the_real_ones(guide_tree, monkeypatch):
    result = _guide(monkeypatch, "tasks/nope")
    assert result.exit_code != 0
    assert "tasks/nope" in result.output
    assert "tasks/post-land, tasks/triage" in result.output


@pytest.mark.parametrize("address", [
    "tasks/post-land/deeper", "index", "index/x", "_help", "_help/guide",
    "nope/post-land",
])
def test_unreadable_addresses_are_refused(guide_tree, monkeypatch, address):
    result = _guide(monkeypatch, address)
    assert result.exit_code != 0
    assert "Unknown " in result.output
    assert "# After landing" not in result.output


def test_agent_banner_shows_the_subsection_address(guide_tree):
    (guide_tree / "_help" / "task.md").write_text(
        "section: tasks/post-land\ncovers: after landing\n")
    ctx = None
    for name in ("endless", "task", "show"):
        ctx = click.Context(click.Command(name), info_name=name, parent=ctx)
    assert "    endless guide tasks/post-land" in agent_help.agent_block(ctx)
