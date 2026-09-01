#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2023 and records what was true when E-2023
# landed. Edit it only if you ARE E-2023. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2023 verification — the own-task-only rule is enforced at the verify runner.
#
# Before: the rule was prose. `endless guide orchestration` said a per-task
# verify script is valid only immediately before land, in its own task's
# worktree, and that you must not run another task's. It was read, understood,
# paraphrased into "not required", and broken — twice. The second time a session
# ran ALL 197 of them as a "project-wide regression check", one of which drove
# the real hook binary and wrote a session row into the MAIN database, taking
# down session tracking in the user's shell.
#
# After: the rule is structural, in three layers.
#
#   1. LOCATION. The scripts left tests/, which is what made them reachable by a
#      glob and what made them read as a suite you re-run. They now sit beside
#      the manifests, one directory per task, under .endless/tasks/e-<id>/.
#   2. THE FRONT DOOR. `endless task verify` runs BOTH suite forms, under the
#      same Tier-0 isolation, so there is one command and no reason to reach for
#      the file. A suite sourcing the shared harness refuses a direct run.
#   3. THE REFUSAL. Before anything runs, the runner refuses a task that has
#      LANDED and is not the caller's own. The conjunction is the design: a task
#      that landed and was then reopened is still yours, and re-verifying it is
#      the documented path when shipped work turns out wrong.
#
# This suite is also the first exemplar of the harness it is testing: it sources
# .endless/tasks/_harness.sh, which is why it cannot be run any other way.
#
#   endless task verify E-2023        # or, in this repo, `just verify`
#
# What it proves, section by section:
#
#   1. FAIL-FAST unit gate: internal/verify, internal/verifycmd, and the Python
#      scaffolding tests. Everything below drives the same code, so a red gate
#      makes the rest noise.
#   2. Every script moved, nothing is left under the old path, and history
#      follows.
#   3. A manifest suite still verifies exactly as before, and a task holding
#      BOTH forms resolves deterministically to the manifest.
#   4. A script-only task verifies through the runner with the same verdict and
#      the same exit code it had when it was executed directly.
#   5. A task with neither form names BOTH filenames in its error.
#   6. The refusal fires on a landed foreign suite, in either form, and does NOT
#      fire on the caller's own task — by worktree or by session.
#   7. A suite sourcing the harness passes under the runner, and a bare direct
#      run of it is refused by the guard, naming the runner.
#   8. The E-2071 shape — a glob over the suite directory — is unreachable.
#   9. _harness.sh and CLAUDE.md sitting beside the per-task directories do not
#      confuse discovery and are not discoverable as tasks.
#  10. The harness emits TAP, and converting a suite to it does not change its
#      verdict — it only adds per-assertion results.
#  11. .endless/tasks/CLAUDE.md exists and the refusals point at it.
#  12. `just verify` resolves its id the way `just land` does, echoes what it
#      derived, cds into that task's worktree, and errors with a usage line when
#      neither the session nor the cwd yields one.
#  13. THE REGRESSION THIS MUST NEVER CAUSE: a landed, reopened, re-claimed task
#      verifies from its own worktree, by id and with no id.
#  14. A freshly registered project has .endless/tasks/CLAUDE.md, and
#      re-registering neither duplicates nor overwrites a customized one.
#  15. EVERY suite in the corpus refuses a direct run, before executing a line
#      of its own — foreign suites on ownership, this worktree's own on
#      isolation.
#  16. $ENDLESS_VERIFY_DIR reaches BOTH suite forms, and no manifest hand-writes
#      its own directory any more.
#  17. `endless task verify` is sufficient by itself: it resolves the task from
#      the session or the cwd, and runs in that task's worktree.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement, the same way every other suite in this tree now
# does it — so nothing in this file runs before the refusal has had its say.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

GO_BIN="${WT}/bin/endless-go"
[[ -x "${GO_BIN}" ]] || setup_error "missing ${GO_BIN} — run 'just go' first"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test ./internal/verify/... ./internal/verifycmd/... >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/verify/... ./internal/verifycmd/..."
else
    report_fail "go test ./internal/verify/... ./internal/verifycmd/..." \
        "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# The main-database read the refusal is built on lives in internal/monitor. Only
# its own tests are selected: the rest of that package is a two-minute suite
# this task does not touch, and `just test-go` covers it.
if go test ./internal/monitor/ -run 'TestSuiteOwnershipDB|TestReadOnlyDSN' -count=1 \
        >"${TMP}/go-monitor.log" 2>&1; then
    report_pass "go test ./internal/monitor (suite-ownership reads are read-only)"
