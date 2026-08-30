#!/usr/bin/env bash
#
# E-1791 verification — the RunnerDriver Go interface + pytest/uv driver.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-1791-verify.sh
#
# Fail-fast: the Go unit suite for the driver seam (internal/verify +
# internal/verifycmd) is the primary gate — it covers name parsing, driver
# dispatch, the gotest/generic/pytest drivers, launcher resolution, and that
# RenderRunScript still emits a bare-clone script. If it fails, this script stops
# before the slower end-to-end scenarios.
#
# End-to-end (through the real `endless-go verify`): one suite composing three
# checks — gotest, pytest/uv, and shell/tap — runs under the runner's isolated
# HOME/XDG, proving the pytest/uv driver resolves the project venv's plain pytest
# (isolation-robust, no PATH `pytest` needed — the original E-1605 defect) and
# that gotest + shell/tap still normalize and merge unchanged. A second scenario
# with a failing pytest proves failures still surface as a non-zero exit.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)"
BIN="${WT}/bin/endless-go"

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

# ─── setup: fresh binary + synced venv ──────────────────────────────────────

section "Setup — build endless-go, sync dev venv"
( cd "${WT}" && go build -o bin/endless-go ./cmd/endless-go ) || die "go build failed"
( cd "${WT}" && uv sync >/dev/null 2>&1 ) || die "uv sync failed"
[[ -x "${WT}/.venv/bin/pytest" ]] || die "project venv pytest missing after uv sync"
printf '  built %s\n  venv pytest present\n' "${BIN}"

# ─── fail-fast gate: the Go driver-seam unit suite ──────────────────────────

section "Fail-fast — Go unit suite (driver seam, parsing, drivers, runscript)"
if ( cd "${WT}" && go test ./internal/verify/... ./internal/verifycmd/... ); then
    report_pass "internal/verify + internal/verifycmd unit tests pass"
else
    report_fail "Go unit suite" "all tests pass" "one or more tests FAILED (see above)"
    printf '\n%sFAIL-FAST:%s driver-seam unit suite failed; skipping end-to-end.\n' "${RED}${BOLD}" "${RESET}"
    exit 1
fi

# ─── end-to-end scenario builder ────────────────────────────────────────────

# make_project ID PYTEST_BODY -> prints the temp project root. Symlinks the
# worktree's synced .venv so the pytest/uv driver resolves .venv/bin/pytest
# offline; writes a go module, a pytest file, and a 3-check verify.toml.
make_project() {
    local id="$1" pybody="$2" root
    root="$(mktemp -d "/tmp/e1791-${id}.XXXXXX")" || die "mktemp failed"
    ln -s "${WT}/.venv" "${root}/.venv"
    printf 'module e1791verify\n\ngo 1.21\n' > "${root}/go.mod"
    printf 'package e1791verify\n\nimport "testing"\n\nfunc TestOK(t *testing.T) {}\n' > "${root}/ok_test.go"
    printf '%s' "${pybody}" > "${root}/test_sample.py"
    mkdir -p "${root}/.endless/tasks/${id}"
    cat > "${root}/.endless/tasks/${id}/verify.toml" <<EOF
schema = 1
task   = "${id}"

[[check]]
runner = "gotest"
paths  = ["./..."]

[[check]]
runner = "pytest/uv"
paths  = ["test_sample.py"]

[[check]]
runner  = "shell"
command = "printf '1..1\\nok 1 shell\\n'"
format  = "tap"
EOF
    printf '%s' "${root}"
}

run_verify() { # ROOT ID -> echoes output; returns endless-go exit code
    local root="$1" id="$2"
    ( cd "${root}" && "${BIN}" verify "${id}" 2>&1 )
}

# ─── scenario 1: passing suite (gotest + pytest/uv + shell) ──────────────────

section "E2E — passing suite merges gotest + pytest/uv + shell/tap"
ROOT="$(make_project E-PYUVPASS $'def test_a():\n    assert True\n\ndef test_b():\n    assert 1 + 1 == 2\n')"
OUT="$(run_verify "${ROOT}" E-PYUVPASS)"; RC=$?

if [[ "${RC}" -eq 0 ]]; then
    report_pass "passing suite exits 0"
else
    report_fail "passing suite exit code" "0" "exit=${RC} | ${OUT}"
fi
if [[ "${OUT}" == *"PASSED: 4 passed (4 tests)"* ]]; then
    report_pass "merged report sums all three checks (4 passed / 4 tests)"
else
    report_fail "merged verdict" "PASSED: 4 passed (4 tests)" "${OUT}"
fi
if [[ "${OUT}" == *"pytest/uv"*"2 passed"* ]]; then
    report_pass "pytest/uv driver ran real pytest and normalized to CTRF"
else
    report_fail "pytest/uv check line" "pytest/uv ... 2 passed" "${OUT}"
fi
if [[ "${OUT}" == *"gotest"*"1 passed"* ]] && [[ "${OUT}" == *"shell"*"1 passed"* ]]; then
    report_pass "gotest and shell/tap checks unchanged (regression)"
else
    report_fail "gotest + shell regression" "both show 1 passed" "${OUT}"
fi
rm -rf "${ROOT}" ~/.cache/endless/verify/E-PYUVPASS

# ─── scenario 2: failing pytest surfaces as non-zero exit ────────────────────

section "E2E — a failing pytest still fails the run"
ROOT="$(make_project E-PYUVFAIL $'def test_a():\n    assert True\n\ndef test_b():\n    assert False\n')"
OUT="$(run_verify "${ROOT}" E-PYUVFAIL)"; RC=$?

if [[ "${RC}" -ne 0 ]]; then
    report_pass "failing suite exits non-zero (exit=${RC})"
else
    report_fail "failing suite exit code" "non-zero" "exit=0 | ${OUT}"
fi
if [[ "${OUT}" == *"FAILED:"*"1 failed"* ]]; then
    report_pass "merged verdict reports the pytest failure"
else
    report_fail "merged verdict" "FAILED: ... 1 failed" "${OUT}"
fi
rm -rf "${ROOT}" ~/.cache/endless/verify/E-PYUVFAIL

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
