"""A session or task named by id resolves in any project (E-2358).

Ids are global. `session goto ES-1259` run from go-cfgstore refused with
"No live session" while `task show` called ES-1259 live, because every by-id
lookup passed the cwd's project to `list-live` and a session recorded under
another project was never in the candidate set. The scope is kept only by
listings, which have no id to be specific about.
"""

import pytest

from endless import session_cmd
from endless.session_cmd import _resolve_companion
from test_session_goto_back import goto_env  # noqa: F401


ELSEWHERE = "/somewhere/else/other-project"


def test_goto_finds_a_live_session_recorded_under_another_project(goto_env):
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1259, pane_id="%4",
          task_id=2208)
    ft = make({"%4", "%1"}, current_pane="%1")

    session_cmd.session_goto("ES-1259")

    assert ft.switched == ["%4"]


def test_goto_by_task_finds_a_session_recorded_under_another_project(goto_env):
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="%44",
          task_id=2208)
    ft = make({"%44", "%1"}, current_pane="%1")

    session_cmd.session_goto("E-2208")

    assert ft.switched == ["%44"]


def test_back_resolves_a_token_recorded_under_another_project(goto_env):
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1259, pane_id="%4")
    ft = make({"%4", "%1"}, current_pane="%1")
    ft.options["@endless_backstack_cli"] = "1259"

    session_cmd.session_back()

    assert ft.switched == ["%4"]


def test_companion_by_id_resolves_in_any_project(goto_env, monkeypatch):
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1259, pane_id="%4")
    make({"%4", "%1"}, current_pane="%1")

    live = session_cmd._live_sessions(all_projects=True)
    assert _resolve_companion("ES-1259", live)["endless_session_id"] == 1259


def test_companion_by_pane_resolves_in_any_project(goto_env, monkeypatch):
    """No id given: the sibling pane in this window is as specific as an id,
    so the session it holds is found whatever project it is recorded under."""
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1259, pane_id="%4")
    make({"%4", "%1"}, current_pane="%1")
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids",
                        lambda: ["%1", "%4"])

    live = session_cmd._live_sessions(all_projects=True)
    assert _resolve_companion(None, live)["endless_session_id"] == 1259


def test_listing_keeps_the_cwd_project_scope(goto_env):
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1259, pane_id="%4")
    stage(endless_session_id=7, pane_id="%7")
    make({"%4", "%7", "%1"}, current_pane="%1")

    ids = {c["endless_session_id"]
           for c in session_cmd._live_sessions(session_cmd._project_root_for_cwd())}
    assert ids == {7}


def test_session_cd_by_id_resolves_in_any_project(goto_env, capsys, tmp_path):
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1259, pane_id="%4",
          cwd=str(tmp_path))
    make({"%4", "%1"}, current_pane="%1")

    session_cmd.session_cd_resolve("ES-1259")

    assert capsys.readouterr().out.strip() == str(tmp_path)


def test_rebind_guard_sees_a_live_window_in_any_project(goto_env, monkeypatch):
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1259, pane_id="%4",
          task_id=2208)
    make({"%4", "%1"}, current_pane="%1")
    monkeypatch.setattr(
        session_cmd, "_tmux_window_of",
        lambda pane: {"%1": ("@1", "1:here"), "%4": ("@4", "4:there")}.get(pane),
    )

    assert session_cmd._live_window_claiming(2208) == ("4:there", 1259)
