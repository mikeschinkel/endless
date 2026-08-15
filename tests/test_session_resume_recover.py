"""Unit coverage for `session resume --review`/`--reopen` recovery (E-1801).

Exercises the resolver's base/status decisions across the state matrix with the
Go resume-target lookup, git worktree recreation, and status emit all stubbed —
so these assertions pin the *policy* (type gate, base chain, per-status
transition), while tests/tasks/e-1801-verify.sh drives the real git side.
"""

import click
import pytest

from endless import session_cmd, worktree_cmd


class _Res:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


@pytest.fixture
def stub_recovery(monkeypatch, tmp_path):
    """Stub the git/DB side of recovery and record calls.

    Yields a dict of recorders: `recreate` (kwargs of recreate_dropped_worktree),
    `status` (args of the status emit), and knobs `branch_exists` / `rev_parse_ok`.
    """
    calls = {
        "recreate": None,
        "status": None,
        "branch_exists": False,
        "rev_parse_ok": True,
    }

    def fake_recreate(task_id, title, project_root, base, *, detached):
        calls["recreate"] = {
            "task_id": task_id, "title": title, "base": base, "detached": detached,
        }
        return tmp_path / f"e-{task_id}"

    def fake_status(task_id, title, old_status, new_status, session_id):
        calls["status"] = {
            "task_id": task_id, "old": old_status, "new": new_status,
            "session_id": session_id,
        }

    monkeypatch.setattr(worktree_cmd, "recreate_dropped_worktree", fake_recreate)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: tmp_path)
    monkeypatch.setattr(
        worktree_cmd, "_branch_exists", lambda b, r: calls["branch_exists"]
    )
    monkeypatch.setattr(worktree_cmd, "_slugify_title", lambda t: "slug")
    monkeypatch.setattr(
        worktree_cmd, "_git_run",
        lambda *a, **k: _Res(0 if calls["rev_parse_ok"] else 1),
    )
    monkeypatch.setattr(session_cmd, "_emit_recovery_status_change", fake_status)
    return calls


def _target(**over):
    base = {
        "endless_id": 99,
        "session_id": "uuid-xyz",
        "active_task_id": 10,
        "worktree_path": "",  # dropped
        "state": "ended",
        "task_type": "todo",
        "task_status": "confirmed",
        "task_title": "Recover me",
        "landed_sha": "sha-new",
    }
    base.update(over)
    return base


def _resolve(monkeypatch, target, **kw):
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: target)
    decision: dict = {}
    uuid, wt, label, eid = session_cmd._resolve_resume(
        "E-10", decision_out=decision, **kw
    )
    return decision, uuid, wt


# ── case 1: landed done task + --review → detached, status unchanged ──────────
def test_review_landed_detaches_at_latest_and_keeps_status(monkeypatch, stub_recovery):
    decision, _, _ = _resolve(
        monkeypatch, _target(), intent="review", override=".landed"
    )
    assert decision["mode"] == "detached"
    assert decision["base"] == "sha-new"
    assert decision["status_to"] is None
    assert stub_recovery["recreate"]["detached"] is True
    assert stub_recovery["status"] is None  # --review never transitions status


# ── case 2: same → --reopen → branch, status → revisit ────────────────────────
def test_reopen_done_task_branches_and_revisits(monkeypatch, stub_recovery):
    decision, _, _ = _resolve(
        monkeypatch, _target(task_status="confirmed"), intent="reopen", override=".landed"
    )
    assert decision["mode"] == "branch"
    assert stub_recovery["recreate"]["detached"] is False
    assert decision["status_to"] == "revisit"
    assert stub_recovery["status"]["old"] == "confirmed"
    assert stub_recovery["status"]["new"] == "revisit"


@pytest.mark.parametrize("status", ["assumed", "completed"])
def test_reopen_done_revisits(monkeypatch, stub_recovery, status):
    decision, _, _ = _resolve(
        monkeypatch, _target(task_status=status), intent="reopen", override=".landed"
    )
    assert decision["status_to"] == "revisit"


# ── E-1889: --reopen refuses a decision-bearing status ────────────────────────
@pytest.mark.parametrize("status", ["declined", "obsolete"])
def test_reopen_refuses_declined_obsolete_and_names_the_route(
    monkeypatch, stub_recovery, status,
):
    """These used to be silently revived to `revisit` — the one reopen route
    that reversed a deliberate decision as a side effect. Now it refuses and
    names the explicit route."""
    with pytest.raises(click.ClickException) as exc:
        _resolve(
            monkeypatch, _target(task_status=status),
            intent="reopen", override=".landed",
        )
    msg = str(exc.value)
    assert f"is '{status}'" in msg
    assert "deliberate decision" in msg
    assert "endless task update E-10 --status revisit" in msg
    assert "without --reopen" in msg
    # Refusal is total: no worktree rebuilt, no status emitted.
    assert stub_recovery["recreate"] is None
    assert stub_recovery["status"] is None


@pytest.mark.parametrize("status", ["declined", "obsolete"])
def test_review_still_allowed_on_declined_obsolete(
    monkeypatch, stub_recovery, status,
):
    """The refusal is scoped to `--reopen`. `--review` is read-only — looking
    at what was declined reverses nothing."""
    decision, _, _ = _resolve(
        monkeypatch, _target(task_status=status),
        intent="review", override=".landed",
    )
    assert decision["mode"] == "detached"
    assert decision["status_to"] is None


