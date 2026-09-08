"""What `task claim` and `task spawn` print, and what they no longer print (E-1428).

Two defects, one shape. Claim listed the per-worktree SANDBOX cache directory
as a bullet directly above the worktree path, so a reader scanning for
somewhere to `cd` took the first path they saw and landed in a directory that
is not a project — after which every endless command failed with "Not in a
registered project directory". And the block's labels were ragged, so the one
value anyone was scanning for started at a different column on every line.

What replaces them: aligned labels, no path that is not a `cd` target, and the
sandbox reachable on demand through `endless worktree sandbox` for the one case
(pointing a SQL client at a worktree's database) that wants it.

The session id is deliberately NOT a labeled row here. It printed a bare
integer in an id space that renders `ES-N` everywhere else, above the two lines
anyone acts on, and on the dominant path — a Claude session claiming its own
task — it restated the caller's own identity. It survives on exactly one path,
in the `session goto ES-N` line, which is the one place the binding is news:
a shell that bound a Claude session in ANOTHER pane.
"""

import re
import subprocess
import types

import pytest

from endless import db, task_cmd


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def project_with_task(seeded_project_at_cwd):
    """A git repo, a registered project at cwd, one `ready` task."""
    repo = seeded_project_at_cwd
    _run(["git", "init", "-q", "-b", "main"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("hi\n")
    _run(["git", "add", "README.md"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)

    proj_id = db.query(
        "SELECT id FROM projects WHERE path = ?", (str(repo),)
    )[0]["id"]
    title = "Rework the claim output"
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, ?, ?, 'ready', 0, "
        "datetime('now'), datetime('now'))",
        (proj_id, title, title),
    )
    task_id = db.query(
        "SELECT id FROM tasks WHERE title = ?", (title,)
    )[0]["id"]
    return {"project_root": repo, "task_id": task_id, "title": title}


def _claim(monkeypatch, capsys, *, session, in_claude, tmux=False, unattended=False,
           task_id):
    """Run a claim with the caller's identity pinned, and return its stdout."""
    monkeypatch.setattr(
        task_cmd, "_resolve_session_id_with_prompt", lambda **kw: session,
    )
    monkeypatch.setattr(task_cmd, "_in_claude_session", lambda: in_claude)
    if tmux:
        monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    task_cmd.claim_item(task_id, unattended=unattended)
    return capsys.readouterr().out


# ── The block itself ────────────────────────────────────────────────────────

def test_the_cache_path_is_not_printed(project_with_task, monkeypatch, capsys):
    """The defect. A cache directory is not a `cd` target, and it was printed
    like one — as a bullet immediately above the path that is."""
    out = _claim(
        monkeypatch, capsys, session=None, in_claude=False, unattended=True,
        task_id=project_with_task["task_id"],
    )
    assert "provisioned" not in out
    assert "/sandboxes/" not in out
    assert ".cache" not in out


def test_labels_are_aligned_and_the_worktree_is_the_only_path(
    project_with_task, monkeypatch, capsys,
):
    """Values start at one column, whatever the label's length."""
    tid = project_with_task["task_id"]
    out = _claim(
        monkeypatch, capsys, session=None, in_claude=False, unattended=True,
        task_id=tid,
    )
    rows = [ln for ln in out.splitlines() if ln.startswith("  • ")]
    assert len(rows) == 2, out
    # Strip the bullet, then the value column must be identical across rows.
    columns = {ln.index(":") for ln in rows}
    assert len(columns) == 2  # labels differ in length ...
    values = {len(ln) - len(ln[ln.index(":") + 1:].lstrip()) for ln in rows}
    assert len(values) == 1, rows  # ... but their values do not.

    assert rows[0].startswith("  • Status:")
    assert "ready -> underway" in rows[0]
    assert rows[1].startswith("  • Git worktree:")
    wt = str(project_with_task["project_root"] / ".endless" / "worktrees" / f"e-{tid}")
    assert rows[1].endswith(wt)


def test_header_then_a_blank_line_then_the_rows(
    project_with_task, monkeypatch, capsys,
):
    tid = project_with_task["task_id"]
    out = _claim(
        monkeypatch, capsys, session=None, in_claude=False, unattended=True,
        task_id=tid,
    )
    lines = out.splitlines()
    assert lines[0] == f"Updated E-{tid} (Rework the claim output):"
    assert lines[1] == ""
    assert lines[2].startswith("  • Status:")


def test_provisioning_a_sandbox_is_silent(tmp_path, monkeypatch, capsys):
    """The removal itself, on the path that used to print. A non-self-dev
    project never provisions a sandbox at all, so asserting on a claim there
    would prove nothing — this drives the provisioner directly, with the
    project marked self-dev and both `endless-go sandbox` calls succeeding."""
    from endless import config, worktree_cmd

    root = tmp_path / "proj"
    (root / ".endless").mkdir(parents=True)
    (root / ".endless" / "config.json").write_text('{"self_dev": true}\n')
    wt = root / ".endless" / "worktrees" / "e-42"
    wt.mkdir(parents=True)
    assert config.project_is_self_dev(root)

    calls: list[list[str]] = []
    monkeypatch.setattr(worktree_cmd.shutil, "which", lambda n: "/bin/endless-go")
    monkeypatch.setattr(
        worktree_cmd.subprocess, "run",
        lambda cmd, **kw: (
            calls.append(list(cmd)),
            types.SimpleNamespace(returncode=0, stdout="", stderr=""),
        )[1],
    )
    worktree_cmd._maybe_auto_sandbox_bind(root, wt, 42)

    assert [c[1:3] for c in calls] == [["sandbox", "init"], ["sandbox", "bind"]]
    captured = capsys.readouterr()
    assert captured.out == ""
    assert captured.err == ""


# ── The session id, on each of the four caller paths ────────────────────────

def test_a_claude_session_claiming_its_own_task_gets_cd_and_no_session_id(
    project_with_task, monkeypatch, capsys,
):
    """The dominant path. The session is already here; only its working
    directory is in the wrong place. Naming the session would restate the
    caller's own identity."""
    tid = project_with_task["task_id"]
    out = _claim(
        monkeypatch, capsys, session=7, in_claude=True, task_id=tid,
    )
    wt = str(project_with_task["project_root"] / ".endless" / "worktrees" / f"e-{tid}")
    assert f"/cd {wt}" in out
    assert "bound to session" not in out
    assert "Session:" not in out
    assert "ES-7" not in out


def test_a_shell_that_bound_a_sibling_names_the_session_once_as_ES_N(
    project_with_task, monkeypatch, capsys,
):
    """The one path where the binding is news: claim bound a Claude session in
    ANOTHER pane, and under ED-1560 `sessions.task_id` is write-once, so a
    wrong binding cannot be undone. The id appears exactly once, in the
    navigator's own spelling — `session goto` refuses a bare integer."""
    tid = project_with_task["task_id"]
    out = _claim(
        monkeypatch, capsys, session=7, in_claude=False, task_id=tid,
    )
    assert "endless session goto ES-7" in out
    assert "bound to session 7" not in out
    # One SPELLING, not one occurrence. E-2106's stanza names the session in
    # its sentence and again in the command it hands you, which is the same
    # `ES-N` twice on adjacent lines. What was wrong before was the id
    # appearing in TWO spellings four lines apart — as a bare `7` in a row of
    # its own, and as `ES-7` down here — when `session goto` refuses the bare
    # form outright.
    assert not re.search(r"session 7\b", out)


def test_a_shell_that_bound_nothing_starts_a_session_and_names_none(
    project_with_task, monkeypatch, capsys,
):
    """A shell cannot become the session, so one is launched. It has no id yet
    — its window name is its identity."""
    tid = project_with_task["task_id"]
    calls: list[list[str]] = []
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "/bin/claude")
    real_run = task_cmd.subprocess.run

    def fake_run(argv, **kw):
        if argv and "spawn-window" in argv:
            calls.append(list(argv))
            return types.SimpleNamespace(returncode=0)
        return real_run(argv, **kw)

    monkeypatch.setattr(task_cmd.subprocess, "run", fake_run)
    # Only `claude` is faked: `_resolve_endless_go` uses the same `which`, and
    # a fake answer there points the event pipeline at a binary that is not
    # there.
    real_which = task_cmd.shutil.which
    monkeypatch.setattr(
        task_cmd.shutil, "which",
        lambda n, *a, **kw: "/bin/claude" if n == "claude" else real_which(n, *a, **kw),
    )
    out = _claim(
        monkeypatch, capsys, session=None, in_claude=False, tmux=True, task_id=tid,
    )
    assert calls, "no session was launched"
    assert f"Started Claude on E-{tid} in window" in out
    assert f"      tmux select-window -t E-{tid}" in out.splitlines()
    assert "ES-" not in out
    assert "bound to session" not in out


def test_unattended_says_nothing_further(project_with_task, monkeypatch, capsys):
    """The caller said there is no Claude session and wants none, so the
    worktree path is the whole answer."""
    tid = project_with_task["task_id"]
    out = _claim(
        monkeypatch, capsys, session=None, in_claude=False, unattended=True,
        task_id=tid,
    )
    assert "/cd" not in out
    assert "session goto" not in out
    assert "Started Claude" not in out
    assert "ES-" not in out


# ── Re-claiming a task this session already owns ────────────────────────────

def test_a_reclaim_by_the_owner_reports_in_the_same_shape_and_still_routes(
    project_with_task, monkeypatch, capsys,
):
    """This path used to print a bare session id — the caller's own — and then
    return without telling a Claude session how to get into the worktree it had
    just been handed."""
    tid = project_with_task["task_id"]
    _claim(monkeypatch, capsys, session=7, in_claude=True, task_id=tid)
    monkeypatch.setattr(task_cmd, "_check_task_ownership", lambda *a, **kw: True)
    out = _claim(monkeypatch, capsys, session=7, in_claude=True, task_id=tid)

    wt = str(project_with_task["project_root"] / ".endless" / "worktrees" / f"e-{tid}")
    assert f"E-{tid} (Rework the claim output) is already claimed by this session:" in out
    assert f"  • Git worktree: {wt}" in out
    assert "already active in session" not in out
    assert f"/cd {wt}" in out


# ── task spawn ──────────────────────────────────────────────────────────────

def test_spawn_prints_the_same_aligned_rows_and_always_says_where_it_landed(
    project_with_task, monkeypatch, capsys,
):
    """Deliverable C: the same discipline on the sibling verb."""
    tid = project_with_task["task_id"]
    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "/bin/claude")
    real_run = task_cmd.subprocess.run

    def fake_run(argv, **kw):
        if argv and "spawn-window" in argv:
            return types.SimpleNamespace(returncode=0)
        return real_run(argv, **kw)

    # Only `claude` is faked: `_resolve_endless_go` uses the same `which`, and
    # a fake answer there points the event pipeline at a binary that is not
    # there.
    real_which = task_cmd.shutil.which
    monkeypatch.setattr(
        task_cmd.shutil, "which",
        lambda n, *a, **kw: "/bin/claude" if n == "claude" else real_which(n, *a, **kw),
    )
    monkeypatch.setattr(task_cmd.subprocess, "run", fake_run)
    task_cmd.spawn_plan(tid)
    out = capsys.readouterr().out

    wt = str(project_with_task["project_root"] / ".endless" / "worktrees" / f"e-{tid}")
    assert f"Spawned a Claude session on E-{tid}:" in out
    assert f"  • Window: E-{tid}" in out.splitlines()
    assert "  Switch to it:" in out.splitlines()
    assert f"      tmux select-window -t E-{tid}" in out.splitlines()
    # The worktree is named once, by the claim block above. `Session cwd` is
    # for the `--worktree` override, and this spawn did not override.
    assert "Session cwd" not in out
    assert out.count(wt) == 1
    assert "/sandboxes/" not in out


