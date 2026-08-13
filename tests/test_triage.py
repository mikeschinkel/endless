"""Tests for E-1859: the description-sufficiency triager.

The model call is the one thing that cannot be asserted deterministically, so
these tests cover everything AROUND it — parsing, model resolution, the
fail-open contract, the guards on the automatic path, and the E-1486 boundary
the module was built to respect. The end-to-end wiring (Go reads, template
render, the emitted event's actor kind) is exercised against a stubbed `claude`
by `tests/tasks/e-1859-verify.sh`.
"""

import json
import subprocess
from pathlib import Path

import pytest

from endless import config, task_cmd, triage


# --- parsing the constrained reply -----------------------------------------

@pytest.mark.parametrize("reply,expected", [
    ("SUBMITTED: the description names the exact rename.",
     ("submitted", "the description names the exact rename.")),
    ("UNPLANNED: no approach is named.",
     ("unplanned", "no approach is named.")),
    # A leading blank line is ordinary model output, not a malformed reply.
    ("\n\nSUBMITTED: fine.", ("submitted", "fine.")),
    # Case-insensitive on the token; the decision is still normalized down.
    ("submitted: fine.", ("submitted", "fine.")),
    # A bare verdict is accepted: the decision is the load-bearing half and
    # the rationale is provenance. Rejecting it would re-run a working call
    # at cost forever.
    ("UNPLANNED", ("unplanned", "")),
])
def test_parse_reply_accepts(reply, expected):
    assert triage.parse_reply(reply) == expected


@pytest.mark.parametrize("reply", [
    "",
    "   \n  \n",
    "I think this one is probably fine?",
    "Here is my answer:\nSUBMITTED: too late, the first line already lost.",
    "MAYBE: neither token.",
    None,
])
def test_parse_reply_rejects(reply):
    """Every unparseable shape yields None, which the caller fails open on."""
    assert triage.parse_reply(reply) is None


def test_parse_reply_bounds_the_rationale():
    """Provenance is one line in an event payload, not an essay."""
    decision, rationale = triage.parse_reply("SUBMITTED: " + "x" * 5000)
    assert decision == "submitted"
    assert len(rationale) <= triage._RATIONALE_MAX


# --- the shared model resolver ----------------------------------------------

def test_internal_model_defaults(isolated_env):
    """Shipped defaults differ per purpose on purpose — a verb lookup is not
    the same judgment as a sufficiency call."""
    assert config.internal_model("verb_check") == "haiku"
    assert config.internal_model("triage") == "sonnet"


def test_internal_model_rejects_an_unknown_purpose(isolated_env):
    """Purposes are a closed set; a typo at a call site must fail loudly
    rather than silently fall through to claude's default model."""
    with pytest.raises(KeyError):
        config.internal_model("nonsense")


def test_internal_model_user_config_overrides_default(isolated_env):
    cfg = json.loads(config.CONFIG_FILE.read_text())
    cfg["models"] = {"triage": "opus"}
    config.CONFIG_FILE.write_text(json.dumps(cfg))
    assert config.internal_model("triage") == "opus"
    # Untouched purposes keep their default.
    assert config.internal_model("verb_check") == "haiku"


def test_internal_model_project_config_wins_over_user(seeded_project_at_cwd):
    cfg = json.loads(config.CONFIG_FILE.read_text())
    cfg["models"] = {"triage": "opus"}
    config.CONFIG_FILE.write_text(json.dumps(cfg))

    proj = config.project_config_read(seeded_project_at_cwd) or {}
    proj["models"] = {"triage": "haiku"}
    config.project_config_write(seeded_project_at_cwd, proj)

    assert config.internal_model("triage", project_root=seeded_project_at_cwd) == "haiku"


def test_internal_model_blank_value_falls_through(isolated_env):
    """A cleared key must not send `--model ''` to claude."""
    cfg = json.loads(config.CONFIG_FILE.read_text())
    cfg["models"] = {"triage": "   "}
    config.CONFIG_FILE.write_text(json.dumps(cfg))
    assert config.internal_model("triage") == "sonnet"


@pytest.mark.no_haiku_stub
def test_verb_check_resolves_through_the_same_mechanism(monkeypatch, isolated_env):
    """E-1859's actual ask: verb-check and triage resolve their model the same
    way. Configuring the verb_check purpose must reach the verb-check call."""
    cfg = json.loads(config.CONFIG_FILE.read_text())
    cfg["models"] = {"verb_check": "opus"}
    config.CONFIG_FILE.write_text(json.dumps(cfg))

    seen = {}

    def fake_run(prompt, *, timeout, model=None):
        seen["model"] = model
        raise FileNotFoundError("no claude here")

    from endless import internal_claude
    monkeypatch.setattr(internal_claude, "run_internal_claude", fake_run)
    task_cmd._check_verb_via_haiku("frobnicate")
    assert seen["model"] == "opus"