else
    report_fail "go test ./internal/monitor (suite-ownership reads)" \
        "exit 0" "$(tail -25 "${TMP}/go-monitor.log")"
    summary
fi

if uv run pytest tests/test_suite_rules.py tests/test_verify_cmd.py -q \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest tests/test_suite_rules.py tests/test_verify_cmd.py"
else
    report_fail "pytest tests/test_suite_rules.py tests/test_verify_cmd.py" \
        "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. the move ─────────────────────────────────────────────────────────────
section "2. Every suite moved out of tests/"

if [[ -e "${WT}/tests/tasks" ]]; then
    report_fail "tests/tasks no longer exists" "absent" "still present"
else
    report_pass "tests/tasks no longer exists"
fi

moved=0
stray=0
for d in "${WT}"/.endless/tasks/e-*/; do
    [[ -d "${d}" ]] || continue
    if [[ -f "${d}verify.sh" || -f "${d}verify.toml" ]]; then
        moved=$((moved + 1))
    else
        stray=$((stray + 1))
    fi
done
if (( moved > 150 )); then
    report_pass "${moved} task directories hold a suite"
else
    report_fail "the whole corpus moved" "more than 150 task directories" "${moved}"
fi
assert_eq "no task directory is empty of both forms" "0" "${stray}"

# The move used `git mv`, so a landed suite's history is still reachable from
# its new path. A relocation that orphaned two hundred files' history would be
# a silent, unrecoverable loss.
sample="$(cd "${WT}" && git log --follow --oneline -- .endless/tasks/e-1603/verify.sh | wc -l | tr -d ' ')"
if (( sample > 1 )); then
    report_pass "git log --follow reaches a sample suite's pre-move history (${sample} commits)"
else
    report_fail "git log --follow reaches pre-move history" "more than 1 commit" "${sample}"
fi

# ── fixtures ────────────────────────────────────────────────────────────────
#
# Everything below drives the real runner against throwaway projects under the
# temp dir. The runner is already running us under an isolated HOME/XDG, so
# these fixtures cannot reach the developer's config or the main database —
# which is also why the refusal fixture has to build a main database of its own.

FIX="${TMP}/fixtures"
mkdir -p "${FIX}" || setup_error "could not create ${FIX}"

# new_project <name> -> prints its root
new_project() {
    local root="${FIX}/$1"
    mkdir -p "${root}/.endless/tasks"
    # Both files, always. _harness.sh sources _guard.sh as its first act and
    # exits 2 when it is not beside it, so a fixture carrying only the harness
    # would refuse for a reason this suite is not testing (E-2090).
    cp "${WT}/.endless/tasks/_harness.sh" "${root}/.endless/tasks/_harness.sh"
    cp "${WT}/.endless/tasks/_guard.sh" "${root}/.endless/tasks/_guard.sh"
    printf '%s\n' "${root}"
}

# add_script <root> <id> <body>
add_script() {
    local root="$1" id="$2" body="$3"
    mkdir -p "${root}/.endless/tasks/${id}"
    printf '%s' "${body}" > "${root}/.endless/tasks/${id}/verify.sh"
    chmod +x "${root}/.endless/tasks/${id}/verify.sh"
}

# add_manifest <root> <id> <body>
add_manifest() {
    local root="$1" id="$2" body="$3"
    mkdir -p "${root}/.endless/tasks/${id}"
    printf '%s' "${body}" > "${root}/.endless/tasks/${id}/verify.toml"
}

# verify_in <root> <args...> -> exit code; output in $LAST_OUT
LAST_OUT=""
verify_in() {
    local root="$1"; shift
    local rc=0
    LAST_OUT="$(cd "${root}" && "${GO_BIN}" verify "$@" 2>&1)" || rc=$?
    printf '%s' "${LAST_OUT}" > "${TMP}/last_out.txt"
    return "${rc}"
}

FAILING_RAW='#!/usr/bin/env bash
echo "something is wrong"
exit 1
'
PASSING_RAW='#!/usr/bin/env bash
echo "ALL PASSED"
exit 0
'

# ── 3. the manifest path is unchanged ───────────────────────────────────────
section "3. A manifest suite still verifies, and wins over a script"

M_OK='schema = 1
task = "E-301"
[[check]]
runner = "sh"
command = "printf '"'"'1..2\nok 1 - m1\nok 2 - m2\n'"'"'"
format = "tap"
'
P3="$(new_project manifest)"
add_manifest "${P3}" "e-301" "${M_OK}"
if verify_in "${P3}" E-301; then
    report_pass "a manifest suite passes through the runner"
