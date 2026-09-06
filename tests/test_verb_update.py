"""Tests for `endless verb update` (E-2114): correcting a registered verb's
definition or category without the remove-then-add round trip.

The property under test throughout is "changes only what is passed": every
field the caller omits must read back exactly as it did before, because the
failure mode this command exists to remove is a remove-then-add where a field
you forgot to retype was silently rewritten instead of left alone.

Layer targeting is by scope: the correction is written to the layer being
addressed — project by default, machine under `machine_only` — creating an
entry there if none exists, while the other layer is kept in step only if it
already carries the verb. A created entry holds only the corrected fields;
`_resolved_verbs`' field-wise fall-through supplies the rest from below.
"""

import json
import subprocess

import click
import pytest

from endless import db, matchers, verb_cmd


def _read_jsonl(path):
    """Parse a JSONL file as a list of dicts. Skips blank lines."""
    return [
        json.loads(line)
        for line in path.read_text().splitlines()
        if line.strip()
    ]


def _entry(path, value):
    """The (single) entry for `value` in the verbs.jsonl at `path`, or None."""
    if not path.exists():
        return None
    matches = [v for v in _read_jsonl(path) if v.get("value") == value]
    assert len(matches) <= 1, f"expected at most one {value!r} entry, got {matches}"
    return matches[0] if matches else None


def _git(args, cwd):
    """Run a git command in cwd; raises on non-zero. Returns stdout."""
    res = subprocess.run(
        ["git", *args], capture_output=True, text=True, check=True, cwd=str(cwd),
    )
    return res.stdout.rstrip("\n")


@pytest.fixture
def git_project_at_cwd(isolated_env, monkeypatch):
    """Registered project at cwd backed by a real git repo with HEAD, so the
    project-layer write reaches the E-1208 auto-commit."""
    proj_dir = isolated_env["projects_root"]
    (proj_dir / ".endless").mkdir(parents=True, exist_ok=True)
    (proj_dir / ".endless" / "config.json").write_text('{"name": "test"}\n')
    monkeypatch.chdir(proj_dir)
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', ?, 'active', datetime('now'), datetime('now'))",
        (str(proj_dir),),
    )
    _git(["init", "-q", "-b", "main"], cwd=proj_dir)
    _git(["config", "user.email", "test@example.com"], cwd=proj_dir)
    _git(["config", "user.name", "Test"], cwd=proj_dir)
    _git(["config", "commit.gpgsign", "false"], cwd=proj_dir)
    _git(["add", ".endless/config.json"], cwd=proj_dir)
    _git(["commit", "-q", "-m", "init"], cwd=proj_dir)
    return proj_dir


# --- the point of the command: omitted fields are left alone ---------------

def test_category_change_leaves_definition_alone(isolated_env):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    matchers.update_verb(value="ponder", category=["investigation"],
                         machine_only=True)

    entry = _entry(isolated_env["config_dir"] / "verbs.jsonl", "ponder")
    assert entry["category"] == ["investigation"]
    assert entry["definition"] == "to deliberate over", (
        "an omitted --definition must survive verbatim"
    )


def test_definition_change_leaves_category_alone(isolated_env):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      category=["investigation"], machine_only=True)
    matchers.update_verb(value="ponder", definition="to weigh at length",
                         machine_only=True)

    entry = _entry(isolated_env["config_dir"] / "verbs.jsonl", "ponder")
    assert entry["definition"] == "to weigh at length"
    assert entry["category"] == ["investigation"], (
        "an omitted --category must survive verbatim"
    )


def test_unrelated_fields_survive(isolated_env):
    """A field no CLI flag covers is neither dropped nor rewritten."""
    path = isolated_env["config_dir"] / "verbs.jsonl"
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    verbs = _read_jsonl(path)
    for v in verbs:
        if v["value"] == "ponder":
            v["enabled"] = False
    matchers._save_verbs_list(path, verbs)

    matchers.update_verb(value="ponder", category=["investigation"],
                         machine_only=True)
    assert _entry(path, "ponder")["enabled"] is False