# --- fail-open --------------------------------------------------------------

@pytest.mark.parametrize("boom", [
    subprocess.TimeoutExpired(cmd="claude", timeout=1),
    FileNotFoundError("claude"),
    OSError("exec format error"),
])
def test_evaluate_fails_open_on_a_broken_call(monkeypatch, boom, isolated_env):
    from endless import internal_claude

    def fake_run(prompt, *, timeout, model=None):
        raise boom

    monkeypatch.setattr(internal_claude, "run_internal_claude", fake_run)
    assert triage.evaluate("prompt") is None


def test_evaluate_fails_open_on_a_nonzero_exit(monkeypatch, isolated_env):
    from endless import internal_claude

    monkeypatch.setattr(
        internal_claude, "run_internal_claude",
        lambda prompt, *, timeout, model=None: subprocess.CompletedProcess(
            args=[], returncode=1, stdout="SUBMITTED: ignore me", stderr="boom",
        ),
    )
    assert triage.evaluate("prompt") is None


def test_triage_one_leaves_the_task_alone_when_the_context_read_fails(monkeypatch):
    """Every failure keeps the task `untriaged` and reports it — the fallback
    is the normal path (a human runs `task submit`), not an error path."""
    def boom(_task_id):
        raise triage.TriageError("session-query exploded")

    monkeypatch.setattr(triage, "build_context", boom)
    result = triage.triage_one(42)
    assert result["outcome"] == "failed"
    assert "session-query exploded" in result["detail"]


def test_triage_batch_fails_open_when_the_queue_read_fails(monkeypatch):
    def boom(**_kwargs):
        raise triage.TriageError("no database")

    monkeypatch.setattr(triage, "select_untriaged", boom)
    results = triage.triage_batch()
    assert [r["outcome"] for r in results] == ["failed"]


def test_triage_one_skips_a_task_that_is_no_longer_untriaged(monkeypatch):
    """Selection and the write are two reads apart. A human who routed the
    task by hand in between always wins."""
    monkeypatch.setattr(
        triage, "build_context",
        lambda _id: {"status": "submitted", "project": "p"},
    )
    result = triage.triage_one(7)
    assert result["outcome"] == "skipped"


def test_triage_one_dry_run_writes_nothing(monkeypatch):
    monkeypatch.setattr(
        triage, "build_context",
        lambda _id: {"status": "untriaged", "project": "p"},
    )
    monkeypatch.setattr(triage, "render_prompt", lambda _ctx: "prompt")
    monkeypatch.setattr(triage, "claim", lambda _id: True)
    monkeypatch.setattr(triage, "release", lambda _id: None)
    monkeypatch.setattr(triage, "evaluate", lambda _p: ("submitted", "because"))

    def fail(*_a, **_kw):
        raise AssertionError("--dry-run must not write")

    monkeypatch.setattr(triage, "apply", fail)
    result = triage.triage_one(7, dry_run=True)
    assert result["outcome"] == "dry-run"
    assert result["decision"] == "submitted"


# --- the automatic file-time path -------------------------------------------

def test_inline_triage_is_suppressed_by_the_env_var(monkeypatch):
    monkeypatch.setenv(triage.NO_TRIAGE_ENV, "1")
    assert triage.inline_suppressed()
    assert triage.spawn_detached(1) is False


def test_a_worktree_pinned_to_main_is_NOT_suppressed_by_itself(monkeypatch):
    """The corrected rule (E-1859 reopened). Being in a self-dev worktree under
    `--db main` is NOT the hazard — it is how every agent session files, and the
    CLI spawned there is main's editable install. Suppressing on this alone was
    the reason automatic triage never fired in endless's own repo. The hazard is
    candidate CODE against the real ledger, which is what the path check below
    (and its own tests) catches."""
    monkeypatch.delenv(triage.NO_TRIAGE_ENV, raising=False)
    monkeypatch.setattr(config, "gated_worktree_root", lambda *_a, **_k: Path("/repo"))
    monkeypatch.setattr(config, "worktree_dir_name", lambda *_a, **_k: "e-1")
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    monkeypatch.setattr(triage, "_self_cli_path", lambda: "/usr/local/bin/endless")
    assert triage.inline_suppressed() == ""


def test_inline_triage_runs_when_nothing_suppresses_it(monkeypatch):
    monkeypatch.delenv(triage.NO_TRIAGE_ENV, raising=False)
    monkeypatch.setattr(config, "gated_worktree_root", lambda *_a, **_k: None)
    assert triage.inline_suppressed() == ""


