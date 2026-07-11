#!/usr/bin/env bash
#
# E-1603 verification script — exercises the Tier-0 verification runner
# (`endless task verify` / `endless-go verify`) end-to-end.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1603-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. It builds the worktree's endless-go, runs the Go
# unit tests for the
# runner and its manifest/normalizer deps, then drives the built binary against
# throwaway fixture suites for: a passing suite (exit 0 + CTRF written + summary),
# a failing suite (non-zero + failure detail), HOME/XDG isolation, two concurrent
# runs, --keep vs default temp-dir cleanup, and the Tier-0 needs/seed guards.
#
# Interim ad-hoc location tests/tasks/ (matches the E-1577 prototype); migrates
# to .endless/tasks/<id>/ once the system it verifies lands.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

REPO_ROOT=""
BIN=""
WORK=""          # throwaway fixtures workspace (removed on exit)
REPO_GT_SUITE=""  # in-repo gotest suite dir (removed on exit)

# ─── output ─────────────────────────────────────────────────────────────────

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
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]  — pass if CMD exits 0.
assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${out}"
}

# assert_fails DESC CMD [ARGS...]  — pass if CMD exits non-zero.
assert_fails() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit != 0" "exit=0 | ${out}"
}

# assert_fails_with DESC PATTERN CMD [ARGS...]  — non-zero AND output has PATTERN.
assert_fails_with() {
    local desc="$1"; local pat="$2"; shift 2
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -ne 0 && "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit != 0 AND output contains: ${pat}" "exit=${rc} | ${out}"
}

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1"; local pat="$2"; shift 2
    local out; out=$("$@" 2>&1)
    if [[ "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pat}" "${out}"
}

# assert_true DESC MESSAGE  — pass if MESSAGE (a prior check) already set rc via [[ ]].
# Convenience for filesystem assertions: caller passes an evaluated condition.
assert_path_exists() {
    local desc="$1"; local path="$2"
    if [[ -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "path exists: ${path}" "missing"
}

assert_path_absent() {
    local desc="$1"; local path="$2"
    if [[ ! -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "path absent: ${path}" "still present"
}

# ─── fixtures ───────────────────────────────────────────────────────────────

# make_suite DIR ID  — create <DIR>/.endless/tasks/<ID>/verify.toml from stdin.
make_suite() {
    local dir="$1"; local id="$2"
    mkdir -p "${dir}/.endless/tasks/${id}"
    cat > "${dir}/.endless/tasks/${id}/verify.toml"
}

# verify_in DIR ID [FLAGS...]  — run the built runner from DIR against ID.
verify_in() {
    local dir="$1"; local id="$2"; shift 2
    ( cd "${dir}" && "${BIN}" verify "$@" "${id}" )
}

# cache_dir  — the OS user cache dir where the runner writes CTRF (matches Go's
# os.UserCacheDir): macOS ~/Library/Caches, else $XDG_CACHE_HOME or ~/.cache.
cache_dir() {
    if [[ "$(uname)" == "Darwin" ]]; then
        printf '%s/Library/Caches\n' "${HOME}"
    else
        printf '%s\n' "${XDG_CACHE_HOME:-${HOME}/.cache}"
    fi
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_go_unit_tests() {
    section "Go unit tests — runner + manifest/normalizer deps"
    assert_succeeds "go test ./internal/verify/... ./internal/verifycmd/..." \
        go test ./internal/verify/... ./internal/verifycmd/...
}

check_passing_suite() {
    section "Passing suite — exit 0, CTRF written, summary shown"
    local id="E-1603V-pass"
    make_suite "${WORK}" "${id}" <<'EOF'
schema = 1
task   = "E-1603V-pass"
[[check]]
runner  = "bats"
command = "printf '1..2\nok 1 alpha\nok 2 beta\n'"
format  = "tap"
EOF
    assert_succeeds "passing suite exits 0" verify_in "${WORK}" "${id}"
    assert_contains "prints PASSED summary" "PASSED:" verify_in "${WORK}" "${id}"
    assert_contains "summary names the task" "verify ${id}" verify_in "${WORK}" "${id}"

    local ctrf="$(cache_dir)/endless/verify/${id}/ctrf.json"
    assert_path_exists "CTRF report written to cache" "${ctrf}"
    assert_contains "CTRF names the tool 'endless'" '"name": "endless"' cat "${ctrf}"
}

check_failing_suite() {
    section "Failing suite — non-zero exit, failure detail surfaced"
    local id="E-1603V-fail"
    make_suite "${WORK}" "${id}" <<'EOF'
schema = 1
task   = "E-1603V-fail"
[[check]]
runner  = "bats"
command = "printf '1..2\nok 1 good\nnot ok 2 bad\n'; exit 1"
format  = "tap"
EOF
    assert_fails "failing suite exits non-zero" verify_in "${WORK}" "${id}"
    assert_contains "prints FAILED summary with a failing count" "1 failed" verify_in "${WORK}" "${id}"
}

check_isolation() {
    section "Isolation — suite sees temp HOME/XDG, not the developer's real ones"
    local id="E-1603V-iso"
    local real_home="${HOME}"
    local real_xdg="${XDG_CONFIG_HOME:-${HOME}/.config}"
    # The check runs under the isolated env; it fails loudly (not ok) if HOME or
    # XDG_CONFIG_HOME still equals the real value. real_* are baked in literally.
    make_suite "${WORK}" "${id}" <<EOF
schema = 1
task   = "${id}"
[[check]]
runner  = "sh"
command = "printf '1..2\\n'; [ \"\$HOME\" = \"${real_home}\" ] && echo 'not ok 1 HOME leaked' || echo 'ok 1 HOME isolated'; [ \"\$XDG_CONFIG_HOME\" = \"${real_xdg}\" ] && echo 'not ok 2 XDG leaked' || echo 'ok 2 XDG isolated'"
format  = "tap"
EOF
    assert_succeeds "check sees isolated HOME and XDG_CONFIG_HOME" verify_in "${WORK}" "${id}"
}

check_concurrency() {
    section "Concurrency — two runs at once do not collide"
    local a="E-1603V-c1" b="E-1603V-c2"
    for id in "${a}" "${b}"; do
        make_suite "${WORK}" "${id}" <<EOF
schema = 1
task   = "${id}"
[[check]]
runner  = "bats"
command = "sleep 1; printf '1..1\\nok 1 x\\n'"
format  = "tap"
EOF
    done
    verify_in "${WORK}" "${a}" >/dev/null 2>&1 & local p1=$!
    verify_in "${WORK}" "${b}" >/dev/null 2>&1 & local p2=$!
    wait "${p1}"; local r1=$?
    wait "${p2}"; local r2=$?
    if [[ "${r1}" -eq 0 && "${r2}" -eq 0 ]]; then
        report_pass "both concurrent runs pass independently"
    else
        report_fail "both concurrent runs pass independently" "r1=0 r2=0" "r1=${r1} r2=${r2}"
    fi
}

check_keep_flag() {
    section "--keep retains the per-run temp dir; default removes it"
    local id="E-1603V-keep"
    make_suite "${WORK}" "${id}" <<'EOF'
schema = 1
task   = "E-1603V-keep"
[[check]]
runner  = "bats"
command = "printf '1..1\nok 1 x\n'"
format  = "tap"
EOF
    # --keep: capture the reported dir and assert it survives.
    local out kept
    out=$(verify_in "${WORK}" "${id}" --keep 2>&1)
    kept=$(printf '%s\n' "${out}" | sed -n 's/^kept per-run dir: //p' | head -1)
    if [[ -n "${kept}" ]]; then
        assert_path_exists "--keep retains the temp dir" "${kept}"
        rm -rf "${kept}"
    else
        report_fail "--keep retains the temp dir" "'kept per-run dir:' line" "${out}"
    fi

    # default: no kept-dir message, and no endless-verify-* dir leaks.
    local before after tmp
    tmp="${TMPDIR:-/tmp}"
    before=$(find "${tmp}" -maxdepth 1 -name 'endless-verify-*' 2>/dev/null | wc -l | tr -d ' ')
    assert_contains "default run reports no kept dir" "PASSED:" verify_in "${WORK}" "${id}"
    out=$(verify_in "${WORK}" "${id}" 2>&1)
    if [[ "${out}" == *"kept per-run dir"* ]]; then
        report_fail "default run does not announce a kept dir" "no 'kept per-run dir'" "${out}"
    else
        report_pass "default run does not announce a kept dir"
    fi
    after=$(find "${tmp}" -maxdepth 1 -name 'endless-verify-*' 2>/dev/null | wc -l | tr -d ' ')
    if [[ "${after}" -le "${before}" ]]; then
        report_pass "default run leaves no temp dir behind"
    else
        report_fail "default run leaves no temp dir behind" "count <= ${before}" "count=${after}"
    fi
}

check_tier_guards() {
    section "Tier-0 boundary — needs and seed fail loudly (not silently unisolated)"
    local id="E-1603V-needs"
    make_suite "${WORK}" "${id}" <<EOF
schema = 1
task   = "${id}"
needs  = ["postgres"]
[[check]]
runner  = "bats"
command = "printf '1..1\\nok 1 x\\n'"
format  = "tap"
EOF
    assert_fails_with "needs -> loud 'Tier 0' refusal" "Tier 0" verify_in "${WORK}" "${id}"

    id="E-1603V-seed"
    make_suite "${WORK}" "${id}" <<EOF
schema = 1
task   = "${id}"
seed   = ["fixtures/baseline.json"]
[[check]]
runner  = "bats"
command = "printf '1..1\\nok 1 x\\n'"
format  = "tap"
EOF
    assert_fails_with "seed -> loud 'seed' refusal" "seed" verify_in "${WORK}" "${id}"

    id="E-1603V-missing"
    assert_fails_with "unknown task -> loud 'no verification suite'" "no verification suite" \
        verify_in "${WORK}" "${id}"
}

check_gotest_first_class() {
    section "First-class gotest — -json capture normalizes to CTRF (in-repo)"
    # gotest runs `go test ./internal/verify/...`, which needs cwd = the repo
    # module root, so this suite lives in the repo (removed on exit).
    local id="E-1603GT"
    REPO_GT_SUITE="${REPO_ROOT}/.endless/tasks/${id}"
    mkdir -p "${REPO_GT_SUITE}"
    cat > "${REPO_GT_SUITE}/verify.toml" <<'EOF'
schema = 1
task   = "E-1603GT"
[[check]]
runner = "gotest"
tests  = ["TestMergeReports"]
paths  = ["./internal/verify/..."]
EOF
    assert_succeeds "gotest first-class check passes via -json capture" \
        verify_in "${REPO_ROOT}" "${id}"
    assert_contains "gotest summary shows a passing gotest check" "gotest" \
        verify_in "${REPO_ROOT}" "${id}"
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${WORK}" ]] && rm -rf "${WORK}"
    [[ -n "${REPO_GT_SUITE}" ]] && rm -rf "${REPO_GT_SUITE}"
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2
    trap cleanup EXIT

    printf '%sE-1603 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    section "Build — worktree endless-go (candidate code under test)"
    assert_succeeds "go build -o bin/endless-go ./cmd/endless-go" \
        go build -o bin/endless-go ./cmd/endless-go
    BIN="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${BIN}" ]]; then
        printf 'ERROR: %s not built\n' "${BIN}" >&2; exit 2
    fi

    WORK=$(mktemp -d)

    check_go_unit_tests
    check_passing_suite
    check_failing_suite
    check_isolation
    check_concurrency
    check_keep_flag
    check_tier_guards
    check_gotest_first_class

    summary
}

main "$@"
