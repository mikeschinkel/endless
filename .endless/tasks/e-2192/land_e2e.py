"""E-2192 end to end: a real `land_worktree` against a main database one goose
migration behind the landing branch — the E-2188 failure, reproduced and fixed.

Run by verify.sh under the runner's temp HOME, never by hand. Usage:

    land_e2e.py <worktree-root> <scratch-dir> <mode>

mode "control" disables `endless-migrate up` (the land as it was before E-2192)
and expects the record to fail on the missing column. mode "fixed" runs the land
as shipped and expects the landing recorded in one run.

REAL: git (a throwaway main repo + feature worktree), the rebase and ff-merge,
the pre-apply backup, `endless-migrate up` (built from this tree), and the
`task.landed` emit by this tree's candidate endless-go against `--db main` under
the temp HOME. STUBBED: only the registry lookups a throwaway repo cannot answer
(which worktree belongs to the task, the project row) and the two `just` builds,
whose outputs are the real binaries already built from this tree.

The "branch adding a migration whose code writes the new column during
task.landed" is this tree itself: its migrations run to the latest version, its
task.landed executor writes sessions.focus_task_id (00009), and the database is
held at 00008. That is exactly E-2188's shape.
"""

import os
import sqlite3
import subprocess
import sys
from pathlib import Path

WT = Path(sys.argv[1])
T = Path(sys.argv[2])
MODE = sys.argv[3]

sys.path.insert(0, str(WT / "src"))

import click  # noqa: E402

from endless import worktree_cmd  # noqa: E402

GO_BIN = WT / "bin" / "endless-go"
MIGRATE_BIN = WT / "bin" / "endless-migrate"
DB = Path(os.environ["HOME"]) / ".config" / "endless" / "endless.db"


def git(*args, cwd):
    subprocess.run(["git", *args], cwd=str(cwd), check=True, capture_output=True)


def head(repo, ref="HEAD"):
    return subprocess.run(
        ["git", "rev-parse", ref], cwd=str(repo),
        capture_output=True, text=True, check=True,
    ).stdout.strip()


# --- a throwaway project: main checkout + feature worktree ------------------
main = T / "proj"
main.mkdir(parents=True)
git("init", "-q", "-b", "main", cwd=main)
for k, v in (("user.email", "t@t.t"), ("user.name", "t"), ("commit.gpgsign", "false")):
    git("config", k, v, cwd=main)
(main / ".endless" / "db-ledger").mkdir(parents=True)
(main / "README").write_text("x\n")
git("add", "-A", cwd=main)
git("commit", "-q", "-m", "init", cwd=main)
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
wc._resolve_land_endless_go = lambda w, r: str(GO_BIN)
wc._resolve_land_migrate_bin = lambda w, r: str(MIGRATE_BIN)
wc._reap_stale_worktrees = lambda root: None
wc._warm_unlanded_cache = lambda w: None
import endless.config as config  # noqa: E402
config.project_is_self_dev = lambda root: True

if MODE == "control":
    wc._migrate_up = lambda migrate_bin: {"status": "disabled"}

main_before = head(main, "main")
try:
    wc.land_worktree("E-1", dry_run=False)
    outcome = "landed"
except click.ClickException as e:
    outcome = "failed: " + e.message.replace("\n", " ")

con = sqlite3.connect(DB)
landings = con.execute("SELECT count(*) FROM task_landings").fetchone()[0]
version = con.execute("SELECT max(version_id) FROM goose_db_version").fetchone()[0]
con.close()

print(f"outcome={outcome}")
# The branch tip is in main. Not equal to it: the task.landed emit commits a
# ledger segment on top of the merge.
branch_in_main = subprocess.run(
    ["git", "merge-base", "--is-ancestor", head(wt), "main"], cwd=str(main),
).returncode == 0
advanced = head(main, "main") != main_before and branch_in_main
print(f"main_advanced={'yes' if advanced else 'no'}")
print(f"landings={landings}")
print(f"db_version={version}")
