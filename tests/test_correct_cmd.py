"""Tests for `endless correct` (E-2486), the Python half: the agent refusal
before anything runs, the relay to `endless-go correct`, and the rendering.

The entries, the gates and the apply are Go's and are tested there
(internal/events/correction_test.go, internal/correctcmd). What is pinned here
is the seam: a person's command reaches Go with the envelope `emit_event` would
have built, and an agent's never leaves Python.
"""

import json
import subprocess

import pytest
from click.testing import CliRunner

from endless import db
from endless.cli import main


def _seed_landed_task(title: str = "Landed with a skipped gate") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, 'landed', 1, 'now', datetime('now'))",
        (title,),
    )
    return cur.lastrowid


def _status(task_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))[0]["status"]


def test_an_agent_is_refused_before_anything_runs(seeded_project_at_cwd, monkeypatch):
    monkeypatch.setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
    tid = _seed_landed_task()

    def no_subprocess(*args, **kwargs):  # pragma: no cover - the assertion
        raise AssertionError(f"an agent's correct reached a subprocess: {args}")
    monkeypatch.setattr(subprocess, "run", no_subprocess)

    r = CliRunner().invoke(
        main, ["correct", "task", f"E-{tid}", "--set", "status=assumed",
               "--reason", "x", "--skip-gates"])
    assert r.exit_code != 0
    assert "the user's command" in r.stderr
    assert _status(tid) == "landed"


def test_a_person_corrects_a_landed_task_past_the_gates(seeded_project_at_cwd):
    tid = _seed_landed_task()
    r = CliRunner().invoke(
        main, ["correct", "task", f"E-{tid}", "--set", "status=assumed",
               "--reason", "the land skipped approval", "--skip-gates"])
    assert r.exit_code == 0, r.stderr
    assert _status(tid) == "assumed"
    assert f"Corrected task {tid}: status landed → assumed" in r.stdout
    assert "gates skipped" in r.stdout
    assert "Reason: the land skipped approval" in r.stdout

    # Both entries are in the project's ledger, adjacent, and committed under
    # a subject that names the correction.
    ledger = seeded_project_at_cwd / ".endless" / "db-ledger"
    lines = []
    for seg in sorted(ledger.glob("*.jsonl")):
        lines += [json.loads(l) for l in seg.read_text().splitlines() if l]
    kinds = [e["kind"] for e in lines]
    assert kinds[-2:] == ["ledger.row_changed", "ledger.corrected"]
    corrected = lines[-1]["payload"]
    assert corrected["reason"] == "the land skipped approval"
    assert corrected["skip_gates"] is True
    assert corrected["before"][0]["values"]["status"] == "landed"
    assert len(corrected["refs"]) == 1
    subject = subprocess.run(
        ["git", "log", "-1", "--format=%s"], cwd=seeded_project_at_cwd,
        capture_output=True, text=True, check=True).stdout.strip()
    assert subject.startswith(f"Endless: ledger.corrected E-{tid}")


def test_a_gate_refusal_is_relayed_and_writes_nothing(seeded_project_at_cwd):
    tid = _seed_landed_task()
    r = CliRunner().invoke(
        main, ["correct", "task", f"E-{tid}", "--set", "status=assumed",
               "--reason", "x"])
    assert r.exit_code != 0
    assert "gate refuses" in r.stderr
    assert "--skip-gates" in r.stderr
    assert _status(tid) == "landed"
    assert not (seeded_project_at_cwd / ".endless" / "db-ledger").exists()


def test_an_annotation_records_a_reason_and_no_change(seeded_project_at_cwd):
    tid = _seed_landed_task()
    r = CliRunner().invoke(
        main, ["correct", "task", f"E-{tid}", "--reason", "landed by hand on 2026-11-01"])
    assert r.exit_code == 0, r.stderr
    assert f"Annotated task {tid}" in r.stdout
    assert "(an annotation)" in r.stdout
    assert _status(tid) == "landed"


def test_reason_is_required(seeded_project_at_cwd):
    r = CliRunner().invoke(main, ["correct", "task", "E-1", "--set", "status=assumed"])
    assert r.exit_code != 0
    assert "--reason" in r.stderr
