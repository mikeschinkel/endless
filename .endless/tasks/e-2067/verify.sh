#!/usr/bin/env bash
#
# E-2067 verification — a parent cycle is refused on `task update`, not only on
# `task move`.
#
# Before: two executors write tasks.parent_id. execTaskMoved walked the ancestor
# chain and refused a circular reference; execTaskFieldsUpdated carried
# "parent_id" in its allowedFields map and wrote it with no walk at all. So
# `endless task update E-A --parent B` — while B's parent was still A — closed a
# loop, and every recursive CTE over the task tree walked it forever. Reproduced
# against the real database on 2026-08-25 (E-799 <-> E-1935).
#
# After: the walk lives in ValidateNoParentCycle and BOTH executors call it,
# exactly as ValidateMaybeParentless is already shared between them. Lifting it
# also closed a hole the original had — it started climbing AT the target parent
# and never compared against it, so a root task could be made its own parent.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-2067-verify.sh
#
# What it proves:
#   1. FAIL-FAST unit gate: the Go suite this task owns passes. Everything below
#      runs against a binary built from the same tree, so a red gate makes it
#      all noise.
#   2. The guard is genuinely SHARED, not copy-pasted: both executors call
#      ValidateNoParentCycle and no private ancestor walk survives in either.
#   3. End to end, through the real emit path against a throwaway database:
#      the filed defect — task.fields_updated closing a two-node loop — is
#      refused, exits non-zero, and leaves both rows untouched.
#   4. Self-parenting is refused on BOTH kinds. This is the hole the old move
#      walk had; task.moved rejects it now too.
#   5. A deep cycle is refused, and a chain that is ALREADY corrupt above the
#      target parent is refused rather than walked forever.
#   6. Nothing legal was collateral damage: an ordinary re-parent still works,
#      `--parent 0` still detaches a row out of a cycle, and an edit that does
#      not touch parent_id is not blocked by a cycle the row is already in.
#   7. WHY the guard is load-bearing, demonstrated rather than asserted: the
#      un-capped ancestor CTE from internal/monitor/session.go, run verbatim
#      against a cycled tasks table, never terminates — and does against the
#      table the guard protected.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

SCHEMA_SQL="${WT}/internal/schema/schema.sql"
EXEC_SRC="${WT}/internal/events/executor.go"
GUARD_SRC="${WT}/internal/events/parent_cycle.go"
SESSION_SRC="${WT}/internal/monitor/session_gate.go"

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    [[ -n "${3:-}" ]] && printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
    return 0
}

setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

TMP_E2067=""
cleanup() { [[ -n "${TMP_E2067}" ]] && rm -rf "${TMP_E2067}"; }
trap cleanup EXIT

for f in "${SCHEMA_SQL}" "${EXEC_SRC}" "${GUARD_SRC}" "${SESSION_SRC}"; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done
command -v sqlite3 >/dev/null || setup_error "sqlite3 is required"

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail-fast)"

# -count=1: without it `go test` will report a cached PASS for a package whose
# guard has since been patched out, which would make a fail-fast gate certify
# the exact regression it exists to catch.
if go test -count=1 ./internal/events/ \
        -run 'TestExecute_FieldsUpdated_(Rejects(Direct|Self|Deep)ParentCycle|Terminates|Allows(LegalReparent|ClearingParentOutOfACycle|UnrelatedEditInsideACycle))|TestExecute_TaskMoved_(StillRejectsParentCycle|RejectsSelfParent|AllowsMoveToRoot)|TestValidateNoParentCycle_' \
        >/tmp/e2067-go-events.log 2>&1; then
    report_pass "go test ./internal/events (parent-cycle suite)"
else
    report_fail "go test ./internal/events" "pass" \
        "failed — see /tmp/e2067-go-events.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# The maybe-parent suite is the invariant this fix was modelled on, and it runs
# through the same two executors. A green cycle suite over a broken maybe suite
# would mean the refactor moved the wrong thing.
if go test -count=1 ./internal/events/ -run 'TestExecute_(TaskCreated|FieldsUpdated|TaskMoved)_.*Maybe' \
        >/tmp/e2067-go-maybe.log 2>&1; then
    report_pass "go test ./internal/events (maybe-parent suite, unregressed)"
else
    report_fail "go test ./internal/events (maybe-parent suite)" "pass" \
        "failed — see /tmp/e2067-go-maybe.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# ── 2. the walk is shared, not duplicated ───────────────────────────────────
