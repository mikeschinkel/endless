#!/usr/bin/env bash
#
# E-1947 verification — `endless worktree drop` refuses a worktree that is
# still in use, using the SAME implementation the reaper uses.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1947-verify.sh
#
# WHAT LANDED
#   drop removed the directory a live process was sitting in, orphaning that
#   session's cwd — recovered only by mkdir'ing the directory back. It checked
#   foreign state and uncommitted changes; it did not check whether anything was
#   USING the worktree. The reaper had two guards for exactly that, and drop had
#   neither.
#
#   The two guards are complementary and both are required:
#     (a) a non-ended `sessions` row with task_id for the task — catches a
#         bound Claude session whose cwd has stepped OUT of the directory, which
#         no process probe can see;
#     (b) a process holding cwd inside the directory — catches anything standing
#         in it, Claude or not, which the sessions table cannot see.
#
#   They are NOT reimplemented in Python. `monitor.WorktreeInUse` is the one
#   implementation; the reaper calls it directly and drop reaches it through a
#   new `endless-go worktree in-use` verb. Two copies of a predicate that gates
#   `rm -rf`, in two languages, is the shape of the failure that already cost
#   two-plus weeks.
#
# FOUND WHILE FIXING IT (in scope, same defect)
#   `hasLiveProcessInDir` — guard (b), the one the reaper already "had" — never
#   fired. `lsof -d cwd +D <dir>` is missing `-a`, so lsof ORs the criteria and
#   matches every process on the machine; and the code read lsof's exit status,
#   which is 1 both when nothing matched AND when the +D walk hit an unstattable
#   entry (the normal case on a real worktree). The two mistakes cancelled into a
#   probe that always answered "nothing here". Adopting that guard as-written
#   would have shipped a refusal that never refuses. Part 2 pins the fix.
#
# DELIBERATELY NOT HERE
#   drop's docstring promises an unlanded refusal it never performs. E-1902 owns
#   replacing it with warn+snapshot+proceed; nothing here touches it.
#
# Layers:
#   0. FAIL-FAST — build, vet, and the unit suites: the extracted guard, the
#      reaper suite UNCHANGED (the proof the extraction was behavior-preserving),
#      drop end-to-end against the real binary, --db threading, and the handoff
#      guidance. If these break, stop.
#   1. The real verb against a real process: refuses, and lets an idle dir go.
#   2. The real lsof probe sees a real process (the regression guard for the
#      defect above).
#   3. The verb answers from the DB it was POINTED at, not whatever it found.
#   4. Single implementation: the session query exists once in the tree, and
#      worktree_cmd.py contains no lsof and no sessions query.
#   5. The guidance half: reset-versus-drop in the guide AND in every handoff.
#   6. Project-wide regression.
#
# What this suite does NOT do: run any other task's verify script. Those are
# pre-land gates for their own task in their own worktree, not a regression
# suite.
#
# Exit 0 all-passed, 1 any failure, 2 setup error.
#
# Model: tests/tasks/e-1962-verify.sh.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

REPO_ROOT=""
SCRATCH=""
BUSY_DIR=""
IDLE_DIR=""
SLEEPER_PID=""

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; YELLOW=$'\033[33m'
    DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; YELLOW=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s•%s %s\n' "${DIM}" "${RESET}" "$1"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

report_skip() {
    printf '  %s−%s %s %s(%s)%s\n' "${YELLOW}" "${RESET}" "$1" "${DIM}" "$2" "${RESET}"
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${output: -400}"
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

assert_text_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains '${needle}'" "${haystack:0:300}"
}

assert_text_lacks() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output does NOT contain '${needle}'" "${haystack:0:300}"
}

# ─── fixtures ───────────────────────────────────────────────────────────────

cleanup() {
    if [[ -n "${SLEEPER_PID}" ]]; then
        kill "${SLEEPER_PID}" 2>/dev/null
        wait "${SLEEPER_PID}" 2>/dev/null
    fi
    [[ -n "${SCRATCH}" && -d "${SCRATCH}" ]] && rm -rf "${SCRATCH}"
    return 0
}

# in_use DIR TASK [CONFIG_DIR] -> prints stdout; returns the verb's exit code.
in_use() {
    local dir="$1" task="$2" cfg="${3:-}"
    if [[ -n "${cfg}" ]]; then
        ./bin/endless-go --config-dir "${cfg}" worktree in-use \
            --dir "${dir}" --task "${task}" 2>/dev/null
    else
        ./bin/endless-go worktree in-use --dir "${dir}" --task "${task}" 2>/dev/null
    fi
}