@pytest.mark.parametrize("status", ["declined", "obsolete"])
def test_reopen_refuses_even_when_the_worktree_survives(
    monkeypatch, stub_recovery, status, tmp_path,
):
    """The refusal is a property of the flag, not of whether recovery runs."""
    live = tmp_path / "live-wt"
    live.mkdir()
    with pytest.raises(click.ClickException) as exc:
        _resolve(
            monkeypatch,
            _target(task_status=status, worktree_path=str(live)),
            intent="reopen", override=".landed",
        )
    assert "deliberate decision" in str(exc.value)


# ── case 3: --reopen on an active/open task → restored, status unchanged ───────
@pytest.mark.parametrize(
    "status", ["underway", "unverified", "unplanned", "submitted", "ready", "revisit"]
)
def test_reopen_active_or_open_keeps_status(monkeypatch, stub_recovery, status):
    stub_recovery["branch_exists"] = True
    decision, _, _ = _resolve(
        monkeypatch, _target(task_status=status, landed_sha=""),
        intent="reopen", override=".landed",
    )
    assert decision["status_to"] is None
    assert stub_recovery["status"] is None
    assert decision["mode"] == "branch"


# ── case 4: explicit and .landed base resolution ──────────────────────────────
def test_review_explicit_ref_used_as_base(monkeypatch, stub_recovery):
    decision, _, _ = _resolve(
        monkeypatch, _target(), intent="review", override="deadbeef"
    )
    assert decision["base"] == "deadbeef"


def test_review_dotlanded_resolves_to_latest_landing(monkeypatch, stub_recovery):
    decision, _, _ = _resolve(
        monkeypatch, _target(landed_sha="sha-new"), intent="review", override=".landed"
    )
    assert decision["base"] == "sha-new"


def test_explicit_ref_that_does_not_resolve_errors(monkeypatch, stub_recovery):
    stub_recovery["rev_parse_ok"] = False
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: _target())
    with pytest.raises(click.ClickException, match="does not resolve"):
        session_cmd._resolve_resume("E-10", intent="review", override="bogus")


# ── base fallback chain: latest sha → branch tip → error ──────────────────────
def test_default_base_falls_back_to_branch_when_never_landed(monkeypatch, stub_recovery):
    stub_recovery["branch_exists"] = True
    decision, _, _ = _resolve(
        monkeypatch, _target(landed_sha=""), intent="reopen", override=".landed"
    )
    assert decision["base"] == "task/10-slug"


def test_never_landed_no_branch_errors_with_explicit_hint(monkeypatch, stub_recovery):
    stub_recovery["branch_exists"] = False
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: _target(landed_sha=""))
    with pytest.raises(click.ClickException, match="never landed"):
        session_cmd._resolve_resume("E-10", intent="reopen", override=".landed")


# ── case 5: epic refused for both intents ─────────────────────────────────────
@pytest.mark.parametrize("intent", ["review", "reopen"])
def test_epic_refused(monkeypatch, stub_recovery, intent):
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: _target(task_type="epic"))
    with pytest.raises(click.ClickException, match="epic"):
        session_cmd._resolve_resume("E-10", intent=intent, override=".landed")


# ── case 6: bare resume, worktree gone → improved error naming both flags ─────
def test_bare_resume_worktree_gone_names_both_flags(monkeypatch):
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: _target())
    with pytest.raises(click.ClickException) as exc:
        session_cmd._resolve_resume("E-10")
    msg = str(exc.value)
    assert "--review" in msg and "--reopen" in msg


# ── mutual exclusivity (resume_session surface) ───────────────────────────────
def test_review_and_reopen_mutually_exclusive(monkeypatch):
    with pytest.raises(click.ClickException, match="mutually exclusive"):
        session_cmd.resume_session("E-10", review=".landed", reopen=".landed")


# E-1918 removed the "--print-decision applies only with --review/--reopen"
# guard: the plain path already populated the same dict, so the guard was the
# only thing keeping the seam off it. The flag must now stop before the exec on
# the plain path too — asserted by a stub `claude` that fails the test if reached.
def test_dry_run_on_plain_path_prints_and_skips_exec(
    monkeypatch, capsys, tmp_path
):
    import json
    wt = tmp_path / "live"
    wt.mkdir()
    monkeypatch.setattr(
        session_cmd, "_resume_target",
        lambda ref: _target(worktree_path=str(wt)),
    )
    monkeypatch.setattr(
        session_cmd.os, "execvp",
        lambda *a: pytest.fail("--dry-run must not exec claude"),
    )
    session_cmd.resume_session("E-10", dry_run=True)
    decision = json.loads(capsys.readouterr().out)
    assert decision["uuid"] == "uuid-xyz"
    assert decision["endless_id"] == 99
    assert decision["active_task_id"] == 10
    assert decision["worktree"] == str(wt)
    assert decision["recovered"] is False
    assert decision["created_task"] is False


# ── present worktree: intent is a no-op (recovery only fires on a drop) ────────
def test_present_worktree_not_recovered(monkeypatch, stub_recovery, tmp_path):
    wt = tmp_path / "live"
    wt.mkdir()
    monkeypatch.setattr(
        session_cmd, "_resume_target",
        lambda ref: _target(worktree_path=str(wt)),
    )
    decision: dict = {}
    session_cmd._resolve_resume("E-10", intent="review", override=".landed", decision_out=decision)
    assert decision["recovered"] is False
    assert stub_recovery["recreate"] is None
