"""Every answer names the store it came from (E-1668).

These pin THE RULE — announce what the invocation resolved, when it could have
resolved otherwise — rather than any one command's wording, so a new surface
inherits the behaviour instead of needing its own test.

The failure this guards against is the one that produced the task: an answer
from the wrong database is indistinguishable from an answer from the right one,
and a session reading `2` where main held `59` concluded the product was broken.
"""

import json

import pytest

from endless import config, provenance


@pytest.fixture(autouse=True)
def _clean_state(monkeypatch):
    """Each test is its own invocation.

    `config.PINNED_DB_CONTEXT` is reset here too, and the reason is worth
    stating: it is process-global, and a production process runs exactly one
    command, so nothing resets it there and nothing needs to. A test session
    runs thousands in one process — so an earlier module calling
    `default_db_to_main` (worktree land, db backup) would leave the announce
    exemption latched on, and every assertion below would pass for the wrong
    reason by observing silence.
    """
    monkeypatch.setattr(config, "PINNED_DB_CONTEXT", False)
    provenance.reset()
    yield
    provenance.reset()


def _self_dev_project(tmp_path, monkeypatch, *, self_dev=True, task_id="1668"):
    """Build a project with a worktree and stand inside the worktree."""
    proj = tmp_path / "proj"
    endless = proj / ".endless"
    wt = endless / "worktrees" / f"e-{task_id}"
    wt.mkdir(parents=True)
    (endless / "config.json").write_text(
        '{"self_dev": %s}\n' % ("true" if self_dev else "false")
    )
    monkeypatch.chdir(wt)
    return proj, wt


# --- the database half -------------------------------------------------------


def test_says_nothing_when_the_store_was_never_touched(tmp_path, monkeypatch):
    """A command that asked no question has no answer to attribute.

    This is what keeps the trace off `endless guide`, `endless verb list` and
    every other surface that never opens a database — and it is why the mark
    lives at the two choke points that open one rather than in each command.
    """
    _self_dev_project(tmp_path, monkeypatch)
    monkeypatch.setattr(config, "CONFIG_DIR", config.main_config_dir())
    assert provenance.line() is None
    assert provenance.fields() is None


def test_names_the_database_in_a_self_dev_project(tmp_path, monkeypatch):
    _self_dev_project(tmp_path, monkeypatch)
    monkeypatch.setattr(config, "CONFIG_DIR", config.main_config_dir())
    provenance.mark_touched()

    assert provenance.line() == "db: main"
    assert provenance.line(llm=True) == "# db: main"
    assert provenance.fields()["db"] == "main"


def test_names_the_sandbox_and_which_one(tmp_path, monkeypatch):
    """`sandbox` alone would not say WHICH worktree's, and with many live at
    once that is the whole question."""
    _self_dev_project(tmp_path, monkeypatch, task_id="1668")
    monkeypatch.setattr(
        config, "CONFIG_DIR", config.sandbox_config_dir("e-1668")
    )
    provenance.mark_touched()

    assert provenance.line() == "db: sandbox (e-1668)"
    assert provenance.fields()["db"] == "sandbox (e-1668)"
    assert provenance.fields()["db_dir"].endswith("/sandboxes/e-1668/endless")


def test_says_nothing_about_the_database_downstream(tmp_path, monkeypatch):
    """A project that is not self-dev has ONE database, so naming it says
    nothing that could have been otherwise."""
    _self_dev_project(tmp_path, monkeypatch, self_dev=False)
    monkeypatch.setattr(config, "CONFIG_DIR", config.main_config_dir())
    provenance.mark_touched()

    assert provenance.line() is None


def test_a_pinned_context_announces_nothing(tmp_path, monkeypatch):
    """The exemption, and it is the rule rather than an exception to it: if the
    caller could not have influenced the choice there is nothing to
    disambiguate."""
    _self_dev_project(tmp_path, monkeypatch)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    monkeypatch.setattr(config, "PINNED_DB_CONTEXT", False)
    config.default_db_to_main()
    provenance.mark_touched()

    assert config.PINNED_DB_CONTEXT is True
    assert provenance.line() is None
    assert provenance.fields() is None


def test_an_explicit_db_beats_the_pin_and_is_announced(tmp_path, monkeypatch):
    """`default_db_to_main` is a no-op when the caller already chose, so the
    choice is the caller's again — and a choice gets announced."""
    _self_dev_project(tmp_path, monkeypatch)
    monkeypatch.setattr(config, "PINNED_DB_CONTEXT", False)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    monkeypatch.setattr(config, "CONFIG_DIR", config.main_config_dir())
    config.default_db_to_main()
    provenance.mark_touched()

    assert config.PINNED_DB_CONTEXT is False
    assert provenance.line() == "db: main"


# --- the project half --------------------------------------------------------


def test_the_project_enclosing_cwd_is_not_repeated(tmp_path, monkeypatch):
    """Repetition where nothing varies is how a line stops being read."""
    _self_dev_project(tmp_path, monkeypatch, self_dev=False)
    monkeypatch.setattr(
        provenance, "_describe_db", lambda: None
    )
    monkeypatch.setattr(
        "endless.project_path.project_name_for_cwd", lambda cwd: "probe"
    )
    provenance.mark_touched()
    provenance.record_project("probe")

    assert provenance.line() is None