def test_category_replaces_the_whole_set(isolated_env):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      category=["action", "investigation"], machine_only=True)
    matchers.update_verb(value="ponder", category=["investigation"],
                         machine_only=True)

    entry = _entry(isolated_env["config_dir"] / "verbs.jsonl", "ponder")
    assert entry["category"] == ["investigation"], (
        "category is a set, not a list that appends"
    )


def test_the_live_case_a_verb_registered_as_action(isolated_env):
    """E-2114's origin: 'brainstorm' registered as an action verb, which made a
    brainstorm-typed task refuse a title led by 'brainstorm'."""
    matchers.add_verb(value="brainstorm", definition="to generate ideas freely",
                      machine_only=True)
    assert matchers.verb_categories("brainstorm") == frozenset({"action"})

    matchers.update_verb(value="brainstorm", category=["investigation"],
                         machine_only=True)
    assert matchers.verb_categories("brainstorm") == frozenset({"investigation"})
    assert matchers.get_verb_definition("brainstorm") == "to generate ideas freely"


# --- matching ---------------------------------------------------------------

def test_value_matches_case_insensitively(isolated_env):
    """Resolution is case-insensitive, so the mutation must be too, and the
    result reports the registered spelling rather than what was typed."""
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    result = matchers.update_verb(value="PONDER", category=["investigation"],
                                  machine_only=True)

    assert result.value == "ponder"
    assert _entry(isolated_env["config_dir"] / "verbs.jsonl",
                  "ponder")["category"] == ["investigation"]


def test_every_duplicate_line_is_updated(isolated_env):
    """`merge=union` on verbs.jsonl can leave two lines for one verb after
    concurrent registrations. Fixing only the line that wins resolution would
    leave the other as a trap for whichever side a later merge keeps."""
    path = isolated_env["config_dir"] / "verbs.jsonl"
    matchers._save_verbs_list(path, [
        {"value": "ponder", "definition": "to deliberate over"},
        {"value": "ponder", "definition": "to deliberate over"},
    ])

    matchers.update_verb(value="ponder", category=["investigation"],
                         machine_only=True)
    entries = [v for v in _read_jsonl(path) if v["value"] == "ponder"]
    assert len(entries) == 2
    assert all(v["category"] == ["investigation"] for v in entries)


# --- built-ins: materialize an override ------------------------------------

def test_builtin_gets_a_project_override_carrying_only_the_change(git_project_at_cwd):
    """`_ensure_default_seeds` writes every built-in into the MACHINE file on
    first run, so a built-in is 'present in a file' and presence-first targeting
    would send this project-scoped correction machine-wide. Scope-first puts the
    override in the project layer, where it belongs."""
    project_path = git_project_at_cwd / ".endless" / "verbs.jsonl"
    assert _entry(project_path, "research") is None, "project layer starts empty"

    result = matchers.update_verb(value="research", category=["action"])

    assert result.materialized is True
    assert result.layers == ("project", "machine")
    assert _entry(project_path, "research") == {
        "value": "research", "category": ["action"],
    }, "the override must carry only the corrected field"


def test_builtin_override_under_machine_only_stays_machine_side(git_project_at_cwd):
    project_path = git_project_at_cwd / ".endless" / "verbs.jsonl"

    result = matchers.update_verb(value="research", category=["action"],
                                  machine_only=True)

    assert result.layers == ("machine",)
    assert _entry(project_path, "research") is None
    assert matchers.verb_categories("research") == frozenset({"action"})


def test_builtin_override_still_resolves_the_builtin_definition(git_project_at_cwd):
    """The override omits `definition`; field-wise layering fills it back in."""
    before = matchers.get_verb_definition("research")
    matchers.update_verb(value="research", category=["action"])

    assert matchers.verb_categories("research") == frozenset({"action"})
    assert matchers.get_verb_definition("research") == before


def test_unknown_verb_is_refused(isolated_env):
    with pytest.raises(matchers.UnknownVerbError):
        matchers.update_verb(value="nonverbword", category=["action"],
                             machine_only=True)


# --- layer targeting --------------------------------------------------------

def test_machine_only_skips_the_project_layer(git_project_at_cwd):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=False)
    head_before = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)

    result = matchers.update_verb(value="ponder", category=["investigation"],
                                  machine_only=True)

    assert result.layers == ("machine",)
    project_path = git_project_at_cwd / ".endless" / "verbs.jsonl"
    assert "category" not in _entry(project_path, "ponder")
    assert _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd) == head_before, (
        "a machine-only update must not commit on main"
    )


