"""Tests for E-1866: session provenance in `endless task show`.

Two surfaces:
  - the `Created:` line gains ` by ES-NNN (E-NNN)` naming the session that
    created the task (the `surfaced` session_tasks row, per ED-1497), and
  - a `Touched by:` block lists every session that touched the task, laid out
    as a peer of the existing `This task:` relations block.

Plus the input side that makes those ids usable: `session goto` accepts the
`ES-NNNN` form the block prints (E-1261).
"""

import json

import pytest
from click.testing import CliRunner

from endless import db, session_cmd, task_cmd
from endless.cli import main


def _add_task(title: str, status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _add_session(
    session_id: int, state: str = "idle", task_id: int | None = None
) -> int:
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state, task_id, "
        "started_at) VALUES (?, ?, 1, ?, ?, datetime('now'))",
        (session_id, f"uuid-{session_id}", state, task_id),
    )
    return session_id


# Relation ids mirror session_task_relations' seeded rows (ED-1497).
_CLAIMED, _SURFACED, _REVISITED = 1, 2, 3


def _touch(session_id: int, task_id: int, relation: int | None, when: str) -> None:
    """Insert a session_tasks row directly.

    The real write path is the Go event executor's upsertSessionTask; driving it
    here would couple a rendering test to the binary, session resolution, and
    git. These tests' subject is the read/render side, so they seed the
    projection the executor produces.
    """
    db.execute(
        "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, "
        "updated_at) VALUES (?, ?, ?, ?, ?)",
        (session_id, task_id, relation, when, when),
    )


def _show(tid: int, *args: str) -> str:
    result = CliRunner().invoke(main, ["task", "show", f"E-{tid}", *args])
    assert result.exit_code == 0, result.output
    return result.output


# --------------------------------------------------------------------------
# session_id_display
# --------------------------------------------------------------------------


def test_session_id_display_uses_two_letter_prefix():
    """Sessions render ES-NNN so they can't be mistaken for a task's E-NNN."""
    assert task_cmd.session_id_display(1020) == "ES-1020"
    assert task_cmd.task_id_display(1020) == "E-1020"


# --------------------------------------------------------------------------
# Created: line
# --------------------------------------------------------------------------


def test_created_line_names_the_surfacing_session(seeded_project_at_cwd):
    tid = _add_task("Target")
    other = _add_task("The session's active task")
    _add_session(1020, state="idle", task_id=other)
    _touch(1020, tid, _SURFACED, "2026-08-04T04:38:51")

    out = _show(tid, "--no-color")
    assert f"by ES-1020 (E-{other})" in out


def test_created_line_omits_creator_without_a_surfaced_row(seeded_project_at_cwd):
    """A revisit is not a creation, so the Created: line stays as it was."""
    tid = _add_task("Target")
    _add_session(1020, task_id=None)
    _touch(1020, tid, _REVISITED, "2026-08-04T04:38:51")

    out = _show(tid, "--no-color")
    assert "ES-1020" in out          # still listed under Touched by:
    assert "by ES-1020" not in out   # but not credited with creating it


def test_created_line_omits_creator_for_pre_relation_rows(seeded_project_at_cwd):
    """relation_id is NULL for pre-E-1462 touches; the how is simply unknown."""
    tid = _add_task("Target")
    _add_session(696)
    _touch(696, tid, None, "2026-01-01T00:00:00")

    out = _show(tid, "--no-color")
    assert "by ES-696" not in out


def test_created_line_untouched_when_no_session_touched_the_task(
    seeded_project_at_cwd,
):
    tid = _add_task("Filed outside any session")
    out = _show(tid, "--no-color")
    assert " by ES-" not in out
    assert "Touched by:" not in out


def test_earliest_surfaced_row_wins(seeded_project_at_cwd):
    """Two surfacing sessions (an import replayed) → the first one created it."""
    tid = _add_task("Target")
    _add_session(100)
    _add_session(200)
    _touch(100, tid, _SURFACED, "2026-08-01T00:00:00")
    _touch(200, tid, _SURFACED, "2026-08-02T00:00:00")

    out = _show(tid, "--no-color")
    assert "by ES-100" in out
    assert "by ES-200" not in out


# --------------------------------------------------------------------------
# Touched by: block
# --------------------------------------------------------------------------


