"""E-2571: `task remove` emits `task.removed`, the kind ED-1547's vocabulary
calls for, and the row it removes is retained.

Until E-2571 the kind was `task.deleted`, which described a hard delete that
has not happened since E-1929. This runs the whole path — Click command, event
bridge, `endless-go event emit`, the Go executor — so it pins both that the
Python emitter sends the live spelling (the Go front door refuses a retired
one) and that the ledger line a later rebuild replays carries it.
"""

import json

from click.testing import CliRunner

from endless import db
from endless.cli import main


def _invoke(*args):
    return CliRunner().invoke(main, list(args))


def _ledger_kinds(project_dir):
    kinds = []
    for f in (project_dir / ".endless" / "db-ledger").glob("db-entries-*.jsonl"):
        for line in f.read_text().splitlines():
            kinds.append(json.loads(line)["kind"])
    return kinds


def test_task_remove_emits_task_removed_and_retains_the_row(seeded_project_at_cwd):
    result = _invoke("task", "add", "Fix the doomed one", "--description", "d")
    assert result.exit_code == 0, result.output
    task_id = db.query(
        "SELECT id FROM tasks WHERE title = 'Fix the doomed one'")[0]["id"]

    result = _invoke("task", "remove", f"E-{task_id}")
    assert result.exit_code == 0, result.output

    row = db.query("SELECT removed FROM tasks WHERE id = ?", (task_id,))
    assert row and row[0]["removed"] == 1, "the row must be retained and marked"
    assert not db.query("SELECT 1 FROM live_tasks WHERE id = ?", (task_id,))

    kinds = _ledger_kinds(seeded_project_at_cwd)
    assert "task.removed" in kinds
    assert "task.deleted" not in kinds