def test_spawn_names_the_session_cwd_when_worktree_overrides_it(
    project_with_task, monkeypatch, capsys, tmp_path,
):
    """The case `Session cwd` exists for: the session lands somewhere other
    than the worktree the block above named, so the two paths differ."""
    tid = project_with_task["task_id"]
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()
    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "/bin/claude")
    real_run = task_cmd.subprocess.run

    def fake_run(argv, **kw):
        if argv and "spawn-window" in argv:
            return types.SimpleNamespace(returncode=0)
        return real_run(argv, **kw)

    real_which = task_cmd.shutil.which
    monkeypatch.setattr(
        task_cmd.shutil, "which",
        lambda n, *a, **kw: "/bin/claude" if n == "claude" else real_which(n, *a, **kw),
    )
    monkeypatch.setattr(task_cmd.subprocess, "run", fake_run)
    task_cmd.spawn_plan(tid, worktree=str(elsewhere))
    out = capsys.readouterr().out

    spawn_block = out.split("Spawned a Claude session")[1]
    rows = [ln for ln in spawn_block.splitlines() if ln.startswith("  • ")]
    assert rows == [
        f"  • Window:      E-{tid}",
        f"  • Session cwd: {elsewhere}",
    ]
    # Aligned: both values start at the same column.
    starts = {len(ln) - len(ln[ln.index(":") + 1:].lstrip()) for ln in rows}
    assert len(starts) == 1, rows


