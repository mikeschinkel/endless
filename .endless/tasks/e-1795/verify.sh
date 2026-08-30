#!/usr/bin/env bash
#
# E-1795 verification script — surface UPSTREAM blocker chains in session status.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1795-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# WHAT this proves: `session status` now walks each shown task's blocked_by chain
# TRANSITIVELY, so a chain head several hops up (e.g. an unplanned epic) surfaces
# wherever its downstream chain already shows. The walk stops at a terminal
# (released, per E-876) blocker: prerequisites BEYOND a done gate are NOT pulled
# in. Children stay one-hop (fan-out); blockers walk the full chain (narrow).
#
# WHY this shape: the full SQL contract (transitive walk, terminal-gate stop,
# per-row blocked-by/blocks counts, terminal-status filter) lives in the Go unit
# suite (internal/monitor). This script runs that suite FIRST as a fail-fast gate,
# then drives the REAL rendered CLI end-to-end against the per-worktree sandbox DB
# to prove the chain actually reaches the pane.
#
# Modeled on tests/tasks/e-1797-verify.sh.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
}

note() {
    printf '  %s· %s%s\n' "${DIM}" "$1" "${RESET}"
}

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    local desc="$1" expected="$2" actual="$3"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "${expected}"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "${actual}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("${desc}")
}

summary() {
    printf '\n%sSummary%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── helpers ──────────────────────────────────────────────────────────────────

# add_task TITLE → echoes the created task's numeric id (E-NNN stripped to NNN).
add_task() {
    uv run endless task add "$1" --db sandbox 2>&1 | grep -oE 'E-[0-9]+' | head -1 | tr -d 'E-'
}

# ─── checks ───────────────────────────────────────────────────────────────────

# Fail-fast gate: the whole SQL contract lives in the unit suite. If it fails,
# stop — the live CLI check below only proves the wiring reaches the pane.
test_unit_contract() {
    section "Unit contract — transitive upstream walk + terminal-gate stop"
    note "internal/monitor SessionStatusRows suite: the recursive blocked_by walk,"
    note "the E-876 terminal-blocker stop, and the per-row block counts"
    local output rc
    output=$(go test ./internal/monitor/ -run TestSessionStatusRows -count=1 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "go test ./internal/monitor -run TestSessionStatusRows — all pass"
        return 0
    fi
    report_fail "go test ./internal/monitor -run TestSessionStatusRows — all pass" \
        "go test exit == 0" "exit=${rc}"
    printf '%s\n' "${output}" | tail -30
    printf '\n%sABORTING — unit contract failed; skipping live CLI check.%s\n' \
        "${RED}${BOLD}" "${RESET}"
    summary
    exit 1
}

# Live end-to-end: seed a real blocker chain in the sandbox and assert the
# rendered CLI surfaces the transitive head while stopping at a terminal gate.
#
#   focal ← mid ← head ← [doneGate] ← ghost
#           (F is blocked by M, M by H, H by the terminal gate G, G by X)
#
# Expected in `session-status --task F`: M and H present (transitive), G omitted
# (terminal), X absent (beyond the released gate).
test_live_chain() {
    section "Live CLI — rendered chain reaches the pane (sandbox DB)"

    local f m h g x
    f=$(add_task "Verify e1795 verify focal")
    m=$(add_task "Build e1795 verify mid")
    h=$(add_task "Design e1795 verify head")
    g=$(add_task "Confirm e1795 verify gate")
    x=$(add_task "Test e1795 verify ghost")
    if [[ -z "${f}" || -z "${m}" || -z "${h}" || -z "${g}" || -z "${x}" ]]; then
        report_fail "seed sandbox chain" "5 task ids" \
            "f=${f} m=${m} h=${h} g=${g} x=${x}"
        return
    fi
    note "seeded F=E-${f} M=E-${m} H=E-${h} gate=E-${g} ghost=E-${x}"

    uv run endless task block "E-${f}" --by "E-${m}" --db sandbox >/dev/null 2>&1
    uv run endless task block "E-${m}" --by "E-${h}" --db sandbox >/dev/null 2>&1
    uv run endless task block "E-${h}" --by "E-${g}" --db sandbox >/dev/null 2>&1
    uv run endless task block "E-${g}" --by "E-${x}" --db sandbox >/dev/null 2>&1
    # Gate terminal → the walk must stop there (E-876 release). Focal open.
    uv run endless task update "E-${g}" --status confirmed --db sandbox >/dev/null 2>&1
    uv run endless task update "E-${f}" --status underway --db sandbox >/dev/null 2>&1

    local out
    out=$(./bin/endless-go session-status --task "${f}" 2>&1)

    if grep -qE "E-${m}\b" <<<"${out}"; then
        report_pass "direct blocker E-${m} surfaces"
    else
        report_fail "direct blocker E-${m} surfaces" "output lists E-${m}" "${out}"
    fi
    if grep -qE "E-${h}\b" <<<"${out}"; then
        report_pass "TRANSITIVE chain head E-${h} surfaces (2 hops up)"
    else
        report_fail "TRANSITIVE chain head E-${h} surfaces (2 hops up)" \
            "output lists E-${h}" "${out}"
    fi
    if grep -qE "E-${g}\b" <<<"${out}"; then
        report_fail "terminal gate E-${g} omitted" "output omits E-${g}" "${out}"
    else
        report_pass "terminal gate E-${g} omitted (released, per E-876)"
    fi
    if grep -qE "E-${x}\b" <<<"${out}"; then
        report_fail "ghost E-${x} beyond the gate NOT surfaced" \
            "output omits E-${x}" "${out}"
    else
        report_pass "ghost E-${x} beyond the terminal gate NOT surfaced (walk stops)"
    fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi
    if [[ ! -x ./bin/endless-go ]]; then
        printf 'ERROR: ./bin/endless-go missing — run `just build` first\n' >&2
        exit 2
    fi

    printf '%sE-1795 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (live CLI); in-memory (unit suite)\n'

    test_unit_contract      # fail-fast gate
    test_live_chain

    summary
}

main "$@"
