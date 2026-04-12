"""Interactive multi-select picker with InquirerPy."""

from dataclasses import dataclass

import click
from InquirerPy import inquirer
from InquirerPy.base.control import Choice


@dataclass
class PickerItem:
    """An item in the picker list."""
    key: str
    label: str
    detail: str = ""
    default_selected: bool = True


def pick_items(
    items: list[PickerItem],
    message: str = "Select items to register",
) -> dict[str, str]:
    """Present a checkbox list.

    Selected = 'yes' (register), unselected = 'ignore'.
    Returns {key: action} mapping.
    """
    if not items:
        return {}

    choices = [
        Choice(
            value=item.key,
            name=f"{item.label}  {item.detail}" if item.detail else item.label,
            enabled=item.default_selected,
        )
        for item in items
    ]

    click.echo()
    click.echo(
        click.style("  Tip:", dim=True)
        + " space=toggle, a=all, i=invert, enter=submit"
    )
    click.echo(
        click.style("  ", dim=True)
        + " selected=register, unselected=will mark as ignored"
    )
    click.echo()

    selected_keys = inquirer.checkbox(
        message=message,
        choices=choices,
        cycle=True,
        instruction="(space=toggle, a=all, i=invert)",
    ).execute()

    if selected_keys is None:
        return {item.key: "ignore" for item in items}

    selected_set = set(selected_keys)
    return {
        item.key: "yes" if item.key in selected_set else "ignore"
        for item in items
    }


def pick_groups(
    groups: list[tuple[str, str, int]],
) -> dict[str, str]:
    """Pick which directories to mark as groups.

    Selected = mark as group, unselected = ignore.

    Args:
        groups: list of (key, name, subdir_count) tuples

    Returns: {key: action} where action is 'yes' or 'ignore'
    """
    if not groups:
        return {}

    choices = [
        Choice(
            value=key,
            name=f"{name:<22} ({count} projects)",
            enabled=True,
        )
        for key, name, count in groups
    ]

    click.echo()
    click.echo(
        click.style("  Tip:", dim=True)
        + " space=toggle, a=all, i=invert, enter=submit"
    )
    click.echo(
        click.style("  ", dim=True)
        + " selected=mark as group, unselected=will mark as ignored"
    )

    selected_keys = inquirer.checkbox(
        message="Mark as project groups",
        choices=choices,
        cycle=True,
        instruction="(space=toggle, a=all, i=invert)",
    ).execute()

    if selected_keys is None:
        return {key: "ignore" for key, _, _ in groups}

    selected_set = set(selected_keys or [])
    return {
        key: "yes" if key in selected_set else "ignore"
        for key, _, _ in groups
    }
