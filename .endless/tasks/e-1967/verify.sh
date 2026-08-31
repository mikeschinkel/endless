#!/usr/bin/env bash
#
# E-1967 verification script — `endless task spawn` refuses a task any session
# already claimed, and the history that refusal reads is durable.
#
# The defect: spawn's multi-owner refusal was scoped to LIVE sessions, and the
# session that worked a task is almost never still live. So a spawn onto an
# already-worked task saw a free task and started a second session over from
# scratch, without the first one's reasoning — which exists nowhere else.
#
# What each section proves:
#
#   0. This task's unit suites, FAIL-FAST. The relation rename touches the Go
#      enum, the SQL seed, both executors and every reader; the guard and the
#      render are covered by Python suites. If `go test ./...` or pytest is red,
#      nothing below is measuring what it claims to.
#   A. The relation `goal` is now `claimed`, slug and label together. `Goal:`
#      cannot be reasoned about; `Claimed:` says what happened. A fresh database
#      is seeded with the new name, and — the part a rename usually gets wrong —
#      a POPULATED database seeded under the old name reconciles itself on the
#      next connect, with no change file, because the seed is an upsert (the
#      E-1659 pattern task_types and process_kinds already use). Relation ids
#      are what session_tasks persists, so no row moves.
#   B. The guard. A task whose only claimant ENDED is refused; the refusal names
#      that session and routes to `session goto --resume`; `--revisit` appears
#      only for settled work, because E-1968 accepts it only there; several
#      claimants name the most recent and list the rest; a task nobody ever
#      claimed is untouched; a LIVE owner still gets the older, more specific
#      refusal; and no message offers an escape hatch, because there is none to
#      offer (`--force` governs the status demotion, `--new-session` is gone,
#      `task release` is disabled).
#   C. `task show` renders `Claimed:` from `sessions.task_id` — the write-once
#      ownership record — and not from `session_tasks`, which records
#      INVOLVEMENT. Three cases that reading the wrong table gets wrong: a
#      claimant stamped `revisited` (the E-1859 case), a claimant stamped
#      `surfaced` that must keep its Created: credit anyway, and a claimant with
#      no session_tasks row at all (a claim from a plain shell has no session
#      actor, so no touch is recorded).
#   D. The repair. The change file restores the bindings `task reopen` destroyed
#      before ED-1560, reading the `task.claimed` entries out of each project's
#      ledger. Ambiguous evidence is skipped rather than guessed at, an existing
#      binding is never overwritten (so the write-once trigger is never
#      approached), and a second run is a clean no-op.
#   E. The repair cannot be undone by a projection. `internal/events/projector.go`
#      has cases for task and decision events only — no `task.claimed`, no
#      `task.released` — so replaying the ledger never reads or writes a session
#      binding. This is the fact the previous revision of E-1967's plan got
#      backwards, and the reason `execTaskReleased` was left alone instead of
#      being neutered. Asserted structurally against the projector, and
#      behaviorally by replaying the ledger through events.ProjectToTempDB's
#      caller and reading the binding back.
#
#      NOT asserted here: that `event rebuild-db --confirm` completes. Its
#      replacement step deletes and re-inserts the tasks table, and
#      `sessions.task_id`'s `ON DELETE SET NULL` collides with E-1969's
#      write-once trigger — a pre-existing defect filed as E-2062, present with
#      or without this task's repair. The projection, which is the half that
#      could undo the repair, is what section E covers.
#   F. Nothing here launched Claude.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1967
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME, and the freshly-built worktree
# binary prepended to PATH. Sections A and D build their own standalone SQLite
# files under the same temp dir. No real DB, ledger, cache or worktree is
# touched, and nothing here launches Claude.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_absent DESC NEEDLE HAYSTACK
assert_absent() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
}

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; DBDIR=""; LAUNCH_MARKER=""; PROJ_ID=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" 2>&1 ); }
# E_TMUX: a $TMUX pointing at no reachable server — enough to satisfy the
# in-tmux gate on `task spawn`, never enough to reach the developer's own tmux
# server. Every spawn below is expected to stop at a guard long before the
# launcher would be reached anyway.
E_TMUX() {
    ( cd "$REPO" \
      && TMUX="$TMP/no-such-tmux,0,0" TMUX_PANE="%999999" \
         uv run --project "$WT" endless "$@" 2>&1 )
}
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }

# PY FILE SQL...: run SQL against a standalone SQLite file, printing either the
# scalar result or "ERROR: <message>". Used by the sections that are about
# SQLite's own behavior and want nothing between them and the engine.
PY() {
    local dbfile="$1"; shift
    python3 - "$dbfile" "$@" <<'PYEOF'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
con.execute("PRAGMA foreign_keys=ON")
out = []
try:
    for stmt in sys.argv[2:]:
        cur = con.execute(stmt)
        rows = cur.fetchall()
        if rows:
            out.append("|".join("" if c is None else str(c) for c in rows[0]))
    con.commit()
except sqlite3.Error as e:
    print(f"ERROR: {e}")
    sys.exit(0)
print("\n".join(out))
PYEOF
}

# Ids are fixed so every assertion can name them literally.
T_ENDED=9201      # claimed by a session that has since ended — the defect case
T_SETTLED=9202    # assumed AND claimed — the --revisit branch
T_MANY=9203       # three claimants — the "name the most recent" branch
T_FREE=9204       # never claimed by anyone — must not be refused
T_LIVE=9205       # held by a session that is not ended — the older refusal
T_SURFACED=9206   # claimant's session_tasks row says `surfaced`
T_NOTOUCH=9207    # claimant with no session_tasks row at all

S_ENDED=9801      # ended, holds T_ENDED
S_SETTLED=9802    # ended, holds T_SETTLED
S_OLD=9803        # oldest of T_MANY's claimants
S_MID=9804        # middle
S_NEW=9805        # most recent — the one the refusal must name
S_LIVE=9806       # working, holds T_LIVE
S_SURF=9807       # ended, holds T_SURFACED, session_tasks says `surfaced`
S_NOTOUCH=9808    # ended, holds T_NOTOUCH, no session_tasks row
S_BYSTANDER=9809  # touched T_ENDED but never claimed anything

setup_fixture() {
    # -P: macOS's mktemp hands back /var/..., a symlink to /private/var/....
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    LAUNCH_MARKER="$TMP/claude-was-launched"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"

    mkdir -p "$TMP/stub" "$REPO" "$DBDIR" "$TMP/schema"
    cat > "$TMP/stub/claude" <<EOF
#!/bin/sh
echo "STUB CLAUDE LAUNCHED: \$*" >> "$LAUNCH_MARKER"
exit 0
EOF
    chmod +x "$TMP/stub/claude"
    export PATH="$TMP/stub:$WT/bin:$PATH"

    git -C "$REPO" init -q
    git -C "$REPO" symbolic-ref HEAD refs/heads/main
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1

    PROJ_ID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PROJ_ID" ]] || return 1

    E sql "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
             ($T_ENDED,    $PROJ_ID, 'Worked then abandoned', 'revisit',  'now'),
             ($T_SETTLED,  $PROJ_ID, 'Settled work',          'assumed',  'now'),
             ($T_MANY,     $PROJ_ID, 'Passed around',         'revisit',  'now'),
             ($T_FREE,     $PROJ_ID, 'Nobody claimed this',   'ready',    'now'),
             ($T_LIVE,     $PROJ_ID, 'Live right now',        'underway', 'now'),
             ($T_SURFACED, $PROJ_ID, 'Filed then claimed',    'revisit',  'now'),
             ($T_NOTOUCH,  $PROJ_ID, 'Claimed from a shell',  'revisit',  'now')" --write >/dev/null 2>&1

    # last_activity drives the ordering the refusal and monitor.resumeByTask
    # share, so it is set explicitly rather than left to insert order.
    E sql "INSERT INTO sessions (id, session_id, project_id, state, kind_id, task_id, last_activity) VALUES
             ($S_ENDED,     'uuid-$S_ENDED',     $PROJ_ID, 'ended',   1, $T_ENDED,    '2026-08-01T00:00:00'),
             ($S_SETTLED,   'uuid-$S_SETTLED',   $PROJ_ID, 'ended',   1, $T_SETTLED,  '2026-08-01T00:00:00'),
             ($S_OLD,       'uuid-$S_OLD',       $PROJ_ID, 'ended',   1, $T_MANY,     '2026-08-02T00:00:00'),
             ($S_MID,       'uuid-$S_MID',       $PROJ_ID, 'ended',   1, $T_MANY,     '2026-08-05T00:00:00'),
             ($S_NEW,       'uuid-$S_NEW',       $PROJ_ID, 'ended',   1, $T_MANY,     '2026-08-09T00:00:00'),
             ($S_LIVE,      'uuid-$S_LIVE',      $PROJ_ID, 'working', 1, $T_LIVE,     '2026-08-09T00:00:00'),
             ($S_SURF,      'uuid-$S_SURF',      $PROJ_ID, 'ended',   1, $T_SURFACED, '2026-08-03T00:00:00'),
             ($S_NOTOUCH,   'uuid-$S_NOTOUCH',   $PROJ_ID, 'ended',   1, $T_NOTOUCH,  '2026-08-03T00:00:00'),
             ($S_BYSTANDER, 'uuid-$S_BYSTANDER', $PROJ_ID, 'ended',   1, NULL,        '2026-08-04T00:00:00')" \
        --write >/dev/null 2>&1

    # relation ids mirror session_task_relations: 1=claimed 2=surfaced 3=revisited.
    E sql "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
             ($S_ENDED,     $T_ENDED,    3, '2026-08-01T00:00:00', '2026-08-01T00:00:00'),
             ($S_BYSTANDER, $T_ENDED,    3, '2026-08-04T00:00:00', '2026-08-04T00:00:00'),
             ($S_SURF,      $T_SURFACED, 2, '2026-08-03T00:00:00', '2026-08-03T00:00:00')" \
        --write >/dev/null 2>&1

    [[ "$(Q "SELECT count(*) FROM tasks")" == "7" ]] || return 1
    [[ "$(Q "SELECT task_id FROM sessions WHERE id=$S_ENDED")" == "$T_ENDED" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM session_tasks")" == "3" ]] || return 1
    return 0
}

