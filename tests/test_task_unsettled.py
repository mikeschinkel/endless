"""Tests for E-1865: `task unsettled` — why a worktree hasn't settled.

The VERDICT is computed in Go (monitor.WorktreeUnsettledAt, shared with the ◆
marker) and is covered by internal/monitor/worktree_unsettled_test.go and
internal/monitor/worktree_unlanded_test.go. These
tests cover the Python half: that each sub-state is rendered with the fix it
actually needs, that a truncated list says so, and that the JSON contract is
stable. The Go probe is stubbed throughout — these are renderer tests, and
shelling out to git would make them slow and machine-dependent.
"""

import json
from pathlib import Path

import pytest

from endless import db, rowcap, task_cmd


def _project_id() -> int:
    return db.query("SELECT id FROM projects WHERE name = 'my-project'")[0]["id"]


def _insert_task(pk: int, title: str = "Some work", status: str = "underway"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, phase) "
        "VALUES (?, ?, ?, ?, 'now')",
        (pk, _project_id(), title, status),
    )


def _probe(**over) -> dict:
    """A settled probe result, overridable per test — mirrors the Go wire shape."""
    base = {
        "worktree_path": "/wt/e-1", "has_worktree": True,
        "unsettled": False, "modified": False, "unlanded": False,
        "reason": "settled", "branch": "task/1-x",
        "modified_files": [], "auto_managed_files": [],
        "unlanded_count": 0, "unlanded_log": [],
        # E-1940 additions: a probe that could not run is its own answer, and
        # the base is resolved rather than assumed to be `main`.
        "undetermined": False, "undetermined_reason": "",
        "base": "main",
    }
    base.update(over)
    return base


@pytest.fixture
def stub_probe(monkeypatch, tmp_path):
    """Patch the Go probe and worktree resolution; yields a setter for results."""
    monkeypatch.setattr(
        "endless.worktree_cmd._project_root", lambda: tmp_path)

    def install(results, exists=True):
        monkeypatch.setattr(task_cmd, "_unsettled_probe", lambda paths: results)
        monkeypatch.setattr(
            task_cmd, "_worktree_path_for_task",
            lambda root, item_id: (Path("/wt") / f"e-{item_id}") if exists else None)
    return install


# ---- reason → colour: the fix each sub-state demands ------------------------

def test_color_distinguishes_the_two_fixes():
    # Modified needs commit-or-discard; unlanded needs a land. They must not
    # render identically, or the command repeats the ◆'s original sin.
    modified = task_cmd._unsettled_color(_probe(unsettled=True, modified=True))
    unlanded = task_cmd._unsettled_color(_probe(unsettled=True, unlanded=True))
    assert modified != unlanded
    assert task_cmd._unsettled_color(_probe()) == "green"


def test_color_of_both_sub_states_follows_modified():
    # With both true the user must commit before landing, so lead with modified.
    both = _probe(unsettled=True, modified=True, unlanded=True)
    assert task_cmd._unsettled_color(both) == task_cmd._unsettled_color(
        _probe(unsettled=True, modified=True))


# ---- unsettled_item: each state names its own fix ---------------------------

def test_item_unlanded_names_the_land_command(registered_project, stub_probe, capsys):
    _insert_task(9101)
    stub_probe([_probe(unsettled=True, unlanded=True, reason="unlanded (2 commits)",
                       unlanded_count=2,
                       unlanded_log=["abc1234 first", "def5678 second"])])
    task_cmd.unsettled_item(9101)
    out = capsys.readouterr().out
    assert "unlanded (2 commits)" in out
    assert "endless worktree land E-9101" in out
    assert "abc1234 first" in out
    # Must NOT suggest committing: there is nothing uncommitted.
    assert "commit or discard" not in out


def test_item_modified_names_commit_or_discard(registered_project, stub_probe, capsys):
    _insert_task(9102)
    stub_probe([_probe(unsettled=True, modified=True, reason="modified (2 files)",
                       modified_files=["a.py", "b.go"])])
    task_cmd.unsettled_item(9102)
    out = capsys.readouterr().out
    assert "commit or discard" in out
    assert "a.py" in out and "b.go" in out
    assert "worktree land" not in out


def test_item_auto_managed_is_separated_from_user_work(
        registered_project, stub_probe, capsys):
    # The confusing case: ◆ raised purely by endless's own ledger churn. It must
    # still report unsettled (matching the marker) while making clear the user
    # has no work of their own to commit.
    _insert_task(9103)
    stub_probe([_probe(unsettled=True, modified=True,
                       reason="modified (1 auto-managed only)",
                       auto_managed_files=[".endless/verbs.jsonl"])])
    task_cmd.unsettled_item(9103)
    out = capsys.readouterr().out
    assert "Auto-managed" in out
    assert ".endless/verbs.jsonl" in out
    assert "land` commits these for you" in out


