"""Tests for the verb-gate redesign (E-1106), verb storage (E-1117),
and the verbs.json file split (E-1124)."""

import json

import click
import pytest

from endless import task_cmd, matchers, verb_cmd


def test_verb_gate_human_form_omits_force_and_alternatives(monkeypatch):
    monkeypatch.delenv("CLAUDECODE", raising=False)

    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_title("nonverbword some title")

    msg = exc.value.message
    assert "Common verbs:" not in msg
    assert "--force" not in msg
    assert "endless verb add 'nonverbword'" in msg


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


def test_verb_gate_passes_for_registered_seed_verb(monkeypatch):
    monkeypatch.delenv("CLAUDECODE", raising=False)
    task_cmd.validate_title("Add a feature")  # 'add' is in DEFAULT_VERBS


def test_verb_add_requires_definition(isolated_env):
    with pytest.raises(click.ClickException) as exc:
        verb_cmd.add_verb("ponder", None, machine_only=True)
    assert "requires --definition" in exc.value.message


def test_verb_add_with_blank_definition_rejected(isolated_env):
    with pytest.raises(click.ClickException):
        verb_cmd.add_verb("ponder", "   ", machine_only=True)


def test_verb_add_persists_in_verbs_file(isolated_env):
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=True)
    verbs_path = isolated_env["config_dir"] / "verbs.json"
    assert verbs_path.exists(), "verbs.json must be created (E-1124)"
    verbs = json.loads(verbs_path.read_text())
    assert isinstance(verbs, list), "verbs.json must be a top-level array"
    entry = next((v for v in verbs if v.get("value") == "ponder"), None)
    assert entry is not None, "verb should be persisted in verbs.json"
    assert entry.get("definition") == "to deliberate over"
    cfg = json.loads((isolated_env["config_dir"] / "config.json").read_text())
    assert "verbs" not in cfg, "config.json must not contain 'verbs' key"
    matcher_verbs = [m for m in cfg.get("matchers", []) if m.get("type") == "verb"]
    assert not matcher_verbs, "config.json must not contain verb matchers"


def test_phrase_add_rejects_type_verb(isolated_env):
    """phrase add no longer handles verb type; verb add is the path."""
    from endless import phrase_cmd
    with pytest.raises(click.ClickException) as exc:
        phrase_cmd.add_phrase(
            type_="verb", value="ponder",
            scope=None, method=None, case_sensitive=False,
            machine_only=True,
        )
    assert "endless verb add" in exc.value.message


def test_phrase_add_pivot_still_works(isolated_env):
    from endless import phrase_cmd
    phrase_cmd.add_phrase(
        type_="pivot", value="testpivot",
        scope=None, method=None, case_sensitive=False,
        machine_only=True,
    )


def test_legacy_verb_matcher_migrated_to_verbs_file(isolated_env, monkeypatch):
    """A pre-E-1117 config with type=verb matcher (with the bad 'definitions'
    field from E-1108) is migrated into the top-level verbs.json file on
    first load."""
    cfg_path = isolated_env["config_dir"] / "config.json"
    verbs_path = isolated_env["config_dir"] / "verbs.json"
    legacy = json.loads(cfg_path.read_text())
    legacy["matchers"] = [
        {
            "type": "verb", "method": "exact",
            "match": ["ponder", "deliberate"],
            "definitions": {"ponder": "to deliberate over"},
        },
        {
            "type": "pivot", "method": "substring",
            "match": ["actually"],
        },
    ]
    legacy.pop("verbs", None)
    cfg_path.write_text(json.dumps(legacy))
    if verbs_path.exists():
        verbs_path.unlink()

    monkeypatch.delenv("CLAUDECODE", raising=False)
    task_cmd.validate_title("Ponder the question")

    cfg_after = json.loads(cfg_path.read_text())
    assert "verbs" not in cfg_after, "verbs key must be stripped from config.json"
    matcher_verbs = [m for m in cfg_after["matchers"] if m.get("type") == "verb"]
    assert not matcher_verbs, "verb matcher must be removed from config.json"
    pivots = [m for m in cfg_after["matchers"] if m.get("type") == "pivot"]
    assert pivots, "non-verb matchers must remain in config.json"

    assert verbs_path.exists(), "verbs.json must be created"
    verbs = json.loads(verbs_path.read_text())
    values = {v.get("value") for v in verbs}
    assert {"ponder", "deliberate"}.issubset(values)
    ponder = next(v for v in verbs if v.get("value") == "ponder")
    assert ponder.get("definition") == "to deliberate over"


def test_inline_verbs_key_migrated_to_verbs_file(isolated_env, monkeypatch):
    """A post-E-1117/pre-E-1124 config with a top-level 'verbs' array key in
    config.json is migrated to the verbs.json file."""
    cfg_path = isolated_env["config_dir"] / "config.json"
    verbs_path = isolated_env["config_dir"] / "verbs.json"
    cfg = json.loads(cfg_path.read_text())
    cfg["verbs"] = [
        {"value": "ponder", "definition": "to deliberate over"},
        {"value": "deliberate"},  # no definition
    ]
    cfg_path.write_text(json.dumps(cfg))
    if verbs_path.exists():
        verbs_path.unlink()

    matchers.load_all_matchers()  # triggers migration

    cfg_after = json.loads(cfg_path.read_text())
    assert "verbs" not in cfg_after, "verbs key must be stripped from config.json"

    assert verbs_path.exists(), "verbs.json must be created"
    verbs = json.loads(verbs_path.read_text())
    values = {v.get("value") for v in verbs}
    assert {"ponder", "deliberate"}.issubset(values)
    ponder = next(v for v in verbs if v.get("value") == "ponder")
    assert ponder.get("definition") == "to deliberate over"


def test_default_seed_creates_verbs_file(isolated_env):
    """First-run seeding writes DEFAULT_VERBS to verbs.json (not into config.json)."""
    verbs_path = isolated_env["config_dir"] / "verbs.json"
    if verbs_path.exists():
        verbs_path.unlink()

    matchers.load_all_matchers()  # triggers _ensure_default_seeds

    assert verbs_path.exists(), "verbs.json must be created on first seed"
    verbs = json.loads(verbs_path.read_text())
    values = {v.get("value") for v in verbs}
    assert "add" in values
    assert "fix" in values
    cfg = json.loads((isolated_env["config_dir"] / "config.json").read_text())
    assert "verbs" not in cfg, "verbs must NOT be seeded into config.json"


def test_verb_remove(isolated_env):
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=True)
    pr, mr = matchers.remove_verb(value="ponder", machine_only=True)
    assert mr == 1
    verbs_path = isolated_env["config_dir"] / "verbs.json"
    verbs = json.loads(verbs_path.read_text())
    values = {v.get("value") for v in verbs}
    assert "ponder" not in values


def test_add_match_value_rejects_verb_type(isolated_env):
    with pytest.raises(ValueError):
        matchers.add_match_value(type_="verb", value="ponder", method="exact")