# ─── Part 0: build + unit suites (fail-fast) ────────────────────────────────

test_build_and_suites() {
    section "Part 0 — build + unit suites (fail-fast)"

    # `just build`, NOT `go build ./...`: the latter compiles and discards,
    # leaving bin/endless-go stale. Every live part below drives that binary,
    # and a stale binary passing looks exactly like the change working.
    assert_succeeds "just build (refreshes bin/endless-go)" just build
    assert_succeeds "go vet ./internal/monitor/... ./internal/worktreecmd/..." \
        go vet ./internal/monitor/... ./internal/worktreecmd/...

    # The reaper suite is UNCHANGED by this task. It passing is the proof that
    # lifting its two inline checks into WorktreeInUse preserved behavior — if
    # it had needed editing, the extraction would have been wrong.
    assert_succeeds "go test ./internal/monitor/... (extracted guard + reaper suite untouched)" \
        go test ./internal/monitor/...
    assert_succeeds "go test ./internal/templatecmd/... (handoff guidance, spawn == claim)" \
        go test ./internal/templatecmd/...
    assert_succeeds "pytest test_worktree_drop_inuse.py (drop end-to-end, real binary)" \
        uv run pytest tests/test_worktree_drop_inuse.py -q
    assert_succeeds "pytest test_worktree_db_context_threading.py (--db threading)" \
        uv run pytest tests/test_worktree_db_context_threading.py -q

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sFail-fast: the unit contracts are broken; skipping the live parts.%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi
}

# ─── Part 1: the real verb against a real process ───────────────────────────

test_live_verb() {
    section "Part 1 — the real verb, a real process, a real directory"
    note "busy: ${BUSY_DIR}"
    note "idle: ${IDLE_DIR}"

    local out rc
    out=$(in_use "${BUSY_DIR}" 0); rc=$?
    assert_eq "in-use exits 3 for a directory holding a live process's cwd" "3" "${rc}"
    assert_text_contains "and names which guard fired" \
        "a process is holding cwd inside the worktree" "${out}"

    out=$(in_use "${IDLE_DIR}" 0); rc=$?
    assert_eq "in-use exits 0 for an idle directory" "0" "${rc}"
    assert_eq "and says nothing" "" "${out}"
}

# ─── Part 2: the lsof probe itself ──────────────────────────────────────────

test_lsof_probe() {
    section "Part 2 — the live-process probe actually sees the process"
    note "the defect: -d cwd +D without -a ORs the criteria, and lsof exits 1"
    note "even when it DID match — so the guard answered 'nothing here', always"

    if ! command -v lsof >/dev/null 2>&1; then
        report_skip "raw lsof discriminates busy from idle" "lsof not installed"
        return
    fi

    # The exact command the Go probe runs. Stdout is the answer; the exit
    # status is not, and is deliberately ignored here for the same reason.
    local busy idle
    busy=$(lsof -t -a -d cwd +D "${BUSY_DIR}" 2>/dev/null)
    idle=$(lsof -t -a -d cwd +D "${IDLE_DIR}" 2>/dev/null)

    if [[ -n "${busy}" ]]; then
        report_pass "raw \`lsof -t -a -d cwd +D\` reports a pid for the busy dir"
    else
        report_fail "raw \`lsof -t -a -d cwd +D\` reports a pid for the busy dir" \
            "at least one pid" "(empty)"
    fi
    assert_eq "and nothing for the idle dir" "" "${idle}"

    # Without -a the criteria are ORed, so this matches processes that have a
    # cwd anywhere on the machine. That is what made the old probe meaningless.
    local unanchored
    unanchored=$(lsof -d cwd +D "${IDLE_DIR}" 2>/dev/null | wc -l | tr -d ' ')
    if [[ "${unanchored}" -gt 1 ]]; then
        report_pass "dropping -a matches unrelated processes (why -a is load-bearing)"
    else
        report_fail "dropping -a matches unrelated processes (why -a is load-bearing)" \
            "many lines from an idle dir without -a" "${unanchored} line(s)"
    fi
}

# ─── Part 3: the verb answers from the DB it was pointed at ─────────────────