def test_project_write_commits_to_main(git_project_at_cwd):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=False)
    head_before = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)

    result = matchers.update_verb(value="ponder", category=["investigation"])

    assert result.layers == ("project", "machine")
    assert _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd) != head_before
    assert _git(["log", "-1", "--format=%s"], cwd=git_project_at_cwd) == (
        "Endless: update verb 'ponder'"
    ), "the subject must distinguish a correction from a registration"
    assert _git(["status", "--porcelain", "--", ".endless/verbs.jsonl"],
                cwd=git_project_at_cwd) == ""


def test_project_only_verb_is_not_copied_into_the_machine_layer(git_project_at_cwd):
    """The fresh-clone shape: verbs.jsonl arrives with the checkout, so the verb
    is in the project file and not this machine's. Updating it must not
    materialize a machine entry as a side effect."""
    project_path = git_project_at_cwd / ".endless" / "verbs.jsonl"
    matchers._save_verbs_list(project_path, [
        {"value": "ponder", "definition": "to deliberate over"},
    ])
    machine_path = matchers.machine_verbs_path()

    result = matchers.update_verb(value="ponder", category=["investigation"])

    assert result.layers == ("project",)
    assert result.materialized is False
    assert _entry(project_path, "ponder")["category"] == ["investigation"]
    assert _entry(machine_path, "ponder") is None, (
        "a project verb must not leak into the machine layer"
    )


def test_no_op_update_reports_no_layers_and_does_not_commit(git_project_at_cwd):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      category=["investigation"], machine_only=False)
    head_before = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)

    result = matchers.update_verb(value="ponder", category=["investigation"])

    assert result.layers == ()
    assert _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd) == head_before


# --- argument validation ----------------------------------------------------

def test_no_fields_is_refused(isolated_env):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    with pytest.raises(ValueError, match="nothing to update"):
        matchers.update_verb(value="ponder", machine_only=True)


def test_blank_definition_is_refused(isolated_env):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    with pytest.raises(ValueError, match="definition cannot be empty"):
        matchers.update_verb(value="ponder", definition="   ", machine_only=True)


def test_category_with_no_usable_token_is_refused(isolated_env):
    """`add_verb` drops junk tokens and omits the field; an update must not,
    or it would report success while leaving the verb miscategorized."""
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    with pytest.raises(ValueError, match="category must name at least one"):
        matchers.update_verb(value="ponder", category=["bogus"], machine_only=True)


# --- the CLI wrapper --------------------------------------------------------

def test_cli_requires_at_least_one_field(isolated_env):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    with pytest.raises(click.ClickException) as exc:
        verb_cmd.update_verb("ponder", None, (), machine_only=True)
    assert "Nothing to update" in exc.value.message


def test_cli_unknown_verb_points_at_verb_add(isolated_env):
    with pytest.raises(click.ClickException) as exc:
        verb_cmd.update_verb("nonverbword", None, ("action",), machine_only=True)
    assert "No verb matched" in exc.value.message
    assert "endless verb add" in exc.value.message


def test_cli_reports_the_layers_and_fields(isolated_env, capsys):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      machine_only=True)
    verb_cmd.update_verb("ponder", "to weigh at length", ("investigation",),
                         machine_only=True)

    out = capsys.readouterr().out
    assert "Updated in machine" in out
    assert "'ponder'" in out
    assert "definition" in out and "category" in out


def test_cli_names_a_created_override_and_the_fall_through(git_project_at_cwd, capsys):
    verb_cmd.update_verb("research", None, ("action",), machine_only=False)
    out = capsys.readouterr().out
    assert "Updated in project + machine" in out
    assert "New override for 'research' in the project layer" in out
    assert "still resolve from below" in out


def test_cli_reports_a_no_op(isolated_env, capsys):
    matchers.add_verb(value="ponder", definition="to deliberate over",
                      category=["investigation"], machine_only=True)
    verb_cmd.update_verb("ponder", None, ("investigation",), machine_only=True)
    assert "Already set (no change)" in capsys.readouterr().out