# The whole point of the task is one validator, two callers. A copy-pasted
# second walk would pass every behavioural test below and still be the bug.
section "2. One validator, both writers"

calls=$(grep -c 'ValidateNoParentCycle(db,' "${EXEC_SRC}")
if [[ "${calls}" == "2" ]]; then
    report_pass "executor.go calls ValidateNoParentCycle exactly twice"
else
    report_fail "executor.go calls ValidateNoParentCycle exactly twice" \
        "2 call sites (task.moved + task.fields_updated)" "${calls}"
fi

if grep -q 'func execTaskMoved' "${EXEC_SRC}" && \
   awk '/func execTaskMoved/,/^func execTaskDeleted/' "${EXEC_SRC}" \
       | grep -q 'ValidateNoParentCycle'; then
    report_pass "execTaskMoved delegates to the shared validator"
else
    report_fail "execTaskMoved delegates to the shared validator" \
        "a ValidateNoParentCycle call inside execTaskMoved" "absent"
fi

if awk '/func execTaskFieldsUpdated/,/^func execTaskMoved/' "${EXEC_SRC}" \
       | grep -q 'ValidateNoParentCycle'; then
    report_pass "execTaskFieldsUpdated delegates to the shared validator"
else
    report_fail "execTaskFieldsUpdated delegates to the shared validator" \
        "a ValidateNoParentCycle call inside execTaskFieldsUpdated" "absent"
fi

# The old inline walk read tasks.parent_id directly inside the executor. If that
# query reappears there, someone re-forked the guard.
if grep -q 'SELECT parent_id FROM tasks' "${EXEC_SRC}"; then
    report_fail "no private ancestor walk survives in executor.go" \
        "no 'SELECT parent_id FROM tasks' in the executor" "still present"
else
    report_pass "no private ancestor walk survives in executor.go"
fi

if grep -q 'ValidateMaybeParentless' "${GUARD_SRC}" || \
   grep -q 'seen\[' "${GUARD_SRC}"; then
    report_pass "the validator carries a visited set — a corrupt chain cannot spin it"
else
    report_fail "the validator carries a visited set" \
        "a seen-node map in parent_cycle.go" "absent"
fi

# ── setup: a binary from THIS tree and hermetic fixtures ────────────────────
TMP_E2067=$(mktemp -d "${TMPDIR:-/tmp}/e2067.XXXXXX") || setup_error "mktemp failed"
BIN="${TMP_E2067}/endless-go"
if ! go build -o "${BIN}" ./cmd/endless-go >/tmp/e2067-build.log 2>&1; then
    setup_error "could not build endless-go (see /tmp/e2067-build.log)"
fi

# Everything from here runs against a throwaway config dir placed UNDER the
# cache sandbox root, which is the E-1281 sandbox switch: the emit path then
# writes its ledger beside the fixture DB and skips the git commit, so no
# fixture can reach the real database, the real ledger, or this repo's history.
# Exported after the go build above so it cannot perturb the Go toolchain.
export XDG_CACHE_HOME="${TMP_E2067}/cache"
SANDBOX_ROOT="${XDG_CACHE_HOME}/endless/sandboxes"

# new_fixture <name> [<edges>] — a schema-applied DB holding a project and the
# task rows named in <edges> ("id:parent id:parent ...", parent 0 = root),
# inserted with plain SQL so the fixture never depends on the guard under test.
# Echoes the config dir.
new_fixture() {
    local name="$1" edges="${2:-}" cfg="${SANDBOX_ROOT}/$1" pair id parent
    mkdir -p "${cfg}" || return 1
    sqlite3 "${cfg}/endless.db" < "${SCHEMA_SQL}" >/dev/null 2>&1 || return 1
    sqlite3 "${cfg}/endless.db" \
        "INSERT INTO projects (id,name,path,status) VALUES (1,'e2067','/tmp/e2067','active');" \
        >/dev/null 2>&1 || return 1
    for pair in ${edges}; do
        id="${pair%%:*}"; parent="${pair##*:}"
        [[ "${parent}" == "0" ]] && parent="NULL"
        sqlite3 "${cfg}/endless.db" \
            "INSERT INTO tasks (id,project_id,title,phase,status,type_id,parent_id)
             VALUES (${id},1,'task ${id}','now','ready',1,${parent});" \
            >/dev/null 2>&1 || return 1
    done
    mkdir -p "${TMP_E2067}/roots/${name}" || return 1
    printf '%s' "${cfg}"
}

