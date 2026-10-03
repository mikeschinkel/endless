"""E-2205 end to end: a real self_dev `land_worktree` that migrates the main
database while a loop polls the installed binary's tmux status line, as the
real status bar does about once a second.

Run by verify.sh under the runner's temp HOME, never by hand. Usage:

    land_e2e.py <worktree-root> <scratch-dir> <mode> <old-endless-go>

The main database starts one goose migration behind this tree, built by
<old-endless-go> (this project at the commit before its newest migration),
which is also the main checkout's installed bin/endless-go. The land's real
`endless-migrate up` takes the database to the newest version, so from that
moment until the new binary is in place, every poll meets a database ahead of
the binary — ERR-0020.

mode "control" restores the land as it was before E-2205: nothing is built
before the migration, after it `just go` builds and swaps, and nothing is
cleared. mode "fixed" runs the land as shipped: `just go-build-next` before the
migration, the same rename `just go-swap` runs, in-process, right after it, and
after the record the land clears any ERR-0020 a reader still caught in the
milliseconds between the two. mode "caught" is "fixed" with one poll forced into
that gap, so the clear is proven on every run.

The main checkout's justfile is a stand-in whose build sleeps two seconds — the
compile time a real build spends — and then copies this tree's endless-go;
its go-swap is the same `mv -f` the project's justfile runs (control uses it). Everything else is
real: git, the ff-merge, the backup, `endless-migrate up`, the poller's
connects and the faults they record, and the task.landed emit.
"""

import os
import sqlite3
import subprocess
import sys
import threading
import time
from pathlib import Path

WT = Path(sys.argv[1])
T = Path(sys.argv[2])
MODE = sys.argv[3]
OLD_BIN = Path(sys.argv[4])

sys.path.insert(0, str(WT / "src"))

import click  # noqa: E402

from endless import worktree_cmd  # noqa: E402

NEW_BIN = WT / "bin" / "endless-go"
MIGRATE_BIN = WT / "bin" / "endless-migrate"
DB = Path(os.environ["HOME"]) / ".config" / "endless" / "endless.db"


def git(*args, cwd):
    subprocess.run(["git", *args], cwd=str(cwd), check=True, capture_output=True)


# --- a throwaway project: main checkout + feature worktree ------------------
main = T / "proj"
main.mkdir(parents=True)
git("init", "-q", "-b", "main", cwd=main)
for k, v in (("user.email", "t@t.t"), ("user.name", "t"), ("commit.gpgsign", "false")):
    git("config", k, v, cwd=main)
(main / ".endless" / "db-ledger").mkdir(parents=True)
(main / ".gitignore").write_text("/bin/\n")
(main / "justfile").write_text(f"""\
go: go-build-next go-swap

go-build-next:
    sleep 2
    cp {NEW_BIN} bin/endless-go.next

go-swap:
    mv -f bin/endless-go.next bin/endless-go
""")
git("add", "-A", cwd=main)
git("commit", "-q", "-m", "init", cwd=main)
(main / "bin").mkdir()
installed = main / "bin" / "endless-go"
installed.write_bytes(OLD_BIN.read_bytes())
installed.chmod(0o755)
wt = T / "wt"
git("worktree", "add", "-q", str(wt), "-b", "feat", "main", cwd=main)
(wt / "feature.txt").write_text("the work\n")
git("add", "-A", cwd=wt)
git("commit", "-q", "-m", "E-1: the work", cwd=wt)

# --- the rows the landing touches -------------------------------------------
con = sqlite3.connect(DB)
con.executescript(f"""
    INSERT INTO projects(id, name, path) VALUES (1, 'p', '{main}');
    INSERT INTO tasks(id, project_id, title) VALUES (1, 1, 'the task');
    INSERT INTO sessions(id, session_id, project_id) VALUES (1, 's-1', 1);
""")
con.commit()
con.close()
os.environ["ENDLESS_SESSION_ID"] = "1"

