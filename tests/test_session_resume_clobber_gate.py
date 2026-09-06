"""`session resume` refuses to clobber a pane holding live work (E-1968 §2).

`session resume` execs `claude --resume` in place, replacing whatever runs in
the current pane. When that pane's session has a task bound, that is destroying
live context, silently, as a side effect of navigating. So it refuses without
--force and points at `session goto <ref> --resume`, which exists precisely to
open the target in a NEW window instead.

The gate runs BEFORE target resolution, because resolution is not read-only: it
can mint a container task and a worktree for a task-less session. A refusal must
not leave those behind.

E-2112 exempted self-resume: when the target's task IS the task the pane holds,
nothing else is there to protect, and the refusal used to name the same task
twice and route you to a new window for the task you were already in. The
exemption reads the target through the read-only `resume-target` query, so the
gate still resolves nothing it would have to clean up, and anything that query
cannot settle keeps the refusal.
"""

import click
import pytest

from endless import db, session_cmd


def _insert_session(*, pk, session_id, project_id, task_id=None):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, task_id) "
        "VALUES (?, ?, ?, 'claude', 'working', '2026-08-15T00:00:00', ?)",
        (pk, session_id, project_id, task_id),
    )


def _insert_task(*, pk, project_id, status="underway"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) "
        "VALUES (?, ?, 'held task', ?)",
        (pk, project_id, status),
    )


@pytest.fixture
def project_id(seeded_project_at_cwd):
    return db.query(
        "SELECT id FROM projects WHERE path = ?",
        (str(seeded_project_at_cwd),),
    )[0]["id"]


@pytest.fixture
def pane_holding(monkeypatch, project_id):
    """Make the current pane resolve to a session bound to E-1958."""
    _insert_task(pk=1958, project_id=project_id)
    _insert_session(pk=70, session_id="s-70", project_id=project_id,
                    task_id=1958)
    monkeypatch.setattr(
        "endless.task_cmd._current_endless_session_id", lambda: 70,
    )


@pytest.fixture
def pane_empty(monkeypatch, project_id):
    """Current pane resolves to a session holding no task."""
    _insert_session(pk=71, session_id="s-71", project_id=project_id)
    monkeypatch.setattr(
        "endless.task_cmd._current_endless_session_id", lambda: 71,
    )


@pytest.fixture
def no_resolution(monkeypatch):
    """Fail loudly if the gate lets the call reach target resolution."""
    def _boom(*a, **kw):
        pytest.fail("resume resolved a target past the clobber gate")

    monkeypatch.setattr(session_cmd, "_resolve_resume", _boom)


def test_refuses_when_the_pane_holds_a_task(pane_holding, no_resolution):
    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-1859")

    msg = str(exc.value)
    assert "E-1958" in msg                      # names what would be destroyed
    assert "endless session goto E-1859 --resume" in msg
    assert "--force" in msg


def test_force_proceeds(pane_holding, monkeypatch):
    seen = {}

    def fake_resolve(ref, **kw):
        seen["ref"] = ref
        raise click.ClickException("reached resolution")

    monkeypatch.setattr(session_cmd, "_resolve_resume", fake_resolve)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-1859", force=True)
    assert "reached resolution" in str(exc.value)
    assert seen["ref"] == "E-1859"


def test_pane_without_a_task_is_not_gated(pane_empty, monkeypatch):
    """The common recovery case — running from a plain shell after a tmux
    crash — must stay frictionless."""
    def fake_resolve(ref, **kw):
        raise click.ClickException("reached resolution")

    monkeypatch.setattr(session_cmd, "_resolve_resume", fake_resolve)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-1859")
    assert "reached resolution" in str(exc.value)


