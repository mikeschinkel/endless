"""Tests for E-1976: `project status` and `project monitor`.

`endless project status` / `endless project monitor` are the project-scoped
counterpart to the `session status` / `session monitor` pair. What Python owns is
the Click surface and the cap resolution; the query, the ranking, the render and
the tmux layout all live in Go and are tested there
(internal/projectstatuscmd, internal/monitor).

So these tests assert the boundary, which is exactly where a pass-through
breaks: a flag declared but never forwarded, a cap resolved with the wrong
default, a renamed command that left its old name behind.
"""

import pytest
from click.testing import CliRunner

from endless import project_status_cmd, rowcap
from endless.cli import main


@pytest.fixture
def runner():
    return CliRunner()


# --- the rename ------------------------------------------------------------
#
# E-1976 took the name `project status` for the attention view and moved the
# project's registration card to `project info`. Both halves of that have to be
# true: the card must still be reachable, and the name must now mean the view.

def test_project_info_exists(runner):
    result = runner.invoke(main, ["project", "info", "--help"])
    assert result.exit_code == 0
    assert "registration card" in result.output


def test_project_status_is_the_attention_view_not_the_card(runner):
    result = runner.invoke(main, ["project", "status", "--help"])
    assert result.exit_code == 0
    assert "needs attention" in result.output
    # The card's vocabulary must NOT still be here — a `project status` that
    # printed metadata would mean the rename half-landed.
    assert "Dependencies" not in result.output


def test_project_monitor_exists(runner):
    result = runner.invoke(main, ["project", "monitor", "--help"])
    assert result.exit_code == 0
    assert "Live monitor" in result.output


def test_both_verbs_take_an_optional_project_name(runner):
    # Naming the project is what makes these usable from anywhere —
    # including from inside a worktree of a different project.
    for verb in ("status", "monitor"):
        result = runner.invoke(main, ["project", verb, "--help"])
        assert "[NAME]" in result.output, f"project {verb} lost its NAME argument"


# --- the cap ---------------------------------------------------------------
#
# `project status` caps PER GROUP, which is a different unit from every other listing.
# What must NOT differ is the validation: same flag names, same refusals.

def test_group_cap_default_differs_from_the_row_cap():
    # Not equal, and that is deliberate — its frame holds several groups
    # where a listing holds one table. If they ever coincide it should be
    # because someone chose that, not because the parameter stopped being passed.
    assert project_status_cmd.DEFAULT_GROUP_CAP != rowcap.DEFAULT_ROW_CAP
    assert rowcap.resolve_cap(None, False,
                              default=project_status_cmd.DEFAULT_GROUP_CAP) \
        == project_status_cmd.DEFAULT_GROUP_CAP


def test_resolve_cap_default_does_not_change_existing_callers():
    # The parameter E-1976 added must be invisible to the sixteen surfaces that
    # do not pass it.
    assert rowcap.resolve_cap(None, False) == rowcap.DEFAULT_ROW_CAP
    assert rowcap.resolve_cap(None, False, machine=True) is None
    assert rowcap.resolve_cap(7, False, default=3) == 7
    assert rowcap.resolve_cap(None, True, default=3) is None


@pytest.mark.parametrize("verb", ["status", "monitor"])
def test_both_flags_are_offered(runner, verb):
    result = runner.invoke(main, ["project", verb, "--help"])
    assert "--limit" in result.output
    assert "--no-limit" in result.output
    assert "rows per group" in result.output, \
        "the help text does not say the cap is per group"


@pytest.mark.parametrize("verb", ["status", "monitor"])
def test_limit_and_no_limit_are_refused_together(runner, verb):
    result = runner.invoke(main, ["project", verb, "--limit", "5", "--no-limit"])
    assert result.exit_code != 0
    assert "mutually exclusive" in result.output


@pytest.mark.parametrize("verb", ["status", "monitor"])
def test_limit_zero_points_at_the_flag_meant(runner, verb):
    # `--limit 0` reads as "no limit" and would mean "no rows". Refusing it and
    # naming the flag meant is the difference between a trap and a signpost.
    result = runner.invoke(main, ["project", verb, "--limit", "0"])
    assert result.exit_code != 0
    assert "--no-limit" in result.output


