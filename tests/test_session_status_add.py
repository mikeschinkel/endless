"""Tests for `endless session status add` XML parsing + validation
(E-1312 / E-1314 schema: <tasks> flat container; <summary>).

The Python layer's job is parse + validate + payload-build. End-to-end
flow through endless-event + the Go handler is tested separately on the
Go side; here we focus on the Python contract.
"""

import pytest
import click

from endless.session_status_cmd import (
    _parse_and_validate,
    _read_input,
    _resolve_process,
)
from pathlib import Path


# --- Happy path ------------------------------------------------------------

def test_parses_full_schema():
    xml = """
    <session-status>
      <headline>One-line summary.</headline>
      <tasks>
        <task id="E-1208" status="confirmed">verbs.jsonl write-time</task>
        <task id="E-1206" status="confirmed" filed="true">db-ledger write-time</task>
        <task id="E-1302" status="unplanned" filed="true">endless task id CLI</task>
        <task id="E-9999" status="blocked">waiting on something</task>
        <task id="E-1312" status="unverified">awaiting confirm</task>
      </tasks>
      <decisions>
        <decision>chose XML over markdown for deterministic parsing</decision>
        <decision>kept filed as an attribute rather than a separate section</decision>
      </decisions>
      <commits>
        <commit sha="1e3bbfc">ledger split 1264 -> 500/500/264</commit>
      </commits>
      <memory>
        <entry path="feedback_no_autonomous_remediation.md">report and ask on partial fail</entry>
      </memory>
      <summary>
        <layer name="Schema" files="internal/monitor/migrate.go">V8 migration</layer>
      </summary>
      <notes>free-form prose.</notes>
    </session-status>
    """
    p = _parse_and_validate(xml)
    assert p["headline"] == "One-line summary."
    for tid in ("E-1208", "E-1206", "E-1302", "E-9999", "E-1312"):
        assert tid in p["tasks"], f"missing {tid} in tasks column"
    assert 'filed="true"' in p["tasks"]
    assert "chose XML" in p["decisions"]
    assert "1e3bbfc" in p["commits"]
    assert "feedback_no_autonomous_remediation.md" in p["memory"]
    assert "Schema" in p["summary"]
    assert p["notes"] == "free-form prose."


def test_each_task_serialized_one_per_line():
    xml = """
    <session-status>
      <tasks>
        <task id="E-1" status="confirmed">a</task>
        <task id="E-2" status="confirmed">b</task>
        <task id="E-3" status="confirmed">c</task>
      </tasks>
    </session-status>
    """
    p = _parse_and_validate(xml)
    assert p["tasks"].count("\n") == 2  # 3 tasks -> 2 newlines
    for tid in ("E-1", "E-2", "E-3"):
        assert tid in p["tasks"]


def test_missing_sections_become_empty_string():
    xml = """
    <session-status>
      <headline>Only a headline.</headline>
    </session-status>
    """
    p = _parse_and_validate(xml)
    assert p["headline"] == "Only a headline."
    for section in ("tasks", "decisions", "commits", "memory",
                    "summary", "notes"):
        assert p[section] == ""


# --- Validation errors -----------------------------------------------------

def test_rejects_missing_root():
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate("<wrong-root/>")
    assert "session-status" in exc.value.message


def test_rejects_malformed_xml():
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate("<session-status><unclosed></session-status>")
    assert "malformed XML" in exc.value.message


def test_rejects_unknown_top_level_element():
    xml = """
    <session-status>
      <decisions/>
      <random-tag/>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "unknown element" in exc.value.message
    assert "random-tag" in exc.value.message


def test_rejects_old_resolved_element():
    """E-1314: <resolved>/<pending>/<blocked>/<verify> are no longer
    top-level; tasks live under <tasks>. The old shape is rejected."""
    xml = """
    <session-status>
      <resolved>
        <task id="E-1" status="confirmed">x</task>
      </resolved>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "unknown element" in exc.value.message
    assert "resolved" in exc.value.message


