#!/usr/bin/env bash
#
# E-1714 verification script — proves the E-1586 cwd-outside-worktree gate
# (`enforceClaimedCwd`, internal/hookcmd/claude.go) actually FIRES end-to-end in
# the real `endless-go hook claude` binary, and discriminates correctly.
#
# Why this exists: E-1586 shipped `assumed` (never verified E2E). The E-1712
# incident (a claimed session's edits reaching main) cast doubt on whether the
# gate fires at all. This reproduces the exact preconditions against the REAL
# shipped code path (runClaude -> handlePreToolUse -> enforceClaimedCwd ->
# blockToolUse/os.Exit(2)) and decides a fork:
#   - gate FIRES  -> E-1586 sound; the incident was the absolute-path-to-main
#                    case, i.e. E-1703 is the real gap to build.
#   - gate SILENT -> E-1586 regressed; that is the bug.
#
# Isolation: the hook binary pins to the real ~/.config/endless/endless.db via
# PinMainDB UNLESS `--config-dir <dir>` is passed. We always pass a temp
# --config-dir, so the real ledger and the live session are never touched (the
# "isolate hook verification" rule). The Go binary auto-applies schema.SQL on
# first connect, so the temp DB self-materializes; we then seed rows directly
# (same way the Go tests seed — claude_revisit_test.go).
#
# False-green guards (E-1612 / E-1692): the block assertion requires exit == 2
# AND the SPECIFIC `/cd <worktree>` + `E-<id>` message, so a "binary failed to
# launch" can't masquerade as a gate-fire; every block case is paired with
# pass-through positive controls that prove the gate discriminates rather than
# erroring on everything; and we build + invoke THIS worktree's binary by
# explicit path so no stale global is exercised.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1714-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. Shape follows tests/tasks/e-1577-verify.sh /
# e-1662-verify.sh. Interim location tests/tasks/ (pre-E-1596); migrates to the
# formal .endless/tasks/<id>/ convention via E-1623 once the runner lands.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ───────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

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

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── globals wired in main ────────────────────────────────────────────────────

REPO_ROOT=""
ENDLESS_GO=""
CFG=""            # temp --config-dir (holds endless.db)
PROJ=""           # synthetic registered project root (outside any worktree)
WORKTREE=""       # PROJ/.endless/worktrees/e-<TID>
SESSION_UUID="e1714-verify-11111111-1111-1111-1111-111111111111"
PID=777           # synthetic projects.id
TID=424242        # synthetic tasks.id (synthetic, not the real E-1714)
WORK_TMP=""       # mktemp root, cleaned on exit

cleanup() { [[ -n "${WORK_TMP}" && -d "${WORK_TMP}" ]] && rm -rf "${WORK_TMP}"; }
trap cleanup EXIT

# ─── hook driver ──────────────────────────────────────────────────────────────

# fire_hook CWD TOOL  — fire one PreToolUse hook against the isolated temp DB.
# Emits combined stdout+stderr; caller reads $? for the exit code.
fire_hook() {
    local cwd="$1" tool="$2"
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"PreToolUse","tool_name":"%s","tool_input":{}}' \
        "${SESSION_UUID}" "${cwd}" "${tool}" \
        | "${ENDLESS_GO}" --config-dir "${CFG}" hook claude 2>&1
}

# db EXEC...  — run a SQL statement against the temp DB.
db() { sqlite3 "${CFG}/endless.db" "$1"; }

# ─── assertions ───────────────────────────────────────────────────────────────