# emit <cfg> <name> <kind> <entity-id> <payload> — the real event path.
# Sets EMIT_OUT / EMIT_RC.
emit() {
    local cfg="$1" name="$2" kind="$3" entity="$4" payload="$5"
    EMIT_OUT=$("${BIN}" --config-dir "${cfg}" event emit \
        --kind "${kind}" --project e2067 --entity-type task --entity-id "${entity}" \
        --actor-kind cli --actor-id e2067-verify --node-id a7f3 \
        --project-root "${TMP_E2067}/roots/${name}" --payload "${payload}" 2>&1)
    EMIT_RC=$?
}

# parents <db> — the whole parent map as one comparable blob.
parents() {
    sqlite3 "$1" "SELECT group_concat(id || '->' || ifnull(parent_id,'root'), ' ')
                  FROM (SELECT id, parent_id FROM tasks ORDER BY id);"
}

# refuses <label> <cfg> <name> <kind> <entity> <payload> — assert the write is
# rejected, exits non-zero, and changes no parent link anywhere.
refuses() {
    local label="$1" cfg="$2" name="$3" kind="$4" entity="$5" payload="$6"
    local db="${cfg}/endless.db" before after
    before=$(parents "${db}")
    emit "${cfg}" "${name}" "${kind}" "${entity}" "${payload}"
    after=$(parents "${db}")
    if (( EMIT_RC != 0 )); then
        report_pass "${label}: exits non-zero (rc=${EMIT_RC})"
    else
        report_fail "${label}: exits non-zero" "a refusal" "exit 0"
    fi
    if grep -qF -- "circular reference" <<<"${EMIT_OUT}"; then
        report_pass "${label}: names the circular reference"
    else
        report_fail "${label}: names the circular reference" \
            "'circular reference' in the output" "${EMIT_OUT:-no output}"
    fi
    if [[ "${before}" == "${after}" ]]; then
        report_pass "${label}: the tree is untouched (${after})"
    else
        report_fail "${label}: the tree is untouched" "${before}" "${after}"
    fi
}

# ── 3. the filed defect ─────────────────────────────────────────────────────
section "3. \`task update --parent\` refuses to close a loop"

# 100 is root, 101 is its child. `update 100 --parent 101` is the exact shape
# that produced 799 -> 1935 -> 799 in the real database.
cfg=$(new_fixture direct "100:0 101:100") || setup_error "could not build the direct fixture"
refuses "task.fields_updated, two-node loop" "${cfg}" direct \
    task.fields_updated 100 '{"fields":{"parent_id":101}}'

# The rejection must not be a lucky side effect of some other field failing:
# the same event carrying an unrelated field is still refused, and the unrelated
# field is not written either.
cfg=$(new_fixture direct_mixed "100:0 101:100") || setup_error "could not build the mixed fixture"
emit "${cfg}" direct_mixed task.fields_updated 100 \
    '{"fields":{"parent_id":101,"description":"should not land"}}'
if (( EMIT_RC != 0 )); then
    report_pass "a cycle rejects the whole event, not just the parent field"
else
    report_fail "a cycle rejects the whole event" "a refusal" "exit 0"
fi
desc=$(sqlite3 "${cfg}/endless.db" "SELECT ifnull(description,'') FROM tasks WHERE id=100;")
if [[ -z "${desc}" ]]; then
    report_pass "the co-travelling field was not written either"
else
    report_fail "the co-travelling field was not written" "empty description" "${desc}"
fi

# ── 4. self-parenting, on both kinds ────────────────────────────────────────
# The old walk started AT the target parent and read ITS parent, so it never
# compared the target against the task itself. A root task moved under itself
# sailed straight through.
section "4. A task cannot be its own parent"

cfg=$(new_fixture self_update "110:0") || setup_error "could not build the self fixture"
refuses "task.fields_updated, self-parent" "${cfg}" self_update \
    task.fields_updated 110 '{"fields":{"parent_id":110}}'

cfg=$(new_fixture self_move "111:0") || setup_error "could not build the self-move fixture"
refuses "task.moved, self-parent" "${cfg}" self_move \
    task.moved 111 '{"new_parent_id":111}'

# ── 5. deep chains and already-corrupt chains ───────────────────────────────
section "5. Deep cycles, and chains that are already corrupt"