def test_item_both_sub_states_reports_both(registered_project, stub_probe, capsys):
    _insert_task(9104)
    stub_probe([_probe(unsettled=True, modified=True, unlanded=True,
                       reason="modified (1 file) + unlanded (1 commit)",
                       modified_files=["x.py"], unlanded_count=1,
                       unlanded_log=["abc1234 work"])])
    task_cmd.unsettled_item(9104)
    out = capsys.readouterr().out
    assert "commit or discard" in out
    assert "worktree land" in out


def test_item_settled_says_so(registered_project, stub_probe, capsys):
    _insert_task(9105)
    stub_probe([_probe()])
    task_cmd.unsettled_item(9105)
    out = capsys.readouterr().out
    assert "Settled" in out
    assert "commit or discard" not in out and "worktree land" not in out


def test_item_no_worktree_is_distinct_from_settled(
        registered_project, stub_probe, capsys):
    _insert_task(9106)
    stub_probe([], exists=False)
    task_cmd.unsettled_item(9106)
    out = capsys.readouterr().out
    assert "No worktree" in out


def test_item_reports_a_failed_git_probe(registered_project, stub_probe, capsys):
    # E-1940 flipped the polarity this test used to describe. A probe that could
    # not run no longer renders as "settled (git status failed: …)" with a note
    # that the verdict may under-report — it IS the verdict: undetermined, the
    # row is marked, and the failure is recorded as a clearable error.
    _insert_task(9107)
    stub_probe([_probe(unsettled=True, undetermined=True,
                       reason="undetermined (git status failed: boom)",
                       undetermined_reason="git status failed: boom",
                       status_error="boom")])
    task_cmd.unsettled_item(9107)
    out = capsys.readouterr().out
    assert "Undetermined" in out
    assert "not\nsettled" in out or "not settled" in out
    assert "endless errors show" in out
    assert "under-report" not in out


def test_item_names_the_resolved_base_rather_than_main(
        registered_project, stub_probe, capsys):
    # E-1940: the renderer used to say "not on main" and "every commit is on
    # main" unconditionally, which is simply false on a project that lands
    # into anything else.
    _insert_task(9117)
    stub_probe([_probe(unsettled=True, unlanded=True, base="master",
                       reason="unlanded (1 commit)", unlanded_count=1)])
    task_cmd.unsettled_item(9117)
    out = capsys.readouterr().out
    assert "not on master" in out


def test_item_settled_names_the_resolved_base(
        registered_project, stub_probe, capsys):
    # E-2087 removed the "N recorded landing(s) credited" qualifier along with
    # the mechanism behind it: a rebase-landed worktree now reads settled
    # because its commits' CONTENT is on the base, which is the same reason a
    # branch that never diverged reads settled. There is nothing left to
    # distinguish, so the sentence no longer tries to.
    _insert_task(9118)
    stub_probe([_probe(base="master")])
    task_cmd.unsettled_item(9118)
    out = capsys.readouterr().out
    assert "every commit is on master" in out
    assert "credited" not in out


def test_item_truncation_note_when_log_is_capped(
        registered_project, stub_probe, capsys):
    # The Go probe caps carried subjects; the count stays exact, so the renderer
    # must account for the difference rather than imply the list is complete.
    _insert_task(9108)
    stub_probe([_probe(unsettled=True, unlanded=True, reason="unlanded (25 commits)",
                       unlanded_count=25,
                       unlanded_log=[f"sha{i} subject" for i in range(20)])])
    task_cmd.unsettled_item(9108)
    assert "5 more" in capsys.readouterr().out


def test_item_json_carries_the_probe_fields(registered_project, stub_probe, capsys):
    _insert_task(9109, title="JSON shape")
    stub_probe([_probe(unsettled=True, unlanded=True, reason="unlanded (1 commit)",
                       unlanded_count=1)])
    task_cmd.unsettled_item(9109, as_json=True)
    out = json.loads(capsys.readouterr().out)
    assert out["id"] == "E-9109"
    assert out["title"] == "JSON shape"
    assert out["unsettled"] is True and out["unlanded"] is True
    assert out["unlanded_count"] == 1
    assert out["reason"] == "unlanded (1 commit)"


def test_item_agent_mode_is_line_oriented(registered_project, stub_probe, capsys):
    _insert_task(9110)
    stub_probe([_probe(unsettled=True, modified=True, unlanded=True,
                       reason="modified (1 file) + unlanded (1 commit)",
                       modified_files=["a.py"], unlanded_count=1,
                       unlanded_log=["abc1234 work"])])
    task_cmd.unsettled_item(9110, agent=True)
    lines = [l for l in capsys.readouterr().out.splitlines() if l]
    assert any(l.startswith("modified a.py") for l in lines)
    assert any(l.startswith("unlanded abc1234") for l in lines)


def test_item_explains_a_worktree_whose_task_row_is_missing(
        registered_project, stub_probe, capsys):
    # A worktree on disk with no task row is exactly the confusing state this
    # command exists to explain, so it must describe it rather than refuse.
    stub_probe([_probe(unsettled=True, unlanded=True, reason="unlanded (1 commit)",
                       unlanded_count=1)])
    task_cmd.unsettled_item(9199)
    assert "no task row" in capsys.readouterr().out