# ── endless worktree sandbox ────────────────────────────────────────────────

def test_worktree_sandbox_prints_the_path_on_demand(
    project_with_task, monkeypatch, capsys, tmp_path,
):
    """Deliverable A's other half. The path claim stopped printing has to be
    reachable, because there is one real use for it: pointing a SQL client at a
    worktree's database."""
    from endless import config, worktree_cmd

    tid = project_with_task["task_id"]
    root = project_with_task["project_root"]
    (root / ".endless").mkdir(parents=True, exist_ok=True)
    (root / ".endless" / "config.json").write_text('{"self_dev": true}\n')
    _claim(
        monkeypatch, capsys, session=None, in_claude=False, unattended=True,
        task_id=tid,
    )
    capsys.readouterr()

    sandbox = config.sandbox_root(f"e-{tid}")
    sandbox.mkdir(parents=True, exist_ok=True)
    worktree_cmd.sandbox_dir(f"E-{tid}")
    assert capsys.readouterr().out.strip() == str(sandbox)

    # And from inside the worktree, with no argument.
    monkeypatch.chdir(root / ".endless" / "worktrees" / f"e-{tid}")
    worktree_cmd.sandbox_dir(None)
    assert capsys.readouterr().out.strip() == str(sandbox)


def test_worktree_sandbox_refuses_rather_than_inventing_a_path(
    project_with_task, monkeypatch, capsys,
):
    """A sandbox is provisioned only for a self-dev project. Anywhere else the
    honest answer is that there is none — not a plausible-looking directory
    nothing ever wrote to."""
    import click
    from endless import worktree_cmd

    tid = project_with_task["task_id"]
    _claim(
        monkeypatch, capsys, session=None, in_claude=False, unattended=True,
        task_id=tid,
    )
    capsys.readouterr()

    with pytest.raises(click.ClickException) as exc:
        worktree_cmd.sandbox_dir(f"E-{tid}")
    assert "does not sandbox its worktrees" in str(exc.value)

    with pytest.raises(click.ClickException) as exc:
        worktree_cmd.sandbox_dir("E-99999")
    assert "No endless-managed worktree for E-99999" in str(exc.value)

    # Outside a worktree, with no argument, it names the form that works.
    with pytest.raises(click.ClickException) as exc:
        worktree_cmd.sandbox_dir(None)
    assert "endless worktree sandbox E-<id>" in str(exc.value)


# ── task release ────────────────────────────────────────────────────────────

def test_release_routes_only_through_commands_that_exist(project_with_task):
    """Deliverable C's other half: every command line a work-instruction block
    prints must be pasteable. Release is a tombstone, so its whole output is
    such a block."""
    import click

    with pytest.raises(click.ClickException) as exc:
        task_cmd.release_item(project_with_task["task_id"])
    msg = str(exc.value)
    assert "endless task update E-<id> --status revisit" in msg
    assert "endless task spawn E-<other>" in msg
    assert "eswt" not in msg