else
    report_fail "a manifest suite passes through the runner" "exit 0" "${LAST_OUT}"
fi
assert_contains "its checks are normalized, not just exit-code'd" "2 passed (2 tests)" "${LAST_OUT}"

# Both forms in one directory. The script would exit 7 if it ran.
add_manifest "${P3}" "e-302" "${M_OK//E-301/E-302}"
add_script "${P3}" "e-302" '#!/usr/bin/env bash
exit 7
'
verify_in "${P3}" E-302; rc302=$?
assert_eq "a task holding BOTH forms resolves to the manifest" "0" "${rc302}"

# ── 4. the script path ──────────────────────────────────────────────────────
section "4. A script-only task verifies through the runner"

P4="$(new_project script)"
add_script "${P4}" "e-401" "${PASSING_RAW}"
add_script "${P4}" "e-402" "${FAILING_RAW}"

# The reference verdict: what the script does when executed directly, which is
# what it did before the move. It sources nothing, so no guard stands in its
# way and it still runs.
"${P4}/.endless/tasks/e-401/verify.sh" >/dev/null 2>&1; direct_pass=$?
"${P4}/.endless/tasks/e-402/verify.sh" >/dev/null 2>&1; direct_fail=$?

verify_in "${P4}" E-401; runner_pass=$?
assert_eq "a passing script's exit code is unchanged through the runner" "${direct_pass}" "${runner_pass}"
assert_contains "and its own output reaches the terminal" "ALL PASSED" "${LAST_OUT}"

verify_in "${P4}" E-402; runner_fail=$?
assert_eq "a failing script's exit code is unchanged through the runner" "${direct_fail}" "${runner_fail}"

# A script that emits no result stream must not normalize to zero tests: the
# runner reads zero tests as a failure, which would invert a passing suite.
verify_in "${P4}" E-401
assert_contains "a stream-less script reports one result, not zero" "1 passed (1 tests)" "${LAST_OUT}"

# ── 5. neither form ─────────────────────────────────────────────────────────
section "5. A task with no suite names both filenames"

verify_in "${P4}" E-999; rc999=$?
if (( rc999 != 0 )); then
    report_pass "a task with no suite exits non-zero"
else
    report_fail "a task with no suite exits non-zero" "non-zero" "0"
fi
assert_contains "the error names verify.toml" "verify.toml" "${LAST_OUT}"
assert_contains "the error names verify.sh" "verify.sh" "${LAST_OUT}"
assert_contains "the error names the directory it looked in" "tasks/e-999" "${LAST_OUT}"

# ── 6. the refusal ──────────────────────────────────────────────────────────
section "6. The own-task-only refusal"

if ! command -v sqlite3 >/dev/null 2>&1; then
    report_skip "the refusal fires on a landed foreign suite" "sqlite3 not installed"
    report_skip "the refusal does not fire on the caller's own task" "sqlite3 not installed"
