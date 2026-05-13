"""Tests for `endless session status add` XML parsing + validation (E-1312).

The Python layer's job is parse + validate + payload-build. End-to-end
flow through endless-event + the Go handler is tested separately on the
Go side; here we focus on the Python contract.
"""

import pytest
import click

from endless.session_status_cmd import _parse_and_validate, _read_input
from pathlib import Path


# --- Happy path ------------------------------------------------------------

def test_parses_full_schema():
    xml = """
    <session-status>
      <headline>One-line summary.</headline>
      <resolved>
        <task id="E-1208" status="confirmed">verbs.jsonl write-time</task>
        <task id="E-1206" status="confirmed" filed="true">db-ledger write-time</task>
      </resolved>
      <pending>
        <task id="E-1302" status="needs_plan" filed="true">endless task id CLI</task>
      </pending>
      <blocked>
        <task id="E-9999" status="blocked">waiting on something</task>
      </blocked>
      <verify>
        <task id="E-1312" status="verify">awaiting confirm</task>
      </verify>
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
      <notes>free-form prose.</notes>
    </session-status>
    """
    p = _parse_and_validate(xml)
    assert p["headline"] == "One-line summary."
    assert "E-1208" in p["resolved"]
    assert "E-1206" in p["resolved"]
    assert 'filed="true"' in p["resolved"]
    assert "E-1302" in p["pending"]
    assert "E-9999" in p["blocked"]
    assert "E-1312" in p["verify"]
    assert "chose XML" in p["decisions"]
    assert "1e3bbfc" in p["commits"]
    assert "feedback_no_autonomous_remediation.md" in p["memory"]
    assert p["notes"] == "free-form prose."


def test_each_task_serialized_one_per_line():
    xml = """
    <session-status>
      <resolved>
        <task id="E-1" status="confirmed">a</task>
        <task id="E-2" status="confirmed">b</task>
        <task id="E-3" status="confirmed">c</task>
      </resolved>
    </session-status>
    """
    p = _parse_and_validate(xml)
    assert p["resolved"].count("\n") == 2  # 3 tasks → 2 newlines between
    for tid in ("E-1", "E-2", "E-3"):
        assert tid in p["resolved"]


def test_missing_sections_become_empty_string():
    xml = """
    <session-status>
      <headline>Only a headline.</headline>
    </session-status>
    """
    p = _parse_and_validate(xml)
    assert p["headline"] == "Only a headline."
    for section in ("resolved", "pending", "blocked", "verify",
                    "decisions", "commits", "memory", "notes"):
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


def test_rejects_invalid_task_id():
    xml = """
    <session-status>
      <resolved>
        <task id="not-an-id" status="confirmed">x</task>
      </resolved>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "must match E-NNN" in exc.value.message


def test_rejects_invalid_status():
    xml = """
    <session-status>
      <resolved>
        <task id="E-1" status="not-a-status">x</task>
      </resolved>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "invalid status" in exc.value.message


def test_rejects_invalid_filed_value():
    xml = """
    <session-status>
      <resolved>
        <task id="E-1" status="confirmed" filed="yes">x</task>
      </resolved>
    </session-status>
    """
    with pytest.raises(click.ClickException) as exc:
        _parse_and_validate(xml)
    assert "filed must be" in exc.value.message


def test_rejects_non_task_inside_task_section():
    xml = """
    <session-status>
      <resolved>
        <not-task id="E-1" status="confirmed">x</not-task>
      </resolved>
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
      <resolved>
        <task id="E-1" status="confirmed" filed="true">recently filed</task>
        <task id="E-2" status="confirmed">already there</task>
      </resolved>
    </session-status>
    """
    p = _parse_and_validate(xml)
    lines = p["resolved"].split("\n")
    # Order preserved; filed attr survives on the first task only.
    assert 'filed="true"' in lines[0]
    assert "filed=" not in lines[1]
