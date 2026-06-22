"""Epic command logic — thin wrappers over the existing task machinery
with `type=epic` pinned (E-1540).

`endless epic` is a convenience surface parallel to `endless decision`. Each
subcommand defers to a `task_cmd` function: `epic add` / `epic update` pin the
task type to `epic`, `epic list` filters the shared listing path to epic-typed
rows, and `epic show` reuses the task detail renderer with children on by
default.

Promotion validation (E-1543), auto-derivation (E-1541), and the
pause-on-revisit hook (E-1542) are owned by sibling tasks and are not
implemented here.
"""

from endless import task_cmd


def add_epic(
    title: str,
    description: str | None = None,
    text: str | None = None,
    phase: str = "now",
    project_name: str | None = None,
    after: int | None = None,
    parent_id: int | None = None,
    status: str | None = None,
    tier: int | None = None,
    force: bool = False,
) -> int | None:
    """Create an epic-typed task (wraps task_cmd.add_item with type=epic)."""
    return task_cmd.add_item(
        title,
        description=description,
        text=text,
        phase=phase,
        project_name=project_name,
        after=after,
        parent_id=parent_id,
        task_type="epic",
        status=status,
        tier=tier,
        force=force,
    )


def list_epics(
    project_name: str | None = None,
    show_all: bool = False,
    status_filter: list[str] | None = None,
    phase_filter: str | None = None,
    tier_filter: int | None = None,
    parent_id: int | None = None,
    sort_by: str | None = None,
    tree: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """List epic-typed tasks (wraps show_plan with type_filter='epic')."""
    task_cmd.show_plan(
        project_name=project_name,
        show_all=show_all,
        status_filter=status_filter,
        phase_filter=phase_filter,
        tier_filter=tier_filter,
        parent_id=parent_id,
        sort_by=sort_by,
        tree=tree,
        llm=llm,
        as_json=as_json,
        type_filter="epic",
    )


def show_epic(
    item_id: int,
    show_description: bool = True,
    show_analysis: bool = False,
    show_text: bool = False,
    show_children: bool = True,
    show_outcome: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """Show an epic's detail (wraps detail_item; children on by default)."""
    task_cmd.detail_item(
        item_id,
        show_description=show_description,
        show_analysis=show_analysis,
        show_text=show_text,
        show_children=show_children,
        show_outcome=show_outcome,
        llm=llm,
        as_json=as_json,
    )


def update_epic(
    item_id: int,
    status: str | None = None,
    title: str | None = None,
    description: str | None = None,
    text: str | None = None,
    parent_id: int | None = None,
    phase: str | None = None,
    tier: int | None = None,
    analysis: str | None = None,
    outcome: str | None = None,
    force: bool = False,
):
    """Update an epic (wraps task_cmd.update_plan with type=epic pinned).

    Pinning task_type='epic' means updating an existing task-typed row through
    this verb also promotes it to an epic.
    """
    task_cmd.update_plan(
        item_id,
        status=status,
        title=title,
        description=description,
        text=text,
        parent_id=parent_id,
        phase=phase,
        tier=tier,
        task_type="epic",
        analysis=analysis,
        outcome=outcome,
        force=force,
    )