else
    # The runner reads landed-ness and session ownership from the DEPLOYED
    # installation's database, whatever database the rest of the command is
    # talking to. Under this suite's isolated HOME that is a path we own, so the
    # fixture is a real main database with real rows.
    MAIN_DB="${HOME}/.config/endless/endless.db"
    mkdir -p "$(dirname "${MAIN_DB}")"
    sqlite3 "${MAIN_DB}" \
        "CREATE TABLE task_landings (id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL);
         CREATE TABLE sessions (id INTEGER PRIMARY KEY, task_id INTEGER);
         INSERT INTO task_landings (task_id) VALUES (500), (600);
         INSERT INTO sessions (id, task_id) VALUES (77, 600);" \
        || setup_error "could not build the fixture main database"

    # A checkout shaped like a real task worktree: its path names its task.
    OWNER="${FIX}/proj/.endless/worktrees/e-500"
    mkdir -p "${OWNER}/.endless/tasks"
    cp "${WT}/.endless/tasks/_harness.sh" "${OWNER}/.endless/tasks/_harness.sh"
    cp "${WT}/.endless/tasks/_guard.sh" "${OWNER}/.endless/tasks/_guard.sh"
    add_script "${OWNER}" "e-500" "${PASSING_RAW}"
    add_script "${OWNER}" "e-600" "${PASSING_RAW}"
    add_manifest "${OWNER}" "e-601" "${M_OK//E-301/E-601}"
    sqlite3 "${MAIN_DB}" "INSERT INTO task_landings (task_id) VALUES (601);"

    env -u ENDLESS_SESSION_ID bash -c \
        "cd '${OWNER}' && '${GO_BIN}' verify E-600" >"${TMP}/refuse.txt" 2>&1; rc_foreign=$?
    LAST_OUT="$(cat "${TMP}/refuse.txt")"
    if (( rc_foreign != 0 )); then
        report_pass "a landed foreign SCRIPT suite is refused, non-zero"
    else
        report_fail "a landed foreign SCRIPT suite is refused" "non-zero" "0: ${LAST_OUT}"
    fi
    assert_contains "the refusal says it landed and is not yours" "it has landed, and it is not yours" "${LAST_OUT}"
    assert_contains "it names what to run instead" "endless task verify E-500" "${LAST_OUT}"
    assert_not_contains "and nothing ran" "ALL PASSED" "${LAST_OUT}"

    env -u ENDLESS_SESSION_ID bash -c \
        "cd '${OWNER}' && '${GO_BIN}' verify E-601" >"${TMP}/refuse2.txt" 2>&1; rc_foreign2=$?
    if (( rc_foreign2 != 0 )); then
        report_pass "a landed foreign MANIFEST suite is refused too — the rule is not per-form"
    else
        report_fail "a landed foreign MANIFEST suite is refused" "non-zero" "0"
    fi

    env -u ENDLESS_SESSION_ID bash -c \
        "cd '${OWNER}' && '${GO_BIN}' verify E-500" >"${TMP}/own.txt" 2>&1; rc_own=$?
    assert_eq "the caller's own task is NOT refused, though it also landed" "0" "${rc_own}"

    # The session's own task is an owner even when the checkout is not its
    # worktree — this is what makes bare `endless task verify` (which resolves
    # to the session's active task) always allowed.
    ENDLESS_SESSION_ID=77 bash -c \
        "cd '${OWNER}' && '${GO_BIN}' verify E-600" >"${TMP}/sess.txt" 2>&1; rc_sess=$?
    assert_eq "the session's own task is not refused (what bare 'task verify' relies on)" "0" "${rc_sess}"

    # An UNLANDED foreign task is live work; the refusal is about landed suites.
    add_script "${OWNER}" "e-700" "${PASSING_RAW}"
    env -u ENDLESS_SESSION_ID bash -c \
        "cd '${OWNER}' && '${GO_BIN}' verify E-700" >/dev/null 2>&1; rc_unlanded=$?
    assert_eq "an UNLANDED foreign task is not refused" "0" "${rc_unlanded}"

    # ── 8. the E-2071 shape ────────────────────────────────────────────────
    section "8. The glob that caused E-2071 is unreachable"

    refused=0; ran=0
    for s in "${OWNER}"/.endless/tasks/e-*/verify.sh; do
        id="e-$(basename "$(dirname "${s}")" | sed 's/^e-//')"
        if env -u ENDLESS_SESSION_ID bash -c \
                "cd '${OWNER}' && '${GO_BIN}' verify '${id}'" >/dev/null 2>&1; then
            ran=$((ran + 1))
        else
            refused=$((refused + 1))
        fi
    done
    # e-500 (own) and e-700 (unlanded) run; e-600 (landed, foreign) refuses.
    assert_eq "sweeping the suite directory refuses the landed foreign suite" "1" "${refused}"
    assert_eq "and runs only what the caller may run" "2" "${ran}"

    # ── 13. the regression this must never cause ───────────────────────────
    section "13. A landed, reopened task still verifies from its own worktree"

    # E-500 landed. Its session reopened it and is working in its worktree.
    # BOTH the by-id form and the bare form (which resolves to the session's
    # active task) must be allowed. A predicate keyed on landed-ness ALONE
    # would refuse both, however elegant.
    sqlite3 "${MAIN_DB}" "INSERT INTO sessions (id, task_id) VALUES (88, 500);"
    ENDLESS_SESSION_ID=88 bash -c \
        "cd '${OWNER}' && '${GO_BIN}' verify E-500" >/dev/null 2>&1; rc_reopen=$?
    assert_eq "by id, from its own worktree, with its own session" "0" "${rc_reopen}"

    # The bare form resolves the id in Python and hands it to the runner, so
    # what the runner must not do is refuse THAT id for THAT session.
    ENDLESS_SESSION_ID=88 bash -c \
        "cd '${FIX}' && '${GO_BIN}' verify E-500" >"${TMP}/bare.txt" 2>&1; rc_bare=$?
    if (( rc_bare != 0 )) && grep -q "it has landed, and it is not yours" "${TMP}/bare.txt"; then
        report_fail "the session's task is allowed from anywhere (the bare form)" \
            "not refused" "$(cat "${TMP}/bare.txt")"
    else
        report_pass "the session's task is allowed from anywhere (the bare form)"
    fi
