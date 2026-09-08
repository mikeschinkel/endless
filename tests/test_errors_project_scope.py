"""The project-scope flags on `endless errors` (E-1960).

One Endless database holds every project on the machine, so `errors show` and
`errors clear` are scoped to one of them. The scope itself is decided on the Go
side — it needs the projects table and the cwd walk — so what Python owns, and
what these tests pin, is the argv it builds:

  * neither flag is passed when neither was given, because ABSENCE is what tells
    the Go side to resolve the ambient project. An empty ``--project ""`` would
    instead read as "the project literally named ''" and be refused.
  * ``--all-projects`` and ``--project`` are never emitted together; the Go side
    refuses that pair, and there is no reason to make it.
  * on ``clear``, the flags come BEFORE the ids. Go's flag package stops parsing
    at the first non-flag argument, so an id in front would leave the flag
    unparsed and silently narrow a machine-wide clear back to one project.
"""

import pytest

from endless import jobs_cmd


@pytest.fixture
def captured(monkeypatch):
    """Capture the args _run_go is handed instead of spawning endless-go."""
    calls = []
    monkeypatch.setattr(jobs_cmd, "_run_go",
                        lambda subcommand, args: calls.append((subcommand, args)))
    return calls


def test_show_passes_no_scope_flag_by_default(captured):
    jobs_cmd.errors_show(False, False, None)
    assert captured == [("errors", ["show"])]


def test_show_all_projects(captured):
    jobs_cmd.errors_show(False, False, None, all_projects=True)
    assert captured == [("errors", ["show", "--all-projects"])]


def test_show_named_project(captured):
    jobs_cmd.errors_show(False, False, None, project="acme")
    assert captured == [("errors", ["show", "--project", "acme"])]


def test_all_projects_wins_over_a_named_project(captured):
    """Never both: the Go side refuses the pair rather than guessing."""
    jobs_cmd.errors_show(False, False, None, project="acme", all_projects=True)
    assert captured == [("errors", ["show", "--all-projects"])]


def test_show_scope_composes_with_the_other_flags(captured):
    jobs_cmd.errors_show(True, True, 7, all_projects=True)
    assert captured == [("errors",
                         ["show", "--all", "--detail", "--id", "7", "--all-projects"])]


def test_clear_passes_no_scope_flag_by_default(captured):
    jobs_cmd.errors_clear(())
    assert captured == [("errors", ["clear"])]


def test_clear_puts_the_scope_flag_before_the_ids(captured):
    """Go's flag package stops parsing at the first non-flag argument."""
    jobs_cmd.errors_clear((12, 13), all_projects=True)
    subcommand, args = captured[0]
    assert args == ["clear", "--all-projects", "12", "13"]
    assert args.index("--all-projects") < args.index("12")


def test_clear_named_project(captured):
    jobs_cmd.errors_clear((), project="acme")
    assert captured == [("errors", ["clear", "--project", "acme"])]


# --- the Click layer actually offers the options ----------------------------
#
# The argv tests above call the impl directly, so a typo in an @click.option
# name would sail past them and only surface as "No such option" at a terminal.


@pytest.fixture
def impl_args(monkeypatch):
    """Capture what the Click callbacks hand to the jobs_cmd implementations."""
    calls = []
    monkeypatch.setattr(jobs_cmd, "errors_show",
                        lambda *a, **k: calls.append(("show", a, k)))
    monkeypatch.setattr(jobs_cmd, "errors_clear",
                        lambda *a, **k: calls.append(("clear", a, k)))
    return calls


def _invoke(argv):
    from click.testing import CliRunner

    from endless.cli import main

    return CliRunner().invoke(main, argv)


def test_click_show_accepts_the_scope_options(impl_args):
    result = _invoke(["errors", "show", "--all-projects"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("show", (False, False, None, "", True), {})]


def test_click_show_accepts_a_named_project(impl_args):
    result = _invoke(["errors", "show", "--project", "acme"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("show", (False, False, None, "acme", False), {})]


def test_click_clear_accepts_the_scope_options(impl_args):
    result = _invoke(["errors", "clear", "12", "--all-projects"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("clear", ((12,), "", True), {})]