def test_rejects_invalid_task_id():
    xml = """
    <session-status>
      <tasks>
        <task id="not-an-id" status="confirmed">x</task>
      </tasks>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "must match E-NNN" in exc.value.message


def test_rejects_invalid_status():
    xml = """
    <session-status>
      <tasks>
        <task id="E-1" status="not-a-status">x</task>
      </tasks>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "invalid status" in exc.value.message


def test_rejects_invalid_filed_value():
    xml = """
    <session-status>
      <tasks>
        <task id="E-1" status="confirmed" filed="yes">x</task>
      </tasks>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "filed must be" in exc.value.message


def test_rejects_non_task_inside_tasks_container():
    xml = """
    <session-status>
      <tasks>
        <not-task id="E-1" status="confirmed">x</not-task>
      </tasks>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "only <task> elements allowed" in exc.value.message


def test_rejects_invalid_commit_sha():
    xml = """
    <session-status>
      <commits>
        <commit sha="too-short">desc</commit>
      </commits>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "must match" in exc.value.message


def test_rejects_memory_entry_missing_path():
    xml = """
    <session-status>
      <memory>
        <entry>missing path attr</entry>
      </memory>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "requires a path attribute" in exc.value.message


def test_rejects_summary_layer_missing_name():
    xml = """
    <session-status>
      <summary>
        <layer files="a, b">purpose</layer>
      </summary>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "requires a name attribute" in exc.value.message


def test_rejects_summary_layer_missing_files():
    xml = """
    <session-status>
      <summary>
        <layer name="Schema">purpose</layer>
      </summary>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "requires a files attribute" in exc.value.message


def test_rejects_non_layer_inside_summary():
    xml = """
    <session-status>
      <summary>
        <not-layer name="x" files="y">z</not-layer>
      </summary>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "only <layer> elements allowed" in exc.value.message


# --- Input handling --------------------------------------------------------

def test_read_input_from_file(tmp_path):
    p = tmp_path / "status.xml"
    p.write_text("<session-status><headline>x</headline></session-status>")
    text = _read_input(str(p))
    assert "<headline>x</headline>" in text


def test_read_input_empty_errors(monkeypatch):
    import io
    monkeypatch.setattr("sys.stdin", io.StringIO(""))
    with pytest.raises(click.ClickException) as exc:
        _read_input(None)
    assert "empty input" in exc.value.message


# --- Filed attribute on <task> --------------------------------------------

def test_filed_attribute_preserved_in_serialization():
    xml = """
    <session-status>
      <tasks>
        <task id="E-1" status="confirmed" filed="true">recently filed</task>
        <task id="E-2" status="confirmed">already there</task>
      </tasks>
    </session-status>
    """
    p = _parse_and_validate(xml)
    lines = p["tasks"].split("\n")
    # Order preserved; filed attr survives on the first task only.
    assert 'filed="true"' in lines[0]
    assert "filed=" not in lines[1]


# --- Process resolution (E-1588) ------------------------------------------

def test_resolve_process_explicit_override():
    # An explicit --session-id produces the sentinel directly, regardless
    # of env / resolver.
    assert _resolve_process(42) == "__session_id=42"


def test_resolve_process_uses_resolver(monkeypatch):
    monkeypatch.setattr(
        "endless.session_status_cmd._current_endless_session_id",
        lambda: 7,
    )
    assert _resolve_process(None) == "__session_id=7"


def test_resolve_process_falls_back_to_pane(monkeypatch):
    # Resolver returns None → fall back to the raw TMUX_PANE so Go's
    # pane lookup still runs and emits its clear error.
    monkeypatch.setattr(
        "endless.session_status_cmd._current_endless_session_id",
        lambda: None,
    )
    monkeypatch.setenv("TMUX_PANE", "%88")
    assert _resolve_process(None) == "%88"


def test_resolve_process_no_session_no_pane(monkeypatch):
    monkeypatch.setattr(
        "endless.session_status_cmd._current_endless_session_id",
        lambda: None,
    )
    monkeypatch.delenv("TMUX_PANE", raising=False)
    assert _resolve_process(None) == ""
