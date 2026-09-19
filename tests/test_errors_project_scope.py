"""The project-scope flags on `endless errors` (E-1960).

One Endless database holds every project on the machine, so `errors list` and
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

E-2148 split the listing off `show`, so these cover ``errors list``. The scope
deliberately does NOT reach ``errors show <id>``: an id is an exact selector the
user typed and the Go side honours it whichever project the row belongs to, so a
scope flag there would be a flag that changes nothing.
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


def test_list_passes_no_scope_flag_by_default(captured):
    jobs_cmd.errors_list(False, False)
    assert captured == [("errors", ["list"])]


def test_list_all_projects(captured):
    jobs_cmd.errors_list(False, False, all_projects=True)
    assert captured == [("errors", ["list", "--all-projects"])]


def test_list_named_project(captured):
    jobs_cmd.errors_list(False, False, project="acme")
    assert captured == [("errors", ["list", "--project", "acme"])]


def test_all_projects_wins_over_a_named_project(captured):
    """Never both: the Go side refuses the pair rather than guessing."""
    jobs_cmd.errors_list(False, False, project="acme", all_projects=True)
    assert captured == [("errors", ["list", "--all-projects"])]


def test_list_scope_composes_with_the_other_flags(captured):
    jobs_cmd.errors_list(True, True, all_projects=True)
    assert captured == [("errors", ["list", "--all", "--detail", "--all-projects"])]


def test_show_takes_an_id_positionally_and_no_scope(captured):
    """An id is an exact selector the user typed (E-2148).

    The Go side honours it whichever project the row belongs to — refusing
    because it belongs to another project would make `show 7` fail right after a
    listing displayed row 7 — so there is no scope flag to pass.
    """
    jobs_cmd.errors_show(7, False)
    assert captured == [("errors", ["show", "7"])]


def test_show_passes_detail_through(captured):
    jobs_cmd.errors_show(7, True)
    assert captured == [("errors", ["show", "7", "--detail"])]


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
    monkeypatch.setattr(jobs_cmd, "errors_list",
                        lambda *a, **k: calls.append(("list", a, k)))
    monkeypatch.setattr(jobs_cmd, "errors_show",
                        lambda *a, **k: calls.append(("show", a, k)))
    monkeypatch.setattr(jobs_cmd, "errors_clear",
                        lambda *a, **k: calls.append(("clear", a, k)))
    return calls


def _invoke(argv):
    from click.testing import CliRunner

    from endless.cli import main

    return CliRunner().invoke(main, argv)


def test_click_list_accepts_the_scope_options(impl_args):
    result = _invoke(["errors", "list", "--all-projects"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("list", (False, False, "", True), {})]


def test_click_list_accepts_a_named_project(impl_args):
    result = _invoke(["errors", "list", "--project", "acme"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("list", (False, False, "acme", False), {})]


def test_click_show_takes_the_id_positionally(impl_args):
    result = _invoke(["errors", "show", "7"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("show", (7, False), {})]


def test_click_show_still_accepts_the_deprecated_id_flag(impl_args):
    """--id is kept for callers that already type it, and hidden from --help."""
    result = _invoke(["errors", "show", "--id", "7"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("show", (7, False), {})]
    assert "--id" not in _invoke(["errors", "show", "--help"]).output


def test_click_show_with_no_id_is_a_usage_error_naming_list(impl_args):
    """It used to print the listing. That is the confusion E-2148 removed."""
    result = _invoke(["errors", "show"])
    assert result.exit_code != 0
    assert "endless errors list" in result.output
    assert impl_args == []


def test_click_clear_accepts_the_scope_options(impl_args):
    result = _invoke(["errors", "clear", "12", "--all-projects"])
    assert result.exit_code == 0, result.output
    assert impl_args == [("clear", ((12,), "", True), {})]