def test_task_add_spawns_triage_only_for_an_untriaged_filing(
    monkeypatch, seeded_project_at_cwd,
):
    """Gated on the RESOLVED status: tier-1's auto-`ready` is exempt from
    planning, so it is exempt from triage — and so is any explicit --status."""
    spawned = []
    monkeypatch.setattr(triage, "spawn_detached", lambda tid: spawned.append(tid))

    task_cmd.add_item(title="Add a normal thing", description="short")
    assert len(spawned) == 1

    task_cmd.add_item(title="Add a tier one thing", description="short", tier=1)
    task_cmd.add_item(title="Add a planned thing", description="short",
                      status="unplanned")
    assert len(spawned) == 1, "only the untriaged filing may spawn triage"


def test_child_db_args_recover_an_explicit_db_choice(monkeypatch):
    """Inside a self-dev worktree the child would be refused by the E-1429
    gate without an explicit --db; XDG_CONFIG_HOME alone does not satisfy it."""
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    assert triage._child_db_args() == []

    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    assert triage._child_db_args() == ["--db", "main"]

    monkeypatch.setattr(config, "worktree_dir_name", lambda *_a, **_k: "e-1859")
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", config.sandbox_config_dir("e-1859"),
    )
    assert triage._child_db_args() == ["--db", "sandbox"]


# --- the E-1486 boundary ----------------------------------------------------

def test_triage_module_holds_no_sqlite_knowledge():
    """The commitment this feature was built on: every read goes through the
    Go read helpers, and the write through emit_event. If this fails, the
    module has acquired direct DB access and belongs back in E-1486's backlog.

    Parsed with `ast`, not grepped: the module docstring NAMES sqlite3 to
    explain the prohibition, and a substring check would flag its own
    documentation.
    """
    import ast

    tree = ast.parse(Path(triage.__file__).read_text())

    imported: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            imported.update(a.name for a in node.names)
        elif isinstance(node, ast.ImportFrom):
            imported.add(node.module or "")
            imported.update(f"{node.module}.{a.name}" for a in node.names)

    assert "sqlite3" not in imported, "triage.py imports sqlite3"
    assert "endless.db" not in imported, (
        "triage.py imports endless.db — reads must go through "
        "`endless-go session-query`, writes through emit_event"
    )

    # …and no `db.<anything>(...)` call slipped in under a local alias.
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        func = node.func
        if isinstance(func, ast.Attribute) and isinstance(func.value, ast.Name):
            assert func.value.id != "db", (
                f"triage.py calls db.{func.attr}() at line {node.lineno}"
            )


# --- E-1859 reopened: the five fixes ----------------------------------------

def test_suppression_allows_a_landed_cli_against_the_real_ledger(monkeypatch):
    """Fix 1. `--db main` from a worktree is how every agent files, and the CLI
    it spawns is main's editable install — landed code. Suppressing that was
    the reason automatic triage never fired in endless's own repo."""
    monkeypatch.delenv(triage.NO_TRIAGE_ENV, raising=False)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    monkeypatch.setattr(triage, "_enclosing_worktree", lambda: Path("/repo/.endless/worktrees/e-1"))
    monkeypatch.setattr(triage, "_self_cli_path", lambda: "/usr/local/bin/endless")
    assert triage.inline_suppressed() == ""


def test_suppression_blocks_a_candidate_cli_against_the_real_ledger(monkeypatch):
    """Fix 1, the half that must stay: candidate code + real ledger is E-698's
    hazard, and path-gating keeps it caught if anyone runs `uv run endless`
    from a worktree."""
    monkeypatch.delenv(triage.NO_TRIAGE_ENV, raising=False)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    wt = Path("/repo/.endless/worktrees/e-1")
    monkeypatch.setattr(triage, "_enclosing_worktree", lambda: wt)
    monkeypatch.setattr(triage, "_self_cli_path", lambda: str(wt / ".venv/bin/endless"))
    assert "candidate CLI" in triage.inline_suppressed()


def test_suppression_allows_candidate_cli_against_a_sandbox(monkeypatch):
    """Fix 1: candidate + sandbox IS the test, never suppressed."""
    monkeypatch.delenv(triage.NO_TRIAGE_ENV, raising=False)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", Path("/cache/endless/sandboxes/e-1/endless"))
    assert triage.inline_suppressed() == ""