fi

# ── 7. the marker ───────────────────────────────────────────────────────────
section "7. A harness-sourcing suite runs under the runner and nowhere else"

P7="$(new_project marker)"
HARNESSED='#!/usr/bin/env bash
set -u
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
section "checks"
assert_eq "one" "1" "1"
assert_eq "two" "2" "2"
summary
'
add_script "${P7}" "e-701" "${HARNESSED}"

verify_in "${P7}" E-701; rc_h=$?
assert_eq "a harness-sourcing suite passes under the runner" "0" "${rc_h}"
assert_contains "its assertions are normalized individually" "2 passed (2 tests)" "${LAST_OUT}"

# A direct run is refused on either of the guard's two grounds, and both are
# exercised here because each is reachable on its own. Neither is a claim the
# caller makes about itself — that was the retired marker's flaw, and its
# absence is asserted elsewhere (tests/test_suite_guard.py); do not reintroduce
# it to make anything here pass.

# Arm 2, NOT ISOLATED: a hand-run with a real config reachable. This is what a
# person or an agent actually does, and the case the arm exists for. HOME is
# pointed at a fixture holding a config dir, since this suite is itself running
# under the runner's isolated HOME, where nothing is reachable.
REAL_HOME="${FIX}/realhome"
mkdir -p "${REAL_HOME}/.config/endless"
direct_out="$(env -u ENDLESS_VERIFY_TAP -u XDG_CONFIG_HOME HOME="${REAL_HOME}" \
    "${P7}/.endless/tasks/e-701/verify.sh" 2>&1)"; rc_direct=$?
if (( rc_direct != 0 )); then
    report_pass "a hand-run with a reachable config is refused, non-zero"
else
    report_fail "a hand-run with a reachable config is refused" "non-zero" "0"
fi
assert_contains "the refusal names the runner" "endless task verify" "${direct_out}"
assert_contains "and points at the rules" ".endless/tasks/CLAUDE.md" "${direct_out}"
assert_not_contains "and nothing ran" "2 passed" "${direct_out}"

# Arm 1, NOT YOURS: the same suite sitting in another task's worktree. Both
# facts come from the file's own path, so this refuses even under the runner's
# isolation — which is why the id in the path, not the environment, is what
# makes it un-forgeable.
FOREIGN="${FIX}/wt/.endless/worktrees/e-702"
mkdir -p "${FOREIGN}/.endless/tasks/e-701"
cp "${WT}/.endless/tasks/_harness.sh" "${FOREIGN}/.endless/tasks/_harness.sh"
cp "${WT}/.endless/tasks/_guard.sh" "${FOREIGN}/.endless/tasks/_guard.sh"
printf '%s' "${HARNESSED}" > "${FOREIGN}/.endless/tasks/e-701/verify.sh"
chmod +x "${FOREIGN}/.endless/tasks/e-701/verify.sh"
foreign_out="$(env -u ENDLESS_VERIFY_TAP "${FOREIGN}/.endless/tasks/e-701/verify.sh" 2>&1)"
rc_foreign=$?
if (( rc_foreign != 0 )); then
    report_pass "the same suite in another task's worktree is refused, non-zero"
else
    report_fail "the same suite in another task's worktree is refused" "non-zero" "0"
fi
assert_contains "the refusal names whose suite it is and where it is sitting" \
    "E-701's verification suite from E-702's worktree" "${foreign_out}"

# ── 9. shared files beside the task directories ─────────────────────────────
section "9. _harness.sh and CLAUDE.md are not tasks"

P9="$(new_project shared)"
add_script "${P9}" "e-901" "${PASSING_RAW}"
cp "${WT}/.endless/tasks/CLAUDE.md" "${P9}/.endless/tasks/CLAUDE.md"

verify_in "${P9}" E-901; rc9=$?
assert_eq "a real task still discovers with shared files beside it" "0" "${rc9}"

verify_in "${P9}" "_harness.sh" || true
assert_contains "_harness.sh is not discoverable as a task" "no verification suite" "${LAST_OUT}"
verify_in "${P9}" "CLAUDE.md" || true
assert_contains "CLAUDE.md is not discoverable as a task" "no verification suite" "${LAST_OUT}"

# ── 10. conversion equivalence ──────────────────────────────────────────────
section "10. Converting a suite to the harness does not change its verdict"

