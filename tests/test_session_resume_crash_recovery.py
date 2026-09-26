"""`session resume` can re-enter a window tmux-resurrect restored (E-2168).

A tmux crash is recoverable: tmux-resurrect restores every window's name,
index, layout, panes and working directories. It does NOT restore the
`@endless_*` window options — its save format carries only `pane`, `window`,
`state` and `grouped_session` lines, and a real save file contains zero
`@endless` strings. Nothing on our side heals the gap either:
`@endless_session_uuid` self-heals on every hook event, but
`@endless_task_id` has no self-heal and no unset.

So a restored window comes back crowded with panes holding dead shells, while
its task claim is stale or absent — and `session resume` refused on both
grounds, routing to `session goto --resume`, which opens a NEW window and
abandons the restored one, losing the layout and scrollback the restore just
recovered.

Two flags, each naming exactly one thing it permits, so a run that needed only
one does not silently waive the other:

    endless session resume E-NNNN --rebind --no-sibling-panes

`--rebind` rewrites the WINDOW's `@endless_task_id`, never `sessions.task_id`
— write-once under ED-1560 and enforced by a trigger. That is not a limitation
worked around, it is why the flag is safe, so these pin it. `--rebind` has one
refusal of its own: another LIVE window still claiming the target task would
put two windows on one task in parallel, which the `tmux window == Endless task
== sessions, in SERIES` invariant forbids.
"""

import click
import pytest

from endless import db, session_cmd


class _Exec(Exception):
    """Raised by the stub execvp so a test can stop where the real one would."""


class _Res:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


def _target(**over):
    base = {
        "endless_id": 99,
        "session_id": "uuid-xyz",
        "task_id": 10,
        "project_id": 3,
        "worktree_path": "",
        "state": "ended",
        "task_type": "todo",
        "task_status": "underway",
        "task_title": "Resume me",
        "landed_sha": "",
    }
    base.update(over)
    return base


# `%42` is this pane, in window `@1`. Any other pane answers window `@9`, so a
# test can put a live session in "another window" without naming tmux ids.
_HERE_PANE = "%42"
_HERE_WINDOW = "@1"


@pytest.fixture
def crash(monkeypatch, tmp_path, stage_transcript):
    """Drive `resume_session` against a simulated restored window.

    Yields a `_Crash` whose attributes set the scene — `panes` (what the
    restore brought back), `window_task` (the stale `@endless_task_id`, `""`
    for the option tmux never restored) and `live` (rows the liveness helper
    reports) — and whose `trace` records what the run did to the outside world.
    """
    wt = tmp_path / "wt"
    wt.mkdir()

    class _Crash:
        panes = [_HERE_PANE]
        window_task = ""
        live: list[dict] = []
        trace: list[tuple] = []

        @property
        def options(self) -> dict[str, str]:
            """The `@endless_*` window options the run wrote."""
            return {
                a[4]: a[5] for k, a in self.trace
                if k == "tmux" and a[:2] == ["set-option", "-w"]
            }

        @property
        def laid_out(self) -> bool:
            return any(k == "layout" for k, _ in self.trace)

        def resume(self, ref="E-10", **kw):
            self.trace = []
            return session_cmd.resume_session(ref, **kw)

    scene = _Crash()
    stage_transcript("uuid-xyz")
    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    monkeypatch.setenv("TMUX_PANE", _HERE_PANE)

    def fake_tmux(args, timeout=2.0):
        scene.trace.append(("tmux", list(args)))
        if args[:1] == ["display-message"]:
            fmt = args[-1]
            pane = args[args.index("-t") + 1] if "-t" in args else _HERE_PANE
            if fmt == "#{@endless_task_id}":
                return _Res(stdout=f"{scene.window_task}\n")
            if fmt.startswith("#{window_id}"):
                if pane == _HERE_PANE:
                    return _Res(stdout=f"{_HERE_WINDOW} main:1 E-10-resume-me\n")
                return _Res(stdout="@9 main:9 E-10-elsewhere\n")
        return _Res()

    def fake_exec(file, argv):
        scene.trace.append(("exec", list(argv)))
        raise _Exec()

    monkeypatch.setattr(session_cmd, "_tmux_run", fake_tmux)
    monkeypatch.setattr(session_cmd.os, "execvp", fake_exec)
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/bin/claude")
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: None)
    monkeypatch.setattr(
        session_cmd, "_tmux_window_pane_ids", lambda: list(scene.panes)
    )
    monkeypatch.setattr(
        session_cmd, "_resume_target", lambda ref: _target(worktree_path=str(wt))
    )
    monkeypatch.setattr(
        session_cmd, "_try_resume_target",
        lambda ref: _target(worktree_path=str(wt)),
    )
    monkeypatch.setattr(
        session_cmd, "_live_sessions", lambda root: list(scene.live)
    )
    monkeypatch.setattr(
        session_cmd, "_project_root_for_cwd", lambda: tmp_path
    )
    monkeypatch.setattr(
        session_cmd, "build_pane_layout",
        lambda pane, cwd: scene.trace.append(("layout", [pane, cwd])),
    )
    return scene


