"""Tests for E-1813: the complexity and risk rating axes at the CLI surface.

The gates themselves (submit proposes, approve ratifies) are pinned in
test_submit_approve.py; the executor/projector half in
internal/events/rating_test.go. What is covered here is the surface a person
and an agent actually touch: setting, clearing, filtering, sorting and seeing
ratings — and that `--tier` is gone rather than silently ignored.
"""

import json

import pytest
from click.testing import CliRunner

from endless import db, ratings
from endless.cli import main


def _run(*args: str, ok: bool = True) -> str:
    result = CliRunner().invoke(main, list(args))
    if ok:
        assert result.exit_code == 0, result.output
    else:
        assert result.exit_code != 0, result.output
    return result.output


def _add(title: str, *flags: str) -> int:
    out = _run("task", "add", title, "--description", "short", *flags)
    return int(out.split("Added E-")[1].split(":")[0])


def _ratings_of(item_id: int) -> tuple:
    row = db.query(
        f"SELECT {ratings.select_sql('tasks')} FROM tasks WHERE id = ?",
        (item_id,),
    )[0]
    return row["complexity"], row["risk"]


def _rows(*args: str) -> list[dict]:
    """A --json listing's rows (provenance.attach wraps every list)."""
    return json.loads(_run(*args, "--json"))["rows"]


def _list_ids(*flags: str) -> list[int]:
    return [int(r["id"][2:]) for r in _rows("task", "list", *flags)]


def test_add_sets_both_ratings(seeded_project_at_cwd):
    tid = _add("Add a rated thing", "--complexity", "low", "--risk", "HIGH")
    assert _ratings_of(tid) == ("low", "high")


def test_update_sets_and_clears_a_rating(seeded_project_at_cwd):
    tid = _add("Add a thing")
    _run("task", "update", f"E-{tid}", "--complexity", "medium", "--risk", "low")
    assert _ratings_of(tid) == ("medium", "low")

    _run("task", "update", f"E-{tid}", "--risk", "none")
    assert _ratings_of(tid) == ("medium", None)

    _run("task", "clear", "complexity", f"E-{tid}")
    assert _ratings_of(tid) == (None, None)


def test_an_unknown_level_is_refused_before_anything_is_written(seeded_project_at_cwd):
    tid = _add("Add a thing")
    out = _run("task", "update", f"E-{tid}", "--complexity", "trivial", ok=False)
    assert "trivial" in out
    assert _ratings_of(tid) == (None, None)


def test_tier_is_gone_not_ignored(seeded_project_at_cwd):
    """A removed flag must fail loudly: a silently accepted --tier would let a
    caller believe a task was rated."""
    out = _run("task", "add", "Add a thing", "--tier", "1", ok=False)
    assert "--tier" in out


def test_list_filters_by_rating_and_by_unrated(seeded_project_at_cwd):
    low = _add("Add a low thing", "--complexity", "low", "--risk", "low")
    high = _add("Add a high thing", "--complexity", "high", "--risk", "low")
    unrated = _add("Add an unrated thing")

    assert _list_ids("--complexity", "low") == [low]
    assert _list_ids("--risk", "low") == [low, high]
    assert _list_ids("--complexity", "none") == [unrated]


def test_list_sorts_by_complexity_unrated_last(seeded_project_at_cwd):
    high = _add("Add a high thing", "--complexity", "high")
    unrated = _add("Add an unrated thing")
    low = _add("Add a low thing", "--complexity", "low")

    assert _list_ids("--sort", "complexity") == [low, high, unrated]


def test_list_json_carries_both_ratings(seeded_project_at_cwd):
    tid = _add("Add a thing", "--risk", "medium")
    row = next(r for r in _rows("task", "list") if r["id"] == f"E-{tid}")
    assert row["complexity"] is None and row["risk"] == "medium"
    assert "tier" not in row


def test_list_table_shows_a_rating_column_only_when_something_is_rated(
    seeded_project_at_cwd
):
    _add("Add an unrated thing")
    assert "Rating" not in _run("task", "list")

    _add("Add a rated thing", "--complexity", "low", "--risk", "high")
    out = _run("task", "list")
    assert "Rating" in out and "low/high" in out


def test_next_filters_by_rating(seeded_project_at_cwd):
    rated = _add("Add a rated thing", "--complexity", "low", "--risk", "low",
                 "--status", "unplanned")
    _add("Add an unrated thing", "--status", "unplanned")

    rows = _rows("task", "next", "--complexity", "low")
    assert [r["id"] for r in rows] == [f"E-{rated}"]
    assert rows[0]["complexity"] == "low" and rows[0]["risk"] == "low"


@pytest.mark.parametrize("flags,expected", [
    ((), "complexity unrated · risk unrated"),
    (("--complexity", "low", "--risk", "high"), "complexity low · risk high"),
])
def test_show_always_renders_the_ratings(flags, expected, seeded_project_at_cwd):
    """Always, `unrated` included — tier rendered only when set, which is why
    it was invisible for ~98% of tasks."""
    tid = _add("Add a thing", *flags)
    out = _run("task", "show", f"E-{tid}", "--no-color")
    line = next(l for l in out.splitlines() if l.startswith("Ratings:"))
    assert expected in line, line


def test_show_json_carries_both_ratings(seeded_project_at_cwd):
    tid = _add("Add a thing", "--complexity", "medium")
    payload = json.loads(_run("task", "show", f"E-{tid}", "--json"))
    assert payload["complexity"] == "medium" and payload["risk"] is None
    assert "tier" not in payload


def test_submit_and_approve_flags_through_the_cli(seeded_project_at_cwd):
    tid = _add("Add a thing")
    out = _run("task", "submit", f"E-{tid}", ok=False)
    assert "unrated" in out

    _run("task", "submit", f"E-{tid}", "--complexity", "low", "--risk", "medium")
    out = _run("task", "approve", f"E-{tid}", "--risk", "low")
    assert "Ratified: complexity low, risk low" in out
    assert _ratings_of(tid) == ("low", "low")
