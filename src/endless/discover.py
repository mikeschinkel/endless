"""Discover command — find and register unregistered projects."""

from pathlib import Path

import click
from tabulate import tabulate

from endless import db, config
from endless.models import Signal
from endless.signals import detect_signals, count_git_subdirs
from endless.register import register_project
from endless.picker import pick_items, pick_groups, PickerItem
from endless.ownership import is_mine

TIER_LABELS = {
    1: "Active AI Projects",
    2: "AI-Configured",
    3: "Active Dev Projects",
    4: "Dormant Projects",
}


def _is_registered(path: Path) -> bool:
    return db.exists(
        "SELECT 1 FROM projects WHERE path=?",
        (str(path),),
    )


def _print_tier_table(entries: list[Signal]):
    rows = [
        [sig.name, sig.language or "-", sig.description, sig.age_str]
        for sig in entries
    ]
    table = tabulate(
        rows,
        headers=["NAME", "LANG", "SIGNALS", "CHANGED"],
        tablefmt="simple",
    )
    for line in table.splitlines():
        click.echo(f"  {line}")


def _register_one(sig: Signal) -> bool:
    try:
        register_project(sig.path, infer=True)
        return True
    except Exception:
        return False


def _present_groups(groups: list[tuple[Path, int]]):
    if not groups:
        return

    click.echo()
    click.echo(click.style(
        f"Found {len(groups)} new group directory(s):", bold=True
    ))

    group_tuples = [
        (str(dir_path), dir_path.name, subcount)
        for dir_path, subcount in groups
    ]

    decisions = pick_groups(group_tuples)

    for dir_path, _ in groups:
        key = str(dir_path)
        action = decisions.get(key, "no")
        if action == "yes":
            config.mark_as_group(dir_path)
            click.echo(
                click.style("•", fg="cyan")
                + f" Marked {click.style(dir_path.name, bold=True)}"
                + " as a project group"
            )
        elif action == "ignore":
            config.add_ignore(dir_path)
            click.echo(
                click.style("•", fg="cyan")
                + f" Ignored {click.style(dir_path.name, bold=True)}"
            )


def _present_tier(
    tier_num: int, entries: list[Signal], show_dormant: bool = False,
) -> int:
    if not entries:
        return 0

    label = TIER_LABELS.get(tier_num, f"Tier {tier_num}")

    click.echo()
    click.echo(click.style(
        f"--- Tier {tier_num}: {label} ({len(entries)} found) ---",
        bold=True,
    ))

    if tier_num == 4 and not show_dormant:
        click.echo()
        click.echo(click.style(
            "  Skipped by default. Use --all to review.", dim=True
        ))
        return 0

    # Build picker items, sorted by path, showing ~/relative path
    home = str(Path.home())
    sorted_entries = sorted(entries, key=lambda s: str(s.path))
    items = [
        PickerItem(
            key=str(sig.path),
            label=str(sig.path).replace(home, "~"),
            default_selected=True,
        )
        for sig in sorted_entries
    ]

    decisions = pick_items(
        items,
        message=f"Register {label}",
    )

    # Process decisions
    registered = 0
    ignored = 0
    sig_by_key = {str(sig.path): sig for sig in entries}

    for key, action in decisions.items():
        sig = sig_by_key[key]
        if action == "yes":
            if _register_one(sig):
                registered += 1
        elif action == "ignore":
            config.add_ignore(sig.path)
            ignored += 1

    parts = []
    if registered:
        parts.append(f"{registered} registered")
    if ignored:
        parts.append(f"{ignored} ignored")
    skipped = len(entries) - registered - ignored
    if skipped:
        parts.append(f"{skipped} skipped")
    click.echo(
        click.style("•", fg="cyan")
        + f" Tier {tier_num}: " + ", ".join(parts)
    )

    return registered


def run_discover(
    discover_path: str | None = None,
    show_all: bool = False,
    reset: bool = False,
):
    if discover_path:
        p = Path(discover_path).expanduser().resolve()
        if not p.is_dir():
            raise click.ClickException(
                f"Directory not found: {discover_path}"
            )
        roots = [p]
    else:
        roots = config.get_roots()

    if not roots:
        raise click.ClickException("No roots to scan")

    if reset:
        click.echo(
            click.style("•", fg="cyan")
            + " Resetting — re-evaluating all directories..."
        )
    else:
        click.echo(
            click.style("•", fg="cyan")
            + " Scanning for unregistered projects..."
        )

    groups: list[tuple[Path, int]] = []
    tiers: dict[int, list[Signal]] = {1: [], 2: [], 3: [], 4: []}
    skipped = 0
    not_mine = 0

    for root in roots:
        for child in sorted(root.iterdir()):
            if not child.is_dir() or child.name.startswith("."):
                continue
            if not reset and config.is_ignored(child):
                continue

            is_known_group = config.is_group_dir(child)
            git_sub_count = count_git_subdirs(child)

            if is_known_group or git_sub_count >= 2:
                if reset or not is_known_group:
                    groups.append((child, git_sub_count))

                for subdir in sorted(child.iterdir()):
                    if not subdir.is_dir():
                        continue
                    if subdir.name.startswith("."):
                        continue
                    if not reset and config.is_ignored(subdir):
                        continue
                    if not reset and _is_registered(subdir):
                        continue
                    if not is_mine(subdir):
                        config.add_ignore(subdir)
                        not_mine += 1
                        continue
                    sig = detect_signals(subdir)
                    if sig.tier <= 4:
                        tiers[sig.tier].append(sig)
                    else:
                        skipped += 1
                continue

            if not reset and _is_registered(child):
                continue

            # Skip repos that aren't mine — auto-ignore them
            if not is_mine(child):
                config.add_ignore(child)
                not_mine += 1
                continue

            sig = detect_signals(child)
            if sig.tier <= 4:
                tiers[sig.tier].append(sig)
            else:
                skipped += 1

    total_found = sum(len(v) for v in tiers.values()) + len(groups)
    if total_found == 0:
        click.echo(
            click.style("•", fg="cyan")
            + " No new unregistered projects found."
        )
        return

    _present_groups(groups)

    # Filter out entries whose parents were just ignored
    for tier_num in tiers:
        tiers[tier_num] = [
            sig for sig in tiers[tier_num]
            if not config.is_ignored(sig.path)
        ]

    total_registered = 0
    for tier_num in (1, 2, 3):
        total_registered += _present_tier(tier_num, tiers[tier_num])

    if show_all:
        total_registered += _present_tier(4, tiers[4], show_dormant=True)
    elif tiers[4]:
        click.echo()
        click.echo(click.style(
            f"{len(tiers[4])} dormant project(s) skipped. "
            "Use --all to review.",
            dim=True,
        ))

    click.echo()
    summary = (
        click.style("Summary:", bold=True)
        + f" Registered {total_registered} project(s)"
    )
    if not_mine > 0:
        summary += f", {not_mine} not-mine repo(s) filtered"
    if skipped > 0:
        summary += f", {skipped} non-project dir(s) skipped"
    click.echo(summary)