# --- the pass-through ------------------------------------------------------
#
# Every flag has to reach the Go argv. A flag declared and dropped is the
# failure mode a thin wrapper actually has.

def _argv(monkeypatch, **kwargs):
    seen = {}

    def fake_run(args):
        seen["argv"] = args

    monkeypatch.setattr(project_status_cmd, "_run", fake_run)
    monkeypatch.setattr(project_status_cmd, "_go_binary", lambda: "/fake/endless-go")
    project_status_cmd.project_status_resolve(**kwargs)
    return seen["argv"]


def test_argv_carries_the_defaults(monkeypatch):
    argv = _argv(monkeypatch, project=None)
    assert argv[:2] == ["/fake/endless-go", "project-status"]
    assert "--limit" in argv
    assert argv[argv.index("--limit") + 1] == str(project_status_cmd.DEFAULT_GROUP_CAP)
    assert "--project" not in argv, "no name given, so cwd resolution must be left to Go"


def test_argv_carries_every_flag(monkeypatch):
    argv = _argv(monkeypatch, project="demo", monitor=True, show_all=True, limit=3)
    assert argv[argv.index("--project") + 1] == "demo"
    assert "--monitor" in argv
    assert "--all" in argv
    assert argv[argv.index("--limit") + 1] == "3"


def test_argv_no_limit_replaces_the_cap(monkeypatch):
    argv = _argv(monkeypatch, project="demo", no_limit=True)
    assert "--no-limit" in argv
    assert "--limit" not in argv, \
        "an uncapped render must not also carry a cap Go would have to reconcile"


def test_argv_json_is_uncapped(monkeypatch):
    # Machine formats are uncapped by default (rowcap's rule) — a consumer
    # parsing a truncated payload has no footer to read.
    argv = _argv(monkeypatch, project="demo", as_json=True)
    assert "--json" in argv
    assert "--no-limit" in argv
    assert "--limit" not in argv


def test_argv_json_honours_an_explicit_limit(monkeypatch):
    argv = _argv(monkeypatch, project="demo", as_json=True, limit=4)
    assert argv[argv.index("--limit") + 1] == "4"
    assert "--no-limit" not in argv


def test_window_argv(monkeypatch):
    seen = {}
    monkeypatch.setattr(project_status_cmd, "_run", lambda a: seen.update(argv=a))
    monkeypatch.setattr(project_status_cmd, "_go_binary", lambda: "/fake/endless-go")

    project_status_cmd.project_window_resolve("demo", no_switch=True)
    assert seen["argv"][:2] == ["/fake/endless-go", "project-window"]
    assert seen["argv"][seen["argv"].index("--project") + 1] == "demo"
    assert "--no-switch" in seen["argv"]


def test_monitor_tmux_routes_to_the_window_verb(runner, monkeypatch):
    called = {}
    monkeypatch.setattr(project_status_cmd, "project_window_resolve",
                        lambda *a, **k: called.update(window=(a, k)))
    monkeypatch.setattr(project_status_cmd, "project_status_resolve",
                        lambda *a, **k: called.update(inline=(a, k)))

    result = runner.invoke(main, ["project", "monitor", "demo", "--tmux"])
    assert result.exit_code == 0, result.output
    assert "window" in called and "inline" not in called, \
        "--tmux must open the layout, not run the monitor in the current pane"


def test_no_switch_without_tmux_is_refused(runner):
    result = runner.invoke(main, ["project", "monitor", "demo", "--no-switch"])
    assert result.exit_code != 0
    assert "--tmux" in result.output


def test_module_is_not_a_seventh_sqlite_reader():
    # CLAUDE.md's standing rule: Go owns database access, Python still reads
    # SQLite directly in six files, and no seventh may be added. A pass-through
    # that reached for `db` to resolve one field would be exactly that seventh.
    #
    # Asserted against the module's IMPORTS rather than its text, so the rule
    # cannot be tripped by a docstring that merely mentions the rule.
    import ast
    import inspect

    tree = ast.parse(inspect.getsource(project_status_cmd))
    imported = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            imported.update(a.name for a in node.names)
        elif isinstance(node, ast.ImportFrom):
            for a in node.names:
                imported.add(f"{node.module}.{a.name}" if node.module else a.name)

    assert "sqlite3" not in imported
    assert "endless.db" not in imported
