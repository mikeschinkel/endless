"""`endless db recover` — undo a migration main should not have taken. E-2397.

On 2026-10-18 a task worktree's branch code migrated the main database to a
schema version the installed endless-go did not carry, and every session was
refused (ED-1570). Recovery took an hour by hand: picking the backup taken just
before the migration, finding and killing the processes holding the database,
a restore that silently produced an EMPTY database because the tmux status line
opened the path mid-swap, and a second restore.

This is that hour as one command. Go does the work (`endless-go event recover`):
it detects the condition, picks the backup by its link to the migration — never
the newest, which may carry the migration — takes the maintenance lock every
Endless opener honors, waits for open handles to close, installs the backup
atomically, verifies it, and releases the lock on every exit path. This module
only renders what Go reports; it opens no database itself.
"""

from __future__ import annotations

import click

from endless import config


def _echo_holders(plan: dict) -> None:
    holders = plan.get("holders") or []
    if plan.get("holders_unknown"):
        click.echo("  open handles:  UNKNOWN — no lsof and no /proc to ask")
    elif not holders:
        click.echo("  open handles:  none")
    else:
        click.echo(f"  open handles:  {len(holders)}")
        for h in holders:
            click.echo(f"                   pid {h['pid']:<7} {h['command']}")


def _echo_land(plan: dict) -> None:
    """The cheaper fix, when the branch that migrated main can land."""
    land = plan.get("land")
    if not land:
        return
    click.echo("")
    if land.get("landable"):
        click.echo(f"{land['task']}'s branch took the database to version "
                   f"{plan['db_version']}, and it can land. Landing it is the "
                   "cheaper fix: the installed endless catches up to the "
                   "database and nothing written since the migration is lost.")
        click.echo(f"    {land['command']}")
    else:
        status = land.get("status") or "unknown"
        click.echo(f"{land['task']}'s branch took the database to version "
                   f"{plan['db_version']}, but it cannot land yet (status "
                   f"{status}; a task lands from unlanded, after a passing "
                   "verify). Once it can, landing is the cheaper fix:")
        click.echo(f"    {land['command']}")


def render(plan: dict, dry_run: bool) -> None:
    """Echo `endless-go event recover`'s plan for a person."""
    db_version, binary_version = plan["db_version"], plan["binary_version"]
    if plan["status"] == "current":
        click.echo(f"Nothing to recover: {config.tilde(plan['db'])} is at "
                   f"schema version {db_version}, and this endless carries "
                   f"version {binary_version}. Nothing was changed.")
        return

    click.echo("Recovery plan (dry run — nothing written)" if dry_run
               else "Recovered")
    click.echo(f"  database:      {config.tilde(plan['db'])}")
    click.echo(f"                 schema version {db_version}, ahead of this "
               f"endless (version {binary_version})")
    click.echo(f"  backup:        {config.tilde(plan['backup'])}")
    click.echo(f"                 schema version {plan['backup_version']}")
    link = plan.get("link")
    if link:
        click.echo(f"                 taken before the migration from version "
                   f"{link['from']} to {link['to']}, at {link['at']}")
    else:
        click.echo("                 named with --backup; no migration links it")
    _echo_holders(plan)

    if not dry_run:
        parked = plan.get("parked") or []
        if parked:
            click.echo(f"  pre-restore:   {config.tilde(parked[0])}")
        check = plan.get("check") or {}
        click.echo(f"  verified:      journal_mode {check.get('journal_mode')}, "
                   f"integrity_check {check.get('integrity')}, schema version "
                   f"{check.get('version')}, row counts match the backup in "
                   f"{check.get('tables')} tables")
        click.echo("")
        click.echo("Task changes made after the backup are not in the restored "
                   "database; the ledger still holds them.")
    _echo_land(plan)
    if dry_run:
        click.echo("")
        click.echo("Dry run: no files were changed.")


def run_recover(backup: str | None, dry_run: bool, force: bool,
                wait: float) -> None:
    """Body of `endless db recover`."""
    from endless.event_bridge import recover_db

    # Like `db restore`: a destructive act a human aims by hand, so inside a
    # self-dev worktree it takes the ordinary explicit --db.
    config.require_db_context()
    if not dry_run:
        click.echo(f"Taking the maintenance lock; waiting up to {wait:g}s for "
                   "open handles to close...")
    plan = recover_db(backup, dry_run, force, wait)
    render(plan, dry_run)