def test_item_rejects_an_id_with_neither_task_nor_worktree(
        registered_project, stub_probe):
    import click
    stub_probe([], exists=False)
    with pytest.raises(click.ClickException):
        task_cmd.unsettled_item(9198)


# ---- unsettled_list ---------------------------------------------------------

def _row(item_id: int, **probe_over) -> dict:
    return {"id": item_id, "title": f"Task {item_id}", "status": "underway",
            "phase": "now", "path": f"/wt/e-{item_id}", "probe": _probe(**probe_over)}


@pytest.fixture
def stub_rows(monkeypatch, tmp_path):
    monkeypatch.setattr("endless.worktree_cmd._project_root", lambda: tmp_path)

    def install(rows):
        monkeypatch.setattr(task_cmd, "_unsettled_rows", lambda pid, root: rows)
    return install


def test_list_hides_settled_rows_by_default(registered_project, stub_rows, capsys):
    stub_rows([
        _row(1, unsettled=True, unlanded=True, reason="unlanded (1 commit)"),
        _row(2),  # settled
    ])
    task_cmd.unsettled_list(project_name="my-project")
    out = capsys.readouterr().out
    assert "E-1" in out
    assert "E-2" not in out


def test_list_include_settled_shows_settled_rows(registered_project, stub_rows, capsys):
    stub_rows([
        _row(1, unsettled=True, unlanded=True, reason="unlanded (1 commit)"),
        _row(2),
    ])
    task_cmd.unsettled_list(project_name="my-project", include_settled=True)
    out = capsys.readouterr().out
    assert "E-1" in out and "E-2" in out


def test_list_sorts_most_entangled_first(registered_project, stub_rows, capsys):
    # A row needing BOTH a commit and a land is further from done than one
    # needing only a land, so it must surface first.
    stub_rows([
        _row(1, unsettled=True, unlanded=True, reason="unlanded (1 commit)"),
        _row(2, unsettled=True, modified=True, unlanded=True,
             reason="modified (1 file) + unlanded (1 commit)"),
    ])
    task_cmd.unsettled_list(project_name="my-project")
    lines = [l for l in capsys.readouterr().out.splitlines() if "E-" in l]
    assert lines[0].index("E-2") >= 0
    assert "E-2" in lines[0] and "E-1" in lines[1]


def test_list_empty_says_everything_is_settled(registered_project, stub_rows, capsys):
    stub_rows([_row(1)])
    task_cmd.unsettled_list(project_name="my-project")
    assert "No unsettled worktrees" in capsys.readouterr().out


def test_list_limit_reports_what_it_hid(registered_project, stub_rows, capsys):
    stub_rows([
        _row(i, unsettled=True, unlanded=True, reason="unlanded (1 commit)")
        for i in range(1, 6)
    ])
    task_cmd.unsettled_list(project_name="my-project", limit=2)
    out = capsys.readouterr().out
    # E-2071 replaced this command's private notice with the shared rowcap
    # footer, which names the dropped count and the flag that reveals them --
    # the session-status shape. The grand total is no longer restated: it is
    # two rendered rows plus three dropped, right above the line.
    assert "3 more rows (--no-limit)" in out


def test_list_json_is_a_flat_array(registered_project, stub_rows, capsys):
    stub_rows([_row(1, unsettled=True, unlanded=True,
                    reason="unlanded (1 commit)", unlanded_count=1)])
    task_cmd.unsettled_list(project_name="my-project", as_json=True)
    out = json.loads(capsys.readouterr().out)
    assert len(out) == 1
    assert out[0]["id"] == "E-1"
    assert out[0]["unlanded_count"] == 1


# ---- the CLI requires an explicit target ------------------------------------
#
# The survey walks every worktree on disk and probes each with git, so it must be
# asked for rather than being what you get by accident.

def _invoke(args):
    from click.testing import CliRunner
    from endless.cli import main
    return CliRunner().invoke(main, ["task", "unsettled", *args])


def test_cli_bare_demands_a_target():
    result = _invoke([])
    assert result.exit_code != 0
    assert "--all" in result.output


def test_cli_rejects_id_and_all_together():
    result = _invoke(["1537", "--all"])
    assert result.exit_code != 0
    assert "not both" in result.output


# ---- truncation notice ------------------------------------------------------
# E-2071 replaced this command's private truncation notice with the shared
# rowcap footer, so these assert the shared behaviour through the same call the
# renderer now makes.

def test_truncation_notice_states_the_hidden_count(capsys):
    rowcap.echo_footer(57 - 20)
    out = capsys.readouterr().out
    assert "37 more" in out and "--no-limit" in out


def test_truncation_notice_silent_when_nothing_hidden(capsys):
    rowcap.echo_footer(0)
    assert capsys.readouterr().out == ""