def test_a_project_that_is_not_cwds_is_announced(tmp_path, monkeypatch):
    _self_dev_project(tmp_path, monkeypatch, self_dev=False)
    monkeypatch.setattr(provenance, "_describe_db", lambda: None)
    monkeypatch.setattr(
        "endless.project_path.project_name_for_cwd", lambda cwd: "probe"
    )
    provenance.mark_touched()
    provenance.record_project("elsewhere")

    assert provenance.line() == "project: elsewhere"
    assert provenance.fields() == {"project": "elsewhere"}


def test_both_halves_read_as_one_line(tmp_path, monkeypatch):
    _self_dev_project(tmp_path, monkeypatch)
    monkeypatch.setattr(config, "CONFIG_DIR", config.main_config_dir())
    monkeypatch.setattr(
        "endless.project_path.project_name_for_cwd", lambda cwd: "probe"
    )
    provenance.mark_touched()
    provenance.record_project("elsewhere")

    assert provenance.line() == "db: main · project: elsewhere"


# --- machine payloads --------------------------------------------------------


def _touched_in_main(tmp_path, monkeypatch):
    _self_dev_project(tmp_path, monkeypatch)
    monkeypatch.setattr(config, "CONFIG_DIR", config.main_config_dir())
    provenance.mark_touched()


def test_object_payload_carries_it_once_at_top_level(tmp_path, monkeypatch):
    _touched_in_main(tmp_path, monkeypatch)
    out = provenance.attach({"id": "E-1668", "status": "underway"})

    assert out["id"] == "E-1668"
    assert out[provenance.FIELD]["db"] == "main"


def test_array_payload_carries_it_per_row(tmp_path, monkeypatch):
    """Per row rather than in a new envelope: an envelope is a breaking change
    to every consumer and an added key is not."""
    _touched_in_main(tmp_path, monkeypatch)
    out = provenance.attach([{"id": "E-1"}, {"id": "E-2"}])

    assert [r["id"] for r in out] == ["E-1", "E-2"]
    assert all(r[provenance.FIELD]["db"] == "main" for r in out)


def test_a_payload_with_nowhere_to_put_it_is_returned_unchanged(
    tmp_path, monkeypatch
):
    """Reshaping a payload to make room would break the consumer this is meant
    to inform."""
    _touched_in_main(tmp_path, monkeypatch)
    assert provenance.attach([1, 2, 3]) == [1, 2, 3]
    assert provenance.attach("plain") == "plain"


def test_attaching_suppresses_the_in_band_line(tmp_path, monkeypatch):
    """The line and the field are the SAME announcement in two formats; a
    machine render must not get both, because the line would corrupt it."""
    _touched_in_main(tmp_path, monkeypatch)
    provenance.attach({"id": "E-1668"})

    assert provenance._machine is True


def test_the_payload_stays_valid_json(tmp_path, monkeypatch):
    _touched_in_main(tmp_path, monkeypatch)
    payload = provenance.attach([{"id": "E-1"}])
    assert json.loads(json.dumps(payload))[0][provenance.FIELD]["db"] == "main"


# --- the head/tail pair ------------------------------------------------------


def test_head_and_tail_are_byte_identical(tmp_path, monkeypatch, capsys):
    """E-2097's rule, and the reason it exists: whichever end a truncating pipe
    leaves has to be sufficient alone, and an agent's `| head` and `| tail` are
    both routine."""
    _touched_in_main(tmp_path, monkeypatch)
    monkeypatch.setattr(provenance, "_agent_mode", lambda: True)

    provenance.echo_head()
    provenance.echo_tail()
    out = capsys.readouterr().out.splitlines()

    assert len(out) == 2
    assert out[0] == out[1] == "# db: main"


def test_the_head_prints_once_however_often_it_is_reached(
    tmp_path, monkeypatch, capsys
):
    """A per-project render loop calls it once per group; one header, not one
    per group."""
    _touched_in_main(tmp_path, monkeypatch)

    provenance.echo_head()
    provenance.echo_head()
    provenance.echo_head()

    assert capsys.readouterr().out.count("db: main") == 1


def test_neither_end_writes_into_a_machine_render(tmp_path, monkeypatch, capsys):
    _touched_in_main(tmp_path, monkeypatch)
    provenance.mark_machine()

    provenance.echo_head()
    provenance.echo_tail()

    assert capsys.readouterr().out == ""


@pytest.mark.parametrize("argv", [
    ["task", "list", "--json"],
    ["task", "list", "--tsv"],
    ["sql", "SELECT 1", "--tsv"],
])
def test_a_machine_flag_anywhere_in_argv_suppresses_the_line(
    monkeypatch, argv
):
    """Belt and braces with `attach`: a machine surface that was never converted
    still suppresses the line. The cost of a missed `attach` is a missing field;
    the cost of a missed suppression is a corrupted payload, so the two
    mechanisms fail in the safe direction."""
    monkeypatch.setattr("sys.argv", ["endless", *argv])
    assert provenance._scan_argv_for_machine_format() is True


def test_a_human_render_is_not_mistaken_for_a_machine_one(monkeypatch):
    monkeypatch.setattr("sys.argv", ["endless", "task", "list"])
    assert provenance._scan_argv_for_machine_format() is False
