#!/usr/bin/env bash
#
# E-1604 verification script — exercises the vendored CTRF-subset result writer
# and the native normalizers (go test -json, pytest JSON, TAP) in the Go package
# internal/verify.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1604-verify.sh
#
# E-1604 ships no CLI surface yet (the `endless verify` consumer is E-1603), so
# this script verifies the Go package three ways:
#   1. Static analysis — builds, vet-clean, gofmt-clean.
#   2. Unit tests       — the *_test.go suite, named per acceptance criterion.
#   3. Behavioral proof — a throwaway `main` (compiled fresh, independent of the
#                         *_test.go files) feeds each native stream through
#                         verify.Normalize and (*Report).Write, then the script
#                         asserts the emitted CTRF document: a single JSON object
#                         with the pinned field names, summary counts matching
#                         the native totals, and failures carrying message/trace.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on an environment problem.
#
# REQUIRES the worktree's go.work (absolute-path replaces). If builds fail with
# "replacement directory ../go-pkgs/... does not exist", run `just go-work-init`.
#
# Shape borrowed from the E-1602 / E-1599 / E-1577 ad-hoc prototypes (the
# formalization task is E-1596); this is a per-task verify script, not a
# deliverable of E-1596.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()
HARNESS_DIR=""

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

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    local desc="$1"
    local expected="$2"
    local actual="$3"
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

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]  — pass if CMD exits 0.
assert_succeeds() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | $(printf '%s' "${output}" | tail -8)"
}

