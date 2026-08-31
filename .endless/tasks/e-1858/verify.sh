#!/usr/bin/env bash
#
# E-1858 verification — the deprecated ".endless/verbs.json" glob is removed from
# BOTH auto-managed mirror lists (the E-1268 migration is complete: verbs.json is
# never produced, only read-and-removed as a legacy straggler).
#
# Run from inside the worktree (esu puts you there):
#   endless task verify E-1858
#
# What it proves:
#   1. FAIL-FAST unit gate: the affected Go + Python unit suites pass.
#        - Go internal/monitor (TestIsAutoManagedPath: verbs.json now unmanaged)
#        - Python tests/test_worktree_land_dedup.py (glob registry)
#          + tests/test_verb_gate.py (legacy migration still works)
#   2. Python AUTO_COMMIT_GLOBS no longer lists ".endless/verbs.json".
#   3. Go AutoManagedStatusGlobs no longer lists ".endless/verbs.json".
#   4. Both mirrors STILL list the live entries (".endless/verbs.jsonl" and the
#      db-ledger glob) — the removal is surgical.
#   5. The two mirrors agree: neither treats the deprecated path as auto-managed.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
PY_SRC="${WT}/src/endless/worktree_cmd.py"
GO_SRC="${WT}/internal/monitor/worktree_anomalies.go"

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

die() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

cd "${WT}" || die "cannot cd to worktree root"
[[ -f "${PY_SRC}" ]] || die "missing ${PY_SRC}"
[[ -f "${GO_SRC}" ]] || die "missing ${GO_SRC}"

# ─── fail-fast unit gate ────────────────────────────────────────────────────

section "Unit gate (fail-fast) — affected Go + Python suites"

if go test ./internal/monitor/ >/tmp/e1858-go.log 2>&1; then
    report_pass "go test ./internal/monitor/ passed"
else
    report_fail "go test ./internal/monitor/" "exit 0" "non-zero (see /tmp/e1858-go.log)"
    tail -20 /tmp/e1858-go.log
    printf '\n%sfail-fast: aborting before content assertions%s\n' "${RED}${BOLD}" "${RESET}"
    exit 1
fi

if uv run pytest tests/test_worktree_land_dedup.py tests/test_verb_gate.py -q \
        >/tmp/e1858-py.log 2>&1; then
    report_pass "pytest test_worktree_land_dedup.py + test_verb_gate.py passed"
else
    report_fail "pytest glob + verb-gate suites" "exit 0" "non-zero (see /tmp/e1858-py.log)"
    tail -20 /tmp/e1858-py.log
    printf '\n%sfail-fast: aborting before content assertions%s\n' "${RED}${BOLD}" "${RESET}"
    exit 1
fi

# ─── the deprecated glob is gone from both mirrors ──────────────────────────

section "Removal — deprecated verbs.json glob dropped from both mirrors"

# Python: introspect the actual tuple, not the source text.
PY_HAS_LEGACY="$(uv run python -c \
    'from endless.worktree_cmd import AUTO_COMMIT_GLOBS as g; print(".endless/verbs.json" in g)' \
    2>/tmp/e1858-pyimp.log)"
if [[ "${PY_HAS_LEGACY}" == "False" ]]; then
    report_pass "Python AUTO_COMMIT_GLOBS excludes '.endless/verbs.json'"
elif [[ "${PY_HAS_LEGACY}" == "True" ]]; then
    report_fail "Python glob removal" "'.endless/verbs.json' absent" "still present in AUTO_COMMIT_GLOBS"
else
    report_fail "Python glob removal" "importable AUTO_COMMIT_GLOBS" "import failed (see /tmp/e1858-pyimp.log)"
fi

# Go: the exact deprecated entry must not appear as an element in the list.
if grep -qE '^\s*"\.endless/verbs\.json"\s*,?\s*$' "${GO_SRC}"; then
    report_fail "Go glob removal" "'.endless/verbs.json' absent" "still listed in AutoManagedStatusGlobs"
else
    report_pass "Go AutoManagedStatusGlobs excludes '.endless/verbs.json'"
fi

# ─── the live entries survive ───────────────────────────────────────────────

section "Surgical — live entries retained in both mirrors"

PY_HAS_JSONL="$(uv run python -c \
    'from endless.worktree_cmd import AUTO_COMMIT_GLOBS as g; print(".endless/verbs.jsonl" in g and ".endless/db-ledger/*.jsonl" in g)' \
    2>>/tmp/e1858-pyimp.log)"
if [[ "${PY_HAS_JSONL}" == "True" ]]; then
    report_pass "Python AUTO_COMMIT_GLOBS keeps verbs.jsonl + db-ledger globs"
else
    report_fail "Python live entries" "verbs.jsonl AND db-ledger present" "one or both missing (${PY_HAS_JSONL})"
fi

if grep -qE '^\s*"\.endless/verbs\.jsonl"\s*,' "${GO_SRC}" \
   && grep -qE '^\s*"\.endless/db-ledger/\*\.jsonl"\s*,' "${GO_SRC}"; then
    report_pass "Go AutoManagedStatusGlobs keeps verbs.jsonl + db-ledger globs"
else
    report_fail "Go live entries" "verbs.jsonl AND db-ledger present" "one or both missing"
fi

# ─── behavior: the deprecated path is unmanaged ─────────────────────────────

section "Behavior — verbs.json now matches no auto-managed glob"

# The Go side is covered authoritatively by TestIsAutoManagedPath in the unit
# gate above. Cross-check the Python mirror's runtime behavior here: fnmatch the
# real tuple, proving a straggler verbs.json is no longer silently swept up.
PY_UNMANAGED="$(uv run python -c 'import fnmatch
from endless.worktree_cmd import AUTO_COMMIT_GLOBS as g
print(not any(fnmatch.fnmatch(".endless/verbs.json", p) for p in g))' \
    2>>/tmp/e1858-pyimp.log)"
if [[ "${PY_UNMANAGED}" == "True" ]]; then
    report_pass "verbs.json matches no glob in AUTO_COMMIT_GLOBS (runtime cross-check)"
else
    report_fail "glob semantics cross-check" "True (no match)" "${PY_UNMANAGED:-eval failed}"
fi

# ─── summary ────────────────────────────────────────────────────────────────

printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
if [[ "${FAIL_COUNT}" -eq 0 ]]; then
    printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
    exit 0
fi
printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
printf '\n'
exit 1