def test_claim_is_taken_before_the_model_call(monkeypatch):
    """Fix 2: the claim must precede evaluate(), or the spend it protects is
    already committed by the time we find out someone else was working."""
    order = []
    monkeypatch.setattr(triage, "build_context", lambda _id: {"status": "untriaged", "project": "p"})
    monkeypatch.setattr(triage, "render_prompt", lambda _c: "prompt")
    monkeypatch.setattr(triage, "claim", lambda _id: order.append("claim") or True)
    monkeypatch.setattr(triage, "release", lambda _id: order.append("release"))
    monkeypatch.setattr(triage, "evaluate", lambda _p: order.append("evaluate") or ("submitted", "why"))
    monkeypatch.setattr(triage, "apply", lambda *_a: True)

    triage.triage_one(5)
    assert order == ["claim", "evaluate", "release"]


def test_losing_the_claim_skips_without_calling_the_model(monkeypatch):
    monkeypatch.setattr(triage, "build_context", lambda _id: {"status": "untriaged", "project": "p"})
    monkeypatch.setattr(triage, "claim", lambda _id: False)

    def boom(_p):
        raise AssertionError("model called despite losing the claim")

    monkeypatch.setattr(triage, "evaluate", boom)
    result = triage.triage_one(5)
    assert result["outcome"] == "skipped"
    assert "claim" in result["detail"]


def test_the_claim_is_released_even_when_the_call_fails(monkeypatch):
    released = []
    monkeypatch.setattr(triage, "build_context", lambda _id: {"status": "untriaged", "project": "p"})
    monkeypatch.setattr(triage, "render_prompt", lambda _c: "prompt")
    monkeypatch.setattr(triage, "claim", lambda _id: True)
    monkeypatch.setattr(triage, "release", lambda tid: released.append(tid))
    monkeypatch.setattr(triage, "evaluate", lambda _p: None)
    monkeypatch.setattr(triage, "report_failure", lambda *_a: None)

    triage.triage_one(5)
    assert released == [5], "a held claim would wedge the task until the TTL lapsed"


def test_a_failed_triage_records_a_fault(monkeypatch):
    """Fix 3: fail-open must not mean silent. The detached child has nowhere
    else to report."""
    recorded = []
    monkeypatch.setattr(triage, "build_context", lambda _id: {"status": "untriaged", "project": "p"})
    monkeypatch.setattr(triage, "render_prompt", lambda _c: "prompt")
    monkeypatch.setattr(triage, "claim", lambda _id: True)
    monkeypatch.setattr(triage, "release", lambda _id: None)
    monkeypatch.setattr(triage, "evaluate", lambda _p: None)
    monkeypatch.setattr(triage, "report_failure", lambda *a: recorded.append(a))

    result = triage.triage_one(5)
    assert result["outcome"] == "failed"
    assert len(recorded) == 1
    assert recorded[0][0] == 5


def test_failure_source_names_the_path(monkeypatch):
    monkeypatch.delenv(triage.INLINE_ENV, raising=False)
    assert triage._failure_source() == "triage:sweep"
    monkeypatch.setenv(triage.INLINE_ENV, "1")
    assert triage._failure_source() == "triage:inline"


def test_a_routed_task_records_no_fault(monkeypatch):
    recorded = []
    monkeypatch.setattr(triage, "build_context", lambda _id: {"status": "untriaged", "project": "p"})
    monkeypatch.setattr(triage, "render_prompt", lambda _c: "prompt")
    monkeypatch.setattr(triage, "claim", lambda _id: True)
    monkeypatch.setattr(triage, "release", lambda _id: None)
    monkeypatch.setattr(triage, "evaluate", lambda _p: ("submitted", "why"))
    monkeypatch.setattr(triage, "apply", lambda *_a: True)
    monkeypatch.setattr(triage, "report_failure", lambda *a: recorded.append(a))

    assert triage.triage_one(5)["outcome"] == "routed"
    assert recorded == []


def test_claim_ttl_exceeds_the_call_timeout():
    """A TTL shorter than the call it guards lets a healthy claimant get
    re-claimed underneath itself — the duplicate spend, reintroduced."""
    assert triage.CLAIM_TTL_SECONDS > triage.CALL_TIMEOUT_SECONDS


def test_claim_and_release_use_one_stable_owner(monkeypatch):
    """Claim and release are two separate `endless-go` invocations. If the
    owner defaulted to the Go helper's own process identity they would never
    match, so every release would be a no-op and the claim would strand the
    task until its TTL lapsed."""
    calls = []
    monkeypatch.setattr(triage, "_endless_go", lambda args, stdin=None: calls.append(args) or "1")

    triage.claim(3)
    triage.release(3)

    owners = [a[a.index("--owner") + 1] for a in calls if "--owner" in a]
    assert len(owners) == 2, "both claim and release must name an explicit owner"
    assert owners[0] == owners[1], "release used a different owner than claim"
