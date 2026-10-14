"""E-2280: the plan-item steps a test takes a task through, as a session would.

A todo or bugfix must have plan items to be approved or spawned, and any task
with items is reconciled against them before `unverified` or `assumed`. Tests
that walk a task through those statuses take these steps on the way.
"""

import tempfile
from pathlib import Path

from endless import plan_items


def itemize(task_id: int, statement: str = "The change works",
            approved: bool = False) -> None:
    """Give the task one planned deliverable item, as a planning session would
    before submitting it — and, with `approved`, have the user approve it as
    `task plan approve` does, which approving the task needs (E-2280)."""
    out = plan_items.emit_ops(task_id, [{"op": "add", "kind": "deliverable",
                                         "statement": statement}],
                              quiet=True, keep_status=True)
    if approved:
        plan_items.baseline(task_id, [plan_items.parse_label(s) for s in out["added"]])


def reconcile_as_planned(task_id: int) -> None:
    """Record a reconciliation of every active item as done as planned (an
    exclusion as kept), so the gate on `unverified` and `assumed` passes."""
    body = "".join(
        f'[[entry]]\nitem = "{plan_items.label(it["seq"])}"\n'
        f'verdict = "{"as_planned" if it["kind"] == "deliverable" else "kept"}"\n\n'
        for it in plan_items.items(task_id))
    with tempfile.TemporaryDirectory() as d:
        path = Path(d) / "reconcile.toml"
        path.write_text(body)
        plan_items.reconcile(task_id, str(path))