# assert_no_output DESC CMD [ARGS...]  — pass if CMD produces no stdout/stderr.
assert_no_output() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    if [[ -z "${output}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "no output" "${output}"
}

# assert_str_contains DESC PATTERN STRING — pass if STRING contains PATTERN.
# Used to assert against already-captured harness output (avoids re-running it).
assert_str_contains() {
    local desc="$1" pattern="$2" str="$3"
    if [[ "${str}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${str}"
}

# assert_valid_json DESC STRING — pass if STRING parses as a single JSON value.
# Prefers jq, falls back to python3; reports an environment problem if neither
# is available rather than passing silently.
assert_valid_json() {
    local desc="$1" str="$2"
    if command -v jq >/dev/null 2>&1; then
        if printf '%s' "${str}" | jq -e . >/dev/null 2>&1; then
            report_pass "${desc}"
            return
        fi
    elif command -v python3 >/dev/null 2>&1; then
        if printf '%s' "${str}" | python3 -m json.tool >/dev/null 2>&1; then
            report_pass "${desc}"
            return
        fi
    else
        report_fail "${desc}" "jq or python3 available to validate JSON" "neither found"
        return
    fi
    report_fail "${desc}" "parses as a single JSON document" "${str}"
}

# ─── native-stream fixtures ───────────────────────────────────────────────────

# normalize FORMAT  — pipe a native stream on stdin; emits the CTRF document.
normalize() {
    go run "${HARNESS_DIR}/main.go" "$1"
}

gotest_stream() {
    cat <<'EOF'
{"Time":"2026-06-23T10:00:00Z","Action":"run","Package":"pkg","Test":"TestA"}
{"Time":"2026-06-23T10:00:00.5Z","Action":"pass","Package":"pkg","Test":"TestA","Elapsed":0.5}
{"Time":"2026-06-23T10:00:01Z","Action":"output","Package":"pkg","Test":"TestB","Output":"    foo_test.go:10: want 1 got 2\n"}
{"Time":"2026-06-23T10:00:01.2Z","Action":"fail","Package":"pkg","Test":"TestB","Elapsed":0.2}
{"Time":"2026-06-23T10:00:01.3Z","Action":"skip","Package":"pkg","Test":"TestC"}
EOF
}

pytest_stream() {
    cat <<'EOF'
{"created":1750672800.0,"duration":1.5,"tests":[
  {"nodeid":"tests/test_x.py::test_a","outcome":"passed",
   "setup":{"duration":0.0,"outcome":"passed"},"call":{"duration":0.01,"outcome":"passed"},"teardown":{"duration":0.0,"outcome":"passed"}},
  {"nodeid":"tests/test_x.py::test_b","outcome":"failed",
   "setup":{"duration":0.0,"outcome":"passed"},
   "call":{"duration":0.1,"outcome":"failed","crash":{"message":"assert 1 == 2"},"longrepr":"E   assert 1 == 2"},
   "teardown":{"duration":0.0,"outcome":"passed"}},
  {"nodeid":"tests/test_x.py::test_c","outcome":"skipped",
   "setup":{"duration":0.0,"outcome":"skipped"},"call":{"duration":0.0,"outcome":"skipped"},"teardown":{"duration":0.0,"outcome":"passed"}}
]}
EOF
}

tap_stream() {
    cat <<'EOF'
TAP version 14
1..3
ok 1 - adds two numbers
not ok 2 - subtracts
  ---
  message: 'want 1 got -1'
  ...
ok 3 - windows only # SKIP not on this platform
EOF
}

# ─── 1: static analysis ──────────────────────────────────────────────────────

test_static() {
    section "1 — Static analysis (build, vet, gofmt)"

    assert_succeeds "internal/verify builds" \
        go build ./internal/verify/
    assert_succeeds "internal/verify is vet-clean" \
        go vet ./internal/verify/
    assert_no_output "internal/verify is gofmt-clean" \
        gofmt -l internal/verify
}

# ─── 2: unit tests, mapped to acceptance criteria ────────────────────────────

test_unit() {
    section "2 — Unit tests (per acceptance criterion)"

    assert_succeeds "go test -json normalizes (pass/fail/skip, duration, message/trace)" \
        go test -run '^TestParseGotestJSON' -count=1 ./internal/verify/
    assert_succeeds "pytest JSON normalizes (nodeid/outcome/durations, outcome mapping)" \
        go test -run '^TestParsePytestJSON' -count=1 ./internal/verify/
    assert_succeeds "TAP normalizes (ok/not ok, SKIP/TODO directives, YAML diagnostics)" \
        go test -run '^TestParseTAP' -count=1 ./internal/verify/
    assert_succeeds "MergeReports sums summaries and concatenates tests into one report" \
        go test -run '^TestMergeReports' -count=1 ./internal/verify/
    assert_succeeds "(*Report).Write emits one JSON document with pinned CTRF field names" \
        go test -run '^TestReport_Write' -count=1 ./internal/verify/
    assert_succeeds "Normalize rejects an unknown format loudly" \
        go test -run '^TestNormalize_UnknownFormat' -count=1 ./internal/verify/
    assert_succeeds "full internal/verify package suite is green" \
        go test -count=1 ./internal/verify/
}

# ─── 3: behavioral proof (independent of *_test.go) ──────────────────────────

test_behavioral() {
    section "3 — Behavioral proof (fresh binary normalizes real streams to CTRF)"

    local out

    # go test -json
    out=$(gotest_stream | normalize gotest-json 2>&1)
    assert_valid_json   "gotest-json → a single JSON document"            "${out}"
    assert_str_contains "gotest-json → tool.name is the producer"         '"name": "go test"' "${out}"
    assert_str_contains "gotest-json → summary partitions 3 tests"        '"tests": 3'        "${out}"
    assert_str_contains "gotest-json → 1 passed"                          '"passed": 1'       "${out}"
    assert_str_contains "gotest-json → 1 failed"                          '"failed": 1'       "${out}"
    assert_str_contains "gotest-json → 1 skipped"                         '"skipped": 1'      "${out}"
    assert_str_contains "gotest-json → failure carries a message"         '"message": "foo_test.go:10: want 1 got 2"' "${out}"
    assert_str_contains "gotest-json → failure carries a trace"           '"trace":'          "${out}"

    # pytest JSON
    out=$(pytest_stream | normalize pytest-json 2>&1)
    assert_valid_json   "pytest-json → a single JSON document"            "${out}"
    assert_str_contains "pytest-json → tool.name is the producer"         '"name": "pytest"'  "${out}"
    assert_str_contains "pytest-json → summary partitions 3 tests"        '"tests": 3'        "${out}"
    assert_str_contains "pytest-json → 1 failed"                          '"failed": 1'       "${out}"
    assert_str_contains "pytest-json → failure carries the crash message" '"message": "assert 1 == 2"' "${out}"

    # TAP
    out=$(tap_stream | normalize tap 2>&1)
    assert_valid_json   "tap → a single JSON document"                    "${out}"
    assert_str_contains "tap → tool.name is the producer"                 '"name": "tap"'     "${out}"
    assert_str_contains "tap → 1 passed"                                  '"passed": 1'       "${out}"
    assert_str_contains "tap → not ok maps to failed"                     '"failed": 1'       "${out}"
    assert_str_contains "tap → # SKIP directive maps to skipped"          '"skipped": 1'      "${out}"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi

    # Compile the normalize harness once. A dot-prefixed temp dir under the
    # module keeps it importable (internal/verify is module-private) while
    # staying invisible to `./...` package patterns. Cleaned up on exit.
    HARNESS_DIR=$(mktemp -d "${repo_root}/tests/tasks/.e1604harness.XXXXXX") || exit 2
    trap 'rm -rf "${HARNESS_DIR}"' EXIT
    cat > "${HARNESS_DIR}/main.go" <<'GO'
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mikeschinkel/endless/internal/verify"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: harness <format>  (native stream on stdin)")
		os.Exit(2)
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "READ_ERROR: %v\n", err)
		os.Exit(1)
	}
	rpt, err := verify.Normalize(verify.Format(os.Args[1]), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NORMALIZE_ERROR: %v\n", err)
		os.Exit(1)
	}
	if err = rpt.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "WRITE_ERROR: %v\n", err)
		os.Exit(1)
	}
}
GO

    printf '%sE-1604 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  scope:   internal/verify (CTRF-subset writer + native normalizers)\n'
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_static
    test_unit
    test_behavioral

    summary
}

main "$@"
