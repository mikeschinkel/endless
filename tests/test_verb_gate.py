"""Tests for the verb-gate redesign (E-1106), verb storage (E-1117),
the verbs.json file split (E-1124), the JSONL format (E-1268), and the
write-time auto-commit (E-1208)."""

import json
import subprocess

import click
import pytest

from endless import db, task_cmd, matchers, verb_cmd


def _read_jsonl(path):
    """Parse a JSONL file as a list of dicts. Skips blank lines."""
    return [
        json.loads(line)
        for line in path.read_text().splitlines()
        if line.strip()
    ]


def _git(args, cwd):
    """Run a git command in cwd; raises on non-zero. Returns stdout."""
    res = subprocess.run(
        ["git", *args], capture_output=True, text=True, check=True, cwd=str(cwd),
    )
    return res.stdout.rstrip("\n")


@pytest.fixture
def git_project_at_cwd(isolated_env, monkeypatch):
    """Registered project at cwd backed by a real git repo with HEAD.

    Used by E-1208 tests that exercise the write-time auto-commit path.
    """
    proj_dir = isolated_env["projects_root"]
    (proj_dir / ".endless").mkdir(parents=True, exist_ok=True)
    # Seed a config.json so resolve helpers find a project here.
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
    verbs_path = isolated_env["config_dir"] / "verbs.jsonl"
    assert verbs_path.exists(), "verbs.jsonl must be created (E-1268)"
    verbs = _read_jsonl(verbs_path)
    entry = next((v for v in verbs if v.get("value") == "ponder"), None)
    assert entry is not None, "verb should be persisted in verbs.jsonl"
    assert entry.get("definition") == "to deliberate over"
    cfg = json.loads((isolated_env["config_dir"] / "config.json").read_text())
    assert "verbs" not in cfg, "config.json must not contain 'verbs' key"
    matcher_verbs = [m for m in cfg.get("matchers", []) if m.get("type") == "verb"]
    assert not matcher_verbs, "config.json must not contain verb matchers"


# --- E-1208: write-time auto-commit to main --------------------------------

def test_verb_add_commits_to_main(git_project_at_cwd):
    """A project-side verb add commits .endless/verbs.jsonl to main."""
    head_before = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=False)
    head_after = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    assert head_after != head_before, "HEAD must advance after verb add"
    subj = _git(["log", "-1", "--format=%s"], cwd=git_project_at_cwd)
    assert subj == "Endless: register verb 'ponder'"
    status = _git(["status", "--porcelain", "--", ".endless/verbs.jsonl"],
                  cwd=git_project_at_cwd)
    assert status == "", "verbs.jsonl should be clean after commit"


def test_verb_add_commits_with_unrelated_main_dirt(git_project_at_cwd):
    """Unrelated dirt on main does not block the commit and is preserved."""
    (git_project_at_cwd / "stray.txt").write_text("user work in flight\n")
    _git(["add", "stray.txt"], cwd=git_project_at_cwd)  # staged but uncommitted
    head_before = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=False)
    head_after = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    assert head_after != head_before
    # The unrelated staged change must remain staged after our -o commit.
    status = _git(["status", "--porcelain", "--", "stray.txt"],
                  cwd=git_project_at_cwd)
    assert status.startswith("A "), (
        f"stray.txt should still be staged-only, got: {status!r}"
    )


def test_verb_add_no_commit_on_noop(git_project_at_cwd):
    """Re-adding the same verb does not advance HEAD."""
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=False)
    head_after_first = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=False)
    head_after_second = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    assert head_after_first == head_after_second


def test_verb_add_machine_only_no_commit(git_project_at_cwd):
    """machine_only=True writes only the machine layer; no commit on main."""
    head_before = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=True)
    head_after = _git(["rev-parse", "HEAD"], cwd=git_project_at_cwd)
    assert head_after == head_before


