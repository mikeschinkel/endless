"""E-2020 end to end: a real `land_worktree` against a main database one goose
migration behind the landing branch, under ED-1601 — a worktree build never
opens the main database.

Run by verify.sh under the runner's temp HOME, never by hand. Usage:

    land_e2e.py <worktree-root> <scratch-dir> <mode>

mode "control" records the landing with THIS WORKTREE's endless-go, as every
land before E-2020 did (E-1664). Under the new connect rules that binary is
refused the main database, so the record fails. mode "fixed" runs the land as
shipped: Step 5.6 rebuilds the main checkout's endless-go and Step 6 records
with it.

REAL: git (a throwaway main repo + feature worktree), the rebase and ff-merge,
the pre-apply backup, `endless-migrate up` (built from this tree), and the
`task.landed` emit against `--db main` under the temp HOME. STUBBED: the
registry lookups a throwaway repo cannot answer, and the `just` builds, whose
outputs are the real binaries already built from this tree — Step 5.6's build
is replaced by COPYING this tree's endless-go into the throwaway main
checkout's bin/, which is exactly the file `just go` would have produced there,
at a path outside any worktree.
"""

import os
import shutil
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
(main / ".gitignore").write_text("bin/\n")
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
wc._resolve_land_migrate_bin = lambda w, r: str(MIGRATE_BIN)
wc._reap_stale_worktrees = lambda root: None
wc._warm_unlanded_cache = lambda w: None
import endless.config as config  # noqa: E402
config.project_is_self_dev = lambda root: True

order = []
real_apply = wc._apply_branch_schema_changes


def traced_apply(*a, **kw):
    order.append("migrate")
    return real_apply(*a, **kw)


wc._apply_branch_schema_changes = traced_apply


def fake_just_go(main_root, canonical, base_branch):
    order.append("rebuild")
    (main_root / "bin").mkdir(exist_ok=True)
    shutil.copy2(GO_BIN, main_root / "bin" / "endless-go")


if MODE == "control":
    # The land before E-2020: record with the worktree's own build.
    wc._resolve_land_endless_go = lambda w, r: str(GO_BIN)
    wc._rebuild_main_binary = lambda m, c, b: None
else:
    wc._rebuild_main_binary = fake_just_go

real_record = wc._record_landing


def traced_record(*a, **kw):
    order.append("record:" + str(kw.get("endless_go_bin")))
    return real_record(*a, **kw)


wc._record_landing = traced_record

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
branch_in_main = subprocess.run(
    ["git", "merge-base", "--is-ancestor", head(wt), "main"], cwd=str(main),
).returncode == 0
advanced = head(main, "main") != main_before and branch_in_main
print(f"main_advanced={'yes' if advanced else 'no'}")
print(f"landings={landings}")
print(f"db_version={version}")
print("order=" + ",".join(
    "record:main" if o == f"record:{main / 'bin' / 'endless-go'}"
    else "record:worktree" if o == f"record:{GO_BIN}"
    else o
    for o in order
))