# ─── section 0: the unit suites, fail-fast ───────────────────────────────────

test_suites_fail_fast() {
    section "0. Unit suites (fail-fast — nothing below means anything if these are red)"

    local out rc
    out=$(cd "$WT" && go test ./... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./... passes"
    else
        report_fail "go test ./..." "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -E '^(---|FAIL|\s+---)' | head -20)"
        return 1
    fi

    # The WHOLE Python suite, not a hand-picked subset: renaming a relation
    # touches session status, task show and the membership verbs, so any subset
    # would under-prove it.
    out=$(cd "$WT" && uv run pytest -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "uv run pytest (full suite) passes"
    else
        report_fail "uv run pytest (full suite)" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"
        return 1
    fi
    return 0
}

# ─── section A: the relation is `claimed`, and the rename self-heals ─────────

FRESH_DB=""

test_relation_rename() {
    section "A. The relation \`goal\` is now \`claimed\`"

    # No live code still spells the old slug. Historical change files reproduce
    # the schema as it stood at their point in history and must keep it; prose
    # that names the old name to explain the new one is allowed to say it.
    local hits
    hits="$(cd "$WT" && grep -rn "RelationGoal\|'goal'\|\"goal\"" \
              --include='*.go' --include='*.py' --include='*.sql' \
              internal/ src/ tests/ cmd/ 2>/dev/null \
            | grep -v '^internal/schema/changes/e-1462-' \
            | grep -v '^internal/sessiontaskrelation/sessiontaskrelation_test.go' \
            || true)"
    assert_eq "no stray \`goal\` relation spellings in Go/Python/SQL" "" "$hits"

    # The one excluded file says "goal" on purpose: it pins the slug as one
    # Parse must now REJECT. Assert that positively rather than trusting the
    # exclusion to stay honest.
    local rejected
    rejected="$(cd "$WT" && grep -c '"", "goal", "Claimed"' internal/sessiontaskrelation/sessiontaskrelation_test.go)"
    assert_eq "\`goal\` is pinned as a slug Parse rejects" "1" "$rejected"

    local hist
    hist="$(cd "$WT" && grep -c "'goal'" internal/schema/changes/e-1462-add-session-tasks-relation.sql)"
    if [[ "$hist" -gt 0 ]]; then
        report_pass "e-1462's historical change still seeds the OLD slug"
    else
        report_fail "e-1462's historical change still seeds the OLD slug" \
            "at least one 'goal'" "$hist"
    fi

    # A fresh database — every new install, the sandbox, every test DB.
    FRESH_DB="$TMP/schema/fresh.db"
    python3 - "$FRESH_DB" "$WT/internal/schema/schema.sql" <<'PYEOF'
import sqlite3, sys, pathlib
con = sqlite3.connect(sys.argv[1])
con.executescript(pathlib.Path(sys.argv[2]).read_text())
con.commit()
PYEOF
    assert_eq "a fresh database seeds id 1 as claimed/Claimed" "1|claimed|Claimed" \
        "$(PY "$FRESH_DB" "SELECT id || '|' || slug || '|' || label FROM session_task_relations WHERE id=1")"
    assert_eq "the other four ids are untouched" "2|3|4|5" \
        "$(PY "$FRESH_DB" "SELECT group_concat(id, '|') FROM (SELECT id FROM session_task_relations WHERE id > 1 ORDER BY id)")"

    # The part a rename usually gets wrong: a POPULATED database still carrying
    # the old name. INSERT OR IGNORE could only add ids, never correct one, so
    # such a DB would keep 'goal' and trip VerifyIntegrity on every connect.
    local old_db="$TMP/schema/old-slug.db"
    cp "$FRESH_DB" "$old_db"
    PY "$old_db" \
        "UPDATE session_task_relations SET slug='goal', label='Goal' WHERE id=1" \
        "INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')" \
        "INSERT INTO tasks (id, project_id, title, status) VALUES (5, 1, 't', 'ready')" \
        "INSERT INTO sessions (id, session_id, project_id, state) VALUES (7, 'u7', 1, 'ended')" \
        "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES (7, 5, 1, 'x', 'x')" \
        >/dev/null
    assert_eq "fixture: the old database really says 'goal'" "goal" \
        "$(PY "$old_db" "SELECT slug FROM session_task_relations WHERE id=1")"

    # Re-applying schema.sql is exactly what monitor.DB() does on every connect,
    # BEFORE the integrity check runs.
    python3 - "$old_db" "$WT/internal/schema/schema.sql" <<'PYEOF'
import sqlite3, sys, pathlib
con = sqlite3.connect(sys.argv[1])
con.executescript(pathlib.Path(sys.argv[2]).read_text())
con.commit()
PYEOF
    assert_eq "connecting reconciles the row to claimed/Claimed" "claimed|Claimed" \
        "$(PY "$old_db" "SELECT slug || '|' || label FROM session_task_relations WHERE id=1")"
    assert_eq "the existing session_tasks row keeps relation_id 1 — no data moved" "1" \
        "$(PY "$old_db" "SELECT relation_id FROM session_tasks WHERE session_id=7 AND task_id=5")"

    # And the real binary agrees: monitor.DB() runs schema.SQL then
    # sessiontaskrelation.VerifyIntegrity, which fails CLOSED on any drift.
    local out rc
    out=$( ENDLESS_CHANGE_DB="$old_db" "$WT/bin/endless-go" event validate-db --project-root "$REPO" 2>&1 ); rc=$?
    assert_absent "the startup integrity check does not report relation drift" \
        "slug mismatch" "$out"
}

# ─── section B: the guard ────────────────────────────────────────────────────

test_spawn_guard() {
    section "B. \`task spawn\` refuses a task any session ever claimed"

    local out

    out="$(E_TMUX task spawn "E-$T_ENDED")"
    assert_contains "a task whose only claimant ENDED is refused" \
        "was claimed by session ES-$S_ENDED" "$out"
    assert_contains "...and the refusal routes to that session" \
        "endless session goto E-$T_ENDED --resume" "$out"
    assert_contains "...and says what a second session would cost" \
        "ES-$S_ENDED's reasoning" "$out"
    assert_absent "...without inventing an escape hatch (--force)" "--force" "$out"
    assert_absent "...without inventing an escape hatch (--new-session)" "--new-session" "$out"
    assert_absent "...without inventing an escape hatch (task release)" "task release" "$out"
    assert_absent "...and without --revisit, which a non-settled task rejects" \
        "--revisit" "$out"

    # Settled work hits E-1968's own refusal first; --force skips that gate and
    # lands on this one, where --revisit IS the right flag to teach.
    out="$(E_TMUX task spawn "E-$T_SETTLED" --force)"
    assert_contains "a settled claimed task is still refused under --force" \
        "was claimed by session ES-$S_SETTLED" "$out"
    assert_contains "...and only there does the command carry --revisit" \
        "endless session goto E-$T_SETTLED --resume --revisit" "$out"
    assert_contains "...with --no-revisit offered alongside" "--no-revisit" "$out"

    out="$(E_TMUX task spawn "E-$T_MANY")"
    assert_contains "several claimants: the most recent is named" \
        "was claimed by session ES-$S_NEW" "$out"
    assert_contains "...and the rest are listed beneath" \
        "Earlier claimants: ES-$S_MID, ES-$S_OLD" "$out"

    # A LIVE owner keeps the older, more specific message. The live check runs
    # first for exactly this reason.
    out="$(E_TMUX task spawn "E-$T_LIVE")"
    assert_contains "a LIVE owner still gets the live-owner refusal" \
        "is already active in session" "$out"
    assert_absent "...and not the prior-claim one" "was claimed by session" "$out"

    # A task nobody ever claimed must get past this guard. It cannot complete
    # here (the tmux server is unreachable), so the assertion is that it fails
    # for some OTHER reason, having left the guard behind.
    out="$(E_TMUX task spawn "E-$T_FREE")"
    assert_absent "a task no session ever claimed is not refused by this guard" \
        "was claimed by session" "$out"
    assert_eq "...and its status was not left demoted by a refused spawn" \
        "underway" "$(Q "SELECT status FROM tasks WHERE id=$T_FREE")"

    # A session that TOUCHED the task but never claimed it is not a claimant:
    # session_tasks is involvement, sessions.task_id is ownership.
    out="$(E_TMUX task spawn "E-$T_ENDED")"
    assert_absent "a session that only touched the task is not named a claimant" \
        "ES-$S_BYSTANDER" "$out"
}

# ─── section C: `Claimed:` comes from `sessions`, not `session_tasks` ────────

test_claimed_render() {
    section "C. \`task show\` renders Claimed: from the ownership record"

    local out

    out="$(E task show "E-$T_ENDED" --no-color)"
    assert_contains "a claimant stamped \`revisited\` renders Claimed:" \
        "- Claimed:    ES-$S_ENDED (E-$T_ENDED) [ended]" "$out"
    assert_contains "...while a genuine revisit keeps its own label" \
        "- Revisited:  ES-$S_BYSTANDER [ended]" "$out"

    out="$(E task show "E-$T_NOTOUCH" --no-color)"
    assert_contains "a claimant with NO session_tasks row is listed anyway" \
        "- Claimed:  ES-$S_NOTOUCH (E-$T_NOTOUCH) [ended]" "$out"

    # A session that filed a task and then claimed it must read Claimed: in the
    # block and still be credited on the Created: line, which is derived from
    # the raw `surfaced` touch underneath the override.
    out="$(E task show "E-$T_SURFACED" --no-color)"
    assert_contains "a claimant stamped \`surfaced\` renders Claimed:" \
        "- Claimed:  ES-$S_SURF (E-$T_SURFACED) [ended]" "$out"
    assert_contains "...and keeps its credit for filing the task" \
        "by ES-$S_SURF (E-$T_SURFACED)" "$out"

    # The same facts in the machine-readable renders.
    out="$(E task show "E-$T_ENDED" --llm)"
    assert_contains "--llm carries the claimed relation" \
        "claimed ES-$S_ENDED (E-$T_ENDED) [ended]" "$out"
    out="$(E task show "E-$T_ENDED" --json)"
    assert_contains "--json carries it too" '"relation": "claimed"' "$out"
    assert_absent "...and nothing still says goal" '"goal"' "$out"
}

# ─── section D: the repair change file ───────────────────────────────────────

REPAIR_DB=""
LEDGER_REPO=""

test_repair() {
    section "D. The change file restores bindings destroyed before ED-1560"

    REPAIR_DB="$TMP/schema/repair.db"
    LEDGER_REPO="$TMP/ledger-repo"
    mkdir -p "$LEDGER_REPO/.endless/db-ledger"

    # A ledger holding four claims: one clean, one for a session that still has
    # a binding, one ambiguous (two different tasks), one naming a task that no
    # longer exists.
    cat > "$LEDGER_REPO/.endless/db-ledger/db-entries-aaaa-000001.jsonl" <<'JSONL'
{"v":1,"ts":"5X4D1C7AE8810AA","kind":"task.claimed","project":"probe","entity":{"type":"task","id":"501"},"actor":{"kind":"cli","id":"someone@somewhere"},"payload":{"session_id":701}}
{"v":1,"ts":"5X4D1C7AE8811AA","kind":"task.claimed","project":"probe","entity":{"type":"task","id":"502"},"actor":{"kind":"cli","id":"someone@somewhere"},"payload":{"session_id":702}}
{"v":1,"ts":"5X4D1C7AE8812AA","kind":"task.claimed","project":"probe","entity":{"type":"task","id":"501"},"actor":{"kind":"cli","id":"someone@somewhere"},"payload":{"session_id":703}}
{"v":1,"ts":"5X4D1C7AE8813AA","kind":"task.claimed","project":"probe","entity":{"type":"task","id":"502"},"actor":{"kind":"cli","id":"someone@somewhere"},"payload":{"session_id":703}}
{"v":1,"ts":"5X4D1C7AE8814AA","kind":"task.claimed","project":"probe","entity":{"type":"task","id":"999"},"actor":{"kind":"cli","id":"someone@somewhere"},"payload":{"session_id":704}}
{"v":1,"ts":"5X4D1C7AE8815AA","kind":"task.released","project":"probe","entity":{"type":"task","id":"501"},"actor":{"kind":"cli","id":"someone@somewhere"},"payload":{"session_id":701}}
JSONL

    cp "$FRESH_DB" "$REPAIR_DB"
    PY "$REPAIR_DB" \
        "INSERT INTO projects (id, name, path) VALUES (1, 'probe', '$LEDGER_REPO')" \
        "INSERT INTO tasks (id, project_id, title, status) VALUES (501, 1, 'a', 'assumed')" \
        "INSERT INTO tasks (id, project_id, title, status) VALUES (502, 1, 'b', 'assumed')" \
        "INSERT INTO sessions (id, session_id, project_id, state, task_id) VALUES (701, 'u701', 1, 'ended', NULL)" \
        "INSERT INTO sessions (id, session_id, project_id, state, task_id) VALUES (702, 'u702', 1, 'ended', 501)" \
        "INSERT INTO sessions (id, session_id, project_id, state, task_id) VALUES (703, 'u703', 1, 'ended', NULL)" \
        "INSERT INTO sessions (id, session_id, project_id, state, task_id) VALUES (704, 'u704', 1, 'ended', NULL)" \
        >/dev/null
    assert_eq "fixture: the destroyed binding really is NULL" "" \
        "$(PY "$REPAIR_DB" "SELECT ifnull(task_id, '') FROM sessions WHERE id=701")"

    local out rc
    out="$( cd "$WT" && ENDLESS_CHANGE_DB="$REPAIR_DB" \
            go run internal/schema/changes/e-1967-restore-claim-bindings.go 2>&1 )"; rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "the change file applies cleanly"
    else report_fail "the change file applies cleanly" "exit 0" "exit=$rc"$'\n'"$out"; fi

    assert_eq "the destroyed binding is restored from the ledger" "501" \
        "$(PY "$REPAIR_DB" "SELECT task_id FROM sessions WHERE id=701")"
    assert_eq "a session that still HELD a binding keeps its own" "501" \
        "$(PY "$REPAIR_DB" "SELECT task_id FROM sessions WHERE id=702")"
    assert_eq "an ambiguous session is skipped, not guessed at" "" \
        "$(PY "$REPAIR_DB" "SELECT ifnull(task_id, '') FROM sessions WHERE id=703")"
    assert_eq "a claim on a task that no longer exists is skipped" "" \
        "$(PY "$REPAIR_DB" "SELECT ifnull(task_id, '') FROM sessions WHERE id=704")"
    assert_contains "...and the run says exactly what it did" \
        "1 session binding(s) restored" "$out"

    # A `task.released` for the very binding just restored must not undo it:
    # the release is the damage, not evidence to replay.
    assert_eq "the trailing task.released did not cancel the restore" "501" \
        "$(PY "$REPAIR_DB" "SELECT task_id FROM sessions WHERE id=701")"

    out="$( cd "$WT" && ENDLESS_CHANGE_DB="$REPAIR_DB" \
            go run internal/schema/changes/e-1967-restore-claim-bindings.go 2>&1 )"; rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "a second run is a clean no-op"
    else report_fail "a second run is a clean no-op" "exit 0" "exit=$rc"$'\n'"$out"; fi
    assert_contains "...gated by the _schema_version marker, not re-run" \
        "already applied; skipping" "$out"

    # And with the marker cleared — the case the runner's gate would otherwise
    # hide — the repair itself is still idempotent: nothing is a candidate, so
    # nothing is written and the write-once trigger is never approached.
    PY "$REPAIR_DB" "DELETE FROM _schema_version WHERE name='e-1967-restore-claim-bindings'" >/dev/null
    out="$( cd "$WT" && ENDLESS_CHANGE_DB="$REPAIR_DB" \
            go run internal/schema/changes/e-1967-restore-claim-bindings.go 2>&1 )"; rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "re-running the repair itself is a no-op, not an abort"
    else report_fail "re-running the repair itself is a no-op, not an abort" "exit 0" "exit=$rc"$'\n'"$out"; fi
    assert_contains "...restoring nothing the second time" \
        "0 session binding(s) restored" "$out"
    assert_eq "...and leaving every binding as it was" "501" \
        "$(PY "$REPAIR_DB" "SELECT task_id FROM sessions WHERE id=701")"
}

# ─── section E: a projection cannot undo the repair ──────────────────────────

test_projection_leaves_bindings_alone() {
    section "E. Replaying the ledger never touches a session binding"

    # Structural: the projector's dispatch has no session-event case at all.
    # This is the fact an earlier revision of E-1967's plan got backwards, and
    # the reason execTaskReleased was left exactly as it is.
    local cases
    cases="$(cd "$WT" && grep -c 'case KindTaskClaimed\|case KindTaskReleased' internal/events/projector.go || true)"
    assert_eq "projector.go has no case for task.claimed / task.released" "0" "$cases"

    # Behavioral: replay the same ledger the repair read, and the restored
    # binding is still there afterwards. `rebuild-db` without --confirm performs
    # the whole projection and stops before the tasks-table replacement — which
    # is the half that could undo the repair.
    local out rc
    out="$( cd "$LEDGER_REPO" && ENDLESS_CHANGE_DB="$REPAIR_DB" \
            "$WT/bin/endless-go" event rebuild-db --project-root "$LEDGER_REPO" 2>&1 )"; rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "the ledger projects without error"
    else report_fail "the ledger projects without error" "exit 0" "exit=$rc"$'\n'"$out"; fi
    assert_eq "the restored binding survives the projection" "501" \
        "$(PY "$REPAIR_DB" "SELECT task_id FROM sessions WHERE id=701")"
}

# ─── section F: nothing launched Claude ──────────────────────────────────────

test_no_launch() {
    section "F. No check launched Claude"

    if [[ -f "$LAUNCH_MARKER" ]]; then
        report_fail "no check exec'd claude" "no launch" "$(cat "$LAUNCH_MARKER")"
    else
        report_pass "no check exec'd claude"
    fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null      || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null     || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    command -v python3 >/dev/null || { printf 'ERROR: python3 not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${WT}" >&2; exit 2; }

    printf '%sE-1967 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! test_suites_fail_fast; then
        printf '\n%sUnit suites failed — stopping before the schema and CLI sections.%s\n\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_relation_rename
    test_spawn_guard
    test_claimed_render
    test_repair
    test_projection_leaves_bindings_alone
    test_no_launch

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"