P10="$(new_project convert)"
# The same two assertions, written raw and written through the harness.
RAW_PAIR='#!/usr/bin/env bash
fail=0
[[ "1" == "1" ]] || fail=1
[[ "2" == "2" ]] || fail=1
exit "${fail}"
'
CONVERTED_PAIR='#!/usr/bin/env bash
set -u
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
assert_eq "one" "1" "1"
assert_eq "two" "2" "2"
summary
'
RAW_PAIR_RED="${RAW_PAIR/\[\[ \"2\" == \"2\" \]\]/[[ \"2\" == \"3\" ]]}"
CONVERTED_PAIR_RED="${CONVERTED_PAIR/assert_eq \"two\" \"2\" \"2\"/assert_eq \"two\" \"2\" \"3\"}"

add_script "${P10}" "e-1001" "${RAW_PAIR}"
add_script "${P10}" "e-1002" "${CONVERTED_PAIR}"
add_script "${P10}" "e-1003" "${RAW_PAIR_RED}"
add_script "${P10}" "e-1004" "${CONVERTED_PAIR_RED}"

verify_in "${P10}" E-1001; raw_green=$?
verify_in "${P10}" E-1002; conv_green=$?
converted_out="${LAST_OUT}"
verify_in "${P10}" E-1003; raw_red=$?
verify_in "${P10}" E-1004; conv_red=$?

assert_eq "green stays green after conversion" "${raw_green}" "${conv_green}"
assert_eq "red stays red after conversion" "${raw_red}" "${conv_red}"
assert_contains "conversion adds per-assertion results" "2 passed (2 tests)" "${converted_out}"

# ── 11. the rules file ──────────────────────────────────────────────────────
section "11. The rules the refusals point at"

if [[ -f "${WT}/.endless/tasks/CLAUDE.md" ]]; then
    report_pass ".endless/tasks/CLAUDE.md exists"
else
    report_fail ".endless/tasks/CLAUDE.md exists" "present" "absent"
fi
rules="$(cat "${WT}/.endless/tasks/CLAUDE.md" 2>/dev/null || true)"
assert_contains "it names the front door" "endless task verify" "${rules}"
assert_contains "it forbids running another task's suite" "Do not run another task's suite" "${rules}"
assert_contains "it forbids editing a landed suite" "Do not edit a landed task's suite" "${rules}"

# ── 12. the just verify recipe ──────────────────────────────────────────────
section "12. just verify resolves its id the way just land does"

# The recipe is exercised for real, in a throwaway repo, with the body lifted
# out of the project's own justfile — so this cannot pass against a recipe that
# has since drifted. Stubs stand in for endless / endless-go so no suite runs.
if ! command -v just >/dev/null 2>&1; then
    report_skip "just verify derives and echoes its task id" "just not installed"
else
    JR="${FIX}/justrepo"
    mkdir -p "${JR}/.endless/worktrees/e-500" "${JR}/stub"
    ( cd "${JR}" && git init -q . && git commit -q --allow-empty -m init ) >/dev/null 2>&1

    # awk lifts the recipe verbatim: from the `verify task_id=""` line to the
    # first line that is neither blank nor indented.
    awk '/^verify task_id=""/{f=1} f{ if (NR>1 && $0 !~ /^[ \t]/ && $0 != "" && !/^verify task_id/) exit; print }' \
        "${WT}/justfile" > "${JR}/justfile"
    grep -q 'endless --db sandbox task verify' "${JR}/justfile" \
        || setup_error "could not lift the verify recipe out of the justfile"
    # A task worktree is a full checkout, so it carries the justfile too. That
    # matters here and not only for realism: `just` cds to the directory of the
    # justfile it finds, so without this the recipe's cwd fallback would be
    # reading the fixture root rather than the worktree.
    cp "${JR}/justfile" "${JR}/.endless/worktrees/e-500/justfile"

    printf '#!/usr/bin/env bash\necho "STUB endless cwd=$(pwd) args=$*"\n' > "${JR}/stub/endless"
    printf '#!/usr/bin/env bash\nexit 1\n' > "${JR}/stub/endless-go"
    chmod +x "${JR}/stub/endless" "${JR}/stub/endless-go"

    jverify() { ( cd "$1" && PATH="${JR}/stub:${PATH}" just verify "${2:-}" 2>&1 ); }

    out_cwd="$(jverify "${JR}/.endless/worktrees/e-500")"
    assert_contains "with no id, cwd derives the task and the recipe says so" \
        "Derived task ID from cwd: E-500" "${out_cwd}"
    assert_contains "it runs against the worktree's endless (--db sandbox)" \
        "args=--db sandbox task verify E-500" "${out_cwd}"
    assert_contains "and it runs from INSIDE that task's worktree" \
        "cwd=${JR}/.endless/worktrees/e-500" "${out_cwd}"

    out_explicit="$(jverify "${JR}" "E-500")"
    assert_contains "an explicit id wins and needs no derivation" \
        "args=--db sandbox task verify E-500" "${out_explicit}"
    assert_not_contains "and is not echoed as derived" "Derived task ID" "${out_explicit}"

    out_none="$(jverify "${JR}")"; rc_none=$?
    assert_contains "with neither session nor cwd, it prints a usage line" \
        "Usage: just verify [E-NNNN]" "${out_none}"

    # The derivation chain is `just land`'s, not a second thinner one. Compare
    # the two recipes' chains directly so they cannot drift apart. Comments and
    # blank lines are stripped: the two recipes explain themselves differently
    # and should, but the CODE that picks a task must be the same code.
    chain() {
        awk -v r="^$1 task_id=\"\"" '$0 ~ r {f=1} f{ if (NR>1 && $0 !~ /^[ \t]/ && $0 != "" && $0 !~ r) exit; print }' \
            "${WT}/justfile" \
            | sed -n '/if \[ -z "\$tid" \]/,/^    fi$/p' \
            | grep -v '^[[:space:]]*#' \
            | grep -v '^[[:space:]]*$' \
            | sed "s/just ${1}/just RECIPE/"
    }
    if [[ "$(chain verify)" == "$(chain land)" ]]; then
        report_pass "the id-derivation chain is byte-identical to just land's"
    else
        report_fail "the id-derivation chain is byte-identical to just land's" \
            "$(chain land)" "$(chain verify)"
    fi