test_db_routing() {
    section "Part 3 — the verb reads the DB it was POINTED at"
    note "the trap: register under PinMainDB, or forget to thread --db, and the"
    note "verb answers from the wrong ledger and reports no live session — a"
    note "guard reading from the wrong database is worse than no guard, because"
    note "a false 'nobody is here' reads as a pass"

    if ! command -v sqlite3 >/dev/null 2>&1; then
        report_skip "same dir + same task, two DBs, two answers" "sqlite3 not installed"
        return
    fi

    local src
    src=$(uv run endless db path --db sandbox 2>/dev/null | tail -1)
    if [[ -z "${src}" || ! -f "${src}" ]]; then
        report_skip "same dir + same task, two DBs, two answers" \
            "no sandbox DB; run \`just dev-sandbox-init\`"
        return
    fi

    # Two config dirs seeded from one VACUUM'd copy of the sandbox schema+rows:
    # identical in every way except the session row inserted into A.
    local cfg_a="${SCRATCH}/cfg-a" cfg_b="${SCRATCH}/cfg-b"
    mkdir -p "${cfg_a}" "${cfg_b}"
    if ! sqlite3 "${src}" "VACUUM INTO '${cfg_a}/endless.db'" 2>/dev/null; then
        report_skip "same dir + same task, two DBs, two answers" "cannot copy the sandbox DB"
        return
    fi
    cp "${cfg_a}/endless.db" "${cfg_b}/endless.db"

    # A synthetic task id, and no tasks row to go with it: the guard COUNTS
    # session rows, so what it needs is a row bound to the id, not a task. That
    # also keeps this part working against an empty sandbox. sqlite3 leaves
    # foreign_keys OFF unless asked, so the bare insert stands.
    local task=999947
    if ! sqlite3 "${cfg_a}/endless.db" \
        "DELETE FROM sessions;
         INSERT INTO sessions (session_id, platform, state, task_id,
                               started_at, last_activity)
         VALUES ('e1947-probe', 'claude', 'working', ${task},
                 datetime('now'), datetime('now'))" 2>/dev/null
    then
        report_skip "same dir + same task, two DBs, two answers" "cannot seed the session row"
        return
    fi
    sqlite3 "${cfg_b}/endless.db" "DELETE FROM sessions" 2>/dev/null

    # Same IDLE directory, same task id — only the database differs.
    local rc
    in_use "${IDLE_DIR}" "${task}" "${cfg_a}" >/dev/null; rc=$?
    assert_eq "DB with a working session on the task → exit 3" "3" "${rc}"

    local out
    out=$(in_use "${IDLE_DIR}" "${task}" "${cfg_a}")
    assert_text_contains "and names the session guard" \
        "a live session has this task active" "${out}"

    in_use "${IDLE_DIR}" "${task}" "${cfg_b}" >/dev/null; rc=$?
    assert_eq "DB with no sessions → exit 0 (same dir, same task)" "0" "${rc}"

    # Guard (a) is the half lsof cannot do, and this is the case that proves it:
    # nothing is standing in IDLE_DIR, and it still refuses.
    sqlite3 "${cfg_a}/endless.db" \
        "UPDATE sessions SET state = 'ended'" 2>/dev/null
    in_use "${IDLE_DIR}" "${task}" "${cfg_a}" >/dev/null; rc=$?
    assert_eq "an ENDED session does not block (state != 'ended' is the predicate)" "0" "${rc}"
}

# ─── Part 4: one implementation, not two ────────────────────────────────────

