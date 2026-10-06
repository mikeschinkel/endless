"""Discover command — find and register unregistered projects."""

from pathlib import Path

import click
from tabulate import tabulate

from endless import agent_help, config
from endless.project_path import match_project_path
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
    # Normalized comparison (E-2002): a discovered directory reached through a
    # symlink is the same project as the registered row, and must not be
    # offered as a new one.
    return match_project_path(path) is not None


def _candidate_dirs(root: Path) -> list[Path]:
    """The non-hidden directories discover may visit under root: its children
    and their children."""
    out = []
    for child in root.iterdir():
        if not child.is_dir() or child.name.startswith("."):
            continue
        out.append(child)
        try:
            out.extend(
                sub for sub in child.iterdir()
                if sub.is_dir() and not sub.name.startswith(".")
            )
        except OSError:
            pass
    return out


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
            raise agent_help.no_report(
                f"{p} is not a directory, so there is nothing to scan. "
                "Nothing was registered.",
                "Correct the path and retry",
                text=f"Directory not found: {discover_path}",
            )
        roots = [p]
    else:
        roots = config.get_roots()

    if not roots:
        # Two ways to have no roots, and they need opposite answers. If the
        # user pointed at a directory in the conversation, naming it as the
        # path argument is the whole fix. If they did not, the set of
        # directories Endless may go looking through is theirs to choose — no
        # CLI writes it, the ~/Projects default exists on few machines, and
        # discover ends in registration prompts about their own directories.
        raise agent_help.report_if(
            "No scan roots are configured and no directory was named, so "
            "discover had nothing to look at. Nothing was registered.",
            "the user has not said which directories Endless may scan",
            "retry naming the directory as the argument: "
            "endless discover <path>",
            "which directories to go looking through, and what to register "
            "out of them, is theirs to choose",
            text="No roots to scan",
        )

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
        if not reset:
            # One resolve call for every directory this walk may ask about,
            # rather than one shellout per directory (E-2251).
            config.prime_ignored(_candidate_dirs(root))
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