fi

# ── 14. a freshly registered project ────────────────────────────────────────
section "14. A fresh project gets the rules, and keeps its own edits"

FRESH="${FIX}/fresh"
mkdir -p "${FRESH}"
scaffold_out="$(cd "${WT}" && uv run python -c "
import sys
from pathlib import Path
from endless.suite_rules import SUITE_RULES, scaffold_suite_rules
root = Path('${FRESH}')
first = scaffold_suite_rules(root)
target = root / '.endless' / 'tasks' / 'CLAUDE.md'
same = target.read_text() == SUITE_RULES
target.write_text('# ours\n')
second = scaffold_suite_rules(root)
kept = target.read_text() == '# ours\n'
print(f'{first} {same} {second} {kept}')
" 2>&1)" || setup_error "scaffolding probe failed: ${scaffold_out}"
assert_eq "written once, matching the shipped text, never overwritten after" \
    "True True False True" "${scaffold_out}"

# ── 15. the whole corpus, not just new suites ───────────────────────────────
section "15. Every suite refuses a direct run"

# The plan deferred this: existing suites were to stay directly runnable, with
# only the runner and E-1916's hook guarding them. Mike overruled it, and he was
# right — "the enforcement point moved from 200 places to one" is only true if
# the 200 cannot still be reached individually.
#
# E-2090 then replaced this task's ENDLESS_VERIFY_RUN marker with an
# un-forgeable guard, and it refuses on TWO grounds with two different messages.
# Read from this worktree, every suite hits exactly one of them, and which one
# is decided by whose suite it is:
#
#   - a FOREIGN suite (its directory names a task that is not this worktree's)
#     refuses on ownership, before it can consider anything else;
#   - THIS worktree's own suite passes ownership and refuses on isolation,
#     because a real Endless config is reachable from a hand-run shell.
#
# Asserting one message for both would pass for the wrong reason on 205 of the
# 206 — which is exactly what this check did until E-2090 landed, and what its
# session flagged rather than let stand.
own=0; foreign=0; ran=0; leaked=0
for s in "${WT}"/.endless/tasks/e-*/verify.sh; do
    id="$(basename "$(dirname "${s}")")"; id="${id#e-}"
    out="$(env -u ENDLESS_VERIFY_TAP -u ENDLESS_VERIFY_TASK \
        "${s}" 2>&1)"; rc=$?
    if (( rc == 0 )); then
        ran=$((ran + 1))
        [[ ${ran} -le 3 ]] && printf '      %sran:%s %s\n' "${DIM}" "${RESET}" "${s#${WT}/}"
    elif [[ "${id}" == "2023" && "${out}" == *"must be run through the verify runner"* ]]; then
        own=$((own + 1))
    elif [[ "${id}" != "2023" && "${out}" == *"verification suite from E-2023's worktree"* ]]; then
        foreign=$((foreign + 1))
    else
        ran=$((ran + 1))
        [[ ${ran} -le 3 ]] && printf '      %swrong refusal:%s %s → %s\n' \
            "${DIM}" "${RESET}" "${s#${WT}/}" "$(printf '%s' "${out}" | head -1)"
    fi
    # A suite that got as far as its own first check would have printed one.
    [[ "${out}" == *"✓"* || "${out}" == *"✗"* ]] && leaked=$((leaked + 1))
