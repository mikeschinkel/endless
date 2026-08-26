"""Tests for E-2071: a truncated list announces itself.

Two properties, and they are the whole point:

1. A HUMAN or `--llm` render stops at the cap and prints a footer naming both
   the dropped count and the flag that shows them. A count printed under the
   table (`N match(es)`, `N item(s)`) reports the SIZE OF THE RESULT, never the
   height of the table — a table capped at 20 under the line "20 match(es)" is
   the exact false-negative this task removed.
2. A MACHINE render (`--json`, `--tsv`) is uncapped by default. Capping a
   payload nothing can read a footer out of would be strictly worse than the
   original defect.

These go through the CLI rather than the render functions, because the flag
wiring is half the surface: a `--limit` that never reaches `resolve_cap`, or a
`--no-limit` missing from one command, is the failure mode most likely to ship.
"""

import json

import pytest
from click.testing import CliRunner

from endless import db, rowcap
from endless.cli import main


# The listing surfaces E-2071 covers, as (argv, needs-rows-of-this-kind).
# Parametrising over the set is deliberate: the bug was one uncapped surface
# among many, so a test that names them individually would go stale the moment
# a tenth appears without its flags.
TASK_LISTINGS = [
    ["task", "list"],
    ["task", "search", "widget"],
    ["task", "next"],
    ["task", "recent"],
]

ALL_LISTINGS = TASK_LISTINGS + [
    ["epic", "list"],
    ["decision", "list"],
    ["task", "landed"],
    ["task", "unsettled", "--all"],
]

# Appended by the runners, not written into the literals above, so the
# parametrize ids stay readable.
PROJECT = ["--project", "my-project"]


def _project_id() -> int:
    return db.query("SELECT id FROM projects WHERE name = 'my-project'")[0]["id"]


def _seed_tasks(n: int, *, status: str = "ready") -> None:
    """n live tasks whose titles all match the search fixture's query."""
    pid = _project_id()
    for i in range(n):
        db.execute(
            "INSERT INTO tasks (id, project_id, title, status, phase) "
            "VALUES (?, ?, ?, ?, 'now')",
            (100 + i, pid, f"widget number {i}", status),
        )


def _run(argv):
    return CliRunner().invoke(main, argv)


@pytest.fixture(autouse=True)
def project(registered_project):
    """Every listing here resolves a project; cwd is the endless repo, which is
    not in the isolated DB, so each argv names the fixture project explicitly."""
    return registered_project


# ---- the cap resolver ------------------------------------------------------

def test_default_cap_is_twenty():
    assert rowcap.resolve_cap(None, False) == 20


def test_explicit_limit_wins():
    assert rowcap.resolve_cap(5, False) == 5


def test_no_limit_means_uncapped():
    assert rowcap.resolve_cap(None, True) is None


def test_machine_format_is_uncapped_by_default():
    assert rowcap.resolve_cap(None, False, machine=True) is None


def test_machine_format_still_honours_an_explicit_limit():
    """Explicit is explicit: --json --limit 5 caps, it does not silently widen."""
    assert rowcap.resolve_cap(5, False, machine=True) == 5


def test_limit_and_no_limit_together_are_refused():
    with pytest.raises(Exception) as exc:
        rowcap.resolve_cap(5, True)
    assert "mutually exclusive" in str(exc.value)


def test_limit_zero_is_refused_and_points_at_no_limit():
    """`--limit 0` reads as 'no limit' but would mean 'no rows'. Refuse, and say
    which flag the caller wanted."""
    with pytest.raises(Exception) as exc:
        rowcap.resolve_cap(0, False)
    assert "--no-limit" in str(exc.value)


# ---- the footer ------------------------------------------------------------

def test_footer_names_the_count_and_the_flag():
    assert rowcap.footer(37) == "… 37 more rows (--no-limit)"


def test_footer_is_singular_for_one():
    assert rowcap.footer(1) == "… 1 more row (--no-limit)"


def test_footer_is_silent_when_nothing_was_dropped(capsys):
    rowcap.echo_footer(0)
    assert capsys.readouterr().out == ""


def test_machine_footer_goes_to_stderr(capsys):
    """So the trace survives without corrupting the payload it describes."""
    rowcap.echo_footer(5, llm=True, err=True)
    captured = capsys.readouterr()
    assert captured.out == ""
    assert "5 more rows" in captured.err


def test_cap_rows_splits_and_counts():
    assert rowcap.cap_rows(list(range(50)), 20) == (list(range(20)), 30)


def test_cap_rows_of_none_is_uncapped():
    rows = list(range(50))
    assert rowcap.cap_rows(rows, None) == (rows, 0)


def test_cap_rows_does_not_footer_an_exact_fit():
    """A result of exactly cap rows is COMPLETE. A footer there would teach the
    reader to distrust a listing that is telling the truth."""
    assert rowcap.cap_rows(list(range(20)), 20) == (list(range(20)), 0)


# ---- every listing surface carries both flags ------------------------------

