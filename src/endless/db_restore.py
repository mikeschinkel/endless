"""`endless db restore` — recover the database from a backup, safely. E-1942.

`endless db backup` has existed for a long time and `just land` calls it, so the
safety net was half built: there was no supported way to USE a backup. Recovery
was an unguided `cp`, and on 2026-08-10 that went wrong twice before it went
right. Both failures are what this module exists to prevent:

1. **Copied over a live database.** Six connections were open (two
   `session-status --monitor`, four `endless task show -p` abandoned in pagers
   for up to 22 days). The copy left a hot `endless.db-journal`; a read-only
   connection cannot roll back a hot journal, so every reader returned
   `database is locked`.

2. **Journal mode silently changed.** `endless db backup` uses `VACUUM INTO`,
   which writes a **rollback-journal** database. The live DB is WAL. After the
   copy every connection fought for an exclusive lock trying to switch back.

So: report the holders rather than killing them, move the whole pre-restore file
set aside (database *and* its `-wal`/`-shm`/`-journal` sidecars — a stale sidecar
beside a fresh file is failure 1 in a bottle), and re-establish WAL plus run
`PRAGMA integrity_check` afterwards, failing loudly on anything but `ok`.

Two deliberate choices worth naming:

- **Nothing here goes through `db.get_db()`.** That connection auto-migrates the
  schema it opens; pointing it at a backup would silently rewrite the very file
  we are validating. Every open in this module is a bare `sqlite3.connect`, and
  the backup is opened read-only.

- **Journal mode is read from the file header, not from a connection.** Bytes 18
  and 19 of a SQLite file are the write/read format versions, and `2` means WAL.
  Reading them takes no lock at all, which matters when the reason you are here
  is that everything returns `database is locked`.
"""

from __future__ import annotations

import os
import shutil
import sqlite3
import subprocess
import time
from dataclasses import dataclass, field
from pathlib import Path

import click

# The filename `monitor.BackupDB` writes: `endless-<YYYYmmdd-HHMMSS>.db`.
BACKUP_GLOB = "endless-*.db"

# Sidecar suffixes SQLite may leave beside a database file. `-journal` is the
# rollback journal (the hot-journal failure above), `-wal`/`-shm` the WAL pair.
SIDECAR_SUFFIXES = ("-wal", "-shm", "-journal")

# The first 16 bytes of any SQLite database file.
SQLITE_MAGIC = b"SQLite format 3\x00"

# Tables that make a file recognizably an Endless database rather than some
# other SQLite database that happens to be lying in the backups directory.
REQUIRED_TABLES = ("tasks", "projects")


# ---------------------------------------------------------------------------
# Inspection — all of it lock-free or read-only
# ---------------------------------------------------------------------------

def journal_mode_from_header(path: Path) -> str:
    """The journal mode of `path`, read from its file header without a lock.

    Returns "wal", "rollback", "empty" (zero-length file, which SQLite treats as
    a valid empty database), "missing", or "unknown" (present but not a SQLite
    file, or unreadable).
    """
    try:
        with open(path, "rb") as f:
            head = f.read(20)
    except FileNotFoundError:
        return "missing"
    except OSError:
        return "unknown"
    if head == b"":
        return "empty"
    if len(head) < 20 or not head.startswith(SQLITE_MAGIC):
        return "unknown"
    # Offset 18 is the file-format WRITE version: 1 = legacy (rollback), 2 = WAL.
    return "wal" if head[18] == 2 else "rollback"


def human_size(path: Path) -> str:
    try:
        size = path.stat().st_size
    except OSError:
        return "?"
    if size < 1024:
        return f"{size} B"
    scaled = float(size)
    for unit in ("KB", "MB", "GB"):
        scaled /= 1024.0
        if scaled < 1024 or unit == "GB":
            return f"{scaled:.1f} {unit}"
    raise AssertionError("unreachable: the GB branch always returns")


@dataclass
class BackupCheck:
    """The result of validating a candidate backup file."""

    ok: bool
    problem: str = ""
    integrity: str = ""