done
assert_eq "no suite in the corpus can be run directly" "0" "${ran}"
if (( foreign > 150 )); then
    report_pass "${foreign} foreign suites refuse on OWNERSHIP, naming both tasks"
else
    report_fail "the foreign corpus refuses on ownership" "more than 150 suites" "${foreign}"
fi
assert_eq "and this worktree's own suite refuses on ISOLATION, naming the runner" "1" "${own}"
assert_eq "none of them executed a check before refusing" "0" "${leaked}"

# ── 16. the suite directory reaches both forms ──────────────────────────────
section "16. \$ENDLESS_VERIFY_DIR, in both suite forms"

P16="$(new_project suitedir)"
add_script "${P16}" "e-1601" '#!/usr/bin/env bash
[[ -n "${ENDLESS_VERIFY_DIR:-}" ]] || exit 30
[[ -f "${ENDLESS_VERIFY_DIR}/companion.txt" ]] || exit 31
[[ "${ENDLESS_VERIFY_TASK:-}" == "E-1601" ]] || exit 32
exit 0
'
printf 'beside me\n' > "${P16}/.endless/tasks/e-1601/companion.txt"
verify_in "${P16}" E-1601; rc16a=$?
assert_eq "a script suite reads a file beside it via \$ENDLESS_VERIFY_DIR" "0" "${rc16a}"

# The same variable on the manifest path — the half E-2092 was filed for, folded
# in here instead (ED-1550: several symptoms of one cause are one task).
add_manifest "${P16}" "e-1602" 'schema = 1
task = "E-1602"
[[check]]
runner = "sh"
command = "sh \"$ENDLESS_VERIFY_DIR\"/probe.sh"
format = "tap"
'
printf '#!/usr/bin/env bash\nprintf "1..1\\nok 1 - reached via ENDLESS_VERIFY_DIR\\n"\n' \
    > "${P16}/.endless/tasks/e-1602/probe.sh"
chmod +x "${P16}/.endless/tasks/e-1602/probe.sh"
verify_in "${P16}" E-1602; rc16b=$?
assert_eq "a manifest check reads one via the same variable" "0" "${rc16b}"
assert_contains "and its result is normalized" "1 passed (1 tests)" "${LAST_OUT}"

# No manifest in this project may hand-write its own suite directory again.
hardcoded=0
for m in "${WT}"/.endless/tasks/e-*/verify.toml; do
    [[ -f "${m}" ]] || continue
    dir="$(basename "$(dirname "${m}")")"
    # Comment lines are excluded: e-1603's manifest explains the mistake it used
    # to make, and quoting the old path in order to warn about it is not making
    # it again.
    if grep -vE '^[[:space:]]*#' "${m}" | grep -qiE "\.endless/tasks/${dir}/"; then
        hardcoded=$((hardcoded + 1))
        printf '      %shard-coded:%s %s\n' "${DIM}" "${RESET}" "${m#${WT}/}"
    fi
done
assert_eq "no manifest hand-writes its own directory" "0" "${hardcoded}"

# ── 17. the product verb is sufficient by itself ────────────────────────────
section "17. endless task verify needs no cd and no id"

probe_out="$(cd "${WT}" && uv run python -c "
from pathlib import Path
from endless import verify_cmd as v
print(v._cwd_task_id(Path('/p/.endless/worktrees/e-1889/src')))
print(v._cwd_task_id(Path('/p/src')))
print(v._main_checkout(Path('/p/.endless/worktrees/e-1889/src')))
" 2>&1)" || setup_error "resolution probe failed: ${probe_out}"
assert_eq "cwd inside a worktree names its task, and elsewhere names none" \
    "1889
None
/p" "${probe_out}"

# The suite has to run against the CANDIDATE tree, so the wrapper picks the
# task's worktree rather than trusting whatever directory the caller stood in.
rundir_out="$(cd "${WT}" && uv run python -c "
from endless import verify_cmd as v
print(v._run_dir(2023))
" 2>&1)" || setup_error "run-dir probe failed: ${rundir_out}"
assert_eq "and the suite runs in that task's worktree" "${WT}" "${rundir_out}"

summary
