"""Tests for E-1474: a task.landed emit failure AFTER the ff-merge is surfaced
as a re-runnable "main was advanced, recording failed" error, not as a total
land failure (which would imply nothing landed).

_record_landing runs in land_worktree() Step 6, after Step 5's ff-merge has
already advanced main. It mocks emit_event rather than driving a full land, so
no git/DB fixture is needed — the behavior under test is the error wrapping.
"""

import click
import pytest

from endless.worktree_cmd import _record_landing


def _args():
    return dict(
        item_id=1474,
        proj_name="endless",
        branch="task/1474-x",
        base_branch="main",
        canonical="E-1474",
        merge_sha="deadbeef",
    )


def test_record_landing_success(monkeypatch):
    calls = []
    monkeypatch.setattr(
        "endless.event_bridge.emit_event", lambda **kw: calls.append(kw)
    )
    _record_landing(**_args())  # must not raise
    assert len(calls) == 1
    assert calls[0]["kind"] == "task.landed"
    assert calls[0]["entity_id"] == "1474"
    assert calls[0]["payload"]["merge_commit_sha"] == "deadbeef"


def test_record_landing_clickexception_is_recoverable(monkeypatch):
    # Simulate the E-1470 attribution gate raising during the emit.
    def boom(**kw):
        raise click.ClickException(
            "Cannot determine the Endless session for this pane."
        )

    monkeypatch.setattr("endless.event_bridge.emit_event", boom)
    with pytest.raises(click.ClickException) as exc:
        _record_landing(**_args())
    msg = exc.value.message
    assert "main was advanced" in msg          # the land DID happen
    assert "just land E-1474" in msg           # how to recover
    assert "ff-merge is idempotent" in msg
    assert "Cannot determine the Endless session" in msg  # original cause kept


def test_record_landing_generic_exception_is_recoverable(monkeypatch):
    def boom(**kw):
        raise ValueError("ledger write blew up")

    monkeypatch.setattr("endless.event_bridge.emit_event", boom)
    with pytest.raises(click.ClickException) as exc:
        _record_landing(**_args())
    msg = exc.value.message
    assert "main was advanced" in msg
    assert "ledger write blew up" in msg
    assert "just land E-1474" in msg
