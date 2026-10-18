"""E-2284: `endless project set` writes registered settings, taking the field
list and accepted values from `endless-go settings list --json` instead of a
list of its own."""

import json
from pathlib import Path

import click
import pytest

from endless import settings_export
from endless.set_cmd import set_field


def test_export_comes_from_the_go_registry(tmp_path):
    """The real binary answers, and the opt-ins it registers are there with
    their doctor metadata."""
    (tmp_path / ".endless").mkdir()
    (tmp_path / ".endless" / "config.json").write_text(
        json.dumps({"main_sync": {"enabled": "on"}}))
    doc = settings_export.export(tmp_path)
    reg = settings_export.by_id(doc)
    ms = reg["main_sync.enabled"]
    assert ms["kind"] == "optin" and ms["files"] == "project"
    assert ms["suggestable"] and ms["rationale"]
    assert ms["value"] == "on" and ms["source"] == "project"
    assert reg["auto_spawn.interval"]["files"] == "machine"


def test_export_relays_a_legacy_boolean(tmp_path):
    (tmp_path / ".endless").mkdir()
    (tmp_path / ".endless" / "config.json").write_text(
        json.dumps({"prime": {"enabled": True}}))
    with pytest.raises(click.ClickException) as exc:
        settings_export.export(tmp_path)
    assert "prime.enabled" in str(exc.value)


@pytest.mark.parametrize("setting,value,want", [
    ({"kind": "optin"}, "on", "on"),
    ({"kind": "optin"}, "unset", "unset"),
    ({"kind": "optin"}, "true", None),
    ({"kind": "int"}, "4", 4),
    ({"kind": "int"}, "four", None),
    ({"kind": "duration"}, "1h30m", "1h30m"),
    ({"kind": "duration"}, "soon", None),
    ({"kind": "choice", "choices": ["a", "b"]}, "b", "b"),
    ({"kind": "choice", "choices": ["a", "b"]}, "c", None),
    ({"kind": "path"}, "/tmp/endless-501", "/tmp/endless-501"),
    ({"kind": "path"}, "~/sock", "~/sock"),
    ({"kind": "path"}, "~", "~"),
    ({"kind": "path"}, "sock", None),
    ({"kind": "path"}, "~other/sock", None),
    ({"kind": "name"}, "endless-tasks", "endless-tasks"),
    ({"kind": "name"}, "", None),
    ({"kind": "name"}, "a/b", None),
    ({"kind": "name"}, "a b", None),
    ({"kind": "name"}, "a:b", None),
    ({"kind": "name"}, "a.b", None),
    ({"kind": "command"}, "ghostty -e {cmd}", "ghostty -e {cmd}"),
    ({"kind": "command"}, "", ""),
    ({"kind": "command"}, "ghostty -e", None),
])
def test_parse(setting, value, want):
    assert settings_export.parse(setting, value) == want


@pytest.mark.parametrize("kind", ["path", "name", "command"])
def test_accepted_describes_the_shaped_kinds(kind):
    assert settings_export.accepted({"kind": kind}) != "a value"


_REGISTRY = {
    "machine_file": "/home/u/.config/endless/config.json",
    "settings": [
        {"id": "main_sync.enabled", "kind": "optin", "files": "project"},
        {"id": "prime.enabled", "kind": "optin", "files": "project"},
        {"id": "prime.cap", "kind": "int", "files": "project"},
        {"id": "main_sync.interval", "kind": "duration", "files": "machine"},
    ],
}


@pytest.fixture
def project(tmp_path, monkeypatch):
    """A registered project `demo` whose config file is under tmp_path, and a
    registry export that does not need the binary."""
    cfg = tmp_path / ".endless" / "config.json"
    cfg.parent.mkdir()
    cfg.write_text(json.dumps({"name": "demo", "prime": {"enabled": "on"}}))
    monkeypatch.setattr(settings_export, "export", lambda path: _REGISTRY)
    monkeypatch.setattr(settings_export, "metadata", lambda: _REGISTRY)
    monkeypatch.setattr(
        "endless.set_cmd.resolve_project",
        lambda name, hint=None: {"name": name, "path": str(tmp_path), "id": 1},
    )
    monkeypatch.setattr("endless.set_cmd.project_name_for_cwd", lambda cwd: "demo")
    return cfg


def test_named_opt_in_is_written_on(project):
    set_field("demo.main_sync.enabled=on")
    cfg = json.loads(project.read_text())
    assert cfg["main_sync"] == {"enabled": "on"}
    assert cfg["prime"] == {"enabled": "on"}, "other keys are kept"


def test_local_key_that_looks_like_a_project_name(project):
    """`main_sync.enabled=on` also matches name.field; the registry decides
    it is a key of the current project."""
    set_field("main_sync.enabled=off")
    assert json.loads(project.read_text())["main_sync"] == {"enabled": "off"}


def test_int_is_written_as_a_number(project):
    set_field("prime.cap=5")
    assert json.loads(project.read_text())["prime"] == {"enabled": "on", "cap": 5}


def test_unset_removes_the_key(project):
    set_field("prime.enabled=unset")
    assert json.loads(project.read_text())["prime"] == {}


def test_a_legacy_boolean_is_refused_with_the_accepted_values(project):
    with pytest.raises(click.ClickException) as exc:
        set_field("main_sync.enabled=true")
    assert '"on", "off" or "unset"' in str(exc.value)
    assert "main_sync" not in json.loads(project.read_text())


def test_a_machine_setting_is_refused_with_its_file(project):
    with pytest.raises(click.ClickException) as exc:
        set_field("main_sync.interval=2m")
    assert _REGISTRY["machine_file"] in str(exc.value)
    assert "main_sync" not in json.loads(project.read_text())


def test_unregistered_dotted_key_falls_through_to_the_old_refusal(project):
    with pytest.raises(click.ClickException) as exc:
        set_field("demo.minimizer=whatever")
    assert "Unknown field 'minimizer'" in str(exc.value)