def test_touched_by_lists_every_session_with_relation_and_state(
    seeded_project_at_cwd,
):
    tid = _add_task("Target")
    a = _add_task("A")
    b = _add_task("B")
    _add_session(994, state="idle", task_id=a)
    _add_session(996, state="working", task_id=b)
    _touch(994, tid, _SURFACED, "2026-08-01T00:00:00")
    _touch(996, tid, _REVISITED, "2026-08-02T00:00:00")

    out = _show(tid, "--no-color")
    assert "Touched by:" in out
    assert f"- Revisited:  ES-996 (E-{b}) [working]" in out
    assert f"- Surfaced:   ES-994 (E-{a}) [idle]" in out


def test_touched_by_orders_most_recent_touch_first(seeded_project_at_cwd):
    """The block exists for navigation, so the freshest session leads."""
    tid = _add_task("Target")
    for sid, when in ((10, "2026-08-01"), (20, "2026-08-03"), (30, "2026-08-02")):
        _add_session(sid)
        _touch(sid, tid, _REVISITED, f"{when}T00:00:00")

    out = _show(tid, "--no-color")
    order = [out.index(f"ES-{sid}") for sid in (20, 30, 10)]
    assert order == sorted(order)


def test_touched_by_labels_a_null_relation_as_touched(seeded_project_at_cwd):
    tid = _add_task("Target")
    _add_session(696, state="ended")
    _touch(696, tid, None, "2026-01-01T00:00:00")

    out = _show(tid, "--no-color")
    assert "- Touched:  ES-696 [ended]" in out


def test_touched_by_omits_parens_when_session_has_no_bound_task(
    seeded_project_at_cwd,
):
    """A `claimed` session_tasks row whose session no longer points at the task
    keeps its stored label — the Claimed: override reads `sessions.task_id`, and
    this session has none."""
    tid = _add_task("Target")
    _add_session(696, state="ended", task_id=None)
    _touch(696, tid, _CLAIMED, "2026-01-01T00:00:00")

    out = _show(tid, "--no-color")
    assert "- Claimed:  ES-696 [ended]" in out


# --------------------------------------------------------------------------
# Claimed: is rendered from `sessions`, not from `session_tasks` (E-1967)
# --------------------------------------------------------------------------


def test_claimant_renders_claimed_over_a_surfaced_row(seeded_project_at_cwd):
    """`session_tasks.relation_id` is stamped at first touch and only ever
    upgraded, so a session that FILED a task and later claimed it kept reading
    `Surfaced` forever. Ownership lives in `sessions.task_id`; the label follows
    it."""
    tid = _add_task("Target")
    _add_session(1046, state="ended", task_id=tid)
    _touch(1046, tid, _SURFACED, "2026-08-01T00:00:00")

    out = _show(tid, "--no-color")
    assert f"- Claimed:  ES-1046 (E-{tid}) [ended]" in out
    assert "Surfaced" not in out


def test_claimant_renders_claimed_over_a_revisited_row(seeded_project_at_cwd):
    """The E-1859 case: the claiming session was recorded `Revisited`, which is
    what made "which sessions claimed this" unanswerable."""
    tid = _add_task("Target")
    _add_session(1046, state="ended", task_id=tid)
    _touch(1046, tid, _REVISITED, "2026-08-01T00:00:00")

    out = _show(tid, "--no-color")
    assert f"- Claimed:  ES-1046 (E-{tid}) [ended]" in out


def test_claimant_with_no_session_tasks_row_is_still_listed(
    seeded_project_at_cwd,
):
    """The touch is recorded from the event's ACTOR, so a claim driven from a
    plain shell binds the session and records no touch at all. Reading only
    `session_tasks` would leave that claimant invisible here while the spawn
    guard refuses in its name."""
    tid = _add_task("Target")
    _add_session(1046, state="ended", task_id=tid)

    out = _show(tid, "--no-color")
    assert f"- Claimed:  ES-1046 (E-{tid}) [ended]" in out


def test_a_claim_does_not_cost_the_session_credit_for_filing(
    seeded_project_at_cwd,
):
    """Created: is derived from the `surfaced` touch, so the Claimed: override
    must not erase the raw relation underneath it."""
    tid = _add_task("Target")
    _add_session(1046, state="ended", task_id=tid)
    _touch(1046, tid, _SURFACED, "2026-08-01T00:00:00")

    out = _show(tid, "--no-color")
    assert f"by ES-1046 (E-{tid})" in out
    assert "- Claimed:" in out


def test_non_claimants_keep_their_own_labels(seeded_project_at_cwd):
    tid = _add_task("Target")
    other = _add_task("Another task")
    _add_session(1046, state="ended", task_id=tid)
    _add_session(994, state="idle", task_id=other)
    _touch(1046, tid, _REVISITED, "2026-08-02T00:00:00")
    _touch(994, tid, _REVISITED, "2026-08-01T00:00:00")

    out = _show(tid, "--no-color")
    assert "- Claimed:    ES-1046" in out
    assert f"- Revisited:  ES-994 (E-{other}) [idle]" in out