# 120 -> 121 -> 122. Re-parenting 120 under its own grandchild is a cycle three
# levels up; the walk has to climb, not just look at the immediate parent.
cfg=$(new_fixture deep "120:0 121:120 122:121") || setup_error "could not build the deep fixture"
refuses "task.fields_updated, three-level loop" "${cfg}" deep \
    task.fields_updated 120 '{"fields":{"parent_id":122}}'

cfg=$(new_fixture deep_move "130:0 131:130 132:131") || setup_error "could not build the deep-move fixture"
refuses "task.moved, three-level loop (the guard that already worked)" "${cfg}" deep_move \
    task.moved 130 '{"new_parent_id":132}'

# 141 <-> 142 already loop, seeded behind the executor's back — the state the
# real database was found in. 140 is unrelated. Attaching 140 under that chain
# adds no NEW edge to the loop, so a naive walk climbs it forever. This
# assertion completing at all is most of what it proves.
cfg=$(new_fixture corrupt "140:0 141:0 142:141") || setup_error "could not build the corrupt fixture"
sqlite3 "${cfg}/endless.db" "UPDATE tasks SET parent_id=142 WHERE id=141;" \
    || setup_error "could not seed the pre-existing cycle"
refuses "attaching under an already-cyclic chain terminates and refuses" "${cfg}" corrupt \
    task.fields_updated 140 '{"fields":{"parent_id":142}}'

# ── 6. nothing legal became collateral damage ───────────────────────────────
section "6. Legal parent writes still work"

# An ordinary re-parent onto an unrelated subtree.
cfg=$(new_fixture legal "150:0 151:150 152:0") || setup_error "could not build the legal fixture"
emit "${cfg}" legal task.fields_updated 151 '{"fields":{"parent_id":152}}'
got=$(sqlite3 "${cfg}/endless.db" "SELECT ifnull(parent_id,'root') FROM tasks WHERE id=151;")
if (( EMIT_RC == 0 )) && [[ "${got}" == "152" ]]; then
    report_pass "an ordinary re-parent still lands (151 -> 152)"
else
    report_fail "an ordinary re-parent still lands" "rc=0 and parent 152" \
        "rc=${EMIT_RC}, parent ${got}"
fi

# The documented way out of a cycle: --parent 0 -> parent_id NULL. Detaching
# can never create a cycle, so it must work even from inside one. This is the
# mechanism the analysis says already exists; the guard must not break it.
cfg=$(new_fixture escape "160:0 161:160") || setup_error "could not build the escape fixture"
sqlite3 "${cfg}/endless.db" "UPDATE tasks SET parent_id=161 WHERE id=160;" \
    || setup_error "could not seed the escape cycle"
emit "${cfg}" escape task.fields_updated 160 '{"fields":{"parent_id":null}}'
got=$(sqlite3 "${cfg}/endless.db" "SELECT ifnull(parent_id,'root') FROM tasks WHERE id=160;")
if (( EMIT_RC == 0 )) && [[ "${got}" == "root" ]]; then
    report_pass "\`--parent 0\` still detaches a row out of a cycle"
else
    report_fail "\`--parent 0\` still detaches a row out of a cycle" \
        "rc=0 and parent root" "rc=${EMIT_RC}, parent ${got}"
fi

# task.moved --root, the same escape hatch on the other verb.
cfg=$(new_fixture escape_move "170:0 171:170") || setup_error "could not build the move-escape fixture"
sqlite3 "${cfg}/endless.db" "UPDATE tasks SET parent_id=171 WHERE id=170;" \
    || setup_error "could not seed the move-escape cycle"
emit "${cfg}" escape_move task.moved 170 '{"new_parent_id":null}'
got=$(sqlite3 "${cfg}/endless.db" "SELECT ifnull(parent_id,'root') FROM tasks WHERE id=170;")
if (( EMIT_RC == 0 )) && [[ "${got}" == "root" ]]; then
    report_pass "\`task move --root\` still detaches a row out of a cycle"
else
    report_fail "\`task move --root\` still detaches a row out of a cycle" \
        "rc=0 and parent root" "rc=${EMIT_RC}, parent ${got}"
fi

# An edit that does not touch parent_id must not be blocked by a cycle the row
# is already in — the same rule the maybe-parent guard follows for a row that
# already violates it. Pre-existing corruption stays editable.
cfg=$(new_fixture unrelated "180:0 181:180") || setup_error "could not build the unrelated fixture"
sqlite3 "${cfg}/endless.db" "UPDATE tasks SET parent_id=181 WHERE id=180;" \
    || setup_error "could not seed the unrelated cycle"