# ── `--no-sibling-panes`: the panes the restore brought back ───────────────

def test_a_crowded_window_names_the_flag(crash):
    """The refusal has to name the route, because the failure is invisible
    until it happens and `session goto --resume` is the wrong one here."""
    crash.panes = [_HERE_PANE, "%43", "%44"]

    with pytest.raises(click.ClickException) as exc:
        crash.resume()

    msg = str(exc.value)
    assert "3 panes" in msg
    assert "endless session resume E-10 --no-sibling-panes" in msg
    # The sibling verb is still offered — it is right when the panes are yours.
    assert "endless session goto E-10 --resume" in msg


def test_no_sibling_panes_proceeds(crash):
    crash.panes = [_HERE_PANE, "%43", "%44"]

    with pytest.raises(_Exec):
        crash.resume(no_sibling_panes=True)


def test_no_sibling_panes_still_lays_the_window_out(crash):
    """The layout is not skipped. A recovered window should come back looking
    like a spawned one — that is the point of recovering it rather than opening
    a new one — so the flag waives the refusal, not the layout."""
    crash.panes = [_HERE_PANE, "%43", "%44"]

    with pytest.raises(_Exec):
        crash.resume(no_sibling_panes=True)

    assert crash.laid_out
    kinds = [k for k, _ in crash.trace]
    assert kinds.index("layout") < kinds.index("exec")


def test_a_lone_pane_never_needed_the_flag(crash):
    with pytest.raises(_Exec):
        crash.resume()


# ── `--rebind`: the task claim the restore did not bring back ──────────────

def test_a_stale_claim_names_the_flag(crash):
    crash.window_task = "999"

    with pytest.raises(click.ClickException) as exc:
        crash.resume()

    msg = str(exc.value)
    assert "E-999" in msg                 # what the window claims
    assert "E-10" in msg                  # what resuming would bind it to
    assert "endless session resume E-10 --rebind" in msg
    assert "endless session goto E-10 --resume" in msg


def test_rebind_rewrites_the_window_option(crash):
    crash.window_task = "999"

    with pytest.raises(_Exec):
        crash.resume(rebind=True)

    assert crash.options["@endless_task_id"] == "10"
    assert crash.options["@endless_project_id"] == "3"


def test_a_window_claiming_the_target_needs_no_flag(crash):
    """The ordinary recovery: the pane's Claude died, the window still carries
    the task. Re-entering where you already are is not a rebind (E-2112)."""
    crash.window_task = "10"

    with pytest.raises(_Exec):
        crash.resume()


def test_an_unset_claim_needs_no_flag(crash):
    """tmux renders an unset window option as an empty line, and a window
    carrying no claim is the plain recovery shell resume is most often run
    from. It must stay frictionless."""
    crash.window_task = ""

    with pytest.raises(_Exec):
        crash.resume()


def test_a_non_numeric_claim_needs_no_flag(crash):
    """Nothing to disagree with is nothing to gate on, whatever the shape."""
    crash.window_task = "not-a-task"

    with pytest.raises(_Exec):
        crash.resume()


def test_the_two_flags_do_not_imply_each_other(crash):
    """Verbose on purpose: a run that needed only one must not silently waive
    the other. This is the crash case end to end — stale AND crowded."""
    crash.panes = [_HERE_PANE, "%43", "%44"]
    crash.window_task = "999"

    with pytest.raises(click.ClickException) as exc:
        crash.resume(no_sibling_panes=True)
    assert "--rebind" in str(exc.value)

    with pytest.raises(click.ClickException) as exc:
        crash.resume(rebind=True)
    assert "--no-sibling-panes" in str(exc.value)

    with pytest.raises(_Exec):
        crash.resume(rebind=True, no_sibling_panes=True)


# ── `--rebind`'s own refusal: a LIVE window already has the target ─────────

def test_rebind_refuses_a_live_window_on_the_target(crash):
    """`tmux window == Endless task == sessions` holds in SERIES, never in
    parallel. The refusal names the window and the session so the user can go
    to the one that already has it."""
    crash.window_task = "999"
    crash.live = [
        {"task_id": 10, "pane_id": "%77", "endless_session_id": 1234},
    ]

    with pytest.raises(click.ClickException) as exc:
        crash.resume(rebind=True)

    msg = str(exc.value)
    assert "main:9" in msg                       # names the window
    assert "1234" in msg                         # names the session
    assert "endless session goto E-10" in msg