def test_json_and_llm_carry_the_claimed_relation(seeded_project_at_cwd):
    tid = _add_task("Target")
    _add_session(1046, state="ended", task_id=tid)
    _touch(1046, tid, _REVISITED, "2026-08-01T00:00:00")

    payload = json.loads(_show(tid, "--json"))
    assert [t["relation"] for t in payload["touched_by"]] == ["claimed"]
    assert f"touched_by=claimed ES-1046 (E-{tid}) [ended]" in _show(tid, "--llm")


def test_touched_by_survives_a_deleted_session(seeded_project_at_cwd):
    """session_tasks carries no FK on purpose: the touch outlives the session."""
    tid = _add_task("Target")
    _touch(555, tid, _REVISITED, "2026-08-01T00:00:00")

    out = _show(tid, "--no-color")
    assert "- Revisited:  ES-555 [gone]" in out


def test_touched_by_colors_finished_sessions_like_terminal_relations(
    seeded_project_at_cwd,
):
    """A finished session reads green (like a resolved relation); a live one
    yellow. `gone` counts as finished — an absent session record can't be live."""
    tid = _add_task("Target")
    _add_session(10, state="working")
    _add_session(20, state="ended")
    _touch(10, tid, _REVISITED, "2026-08-03T00:00:00")
    _touch(20, tid, _REVISITED, "2026-08-02T00:00:00")
    _touch(30, tid, _REVISITED, "2026-08-01T00:00:00")  # no sessions row → gone

    result = CliRunner().invoke(main, ["task", "show", f"E-{tid}"], color=True)
    assert result.exit_code == 0, result.output
    assert "ES-10 [\x1b[33mworking\x1b[0m]" in result.output
    assert "ES-20 [\x1b[32mended\x1b[0m]" in result.output
    assert "ES-30 [\x1b[32mgone\x1b[0m]" in result.output


def test_touched_by_shares_a_label_column_with_this_task(seeded_project_at_cwd):
    """The two bullet blocks are siblings, so their ids line up."""
    tid = _add_task("Target")
    blocked = _add_task("Downstream")
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, "
        "dep_type) VALUES ('task', ?, 'task', ?, 'blocks')",
        (tid, blocked),
    )
    _add_session(994, state="idle")
    _touch(994, tid, _REVISITED, "2026-08-01T00:00:00")

    out = _show(tid, "--no-color")
    links_row = next(ln for ln in out.splitlines() if ln.startswith("- Blocks:"))
    touch_row = next(ln for ln in out.splitlines() if ln.startswith("- Revisited:"))
    assert links_row.index("E-") == touch_row.index("ES-")


def test_touched_by_follows_this_task(seeded_project_at_cwd):
    tid = _add_task("Target")
    blocked = _add_task("Downstream")
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, "
        "dep_type) VALUES ('task', ?, 'task', ?, 'blocks')",
        (tid, blocked),
    )
    _add_session(994)
    _touch(994, tid, _REVISITED, "2026-08-01T00:00:00")

    out = _show(tid, "--no-color")
    assert out.index("This task:") < out.index("Touched by:")


# --------------------------------------------------------------------------
# --json / --llm carry the same facts
# --------------------------------------------------------------------------


def test_json_reports_created_by_and_touched_by(seeded_project_at_cwd):
    tid = _add_task("Target")
    a = _add_task("A")
    _add_session(994, state="idle", task_id=a)
    _add_session(996, state="working", task_id=None)
    _touch(994, tid, _SURFACED, "2026-08-01T00:00:00")
    _touch(996, tid, _REVISITED, "2026-08-02T00:00:00")

    payload = json.loads(_show(tid, "--json"))
    assert payload["created_by"] == {
        "session": "ES-994",
        "relation": "surfaced",
        "task": f"E-{a}",
        "state": "idle",
        "touched_at": "2026-08-01T00:00:00",
    }
    assert [t["session"] for t in payload["touched_by"]] == ["ES-996", "ES-994"]
    assert payload["touched_by"][0]["task"] is None


def test_json_created_by_is_null_without_a_creator(seeded_project_at_cwd):
    tid = _add_task("Target")
    payload = json.loads(_show(tid, "--json"))
    assert payload["created_by"] is None
    assert payload["touched_by"] == []