# assert_blocks DESC CWD TOOL — the gate MUST fire: exit==2 AND the specific
# /cd <worktree> + E-<id> directive (specific match defeats the E-1692 false
# green where a launch failure exits non-zero with unrelated output).
assert_blocks() {
    local desc="$1" cwd="$2" tool="$3"
    local out rc
    out=$(fire_hook "${cwd}" "${tool}"); rc=$?
    if [[ "${rc}" -eq 2 && "${out}" == *"/cd ${WORKTREE}"* && "${out}" == *"E-${TID}"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" \
            "exit==2 AND output has '/cd ${WORKTREE}' AND 'E-${TID}'" \
            "exit=${rc} | out=${out}"
    fi
}

# assert_passes DESC CWD TOOL — the gate must NOT fire: clean exit 0 and no /cd
# directive. Positive control / tripwire (E-1612): proves the gate discriminates
# on its keying signal rather than blocking (or crashing) unconditionally.
assert_passes() {
    local desc="$1" cwd="$2" tool="$3"
    local out rc
    out=$(fire_hook "${cwd}" "${tool}"); rc=$?
    if [[ "${rc}" -eq 0 && "${out}" != *"/cd "* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" \
            "exit==0 AND no '/cd ' directive in output" \
            "exit=${rc} | out=${out}"
    fi
}

# ─── fixture ──────────────────────────────────────────────────────────────────

seed_fixture() {
    section "Fixture — isolated temp DB, registered project, claimed session"

    WORK_TMP=$(mktemp -d)
    CFG="${WORK_TMP}/config"
    PROJ="${WORK_TMP}/proj"
    WORKTREE="${PROJ}/.endless/worktrees/e-${TID}"
    mkdir -p "${CFG}" "${PROJ}" "${WORK_TMP}/prime" "${WORKTREE}"

    # Prime: force schema creation into the temp DB (neutral cwd so PROJ is not
    # auto-registered as a side effect — we register it explicitly below). Uses a
    # THROWAWAY session id: the per-event TouchSession UPSERT would otherwise
    # insert a row for SESSION_UUID and collide with our explicit seed INSERT.
    printf '{"session_id":"e1714-prime","cwd":"%s","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{}}' \
        "${WORK_TMP}/prime" \
        | "${ENDLESS_GO}" --config-dir "${CFG}" hook claude >/dev/null 2>&1
    if [[ ! -f "${CFG}/endless.db" ]]; then
        printf 'ERROR: temp DB not created at %s (priming hook failed)\n' \
            "${CFG}/endless.db" >&2
        exit 2
    fi
    report_pass "temp DB materialized under --config-dir (real ledger untouched)"

    # Seed: registered project -> non-terminal claimed task -> session bound to it.
    db "INSERT INTO projects (id, name, path, status) VALUES (${PID}, 'e1714demo', '${PROJ}', 'active');"
    db "INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (${TID}, ${PID}, 'e1714 demo task', 'underway', 1);"
    db "INSERT INTO sessions (session_id, project_id, active_task_id, state, kind_id) VALUES ('${SESSION_UUID}', ${PID}, ${TID}, 'working', 1);"

    local seeded
    seeded=$(db "SELECT s.state || '/' || t.status FROM sessions s JOIN tasks t ON t.id=s.active_task_id WHERE s.session_id='${SESSION_UUID}';")
    if [[ "${seeded}" == "working/underway" ]]; then
        report_pass "seeded session -> non-terminal task (working/underway)"
    else
        report_fail "seed session/task" "working/underway" "${seeded}"
        exit 2
    fi
}

# ─── checks ───────────────────────────────────────────────────────────────────

test_gate_fires() {
    section "Gate FIRES — cwd outside the claimed worktree, every tool refused"

    # cwd == project root (main checkout), the exact E-1712 drift condition.
    assert_blocks "Write refused with /cd directive (cwd in main)"  "${PROJ}" "Write"
    # Runs before the write-tools filter -> Bash is refused too, not just writes.
    assert_blocks "Bash refused with /cd directive (all tools, not just writes)" "${PROJ}" "Bash"
    # A sibling worktree dir sharing a name prefix (still inside the registered
    # project, so the gate evaluates) must count as OUTSIDE the claimed worktree
    # — the pathWithin prefix-sibling guard, exercised through the real binary.
    local sibling="${PROJ}/.endless/worktrees/e-${TID}-sibling"
    mkdir -p "${sibling}"
    assert_blocks "prefix-sibling worktree counts as outside the claimed worktree" "${sibling}" "Bash"
}

test_gate_discriminates() {
    section "Positive controls — gate PASSES when its keying signal is absent"

    # cwd inside the worktree (and a descendant) — the invariant holds.
    assert_passes "cwd == worktree root: not blocked"           "${WORKTREE}"        "Bash"
    mkdir -p "${WORKTREE}/internal/monitor"
    assert_passes "cwd descendant of worktree: not blocked"     "${WORKTREE}/internal/monitor" "Bash"

    # Worktree directory absent -> WorktreePathForTask returns "" -> no anchor.
    rm -rf "${WORKTREE}"
    assert_passes "worktree dir absent: not blocked (worktree keying)" "${PROJ}" "Bash"
    mkdir -p "${WORKTREE}"   # restore for the next isolated control

    # Terminal task status -> gate ignores (bind/landed cases).
    db "UPDATE tasks SET status='confirmed' WHERE id=${TID};"
    assert_passes "terminal task status: not blocked (status keying)" "${PROJ}" "Bash"
    db "UPDATE tasks SET status='underway' WHERE id=${TID};"   # restore

    # No active task on the session -> gate no-op.
    db "UPDATE sessions SET active_task_id=NULL WHERE session_id='${SESSION_UUID}';"
    assert_passes "session has no active task: not blocked" "${PROJ}" "Bash"
    db "UPDATE sessions SET active_task_id=${TID} WHERE session_id='${SESSION_UUID}';"   # restore
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    command -v sqlite3 >/dev/null 2>&1 || { printf 'ERROR: sqlite3 not on PATH\n' >&2; exit 2; }

    # Build + require THIS worktree's binary (stale-binary tripwire, E-1612).
    if ! go build -o "${REPO_ROOT}/bin/endless-go" ./cmd/endless-go 2>/tmp/e1714-build.err; then
        printf 'ERROR: go build failed:\n%s\n' "$(cat /tmp/e1714-build.err)" >&2
        exit 2
    fi
    ENDLESS_GO="${REPO_ROOT}/bin/endless-go"
    [[ -x "${ENDLESS_GO}" ]] || { printf 'ERROR: %s not executable\n' "${ENDLESS_GO}" >&2; exit 2; }

    printf '%sE-1714 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n  binary: %s\n' "${REPO_ROOT}" "${ENDLESS_GO}"

    seed_fixture
    test_gate_fires
    test_gate_discriminates

    summary
}

main "$@"