def _patched_run_factory(real_run):
    """Build a subprocess.run replacement that fails any `git commit ...`
    call and delegates everything else to `real_run`. Captured outside the
    monkeypatch to avoid recursing into ourselves."""
    def fail_on_commit(args, **kwargs):
        if isinstance(args, (list, tuple)) and "commit" in args:
            return subprocess.CompletedProcess(
                args=args, returncode=1, stdout="", stderr="forced failure\n",
            )
        return real_run(args, **kwargs)
    return fail_on_commit


def test_verb_add_commit_failure_raises(git_project_at_cwd, monkeypatch):
    """When git commit fails, verb_cmd.add_verb raises ClickException
    and the file write persists on disk."""
    monkeypatch.setattr(
        subprocess, "run", _patched_run_factory(subprocess.run),
    )

    with pytest.raises(click.ClickException) as exc:
        verb_cmd.add_verb("ponder", "to deliberate over", machine_only=False)
    assert "forced failure" in exc.value.message

    # The file write happened before the failed commit.
    verbs_path = git_project_at_cwd / ".endless" / "verbs.jsonl"
    assert verbs_path.exists()
    entries = _read_jsonl(verbs_path)
    assert any(e.get("value") == "ponder" for e in entries), (
        "verb must be persisted in file even when commit fails"
    )


def test_auto_register_propagates_commit_failure(git_project_at_cwd, monkeypatch):
    """task_cmd.validate_title's auto-register path does not catch RuntimeError;
    a commit failure surfaces as a task add failure."""
    # Force the haiku verb-check to say YES with a definition so auto-register fires.
    monkeypatch.setattr(
        task_cmd, "_check_verb_via_haiku",
        lambda _word: (True, "to chew over thoughtfully"),
    )
    monkeypatch.setattr(
        subprocess, "run", _patched_run_factory(subprocess.run),
    )

    with pytest.raises(RuntimeError) as exc:
        task_cmd.validate_title("mull over the design")
    assert "forced failure" in str(exc.value)

    # File write happened before commit failed.
    verbs_path = git_project_at_cwd / ".endless" / "verbs.jsonl"
    entries = _read_jsonl(verbs_path)
    assert any(e.get("value") == "mull" for e in entries)


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