emit "${cfg}" unrelated task.fields_updated 180 '{"fields":{"description":"edited"}}'
got=$(sqlite3 "${cfg}/endless.db" "SELECT ifnull(description,'') FROM tasks WHERE id=180;")
if (( EMIT_RC == 0 )) && [[ "${got}" == "edited" ]]; then
    report_pass "an unrelated edit inside a cycle is not blocked"
else
    report_fail "an unrelated edit inside a cycle is not blocked" \
        "rc=0 and description 'edited'" "rc=${EMIT_RC}, description '${got}'"
fi

# ── 7. what the guard is protecting, demonstrated ───────────────────────────
# Everything above tests the refusal. This tests the CLAIM behind it, by running
# the ancestor CTE from internal/monitor/session_gate.go (UNION ALL, no depth
# cap, so SQLite must materialise the whole thing) against a table that has a
# cycle in it. Bounded with LIMIT so the demonstration cannot hang the suite:
# reaching the bound IS the finding.
#
# The probed file was internal/monitor/session.go until E-2074, which deleted
# nearestEpicAncestor along with the background-agent dispatch that was its only
# caller. The same unbounded ancestry walk lives on in session_gate.go — and in
# internal/events/executor.go — so the claim is unchanged; only the specimen
# moved.
section "7. What a cycle does to the queries that walk the tree"

ANCESTRY_CTE="WITH RECURSIVE ancestry(id, parent_id, depth) AS (
    SELECT id, parent_id, 0 FROM tasks WHERE id = 191
    UNION ALL
    SELECT t.id, t.parent_id, a.depth + 1 FROM tasks t JOIN ancestry a ON t.id = a.parent_id
) SELECT count(*) FROM (SELECT id FROM ancestry LIMIT 5000);"

if ! grep -q 'JOIN ancestry a ON t.id = a.parent_id' "${SESSION_SRC}"; then
    report_fail "the demonstrated query still matches the source" \
        "an unbounded ancestry join" "session_gate.go has changed shape"
else
    report_pass "the demonstrated query still matches the ancestry walk in session_gate.go"
fi

cfg=$(new_fixture harm "190:0 191:190") || setup_error "could not build the harm fixture"
harm_db="${cfg}/endless.db"

rows=$(sqlite3 "${harm_db}" "${ANCESTRY_CTE}")
if [[ "${rows}" == "2" ]]; then
    report_pass "acyclic: the ancestor walk yields 2 rows and stops"
else
    report_fail "acyclic: the ancestor walk terminates" "2 rows" "${rows}"
fi

# Close the loop by hand — the write the guard now refuses.
sqlite3 "${harm_db}" "UPDATE tasks SET parent_id=191 WHERE id=190;" \
    || setup_error "could not seed the harm cycle"
rows=$(sqlite3 "${harm_db}" "${ANCESTRY_CTE}")
if [[ "${rows}" == "5000" ]]; then
    report_pass "cyclic: the same walk hits the 5000-row bound — unbounded without it"
else
    report_fail "cyclic: the same walk is unbounded" "5000 rows (the LIMIT)" "${rows}"
fi

# And the write that would have produced that state is exactly what section 3
# refused, on a fixture built the same way.
cfg=$(new_fixture harm_guarded "190:0 191:190") || setup_error "could not build the guarded fixture"
emit "${cfg}" harm_guarded task.fields_updated 190 '{"fields":{"parent_id":191}}'
rows=$(sqlite3 "${cfg}/endless.db" "${ANCESTRY_CTE}")
if (( EMIT_RC != 0 )) && [[ "${rows}" == "2" ]]; then
    report_pass "guarded: the emit is refused and the walk still terminates at 2"
else
    report_fail "guarded: the emit is refused and the walk terminates" \
        "a refusal and 2 rows" "rc=${EMIT_RC}, ${rows} rows"
fi

# ── summary ─────────────────────────────────────────────────────────────────
section "Summary"
printf '  %s%d passed%s, %s%d failed%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" \
    "$([[ ${FAIL_COUNT} -gt 0 ]] && printf '%s' "${RED}")" "${FAIL_COUNT}" "${RESET}"

if (( FAIL_COUNT > 0 )); then
    printf '\n  Failed:\n'
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    exit 1
fi
exit 0
