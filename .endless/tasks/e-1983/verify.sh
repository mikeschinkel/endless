#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1983 and records what was true when E-1983
# landed. Edit it only if you ARE E-1983. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1983: stop a reused spawn window from permanently mis-binding a fresh
# session.
#
# THE BUG. `endless task spawn` sets @endless_task_id on the tmux window it
# creates, and nothing ever clears it. `trySpawnBind` read that option at every
# SessionStart and bound the session to it, so a window that outlived its
# spawned session handed that task to whatever `claude` started in it next.
# E-1969 made `sessions.task_id` write-once, which fixed the RESUMED case (the
# stale write became a refused reassignment) and made the FRESH case
# unrecoverable: on a NULL row the stale option is the FIRST write, the trigger
# permits it, and `task bind` can no longer move it.
#
# WHAT SHIPPED, as four decisions:
#   1. The working directory is the only thing that binds. trySpawnBind no
#      longer writes sessions.task_id; what is left of it only LOGS the
#      window/cwd disagreement it used to act on.
#   2. FindWorktreeRoot resolves both of its arguments. Its stop condition is
#      `dir == root`, so one unresolved symlink component meant the walk ran
#      past the project root — silently, and now user-visible because nothing
#      covers for it.
#   3. A PreToolUse gate on the one state left genuinely wrong: cwd is a task
#      worktree and the session holds no task. It is the mirror of
#      enforceClaimedCwd, which returns early on exactly that case.
#   4. A schema change repairing the rows the bug already wrote, keyed on the
#      launch directory each session's FIRST hook event recorded.
#
# Sections, fail-fast in order:
#   A. This task's own tests.
#   B. The claims only visible from inside, named one by one.
#   C. The mechanism is GONE, not merely unused — asserted against the source,
#      because a test can only show that a path did not fire today.
#   D. The repair against a REAL database this suite builds and migrates, run
#      through the real change script. Everything above is unit-level; this is
#      the only check that proves the shipped .go change file works.
#
# Run from inside the worktree:  esu && endless task verify E-1983
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# Folded in as the first check so the one command is a complete proof: if these
# are red, nothing below is worth reading.