def check_backup(path: Path) -> BackupCheck:
    """Validate that `path` is a sound Endless database, without modifying it.

    Opened read-only through a `file:...?mode=ro` URI so a validation pass can
    never checkpoint, migrate, or otherwise rewrite the one file standing
    between the user and data loss.
    """
    if not path.exists():
        return BackupCheck(False, f"no such file: {path}")
    if not path.is_file():
        return BackupCheck(False, f"not a regular file: {path}")
    if path.stat().st_size == 0:
        return BackupCheck(False, f"backup is empty (0 bytes): {path}")
    if journal_mode_from_header(path) == "unknown":
        return BackupCheck(False, f"not a SQLite database: {path}")

    # as_uri() percent-encodes, which matters because SQLite's URI parser gives
    # `?`, `#` and `%` their own meaning — a backup path containing any of them
    # would otherwise be silently truncated or mis-decoded.
    uri = f"{path.resolve().as_uri()}?mode=ro"
    try:
        conn = sqlite3.connect(uri, uri=True, timeout=5)
    except sqlite3.Error as e:
        return BackupCheck(False, f"cannot open backup: {e}")
    try:
        rows = conn.execute("PRAGMA integrity_check").fetchall()
        integrity = "; ".join(str(r[0]) for r in rows) if rows else "no result"
        if integrity != "ok":
            return BackupCheck(False, f"integrity_check failed: {integrity}",
                               integrity)
        missing = [
            t for t in REQUIRED_TABLES
            if not conn.execute(
                "SELECT count(*) FROM sqlite_master "
                "WHERE type='table' AND name=?", (t,)
            ).fetchone()[0]
        ]
        if missing:
            return BackupCheck(
                False,
                f"not an Endless database (no {', '.join(missing)} table): {path}",
                integrity,
            )
    except sqlite3.DatabaseError as e:
        return BackupCheck(False, f"cannot read backup: {e}")
    finally:
        conn.close()
    return BackupCheck(True, "", integrity)


# ---------------------------------------------------------------------------
# Who has the database open
# ---------------------------------------------------------------------------

@dataclass
class Holder:
    pid: int
    command: str