test_single_implementation() {
    section "Part 4 — one implementation of 'is this worktree in use'"
    note "this is the check that keeps the duplication from growing back"

    # Scoped to Go on purpose. src/endless/task_cmd.py has three copies of the
    # same SQL asking a DIFFERENT question — "which live session OWNS this
    # task", for claim/release/spawn, returning session ids rather than a
    # yes/no about a directory. Folding those in is not this task's job, and
    # counting them here would make this assertion unfalsifiable noise.
    local hits
    hits=$(grep -rn --include='*.go' \
        "task_id = ? AND state != 'ended'" . 2>/dev/null \
        | grep -v '/vendor/' | grep -v '_test.go' | wc -l | tr -d ' ')
    assert_eq "the worktree-safety session query appears exactly once in Go" "1" "${hits}"

    local home
    home=$(grep -rln --include='*.go' "task_id = ? AND state != 'ended'" . 2>/dev/null \
        | grep -v '/vendor/' | grep -v '_test.go' | head -1)
    assert_eq "and it lives in the shared guard" \
        "./internal/monitor/worktree_inuse.go" "${home}"

    # An `lsof` INVOCATION, not the word — the module's own comments say not to
    # add one, and an assertion that forbids saying so forbids the warning.
    local invocations
    invocations=$(grep -cE "['\"]lsof['\"]" src/endless/worktree_cmd.py 2>/dev/null)
    assert_eq "worktree_cmd.py runs no lsof of its own" "0" "${invocations}"

    # The SQL predicate, not the bare column name: E-1969 renamed the column to
    # `task_id`, which is a substring of half the identifiers in this module.
    local py
    py=$(cat src/endless/worktree_cmd.py)
    assert_text_lacks "worktree_cmd.py queries no sessions table of its own" \
        "FROM sessions" "${py}"
    assert_text_contains "it shells to the shared verb instead" \
        '"worktree", "in-use"' "${py}"

    # The reaper must be a CALLER, not a second copy.
    local reap
    reap=$(cat internal/monitor/reap_worktrees.go)
    assert_text_contains "the reaper calls the shared guard" \
        "WorktreeInUse(db, dir, taskID)" "${reap}"
}

# ─── Part 5: the guidance half ──────────────────────────────────────────────

test_guidance() {
    section "Part 5 — the guidance: reset/rebase in place, not drop"
    note "a gate stops the damage but not the ADVICE; an agent blocked by a"
    note "guard still needs to know what the correct move was"

    local guide
    guide=$(cat docs/guide/orchestration.md)
    assert_text_contains "the guide names the correct move for diverged history" \
        "Diverged history is a reset, never a drop" "${guide}"
    assert_text_contains "the guide shows rebase in place" \
        "git -C <worktree> rebase main" "${guide}"
    assert_text_contains "the guide says what drop actually deletes" \
        "deletes that session's cwd" "${guide}"

    # And the same sentence reaches a spawned session, in every handoff type.
    local vars typ out
    for typ in todo bugfix epic research brainstorm; do
        vars=$(printf '{"spawned_id":"9999","title":"T","label_prefix":"E-9999","worktree_path":"/tmp/wt/e-9999","branch":"task/9999","task_type":"%s"}' "${typ}")
        out=$(printf '%s' "${vars}" | ./bin/endless-go template render "handoff/${typ}" 2>&1)
        assert_text_contains "handoff/${typ} carries the reset-versus-drop rule" \
            'the fix is `git rebase main` or `git reset --hard main` **in place**' "${out}"
    done
    out=$(printf '%s' "${vars}" | ./bin/endless-go template render handoff/claim 2>&1)
    assert_text_contains "handoff/claim carries it too (spawn and claim agree)" \
        'the fix is `git rebase main` or `git reset --hard main` **in place**' "${out}"
}

# ─── Part 6: project-wide regression ────────────────────────────────────────

test_regression() {
    section "Part 6 — project-wide regression"
    assert_succeeds "go build ./..." go build ./...
    assert_succeeds "go vet ./..." go vet ./...
    assert_succeeds "go test ./..." go test ./...
    assert_succeeds "just test (full Python suite)" just test
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    SCRATCH=$(mktemp -d) || exit 2
    trap cleanup EXIT

    # Nest the probe targets the way a real worktree is nested — the original
    # reproduction was path-shaped enough that a bare tmpdir hid the defect.
    BUSY_DIR="${SCRATCH}/proj/.endless/worktrees/e-9947"
    IDLE_DIR="${SCRATCH}/proj/.endless/worktrees/e-9948"
    mkdir -p "${BUSY_DIR}" "${IDLE_DIR}" || exit 2

    ( cd "${BUSY_DIR}" && exec sleep 900 ) &
    SLEEPER_PID=$!
    sleep 0.5

    printf '%sE-1947 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:     %s\n' "${REPO_ROOT}"
    printf '  scratch: %s\n' "${SCRATCH}"
    printf '  sleeper: pid %s in %s\n' "${SLEEPER_PID}" "${BUSY_DIR}"

    test_build_and_suites
    test_live_verb
    test_lsof_probe
    test_db_routing
    test_single_implementation
    test_guidance
    test_regression

    summary
}

main "$@"