def test_unresolvable_pane_is_not_gated(monkeypatch, seeded_project_at_cwd):
    """A pane whose session cannot be resolved is treated as empty. The gate
    exists to stop an accidental replacement of live work, not to block a
    recovery run from a shell Endless has never heard of."""
    monkeypatch.setattr(
        "endless.task_cmd._current_endless_session_id", lambda: None,
    )

    def fake_resolve(ref, **kw):
        raise click.ClickException("reached resolution")

    monkeypatch.setattr(session_cmd, "_resolve_resume", fake_resolve)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-1859")
    assert "reached resolution" in str(exc.value)


def test_dry_run_is_not_gated(pane_holding, monkeypatch, capsys):
    """--dry-run stops short of the exec, so it replaces nothing and needs no
    --force."""
    monkeypatch.setattr(
        session_cmd, "_resolve_resume",
        lambda ref, **kw: ("uuid-x", "/tmp/wt", "E-1859", 99),
    )

    session_cmd.resume_session("E-1859", dry_run=True)
    assert "{" in capsys.readouterr().out


# ── E-2112: self-resume ─────────────────────────────────────────────────────

@pytest.fixture
def target(monkeypatch):
    """Stub the read-only resume-target query the self-resume check consults.

    Returns a setter taking the target's task id — `None` for a session that
    never claimed one, and `False` for a ref that does not resolve at all.
    """
    def _set(task_id):
        if task_id is False:
            monkeypatch.setattr(
                session_cmd, "_resume_target",
                lambda ref: (_ for _ in ()).throw(click.ClickException("no such ref")),
            )
            return
        monkeypatch.setattr(
            session_cmd, "_resume_target",
            lambda ref: {"endless_id": 88, "session_id": "s-88",
                         "task_id": task_id, "worktree_path": "/tmp/wt"},
        )
    return _set


@pytest.fixture
def resolution_reached(monkeypatch):
    """Make reaching target resolution observable as a distinct exception."""
    def fake_resolve(ref, **kw):
        raise click.ClickException("reached resolution")

    monkeypatch.setattr(session_cmd, "_resolve_resume", fake_resolve)


def test_self_resume_of_the_held_task_is_not_gated(
    pane_holding, target, resolution_reached
):
    """The pane holds E-1958 and the target IS E-1958 — re-entering the task
    you are already on replaces nothing, so no --force."""
    target(1958)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-1958")
    assert "reached resolution" in str(exc.value)


def test_a_resolvable_different_task_still_refuses(
    pane_holding, target, no_resolution
):
    """The exemption is equality, not presence: a target that resolves to
    OTHER work is exactly what the gate was built for."""
    target(1859)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-1859")
    assert "E-1958" in str(exc.value)


def test_task_less_target_still_refuses(pane_holding, target, no_resolution):
    """A target session holding no task is not the held task — resolution
    would mint a container task for it, which is other work by definition."""
    target(None)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("ES-88")
    assert "E-1958" in str(exc.value)


def test_unresolvable_target_still_refuses(pane_holding, target, no_resolution):
    """A ref the query cannot settle keeps the refusal: the gate must not fall
    open on a typo."""
    target(False)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-nope")
    assert "E-1958" in str(exc.value)


def test_self_resume_reaches_the_exec(pane_holding, target, monkeypatch, capsys):
    """End to end past the gate: a self-resume execs `claude --resume` in the
    pane instead of raising."""
    target(1958)
    monkeypatch.setattr(
        session_cmd, "_resolve_resume",
        lambda ref, **kw: ("uuid-1958", "/tmp/wt", "E-1958", 70),
    )
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/bin/claude")
    monkeypatch.setattr(session_cmd, "_bind_pane_window_options",
                        lambda *a, **kw: None)
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)
    execed = {}
    monkeypatch.setattr(session_cmd.os, "execvp",
                        lambda f, a: execed.update(file=f, args=a))

    session_cmd.resume_session("E-1958")

    assert execed == {"file": "/bin/claude",
                      "args": ["claude", "--resume", "uuid-1958"]}
    assert "Resuming session 70" in capsys.readouterr().err