def _pids_via_lsof(paths: list[Path]) -> list[int] | None:
    """PIDs holding any of `paths` open, per `lsof`, or None if lsof is absent.

    `lsof -t` exits 1 when nothing has the files open, which is a normal answer
    and not an error — only a missing binary means "could not determine".
    """
    if shutil.which("lsof") is None:
        return None
    try:
        out = subprocess.run(
            ["lsof", "-t", "--", *[str(p) for p in paths]],
            capture_output=True, text=True, timeout=15,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    pids = []
    for line in out.stdout.split():
        try:
            pids.append(int(line))
        except ValueError:
            continue
    return pids


def _pids_via_proc(paths: list[Path]) -> list[int] | None:
    """PIDs holding any of `paths` open, by walking /proc/*/fd (Linux).

    The fallback for a Linux box with no lsof installed, which is common enough
    on a server that leaving it out would make restore unusable there.
    """
    proc = Path("/proc")
    if not proc.is_dir():
        return None
    targets = {os.path.realpath(p) for p in paths}
    pids = []
    for entry in proc.iterdir():
        if not entry.name.isdigit():
            continue
        try:
            fds = list((entry / "fd").iterdir())
        except OSError:
            continue  # gone, or another user's process
        for fd in fds:
            try:
                if os.path.realpath(fd) in targets:
                    pids.append(int(entry.name))
                    break
            except OSError:
                continue
    return pids


def _commands_for_pids(pids: list[int]) -> dict[int, str]:
    """Full command lines for `pids` via one `ps` call. Missing pids are absent."""
    if not pids:
        return {}
    try:
        out = subprocess.run(
            ["ps", "-o", "pid=,command=", "-p", ",".join(str(p) for p in pids)],
            capture_output=True, text=True, timeout=15,
        )
    except (OSError, subprocess.SubprocessError):
        return {}
    commands = {}
    for line in out.stdout.splitlines():
        parts = line.strip().split(None, 1)
        if len(parts) != 2:
            continue
        try:
            commands[int(parts[0])] = parts[1]
        except ValueError:
            continue
    return commands


def holders_of(paths: list[Path]) -> list[Holder] | None:
    """Processes holding any of `paths` open, or None if that can't be determined.

    None is NOT "nothing is open" — it means neither lsof nor /proc was
    available to ask, and the caller must treat it as the unsafe case rather
    than as an all-clear.
    """
    existing = [p for p in paths if p.exists()]
    if not existing:
        return []
    pids = _pids_via_lsof(existing)
    if pids is None:
        pids = _pids_via_proc(existing)
    if pids is None:
        return None
    pids = sorted({p for p in pids if p != os.getpid()})
    commands = _commands_for_pids(pids)
    return [Holder(p, commands.get(p, "?")) for p in pids]


# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

def backups_dir(db_path: Path) -> Path:
    """Where `endless db backup` writes — beside the database it backs up,
    matching monitor.BackupDB's `filepath.Dir(DBPath())/backups`."""
    return db_path.parent / "backups"


def pre_restore_dir(db_path: Path) -> Path:
    """Where a restore parks the database it is about to replace.

    Deliberately NOT the backups directory: monitor.BackupDB rotates that
    directory by name, keeping the last 60 entries, so parking files there would
    quietly evict real backups (and `pre-restore-*` sorts after `endless-*`, so
    it would evict them first).
    """
    return db_path.parent / "pre-restore"


def list_backups(db_path: Path) -> list[Path]:
    """Backups newest-first. The `endless-<ts>.db` name sorts chronologically,
    so a name sort is a time sort and needs no stat call per file."""
    d = backups_dir(db_path)
    if not d.is_dir():
        return []
    return sorted(d.glob(BACKUP_GLOB), key=lambda p: p.name, reverse=True)


def resolve_backup(db_path: Path, arg: str | None) -> Path:
    """The backup to restore from: `arg` if given, else the newest one.

    A bare `arg` that isn't a path is looked up inside the backups directory, so
    `endless db restore endless-20260810-051500.db` works from anywhere.
    """
    if arg is None:
        found = list_backups(db_path)
        if not found:
            raise click.ClickException(
                f"no backups found in {backups_dir(db_path)} "
                f"(expected files named {BACKUP_GLOB}). "
                f"Pass a path explicitly: endless db restore <file>"
            )
        return found[0]
    candidate = Path(arg).expanduser()
    if candidate.exists():
        return candidate.resolve()
    in_dir = backups_dir(db_path) / arg
    if in_dir.exists():
        return in_dir.resolve()
    raise click.ClickException(
        f"backup not found: {arg} (also looked in {backups_dir(db_path)})"
    )


def file_set(db_path: Path) -> list[Path]:
    """The database file and every sidecar SQLite may have left beside it."""
    return [db_path] + [Path(str(db_path) + s) for s in SIDECAR_SUFFIXES]


# ---------------------------------------------------------------------------
# The restore itself
# ---------------------------------------------------------------------------

@dataclass
class RestoreResult:
    aside: list[Path] = field(default_factory=list)
    journal_mode: str = ""
    integrity: str = ""


def _move_aside(db_path: Path, dest_dir: Path, stamp: str) -> list[Path]:
    """Move the live database and its sidecars into `dest_dir`, names preserved
    relative to each other so the parked copy is openable as a unit."""
    moved = []
    for src in file_set(db_path):
        if not src.exists():
            continue
        suffix = str(src)[len(str(db_path)):]
        dest = dest_dir / f"endless-{stamp}.db{suffix}"
        dest_dir.mkdir(parents=True, exist_ok=True)
        shutil.move(str(src), str(dest))
        moved.append(dest)
    return moved


def _copy_into_place(backup: Path, db_path: Path, mode: int | None) -> None:
    """Copy `backup` (and any sidecars it has) to `db_path`, atomically.

    Written to a temp name in the destination directory and renamed, so an
    interrupted copy can never leave a half-written file where the database goes.
    """
    db_path.parent.mkdir(parents=True, exist_ok=True)
    for suffix in ("",) + SIDECAR_SUFFIXES:
        src = Path(str(backup) + suffix)
        if not src.exists():
            continue
        dest = Path(str(db_path) + suffix)
        tmp = dest.with_name(dest.name + ".restore-tmp")
        shutil.copyfile(src, tmp)
        if mode is not None:
            os.chmod(tmp, mode)
        os.replace(tmp, dest)


def _reestablish_wal(db_path: Path) -> tuple[str, str]:
    """Put the restored database back into WAL and verify it. Returns
    (journal_mode, integrity_check)."""
    conn = sqlite3.connect(str(db_path), timeout=10)
    try:
        row = conn.execute("PRAGMA journal_mode=WAL").fetchone()
        mode = str(row[0]).lower() if row else "unknown"
        rows = conn.execute("PRAGMA integrity_check").fetchall()
        integrity = "; ".join(str(r[0]) for r in rows) if rows else "no result"
    finally:
        conn.close()
    return mode, integrity


def perform_restore(db_path: Path, backup: Path, stamp: str) -> RestoreResult:
    """Park the current database, copy the backup into place, restore WAL, verify.

    Raises click.ClickException if the restored file is not WAL or does not pass
    `PRAGMA integrity_check` — naming the parked copy, because at that point
    rolling back by hand is the user's next move.
    """
    mode = None
    if db_path.exists():
        mode = db_path.stat().st_mode & 0o777

    aside = _move_aside(db_path, pre_restore_dir(db_path), stamp)
    try:
        _copy_into_place(backup, db_path, mode)
        journal_mode, integrity = _reestablish_wal(db_path)
    except (OSError, sqlite3.Error) as e:
        # The parked copy is intact; whoever is here at 5am needs its path more
        # than they need a traceback.
        parked = _aside_db(aside)
        undo = (f"\nThe pre-restore database is intact at {parked}; "
                f"copy it back to {db_path} to undo this."
                if parked is not None else "")
        raise click.ClickException(f"restore failed: {e}{undo}") from e

    result = RestoreResult(aside=aside, journal_mode=journal_mode,
                           integrity=integrity)
    if journal_mode != "wal" or integrity != "ok":
        parked = _aside_db(aside)
        where = (f"The pre-restore database is at {parked}; copy it back to "
                 f"{db_path} to undo this."
                 if parked is not None
                 else "No pre-restore database was kept — the target did not "
                      "exist before this run.")
        raise click.ClickException(
            "restore completed but the result did not verify:\n"
            f"  journal_mode: {journal_mode} (wanted wal)\n"
            f"  integrity_check: {integrity} (wanted ok)\n"
            f"{where}"
        )
    return result


def _aside_db(aside: list[Path]) -> Path | None:
    """The parked database file itself (not its sidecars)."""
    for p in aside:
        if p.name.endswith(".db"):
            return p
    return None


# ---------------------------------------------------------------------------
# Command body
# ---------------------------------------------------------------------------

def _echo_holders(holders: list[Holder] | None) -> None:
    if holders is None:
        click.echo("  open handles:  UNKNOWN — no lsof and no /proc to ask")
        return
    if not holders:
        click.echo("  open handles:  none")
        return
    click.echo(f"  open handles:  {len(holders)}")
    for h in holders:
        click.echo(f"                   pid {h.pid:<7} {h.command}")


def run_restore(backup_arg: str | None, dry_run: bool, force: bool) -> None:
    """Body of `endless db restore`. Echoes its report; raises ClickException
    to refuse."""
    from endless import config

    # The self-dev worktree gate normally fires at db.get_db() / the Go
    # shellout. This command reaches neither, so it enforces the gate itself
    # rather than silently restoring whichever DB the ambient XDG routing found.
    config.require_db_context()

    db_path = Path(config.DB_PATH)
    backup = resolve_backup(db_path, backup_arg)

    check = check_backup(backup)
    if not check.ok:
        raise click.ClickException(f"refusing to restore: {check.problem}")

    holders = holders_of(file_set(db_path))
    all_backups = list_backups(db_path)
    stamp = time.strftime("%Y%m%d-%H%M%S")
    parked = pre_restore_dir(db_path) / f"endless-{stamp}.db"

    header = "Restore plan (dry run — nothing written)" if dry_run else "Restoring"
    click.echo(header)
    if db_path.exists():
        click.echo(f"  target:        {db_path}")
        click.echo(f"                 {human_size(db_path)}, "
                   f"journal: {journal_mode_from_header(db_path)}")
    else:
        click.echo(f"  target:        {db_path}")
        click.echo("                 does not exist yet — restoring creates it")
    click.echo(f"  backup:        {backup}")
    click.echo(f"                 {human_size(backup)}, "
               f"journal: {journal_mode_from_header(backup)}, "
               f"integrity: {check.integrity}")
    if backup_arg is None and all_backups:
        click.echo(f"                 newest of {len(all_backups)} in "
                   f"{backups_dir(db_path)}")
    click.echo(f"  pre-restore:   {parked}")
    _echo_holders(holders)

    unsafe = holders is None or bool(holders)
    if dry_run:
        if unsafe:
            click.echo("\nHolders present — a real run would refuse. "
                       "Stop them, or re-run with --force.")
        else:
            click.echo("\nNothing holds the database open; a real run would "
                       "proceed.")
        click.echo("Dry run: no files were changed.")
        return

    if unsafe and not force:
        why = ("could not determine what has the database open"
               if holders is None
               else f"{len(holders)} process(es) still have the database open")
        raise click.ClickException(
            f"refusing to restore: {why}.\n"
            "Copying over an open database is how the 2026-08-10 recovery went "
            "wrong: it leaves a hot journal and every reader then fails with "
            "'database is locked'.\n"
            "Stop the processes listed above and retry, or pass --force to "
            "restore anyway (they will keep reading the parked copy until "
            "they are restarted)."
        )

    result = perform_restore(db_path, backup, stamp)

    click.echo("")
    click.echo(f"Restored {db_path} from {backup}.")
    aside_db = _aside_db(result.aside)
    if aside_db is not None:
        click.echo(f"  pre-restore database kept at {aside_db}")
        if len(result.aside) > 1:
            click.echo(f"  ({len(result.aside) - 1} sidecar file(s) parked "
                       "alongside it)")
    else:
        click.echo("  no pre-restore database kept — the target did not exist")
    click.echo(f"  journal_mode: {result.journal_mode}")
    click.echo(f"  integrity_check: {result.integrity}")
    if holders:
        click.echo("")
        click.echo(f"WARNING: {len(holders)} process(es) held the old database "
                   "open and still do —")
        click.echo("         they read the parked copy until restarted:")
        for h in holders:
            click.echo(f"           pid {h.pid:<7} {h.command}")