go_pkg() { # go_pkg <package> <-run regex> <label>
    if out=$(go test "$1" -run "$2" -count=1 2>&1); then
        report_pass "go test: $3"
    else
        report_fail "go test: $3" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

go_pkg ./internal/hookcmd/ \
    'TestSessionStartBind_|TestLogWindowTaskDisagreement|TestTaskIDOrNone|TestUnboundWorktreeGate|TestAutoBindFromCwd|TestShouldSkipForWorktree|TestWorktreeOverrideRegistered' \
    "the SessionStart bind path and the new gate"
go_pkg ./internal/monitor/ \
    'TestFindWorktreeRoot|TestRepairMisboundSessions|TestSessionIsLive|TestTaskIDFromWorktreePath' \
    "worktree-root resolution and the row repair"

# ---------------------------------------------------------------------------
section "B. The claims, named one by one"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing all of them into two green packages.

go_claim() { # go_claim <package> <TestName> <claim>
    if out=$(go test "$1" -run "^$2\$" -count=1 2>&1); then
        report_pass "$3"
    else
        report_fail "$3" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

H=./internal/hookcmd/
M=./internal/monitor/

# Decision 1 — the reproduction, and the rule that replaces it.
go_claim "${H}" TestSessionStartBind_MainCheckoutStaysUnbound \
    "THE REPRODUCTION: a fresh session in the MAIN CHECKOUT, in a window still naming another task, lands UNBOUND"
go_claim "${H}" TestSessionStartBind_CwdWinsOverStaleWindowOption \
    "a fresh session in worktree e-A binds to A even when the window still says B"
go_claim "${H}" TestSessionStartBind_SpawnedWorkerBindsFromCwd \
    "a spawned worker still binds on its first event — spawn opens the window AT the worktree"
go_claim "${H}" TestLogWindowTaskDisagreement_DoesNotBind \
    "what is left of the spawn-marker path observes the disagreement and never writes task_id"
go_claim "${H}" TestAutoBindFromCwd_ResumeDoesNotRebindDifferentTask \
    "E-1856 still holds: a resume whose cwd is another task's worktree is NOT re-pointed"

# Decision 2 — the walk.
go_claim "${M}" TestFindWorktreeRoot_ResolvesSymlinkedCwd \
    "a symlinked cwd no longer walks PAST the project root (the E-1219 exclusion holds through /var)"
go_claim "${M}" TestFindWorktreeRoot_FindsWorktreeThroughSymlinkedCwd \
    "a project reached through a symlink resolves its worktree, in the form WorktreePathForTask builds"
go_claim "${H}" TestWorktreeOverrideRegistered_SurvivesPathSpelling \
    "the self-skip check survives the two sides spelling the project root differently"
go_claim "${H}" TestWorktreeOverrideRegistered_DoesNotMatchAnotherWorktree \
    "...and stays specific: a sibling worktree's override is not mistaken for this one's"

# Decision 3 — the gate, and every false positive it must not produce.
go_claim "${H}" TestUnboundWorktreeGate_BlocksUnboundSessionInWorktree \
    "THE GATE: a session in a task worktree holding no task is blocked, and told the two ways out"
go_claim "${H}" TestUnboundWorktreeGate_NamesTheFailedStep \
    "the block names WHICH step of the cwd bind failed, because the fixes differ"
go_claim "${H}" TestUnboundWorktreeGate_DoesNotBlockSubagents \
    "NOT BLOCKED: Agent-tool subagents — the false positive that would make the gate unusable"
go_claim "${H}" TestUnboundWorktreeGate_DoesNotBlockBackgroundAgents \
    "NOT BLOCKED: background agents, deliberately never bound for the same reason"
go_claim "${H}" TestUnboundWorktreeGate_DoesNotBlockMainCheckout \
    "NOT BLOCKED: an unbound session in the main checkout — the correct outcome of the cwd rule"
go_claim "${H}" TestUnboundWorktreeGate_DoesNotBlockForeignTree \
    "NOT BLOCKED: a worktree-shaped path outside the registered project"
go_claim "${H}" TestUnboundWorktreeGate_DoesNotBlockBoundSession \
    "NOT BLOCKED: a session that holds a task — that half is enforceClaimedCwd's"
go_claim "${H}" TestUnboundWorktreeGate_DoesNotBlockItsOwnEscape \
    'the gate never blocks `task claim` / `task bind` — a gate that blocks its own exit strands the window'

# Decision 4 — the repair's judgment.
go_claim "${M}" TestRepairMisboundSessions_E1732Shape \
    "E-1732's shape: the worktree-launched row is KEPT though it is the oldest; the two main-checkout rows are unbound"
go_claim "${M}" TestRepairMisboundSessions_ReadsFirstWorkingDir \
    "lineage comes from the FIRST working_dir — a /cd moves the cwd, not the session"
go_claim "${M}" TestRepairMisboundSessions_LeavesInstancesAlone \
    "rows sharing one launch directory are one session's instances (E-2063's), and are left alone"
go_claim "${M}" TestRepairMisboundSessions_UndecidedChangesNothing \
    "an undecidable task is reported and NOT changed — a guess could destroy the only binding it has"
go_claim "${M}" TestRepairMisboundSessions_LeavesLiveSessionBound \
    "a session active minutes ago keeps its task: unbinding mid-turn is not recoverable under write-once"
go_claim "${M}" TestRepairMisboundSessions_SecondRunIsANoOp \
    "the repair is idempotent on its own, not merely gated by its _schema_version marker"

# ---------------------------------------------------------------------------
section "C. The mechanism is gone from the source, not merely unfired"
# ---------------------------------------------------------------------------
# A passing test shows a path did not fire on the inputs it was given. These
# assert the path does not EXIST, which is the claim this task actually makes.

if grep -q 'func trySpawnBind' internal/hookcmd/claude.go; then
    report_fail "trySpawnBind no longer exists" "absent" "still defined in internal/hookcmd/claude.go"
else
    report_pass "trySpawnBind no longer exists"
fi

if grep -q 'tmuxSpawnedBy' internal/hookcmd/claude.go; then
    report_fail "hookcmd no longer reads @endless_spawned_by" "absent" "tmuxSpawnedBy still in internal/hookcmd/claude.go"
else
    report_pass "hookcmd no longer reads @endless_spawned_by"
fi

# The one remaining reader of @endless_task_id in the hook must not be able to
# write a binding. BindSessionToTask is the only call that writes task_id, so
# asserting the observer's body does not contain it is the whole property.
observer_body=$(awk '/^func logWindowTaskDisagreement/,/^}/' internal/hookcmd/claude.go)
assert_not_contains "the window-option observer cannot bind (no BindSessionToTask in its body)" \
    "BindSessionToTask" "${observer_body}"

# Decision 1's whole point: the cwd bind is unconditional now, not a fallback
# gated on the window option having declined.
maybe_body=$(awk '/^func maybeCwdBind/,/^}/' internal/hookcmd/claude.go)
assert_not_contains "maybeCwdBind no longer defers to a spawn-marker bind" \
    "spawnBound" "${maybe_body}"

# ---------------------------------------------------------------------------
section "D. The repair, through the real change script, on a real database"
# ---------------------------------------------------------------------------
# Everything above is unit-level. This builds a database with the real schema,
# seeds E-1732's exact shape, and runs the SHIPPED change file against it — so
# the .go script itself, its trigger drop/re-create and its _schema_version
# marker are exercised, not just the function it calls.

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
DB="${TMP}/endless.db"

# The schema comes from the same migrator the application uses, via a tiny
# throwaway program, so this suite never hand-copies DDL that could drift. It
# has to live INSIDE the module: `go run` on a file outside it cannot import an
# internal/ package. `.endless/tmp/` is the scratch directory the project
# already uses for exactly this, and the trap below removes what we put there.
MIG="${WT}/.endless/tmp/verify-e-1983-migrate"
mkdir -p "${MIG}"
trap 'rm -rf "${TMP}" "${MIG}"' EXIT
cat > "${MIG}/main.go" <<'GO'
package main

import (
	"database/sql"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

func main() {
	db, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err = schema.Migrate(db); err != nil {
		log.Fatal(err)
	}
}
GO
if ! out=$(go run "${MIG}/main.go" "${DB}" 2>&1); then
    setup_error "could not build the fixture database: ${out}"
fi

# Seed E-1732's shape: one session launched in the task's own worktree, two
# launched in the main checkout and bound by the window.
seed=$(cat <<'SQL'
INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p');
INSERT INTO tasks (id, project_id, title, status) VALUES (1732, 1, 't', 'underway');
INSERT INTO sessions (id, session_id, project_id, platform, state, task_id, last_activity)
VALUES (879, 'aaa', 1, 'claude', 'ended', 1732, '2026-07-04T05:04:36'),
       (881, 'bbb', 1, 'claude', 'ended', 1732, '2026-07-04T05:29:52'),
       (882, 'ccc', 1, 'claude', 'ended', 1732, '2026-07-04T05:30:17');
INSERT INTO activity (project_id, source, working_dir, session_context) VALUES
  (1, 'claude', '/p/.endless/worktrees/e-1732', json_object('session_id','aaa')),
  (1, 'claude', '/p', json_object('session_id','bbb')),
  (1, 'claude', '/p', json_object('session_id','ccc'));
SQL
)
if ! out=$(printf '%s\n' "${seed}" | python3 -c '
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.executescript(sys.stdin.read())
db.commit()
' "${DB}" 2>&1); then
    setup_error "could not seed the fixture: ${out}"
fi

# Run the SHIPPED change script against it.
if ! change_out=$(ENDLESS_CHANGE_DB="${DB}" \
        go run ./internal/schema/changes/e-1983-repair-misbound-sessions.go 2>&1); then
    report_fail "the change script runs to completion" "exit 0" "${change_out}"
    summary
fi
report_pass "the change script runs to completion"

assert_contains "it prints its verdict for the task rather than folding rows in silently" \
    "E-1732 repaired" "${change_out}"

query() { python3 -c '
import sqlite3, sys
print("|".join("" if v is None else str(v) for row in sqlite3.connect(sys.argv[1]).execute(sys.argv[2]) for v in row))
' "${DB}" "$1"; }

assert_eq "the worktree-launched row keeps the task (never keep-the-newest)" \
    "1732" "$(query 'SELECT task_id FROM sessions WHERE id=879')"
assert_eq "both main-checkout rows are unbound" \
    "|" "$(query 'SELECT task_id FROM sessions WHERE id IN (881,882) ORDER BY id')"

assert_eq "the write-once trigger is present again after the repair" \
    "sessions_task_id_write_once" \
    "$(query "SELECT name FROM sqlite_master WHERE type='trigger' AND name='sessions_task_id_write_once'")"

# Present is not the same as functional: SQLite resolves a trigger body lazily,
# so a trigger can exist and still never fire. Both directions are checked —
# write-once forbids a reassignment AND an unbind, and the repair's own
# drop/re-create must not have left either one permitted.
probe() { python3 -c '
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
try:
    db.execute("UPDATE sessions SET task_id=? WHERE id=879", (None if sys.argv[2]=="null" else int(sys.argv[2]),))
    print("PERMITTED")
except sqlite3.IntegrityError as e:
    print(str(e))
' "${DB}" "$1"; }

assert_contains "write-once still refuses a REASSIGNMENT" \
    "write-once" "$(probe 99999)"
assert_contains "write-once still refuses an UNBIND (the write the repair had to drop it for)" \
    "write-once" "$(probe null)"

# The marker gates a re-run, which is what makes the change safe to leave in the
# tree: `db apply-change` over the whole directory must not repair twice.
if ! rerun_out=$(ENDLESS_CHANGE_DB="${DB}" \
        go run ./internal/schema/changes/e-1983-repair-misbound-sessions.go 2>&1); then
    report_fail "a second run exits cleanly" "exit 0" "${rerun_out}"
else
    report_pass "a second run exits cleanly"
fi
assert_contains "a second run is skipped by its _schema_version marker" \
    "already applied" "${rerun_out}"
assert_eq "the kept row is untouched by the second run" \
    "1732" "$(query 'SELECT task_id FROM sessions WHERE id=879')"

summary
