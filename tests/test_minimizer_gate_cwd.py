"""Python resolves the minimizer switches the way the Go hook does (E-2030).

The bug this pins was found by E-2030's own work. `endless minimizer --help`
printed `enabled false` in a worktree whose Stop hook was, at that moment,
holding the session's turns against the channel — because Python asked
`enclosing_project_root()`, which deliberately maps a worktree back to the MAIN
checkout, while Go's `MinimizerConfigForCwd` walks up from cwd and takes the
nearest declaring config.

That is `reportChannelOn`'s invariant broken from the side nobody watches: not
"told to use a channel that will not gate it", but gated without having been
told. A self-dev branch enabling the minimizer for itself — exactly what E-1975's
branch did — hits it every time.

So these are parity tests, and they mirror `TestMinimizerConfigForCwd_*` on the
Go side case for case. When the two disagree, the Go side wins: it is the one
enforcing.
"""

import json
from pathlib import Path

import pytest

from endless import config

# The worktree path shape `enclosing_project_root` recognizes. A directory that
# merely sits under the project root is NOT a worktree and resolves differently,
# so the fixtures use the real layout.
WORKTREE_REL = Path(".endless") / "worktrees" / "e-9999"


def write_config(root: Path, payload: dict | None) -> None:
    """Write (or clear) `<root>/.endless/config.json`."""
    path = root / ".endless" / "config.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    if payload is None:
        path.unlink(missing_ok=True)
        return
    path.write_text(json.dumps(payload) + "\n")


@pytest.fixture
def project(tmp_path):
    """A project root with a task worktree inside it, both configurable."""
    root = tmp_path / "proj"
    worktree = root / WORKTREE_REL
    worktree.mkdir(parents=True)
    write_config(root, {"name": "proj"})
    return root, worktree


# --------------------------------------------------------------------------- #
# the bug
# --------------------------------------------------------------------------- #

def test_worktree_config_beats_the_project_root(project):
    """The case that was wrong: a branch enabling the channel for itself.

    Reading the project root here reports `false` while the Stop hook, walking up
    from cwd, reports `true` — so every Python emitter tells the session the
    channel is off while the hook holds its turns against it.
    """
    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    write_config(worktree, {"minimizer": {"enabled": True, "optimizer": True}})

    assert config.minimizer_config_for_cwd(worktree)["enabled"] is True
    assert config.minimizer_config_for_cwd(root)["enabled"] is False

    # And the old resolution, kept only to show the two really do differ — a
    # regression that reverted this would otherwise pass every assertion above
    # by making both answers the same wrong one.
    assert config.project_report_gate(root) is False


def test_a_deep_directory_inside_the_worktree_still_gets_the_worktree(project):
    """Sessions do not sit at the worktree root; they cd into src/, tests/, ..."""
    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    write_config(worktree, {"minimizer": {"enabled": True}})

    deep = worktree / "src" / "endless"
    deep.mkdir(parents=True)
    assert config.minimizer_config_for_cwd(deep)["enabled"] is True


# --------------------------------------------------------------------------- #
# precedence, mirroring Go's readMinimizer / MinimizerConfigForCwd
# --------------------------------------------------------------------------- #

def test_silence_inherits_rather_than_resetting(project):
    """A worktree that says nothing must not reset the project to the default.

    Deleting the key from a branch would otherwise silently re-enable a gate the
    project had switched off — a config change that turns enforcement back on by
    doing nothing.
    """
    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    write_config(worktree, {"name": "proj"})  # present, but silent on minimizer

    assert config.minimizer_config_for_cwd(worktree)["enabled"] is False


def test_absent_worktree_config_inherits_too(project):
    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    assert config.minimizer_config_for_cwd(worktree)["enabled"] is False


@pytest.mark.parametrize("payload,enabled,optimizer", [
    ({"minimizer": {"enabled": True, "optimizer": False}}, True, False),
    ({"minimizer": {"enabled": False}}, False, True),   # optimizer keeps default
    ({"minimizer": False}, False, False),               # scalar shorthand: both
    ({"minimizer": True}, True, True),
    ({"report_gate": False}, False, True),              # E-1953's name
    ({"report_gate": True}, True, True),
    ({"name": "proj"}, True, True),                     # silent -> both default
    ({}, True, True),
])
def test_the_accepted_spellings(project, payload, enabled, optimizer):
    root, worktree = project
    write_config(root, {"name": "proj", **payload})
    got = config.minimizer_config_for_cwd(root)
    assert (got["enabled"], got["optimizer"]) == (enabled, optimizer)


def test_the_new_key_outranks_the_legacy_one(project):
    """Both present is a project mid-migration; the current spelling wins."""
    root, _ = project
    write_config(root, {"minimizer": {"enabled": True}, "report_gate": False})
    assert config.minimizer_config_for_cwd(root)["enabled"] is True


def test_malformed_config_is_silence_not_false(project):
    """Unparseable means "this layer says nothing", so resolution continues.

    Treating it as `false` would let a stray comma switch enforcement off across
    a whole project without anything reporting a problem.
    """
    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    (worktree / ".endless").mkdir(parents=True, exist_ok=True)
    (worktree / ".endless" / "config.json").write_text('{"minimizer": fal')

    assert config.minimizer_config_for_cwd(worktree)["enabled"] is False


def test_outside_any_project_defaults_on(tmp_path):
    """Ignorance is not an opt-out — the channel ships on."""
    assert config.minimizer_config_for_cwd(tmp_path)["enabled"] is True


# --------------------------------------------------------------------------- #
# the emitters follow it
# --------------------------------------------------------------------------- #

def test_report_gate_on_uses_the_cwd_resolution(project, monkeypatch):
    """`_report_gate_on` is what the guide, the handoff and the nudge all ask."""
    from endless import task_cmd

    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    write_config(worktree, {"minimizer": {"enabled": True}})

    monkeypatch.chdir(worktree)
    assert task_cmd._report_gate_on() is True

    monkeypatch.chdir(root)
    assert task_cmd._report_gate_on() is False


def test_guide_conditions_follow_the_same_answer(project, monkeypatch):
    """The guide must not describe a channel the hook is enforcing, or vice versa."""
    from endless import cli

    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    write_config(worktree, {"minimizer": {"enabled": True}})

    monkeypatch.chdir(worktree)
    assert cli.guide_conditions()["report_gate"] is True

    monkeypatch.chdir(root)
    assert cli.guide_conditions()["report_gate"] is False


def test_help_block_reports_the_config_actually_in_force(project, monkeypatch):
    """The help that started this: it must name the file whose value won."""
    from endless import help_settings

    root, worktree = project
    write_config(root, {"name": "proj", "minimizer": {"enabled": False}})
    write_config(worktree, {"minimizer": {"enabled": True, "optimizer": True}})

    monkeypatch.chdir(worktree)
    block = help_settings.render(help_settings.MINIMIZER, cwd=worktree)
    assert "minimizer.enabled    true" in block
    assert str(WORKTREE_REL) in block, \
        f"the block names a config the worktree does not obey:\n{block}"

    monkeypatch.chdir(root)
    block = help_settings.render(help_settings.MINIMIZER, cwd=root)
    assert "minimizer.enabled    false" in block
    assert str(WORKTREE_REL) not in block


def test_help_block_says_where_a_default_came_from(project, monkeypatch):
    """A defaulted `true` and a chosen `true` need different edits to change."""
    from endless import help_settings

    root, _ = project
    monkeypatch.chdir(root)
    block = help_settings.render(help_settings.MINIMIZER, cwd=root)
    assert "minimizer.enabled    true" in block
    assert "at their default" in block