def test_legacy_verb_matcher_migrated_to_verbs_file(isolated_env, monkeypatch):
    """A pre-E-1117 config with type=verb matcher (with the bad 'definitions'
    field from E-1108) is migrated into the verbs file on first load."""
    cfg_path = isolated_env["config_dir"] / "config.json"
    verbs_path = isolated_env["config_dir"] / "verbs.jsonl"
    legacy = json.loads(cfg_path.read_text())
    legacy["matchers"] = [
        {
            "type": "verb", "method": "exact",
            "match": ["ponder", "deliberate"],
            "definitions": {"ponder": "to deliberate over"},
        },
        {
            "type": "start", "scope": "task", "method": "regex",
            "match": r"endless\s+task\s+claim\s+(?:[Ee]-)?(\d+)",
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
    non_verb = [m for m in cfg_after["matchers"] if m.get("type") != "verb"]
    assert non_verb, "non-verb matchers must remain in config.json"

    assert verbs_path.exists(), "verbs.jsonl must be created"
    verbs = _read_jsonl(verbs_path)
    values = {v.get("value") for v in verbs}
    assert {"ponder", "deliberate"}.issubset(values)
    ponder = next(v for v in verbs if v.get("value") == "ponder")
    assert ponder.get("definition") == "to deliberate over"


def test_inline_verbs_key_migrated_to_verbs_file(isolated_env, monkeypatch):
    """A post-E-1117/pre-E-1124 config with a top-level 'verbs' array key in
    config.json is migrated to the verbs file."""
    cfg_path = isolated_env["config_dir"] / "config.json"
    verbs_path = isolated_env["config_dir"] / "verbs.jsonl"
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

    assert verbs_path.exists(), "verbs.jsonl must be created"
    verbs = _read_jsonl(verbs_path)
    values = {v.get("value") for v in verbs}
    assert {"ponder", "deliberate"}.issubset(values)
    ponder = next(v for v in verbs if v.get("value") == "ponder")
    assert ponder.get("definition") == "to deliberate over"


def test_default_seed_creates_verbs_file(isolated_env):
    """First-run seeding writes DEFAULT_VERBS to verbs.jsonl."""
    verbs_path = isolated_env["config_dir"] / "verbs.jsonl"
    if verbs_path.exists():
        verbs_path.unlink()

    matchers.load_all_matchers()  # triggers _ensure_default_seeds

    assert verbs_path.exists(), "verbs.jsonl must be created on first seed"
    verbs = _read_jsonl(verbs_path)
    values = {v.get("value") for v in verbs}
    assert "add" in values
    assert "fix" in values
    cfg = json.loads((isolated_env["config_dir"] / "config.json").read_text())
    assert "verbs" not in cfg, "verbs must NOT be seeded into config.json"


def test_verb_remove(isolated_env):
    verb_cmd.add_verb("ponder", "to deliberate over", machine_only=True)
    pr, mr = matchers.remove_verb(value="ponder", machine_only=True)
    assert mr == 1
    verbs_path = isolated_env["config_dir"] / "verbs.jsonl"
    verbs = _read_jsonl(verbs_path)
    values = {v.get("value") for v in verbs}
    assert "ponder" not in values


def test_legacy_verbs_json_migrated_to_jsonl(isolated_env):
    """E-1268: a pre-existing verbs.json (array format) is migrated to
    verbs.jsonl (one object per line) on first load, and the legacy file
    is removed.
    """
    config_dir = isolated_env["config_dir"]
    legacy_path = config_dir / "verbs.json"
    jsonl_path = config_dir / "verbs.jsonl"
    if jsonl_path.exists():
        jsonl_path.unlink()
    legacy_path.write_text(json.dumps([
        {"value": "ponder", "definition": "to deliberate over"},
        {"value": "deliberate"},
    ]))

    loaded = matchers._load_verbs_list(jsonl_path)

    values = {v.get("value") for v in loaded}
    assert {"ponder", "deliberate"}.issubset(values)
    assert jsonl_path.exists(), "verbs.jsonl must be created after migration"
    assert not legacy_path.exists(), "legacy verbs.json must be removed"

    on_disk = _read_jsonl(jsonl_path)
    on_disk_values = {v.get("value") for v in on_disk}
    assert {"ponder", "deliberate"}.issubset(on_disk_values)


def test_legacy_verbs_json_merges_with_existing_jsonl(isolated_env):
    """E-1268: if both verbs.json (legacy) and verbs.jsonl exist, entries
    from .json are merged into .jsonl (jsonl wins on same-value conflicts)
    and .json is removed.
    """
    config_dir = isolated_env["config_dir"]
    legacy_path = config_dir / "verbs.json"
    jsonl_path = config_dir / "verbs.jsonl"

    jsonl_path.write_text(
        json.dumps({"value": "ponder", "definition": "jsonl version"}) + "\n"
    )
    legacy_path.write_text(json.dumps([
        {"value": "ponder", "definition": "legacy version"},
        {"value": "deliberate", "definition": "only-in-legacy"},
    ]))

    loaded = matchers._load_verbs_list(jsonl_path)
    by_value = {v["value"]: v for v in loaded if isinstance(v, dict)}
    assert by_value["ponder"]["definition"] == "jsonl version"
    assert by_value["deliberate"]["definition"] == "only-in-legacy"
    assert not legacy_path.exists()


def test_add_match_value_rejects_verb_type(isolated_env):
    with pytest.raises(ValueError):
        matchers.add_match_value(type_="verb", value="ponder", method="exact")


# E-1264: auto-register an unrecognized first word as a verb when claude
# haiku confirms it is one.

def test_verb_gate_auto_registers_when_haiku_says_yes(monkeypatch, isolated_env, capsys):
    # chdir somewhere with no ancestor .endless/config.json so the project
    # verbs layer doesn't leak in from the surrounding repo.
    monkeypatch.chdir(isolated_env["projects_root"])
    monkeypatch.setattr(
        task_cmd, "_check_verb_via_haiku",
        lambda word: (True, "to make stronger, more robust, or more resilient"),
    )

    assert "frobnishop" not in matchers.get_verbs()
    task_cmd.validate_title("Frobnishop a new path")

    out = capsys.readouterr().out
    assert "Auto-registered verb 'frobnishop'" in out
    assert "to make stronger" in out
    assert "frobnishop" in matchers.get_verbs()


def test_verb_gate_falls_through_when_haiku_says_no(monkeypatch, isolated_env):
    monkeypatch.chdir(isolated_env["projects_root"])
    monkeypatch.setattr(
        task_cmd, "_check_verb_via_haiku", lambda word: (False, None),
    )

    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_title("Frobnishop the widget")

    assert "not registered" in exc.value.message
    assert "frobnishop" not in matchers.get_verbs()


def test_verb_gate_falls_through_when_haiku_returns_empty_definition(monkeypatch, isolated_env):
    """YES with no definition is treated as malformed → fall through."""
    monkeypatch.chdir(isolated_env["projects_root"])
    monkeypatch.setattr(
        task_cmd, "_check_verb_via_haiku", lambda word: (True, ""),
    )

    with pytest.raises(click.ClickException):
        task_cmd.validate_title("Frobnishop the widget")
    assert "frobnishop" not in matchers.get_verbs()


def test_verb_gate_force_bypasses_haiku_check(monkeypatch, isolated_env):
    """--force should skip validation entirely — haiku should never be called."""
    called = []

    def fake(word):
        called.append(word)
        return (False, None)

    monkeypatch.setattr(task_cmd, "_check_verb_via_haiku", fake)
    task_cmd.validate_title("Frobnicate the widget", force=True)
    assert called == []


@pytest.mark.no_haiku_stub
def test_check_verb_via_haiku_handles_missing_binary(monkeypatch):
    """When `claude` is not on PATH, return (False, None) — no exception."""
    import subprocess

    def raise_fnf(*args, **kwargs):
        raise FileNotFoundError(2, "No such file or directory: 'claude'")

    monkeypatch.setattr(subprocess, "run", raise_fnf)
    is_verb, defn = task_cmd._check_verb_via_haiku("anything")
    assert is_verb is False
    assert defn is None


@pytest.mark.no_haiku_stub
def test_check_verb_via_haiku_parses_yes_response(monkeypatch):
    """A clean `YES: <definition>` reply yields (True, definition)."""
    import subprocess

    class FakeResult:
        returncode = 0
        stdout = "YES: To bring into existence by forming or shaping.\n"
        stderr = ""

    monkeypatch.setattr(subprocess, "run", lambda *a, **k: FakeResult())
    is_verb, defn = task_cmd._check_verb_via_haiku("forge")
    assert is_verb is True
    assert defn == "To bring into existence by forming or shaping."


@pytest.mark.no_haiku_stub
def test_check_verb_via_haiku_no_response(monkeypatch):
    import subprocess

    class FakeResult:
        returncode = 0
        stdout = "NO"
        stderr = ""

    monkeypatch.setattr(subprocess, "run", lambda *a, **k: FakeResult())
    is_verb, defn = task_cmd._check_verb_via_haiku("frobnicate")
    assert is_verb is False
    assert defn is None


@pytest.mark.no_haiku_stub
def test_check_verb_via_haiku_timeout(monkeypatch):
    import subprocess

    def raise_timeout(*args, **kwargs):
        raise subprocess.TimeoutExpired(cmd="claude", timeout=30)

    monkeypatch.setattr(subprocess, "run", raise_timeout)
    is_verb, defn = task_cmd._check_verb_via_haiku("anything")
    assert is_verb is False
    assert defn is None


@pytest.mark.no_haiku_stub
def test_check_verb_via_haiku_non_zero_exit(monkeypatch):
    import subprocess

    class FakeResult:
        returncode = 1
        stdout = ""
        stderr = "boom"

    monkeypatch.setattr(subprocess, "run", lambda *a, **k: FakeResult())
    is_verb, defn = task_cmd._check_verb_via_haiku("anything")
    assert is_verb is False
    assert defn is None
