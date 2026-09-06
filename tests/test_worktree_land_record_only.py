"""Tests for E-1719: the `worktree land --record-only` wiring.

_record_only_landing records a historical landing that already happened, without
any git rebase/ff-merge. These tests mock the collaborators (_project_root,
_resolve_project, _git, emit_event) so they assert the wiring — actor/session and
the commit-date-derived timestamp — with no git/DB fixture.

E-2108 removed the `--branch` this used to carry: a landing records no branch at
all now, so the payload is (merge_commit_sha) and nothing else.
"""

from pathlib import Path

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import _record_only_landing


@pytest.fixture
def wired(monkeypatch):
    """Stub the collaborators and capture the emit_event call."""
    calls = {"emit": [], "git": []}
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: Path("/tmp/proj"))
    monkeypatch.setattr(worktree_cmd, "_resolve_project", lambda _: (1, "endless"))

    def fake_git(args, cwd):
        calls["git"].append((args, cwd))
        return "2026-05-09T18:30:00-04:00"  # %cI committer date

    monkeypatch.setattr(worktree_cmd, "_git", fake_git)
    monkeypatch.setattr(
        "endless.event_bridge.emit_event", lambda **kw: calls["emit"].append(kw)
    )
    return calls


def test_record_only_derives_at_from_commit_date(wired):
    _record_only_landing("E-1209", "6671bca9", None, dry_run=False)
    # Derived --at via `git show -s --format=%cI`.
    assert wired["git"], "expected a git call to read the commit date"
    assert wired["git"][0][0] == ["show", "-s", "--format=%cI", "6671bca9"]
    assert len(wired["emit"]) == 1
    kw = wired["emit"][0]
    assert kw["kind"] == "task.landed"
    assert kw["entity_id"] == "1209"
    assert kw["actor_kind"] == "system"
    assert kw["actor_id"] == "backfill"
    assert kw["session_id"] is None
    assert kw["ts"] == "2026-05-09T18:30:00-04:00"
    # The whole payload: no branch key, because no column reads one (E-2108).
    assert kw["payload"] == {"merge_commit_sha": "6671bca9"}


def test_record_only_explicit_at_skips_git(wired):
    _record_only_landing("E-1209", "6671bca9", "2026-01-02T03:04:05Z", dry_run=False)
    assert wired["git"] == [], "explicit --at must not shell out for the commit date"
    assert wired["emit"][0]["ts"] == "2026-01-02T03:04:05Z"


def test_record_only_requires_sha(wired):
    with pytest.raises(click.ClickException) as exc:
        _record_only_landing("E-1209", None, None, dry_run=False)
    assert "--sha" in exc.value.message
    assert wired["emit"] == []


def test_record_only_dry_run_emits_nothing(wired):
    _record_only_landing("E-1209", "6671bca9", None, dry_run=True)
    assert wired["emit"] == [], "dry-run must not emit"