@pytest.mark.parametrize("argv", ALL_LISTINGS, ids=lambda a: " ".join(a))
def test_every_listing_offers_no_limit(argv):
    result = _run(argv + ["--help"])
    assert result.exit_code == 0
    assert "--no-limit" in result.output


@pytest.mark.parametrize("argv", ALL_LISTINGS, ids=lambda a: " ".join(a))
def test_every_listing_offers_limit(argv):
    result = _run(argv + ["--help"])
    assert result.exit_code == 0
    assert "--limit" in result.output


@pytest.mark.parametrize("argv", ALL_LISTINGS, ids=lambda a: " ".join(a))
def test_every_listing_refuses_both_flags_together(argv):
    result = _run(argv + ["--limit", "5", "--no-limit"])
    assert result.exit_code != 0
    assert "mutually exclusive" in result.output


def test_sql_offers_both_flags():
    result = _run(["sql", "--help"])
    assert "--no-limit" in result.output and "--limit" in result.output


# ---- the truncation is announced -------------------------------------------

@pytest.mark.parametrize("argv", TASK_LISTINGS, ids=lambda a: " ".join(a))
def test_a_capped_listing_says_how_many_it_dropped(argv):
    _seed_tasks(35)
    result = _run(argv + PROJECT)
    assert result.exit_code == 0, result.output
    assert "15 more rows (--no-limit)" in result.output


@pytest.mark.parametrize("argv", TASK_LISTINGS, ids=lambda a: " ".join(a))
def test_no_limit_renders_everything_and_drops_the_footer(argv):
    _seed_tasks(35)
    result = _run(argv + PROJECT + ["--no-limit"])
    assert result.exit_code == 0, result.output
    assert "--no-limit)" not in result.output
    assert "E-134" in result.output  # the 35th row


@pytest.mark.parametrize("argv", TASK_LISTINGS, ids=lambda a: " ".join(a))
def test_an_uncapped_result_prints_no_footer(argv):
    """The footer must mean something. A listing that fits says nothing."""
    _seed_tasks(5)
    result = _run(argv + PROJECT)
    assert result.exit_code == 0, result.output
    assert "--no-limit)" not in result.output


@pytest.mark.parametrize("argv", TASK_LISTINGS, ids=lambda a: " ".join(a))
def test_llm_output_carries_the_footer_too(argv):
    """--llm is prose an agent reads, and the agent is who got fooled."""
    _seed_tasks(35)
    result = _run(argv + PROJECT + ["--llm"])
    assert result.exit_code == 0, result.output
    assert "# 15 more rows (--no-limit)" in result.output


def test_sql_table_announces_its_truncation():
    _seed_tasks(35)
    result = _run(["sql", "SELECT id FROM tasks ORDER BY id"])
    assert result.exit_code == 0, result.output
    assert "15 more rows (--no-limit)" in result.output


# ---- the count under the table is the count of MATCHES ---------------------

def test_search_reports_total_matches_not_rendered_rows():
    """The filed defect in one assertion. Before E-2071 this line read
    '20 match(es)' over a table of 20 of 35 — indistinguishable from a complete
    result, which is how two searches produced false 'no existing task'
    conclusions."""
    _seed_tasks(35)
    result = _run(["task", "search", "widget"] + PROJECT)
    assert "35 match(es)" in result.output
    assert "20 match(es)" not in result.output


def test_list_reports_total_items_not_rendered_rows():
    _seed_tasks(35)
    result = _run(["task", "list"] + PROJECT)
    assert "35 item(s)" in result.output


# ---- machine formats stay whole and stay parseable -------------------------

@pytest.mark.parametrize("argv", TASK_LISTINGS, ids=lambda a: " ".join(a))
def test_json_is_uncapped_by_default(argv):
    _seed_tasks(35)
    result = _run(argv + PROJECT + ["--json"])
    assert result.exit_code == 0, result.output
    assert len(json.loads(result.output)) == 35


@pytest.mark.parametrize("argv", TASK_LISTINGS, ids=lambda a: " ".join(a))
def test_json_stays_parseable_when_an_explicit_limit_truncates_it(argv):
    """The footer exists — on stderr — so `| jq` never sees it."""
    _seed_tasks(35)
    result = CliRunner().invoke(main, argv + PROJECT + ["--json", "--limit", "5"])
    assert result.exit_code == 0, result.output
    assert len(json.loads(result.stdout)) == 5
    assert "30 more rows" in result.stderr


def test_sql_tsv_is_uncapped_by_default():
    _seed_tasks(35)
    result = _run(["sql", "SELECT id FROM tasks", "--tsv"])
    assert result.exit_code == 0, result.output
    assert len([ln for ln in result.output.splitlines() if ln.strip()]) == 35


def test_sql_tsv_keeps_its_payload_clean_under_an_explicit_limit():
    _seed_tasks(35)
    result = CliRunner().invoke(
        main, ["sql", "SELECT id FROM tasks", "--tsv", "--limit", "5"])
    assert result.exit_code == 0, result.output
    assert len([ln for ln in result.stdout.splitlines() if ln.strip()]) == 5
    assert "30 more rows" in result.stderr
