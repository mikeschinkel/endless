"""Tests for the verb-gate redesign (E-1106)."""

import json

import click
import pytest

from endless import phrase_cmd, task_cmd, matchers


def test_verb_gate_human_form_omits_force_and_alternatives(monkeypatch):
    monkeypatch.delenv("CLAUDECODE", raising=False)

    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_title("nonverbword some title")

    msg = exc.value.message
    assert "Common verbs:" not in msg
    assert "--force" not in msg
    assert "endless phrase add verb 'nonverbword'" in msg
    assert "--definition" in msg


def test_verb_gate_agent_form_includes_binary_and_anti_rationalization(monkeypatch):
    monkeypatch.setenv("CLAUDECODE", "1")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_title("nonverbword some title")

    msg = exc.value.message
    assert "IF YES:" in msg
    assert "IF NO:" in msg
    assert "Do not register a non-verb" in msg
    assert "Common verbs:" not in msg
    assert "--force" not in msg


def test_verb_gate_passes_for_registered_verb(monkeypatch):
    # Default seeds include 'add' as a verb.
    monkeypatch.delenv("CLAUDECODE", raising=False)
    task_cmd.validate_title("Add a feature")  # no exception


def test_phrase_add_verb_requires_definition():
    with pytest.raises(click.ClickException) as exc:
        phrase_cmd.add_phrase(
            type_="verb", value="ponder",
            scope=None, method=None, case_sensitive=False,
            machine_only=True, definition=None,
        )
    assert "requires --definition" in exc.value.message


def test_phrase_add_verb_with_blank_definition_rejected():
    with pytest.raises(click.ClickException):
        phrase_cmd.add_phrase(
            type_="verb", value="ponder",
            scope=None, method=None, case_sensitive=False,
            machine_only=True, definition="   ",
        )


def test_phrase_add_verb_with_definition_persists(isolated_env):
    phrase_cmd.add_phrase(
        type_="verb", value="ponder",
        scope=None, method=None, case_sensitive=False,
        machine_only=True, definition="to deliberate over",
    )

    cfg = json.loads((isolated_env["config_dir"] / "config.json").read_text())
    verb_matchers = [m for m in cfg.get("matchers", [])
                     if m.get("type") == "verb" and "ponder" in (m.get("match") or [])]
    assert verb_matchers, "verb 'ponder' should be persisted"
    defs = verb_matchers[0].get("definitions") or {}
    assert defs.get("ponder") == "to deliberate over"


def test_phrase_add_non_verb_does_not_require_definition(isolated_env):
    phrase_cmd.add_phrase(
        type_="pivot", value="some-pivot",
        scope=None, method=None, case_sensitive=False,
        machine_only=True, definition=None,
    )
    # No exception = pass.


def test_existing_verb_without_definition_still_validates(isolated_env, monkeypatch):
    """Backward compat: verbs registered before this change have no
    `definitions` field. They must still satisfy validate_title."""
    monkeypatch.delenv("CLAUDECODE", raising=False)
    matchers.add_match_value(
        type_="verb", value="ponder",
        method="exact", machine_only=True,
        # No definition argument — simulates a pre-E-1106 entry.
    )
    task_cmd.validate_title("Ponder the question")  # no exception