def test_llm_reports_created_by_and_touched_by(seeded_project_at_cwd):
    tid = _add_task("Target")
    a = _add_task("A")
    _add_session(994, state="idle", task_id=a)
    _touch(994, tid, _SURFACED, "2026-08-01T00:00:00")

    out = _show(tid, "--llm")
    assert f"created_by=ES-994 (E-{a})" in out
    assert f"touched_by=surfaced ES-994 (E-{a}) [idle]" in out


def test_llm_omits_both_lines_when_untouched(seeded_project_at_cwd):
    tid = _add_task("Target")
    out = _show(tid, "--llm")
    assert "created_by=" not in out
    assert "touched_by=" not in out


# --------------------------------------------------------------------------
# session goto accepts the ES-NNNN form the block prints
# --------------------------------------------------------------------------


def _live(session_id: int, task_id: int | None, pane: str) -> dict:
    return {
        "endless_session_id": session_id,
        "task_id": task_id,
        "pane_id": pane,
        "harness_session_id": f"uuid-{session_id}",
        "last_activity": "2026-08-04T00:00:00",
    }


def test_goto_resolves_es_prefix_to_a_session(monkeypatch):
    monkeypatch.setattr(session_cmd, "_pane_exists", lambda pane: True)
    live = [_live(1020, 1865, "%7")]
    pane, label = session_cmd._resolve_goto_target("ES-1020", live)
    assert pane == "%7"
    assert "1020" in label


def test_goto_es_prefix_is_case_insensitive(monkeypatch):
    monkeypatch.setattr(session_cmd, "_pane_exists", lambda pane: True)
    live = [_live(1020, 1865, "%7")]
    assert session_cmd._resolve_goto_target("es-1020", live)[0] == "%7"


def test_goto_es_prefix_never_resolves_to_a_task(monkeypatch):
    """`ES-42` names session 42, even when task E-42 has a live session."""
    monkeypatch.setattr(session_cmd, "_pane_exists", lambda pane: True)
    live = [_live(42, None, "%1"), _live(99, 42, "%2")]
    assert session_cmd._resolve_goto_target("ES-42", live)[0] == "%1"
    # The bare integer, by contrast, is ambiguous and refuses to guess.
    with pytest.raises(SystemExit):
        session_cmd._resolve_goto_target("42", live)


def test_goto_es_prefix_not_live_is_recoverable(monkeypatch):
    """Not-live raises _GotoNotLive carrying the bare id, so --resume works."""
    monkeypatch.setattr(session_cmd, "_pane_exists", lambda pane: True)
    with pytest.raises(session_cmd._GotoNotLive) as excinfo:
        session_cmd._resolve_goto_target("ES-1020", [])
    assert excinfo.value.ref == "1020"


def test_goto_still_resolves_e_prefix_to_a_task(monkeypatch):
    monkeypatch.setattr(session_cmd, "_pane_exists", lambda pane: True)
    live = [_live(99, 42, "%2")]
    pane, label = session_cmd._resolve_goto_target("E-42", live)
    assert pane == "%2"
    assert "E-42" in label


# ── E-2106: a session whose Claude transcript is gone ────────────────────────
#
# The database row and the worktree survive a lost transcript, so a `Touched by:`
# row goes on naming a session that did real work — while `session goto --resume`
# and `session resume` can no longer open it. The marker puts that loss where the
# task is read, instead of leaving it to surface as a failed resume weeks later.


def test_touched_by_marks_a_gone_transcript(seeded_project_at_cwd):
    """`isolated_env` points the transcript home at an empty directory, so this
    session's uuid has nothing behind it."""
    tid = _add_task("lost its session")
    _add_session(4242, state="ended", task_id=tid)

    out = _show(tid, "--no-color")
    assert "ES-4242" in out
    assert "(transcript gone)" in out


def test_touched_by_is_quiet_when_the_transcript_is_there(
    seeded_project_at_cwd, stage_transcript
):
    tid = _add_task("still resumable")
    _add_session(4243, state="ended", task_id=tid)
    stage_transcript("uuid-4243")

    out = _show(tid, "--no-color")
    assert "ES-4243" in out
    assert "transcript gone" not in out


def test_a_session_with_no_uuid_is_not_reported_as_lost(seeded_project_at_cwd):
    """A session_tasks row can name a session whose sessions row is gone —
    there is no uuid to stat, and "nothing to look for" is not evidence of
    loss."""
    tid = _add_task("orphan touch")
    _touch(9911, tid, _SURFACED, "2026-01-01T00:00:00")

    assert "transcript gone" not in _show(tid, "--no-color")