def test_rebind_allows_a_live_session_in_this_window(crash):
    """A session in THIS window is not a parallel claim — it is the one being
    re-entered. Excluded by window id, not pane id: resume is routinely run
    from a sibling shell pane, so the pane asking is not the pane the session
    sits in while the window is the same."""
    crash.window_task = "999"
    crash.live = [
        {"task_id": 10, "pane_id": _HERE_PANE, "endless_session_id": 1234},
    ]

    with pytest.raises(_Exec):
        crash.resume(rebind=True)


def test_rebind_allows_a_dead_claim(crash):
    """Liveness is DERIVED, never read off a `state` column (E-1898), so the
    candidate set is what the liveness helper reports. A claim left behind by a
    dead session must not block the recovery the flag exists for."""
    crash.window_task = "999"
    crash.live = [
        # A live session, but on some other task — not a competing claim.
        {"task_id": 4242, "pane_id": "%77", "endless_session_id": 1234},
    ]

    with pytest.raises(_Exec):
        crash.resume(rebind=True)


# ── What neither flag touches ─────────────────────────────────────────────

def test_neither_flag_waives_force(crash, monkeypatch):
    """`--force` governs replacing LIVE work in this pane — a different
    decision, and not one a recovery flag gets to make."""
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: (70, 1958))
    crash.panes = [_HERE_PANE, "%43"]
    crash.window_task = "999"

    with pytest.raises(click.ClickException) as exc:
        crash.resume(rebind=True, no_sibling_panes=True)

    msg = str(exc.value)
    assert "E-1958" in msg
    assert "--force" in msg


def test_dry_run_is_gated_by_neither(crash, capsys):
    """--dry-run stops short of the exec, so it rewrites no window option and
    resizes no pane. Nothing to permit means nothing to refuse."""
    crash.panes = [_HERE_PANE, "%43", "%44"]
    crash.window_task = "999"

    crash.resume(dry_run=True)

    assert crash.options == {}
    capsys.readouterr()


def test_the_gates_refuse_before_target_resolution(crash, monkeypatch):
    """`_resolve_resume` can mint a container task and a worktree, so a
    refusal must land before it — the ordering E-1968 established. The gate
    reads the target through the read-only `resume-target` query instead."""
    def _boom(*a, **kw):
        pytest.fail("resume resolved a target past the window-claim gate")

    monkeypatch.setattr(session_cmd, "_resolve_resume", _boom)
    crash.window_task = "999"

    with pytest.raises(click.ClickException):
        crash.resume()


def test_an_unresolvable_ref_is_not_gated(crash, monkeypatch):
    """A gate has no business guessing at a diagnostic `_resolve_resume` is
    about to produce properly."""
    monkeypatch.setattr(session_cmd, "_try_resume_target", lambda ref: None)
    crash.window_task = "999"

    with pytest.raises(_Exec):
        crash.resume()


def test_a_taskless_target_is_gated(crash, monkeypatch):
    """Resolution would MINT a task for a session that never claimed one, so
    the window's claim really is about to be overwritten with a different task.
    That is exactly what `--rebind` names."""
    monkeypatch.setattr(
        session_cmd, "_try_resume_target",
        lambda ref: _target(task_id=None, worktree_path=""),
    )
    crash.window_task = "999"

    with pytest.raises(click.ClickException) as exc:
        crash.resume()
    assert "--rebind" in str(exc.value)


def test_outside_tmux_nothing_is_gated(crash, monkeypatch):
    """Recovery from a bare shell outside tmux still resumes: there is no
    window to hold a stale claim and none to be crowded."""
    monkeypatch.delenv("TMUX_PANE")
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: None)
    crash.window_task = "999"

    with pytest.raises(_Exec):
        crash.resume()


# ── The invariant `--rebind` must not breach ───────────────────────────────

def test_rebind_never_writes_the_session_task_binding(crash, monkeypatch):
    """`sessions.task_id` is write-once (ED-1560, enforced by the
    `sessions_task_id_write_once` trigger). `--rebind` rewrites the WINDOW's
    claim and nothing else, which is why it cannot breach that invariant
    rather than merely happening not to. Proven by the absence of any write at
    all on the rebind path."""
    writes: list[str] = []
    real_execute = db.execute
    monkeypatch.setattr(
        db, "execute",
        lambda sql, *a, **kw: (writes.append(sql), real_execute(sql, *a, **kw))[1],
    )
    crash.window_task = "999"

    with pytest.raises(_Exec):
        crash.resume(rebind=True)

    assert writes == []
    # The claim moved on the window, and only there.
    assert crash.options["@endless_task_id"] == "10"
