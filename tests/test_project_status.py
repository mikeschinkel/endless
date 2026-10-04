"""Tests for E-1976 and E-2156: `project status` and `project monitor`.

`endless project status` / `endless project monitor` are the project-scoped
counterpart to the `session status` / `session monitor` pair. What Python owns is
the Click surface; the query, the lists, the render and the tmux layout all live
in Go and are tested there (internal/projectstatuscmd, internal/monitor).

So these tests assert the boundary, which is exactly where a pass-through
breaks: a flag declared but never forwarded, a flag removed from one verb and
left on the other, a renamed command that left its old name behind.
"""

import pytest
from click.testing import CliRunner

from endless import project_status_cmd
from endless.cli import main


@pytest.fixture
def runner():
    return CliRunner()


# --- the rename ------------------------------------------------------------
#
# E-1976 moved the project's registration card to `project info` and gave the
# name `project status` to the command that lists the project's work. Both halves of that have to be
# true: the card must still be reachable, and the name must now mean the view.

def test_project_info_exists(runner):
    result = runner.invoke(main, ["project", "info", "--help"])
    assert result.exit_code == 0
    assert "registration card" in result.output


def test_project_status_is_the_task_view_not_the_card(runner):
    result = runner.invoke(main, ["project", "status", "--help"])
    assert result.exit_code == 0
    assert "open tasks" in result.output
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


# --- the flags ------------------------------------------------------------
#
# E-2156 removed the per-group cap and `--all` from both verbs, gave both
# `--sort`, and gave `project status` alone `--later`.

@pytest.mark.parametrize("verb", ["status", "monitor"])
@pytest.mark.parametrize("flag", ["--limit", "--no-limit", "--all"])
def test_removed_flags_are_gone(runner, verb, flag):
    result = runner.invoke(main, ["project", verb, "--help"])
    assert flag not in result.output.split(), f"project {verb} still offers {flag}"


@pytest.mark.parametrize("verb", ["status", "monitor"])
def test_both_verbs_take_sort(runner, verb):
    result = runner.invoke(main, ["project", verb, "--help"])
    assert "--sort" in result.output
    assert "updated" in result.output and "id" in result.output


@pytest.mark.parametrize("verb", ["status", "monitor"])
def test_sort_refuses_an_unknown_key(runner, verb):
    result = runner.invoke(main, ["project", verb, "--sort", "title"])
    assert result.exit_code != 0


def test_later_is_on_status_only(runner):
    assert "--later" in runner.invoke(main, ["project", "status", "--help"]).output
    assert "--later" not in runner.invoke(main, ["project", "monitor", "--help"]).output


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
    assert argv[argv.index("--sort") + 1] == "updated"
    assert "--project" not in argv, "no name given, so cwd resolution must be left to Go"
    for gone in ("--limit", "--no-limit", "--all"):
        assert gone not in argv


def test_argv_carries_every_flag(monkeypatch):
    argv = _argv(monkeypatch, project="demo", monitor=True, later=True,
                 sort="id", as_json=True)
    assert argv[argv.index("--project") + 1] == "demo"
    assert argv[argv.index("--sort") + 1] == "id"
    for flag in ("--monitor", "--later", "--json"):
        assert flag in argv


@pytest.mark.parametrize("verb,extra", [("status", ["--later"]), ("monitor", [])])
def test_cli_forwards_sort(runner, monkeypatch, verb, extra):
    seen = {}
    monkeypatch.setattr(project_status_cmd, "project_status_resolve",
                        lambda *a, **k: seen.update(args=a, kwargs=k))
    result = runner.invoke(main, ["project", verb, "demo", "--sort", "id", *extra])
    assert result.exit_code == 0, result.output
    assert seen["kwargs"]["sort"] == "id"
    if extra:
        assert seen["kwargs"]["later"] is True


def test_window_argv(monkeypatch):
    seen = {}
    monkeypatch.setattr(project_status_cmd, "_run", lambda a: seen.update(argv=a))
    monkeypatch.setattr(project_status_cmd, "_go_binary", lambda: "/fake/endless-go")

    project_status_cmd.project_window_resolve("demo", no_switch=True)
    assert seen["argv"][:2] == ["/fake/endless-go", "project-window"]
    assert seen["argv"][seen["argv"].index("--project") + 1] == "demo"
    assert "--no-switch" in seen["argv"]
    assert "--use-existing" not in seen["argv"]

    project_status_cmd.project_window_resolve("demo", use_existing=True)
    assert "--use-existing" in seen["argv"]


def test_use_existing_without_tmux_is_refused(runner):
    result = runner.invoke(main, ["project", "monitor", "demo", "--use-existing"])
    assert result.exit_code != 0
    assert "--tmux" in result.output


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