# --- stub only what a throwaway repo cannot answer --------------------------
wc = worktree_cmd
wc._enriched_list = lambda root: [{"_": 1}]
wc._branch_for_task = lambda rows, canonical: {
    "branch": "feat", "path": str(wt), "companion": {"base_branch": "main"},
}
wc._project_root = lambda: main
wc._resolve_project = lambda arg: (None, "p")
wc._rebuild_worktree_binary = lambda w, c: None
wc._build_migration_executable = lambda w, c: None
wc._resolve_land_migrate_bin = lambda w, r: str(MIGRATE_BIN)
wc._reap_stale_worktrees = lambda root: None
wc._warm_unlanded_cache = lambda w: None
import endless.config as config  # noqa: E402
config.project_is_self_dev = lambda root: True

if MODE == "control":
    # The land before E-2205: no build ahead of the migration, then Step 5.6
    # compiled and swapped in one `just go`.
    wc._build_main_binary_next = lambda m, c, b: None
    wc._swap_main_binary = lambda m, c, b: subprocess.run(
        ["just", "go"], cwd=str(m), check=True, capture_output=True,
    )
    wc._clear_land_schema_faults = lambda up, since, c, bin_: None

if MODE == "caught":
    # The residual race, forced: one status-line poll through the old binary
    # after the migration commits and before the swap — the reader the clear
    # exists for — so the clear is exercised on every run, not by luck.
    _real_apply = wc._apply_branch_schema_changes

    def _apply_then_poll(*a, **kw):
        res = _real_apply(*a, **kw)
        subprocess.run(
            [str(main / "bin" / "endless-go"), "tmux", "status-line", "--pane", "%0"],
            cwd=str(T), capture_output=True,
        )
        return res

    wc._apply_branch_schema_changes = _apply_then_poll

# --- the status bar: poll the installed binary until the land is done -------
done = threading.Event()
polls = [0]


def poll():
    while not done.is_set():
        subprocess.run(
            [str(installed), "tmux", "status-line", "--pane", "%0"],
            cwd=str(T), capture_output=True,
        )
        polls[0] += 1
        time.sleep(0.1)


poller = threading.Thread(target=poll)
poller.start()
try:
    wc.land_worktree("E-1", dry_run=False)
    outcome = "landed"
except click.ClickException as e:
    outcome = "failed: " + e.message.replace("\n", " ")
except subprocess.CalledProcessError as e:
    outcome = f"failed: {e.cmd}: {e.stderr}"
finally:
    done.set()
    poller.join()

con = sqlite3.connect(DB)
err20 = con.execute(
    "SELECT count(*), coalesce(sum(occurrences), 0) FROM errors "
    "WHERE code = 'ERR-0020'"
).fetchone()
err20_open = con.execute(
    "SELECT count(*) FROM errors WHERE code = 'ERR-0020' AND cleared_at IS NULL"
).fetchone()[0]
err20_cleared_by_land = con.execute(
    "SELECT count(*) FROM errors WHERE code = 'ERR-0020' "
    "AND cleared_by = 'worktree land E-1'"
).fetchone()[0]
landings = con.execute("SELECT count(*) FROM task_landings").fetchone()[0]
version = con.execute("SELECT max(version_id) FROM goose_db_version").fetchone()[0]
con.close()

print(f"outcome={outcome}")
print(f"polls={polls[0]}")
print(f"err0020_incidents={err20[0]}")
print(f"err0020_occurrences={err20[1]}")
print(f"err0020_open={err20_open}")
print(f"err0020_cleared_by_land={err20_cleared_by_land}")
print(f"landings={landings}")
print(f"db_version={version}")
print(f"next_left={'yes' if (main / 'bin' / 'endless-go.next').exists() else 'no'}")
print(f"installed_is_new={'yes' if installed.read_bytes() == NEW_BIN.read_bytes() else 'no'}")
