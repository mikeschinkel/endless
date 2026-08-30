#!/usr/bin/env bash
#
# E-1602 verification script — exercises the verify.toml manifest schema and
# the .endless/tasks/ discovery convention (Go package internal/verify).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1602-verify.sh
#
# Unlike the CLI-oriented prototypes, E-1602 ships no CLI surface yet (the
# `endless verify` consumer is E-1603), so this script verifies the Go package
# three ways:
#   1. Static analysis — builds, vet-clean, gofmt-clean, dependency wired.
#   2. Unit tests       — the *_test.go suite, named per acceptance criterion.
#   3. Behavioral proof — a throwaway `main` (compiled fresh, independent of the
#                         *_test.go files) runs verify.Discover against real
#                         on-disk .endless/tasks/ fixtures and asserts the
#                         observable results.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on an environment problem.
#
# REQUIRES the worktree's go.work (absolute-path replaces). If builds fail with
# "replacement directory ../go-pkgs/... does not exist", run `just go-work-init`.
#
# Shape borrowed from the E-1599 / E-1577 / E-1601 ad-hoc prototypes (the
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

# assert_fails_with DESC PATTERN CMD [ARGS...]
#   Pass if CMD exits non-zero AND its combined output contains PATTERN.
assert_fails_with() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output contains: ${pattern}" \
        "exit=${rc} | output=${output}"
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

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_not_contains DESC PATTERN CMD [ARGS...]
assert_not_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

# ─── fixtures ───────────────────────────────────────────────────────────────

# write_suite ROOT ID CONTENT — writes ROOT/.endless/tasks/ID/verify.toml.
write_suite() {
    local root="$1" id="$2" content="$3"
    mkdir -p "${root}/.endless/tasks/${id}"
    printf '%s' "${content}" > "${root}/.endless/tasks/${id}/verify.toml"
}

# manifest_for ID RUNNER — a minimal valid manifest body for ID with one
# first-class check on RUNNER (revised [[check]] schema).
manifest_for() {
    local id="$1" runner="$2"
    printf 'schema = 1\ntask = "%s"\n[[check]]\nrunner = "%s"\ntests = ["TestX"]\n' \
        "${id}" "${runner}"
}

# discover ROOT — run the compiled harness against project root ROOT.
discover() {
    go run "${HARNESS_DIR}/main.go" "$1"
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
    assert_succeeds "whole module still compiles (go build ./...)" \
        go build ./...
}

# ─── 2: dependency wiring ────────────────────────────────────────────────────

test_dependency() {
    section "2 — TOML dependency wired into go.mod / go.sum"

    assert_succeeds "go.mod requires github.com/BurntSushi/toml" \
        grep -q 'github.com/BurntSushi/toml' go.mod
    assert_succeeds "go.sum pins BurntSushi/toml v1.5.0 (non-workspace builds)" \
        grep -q 'github.com/BurntSushi/toml v1.5.0 h1:' go.sum
}

# ─── 3: unit tests, mapped to acceptance criteria ────────────────────────────

test_unit() {
    section "3 — Unit tests (per acceptance criterion)"

    assert_succeeds "sample verify.toml parses; missing field / no-checks / unknown schema fail loudly" \
        go test -run '^TestParseManifest' -count=1 ./internal/verify/
    assert_succeeds "Format enum validates the closed set (gotest-json|pytest-json|tap)" \
        go test -run '^TestFormat' -count=1 ./internal/verify/
    assert_succeeds "discovery finds all manifests and ignores unrelated files" \
        go test -run '^TestDiscover' -count=1 ./internal/verify/
    assert_succeeds "full internal/verify package suite is green" \
        go test -count=1 ./internal/verify/
}

# ─── 4: behavioral proof (independent of *_test.go) ──────────────────────────

test_behavioral() {
    section "4 — Behavioral proof (fresh binary vs. real .endless/tasks/ fixtures)"

    local root out

    # Positive: two valid suites, plus a manifest-less subdir and a stray file
    # that discovery must ignore.
    root=$(mktemp -d)
    write_suite "${root}" "E-1234" "$(manifest_for E-1234 gotest)"
    write_suite "${root}" "E-5678" "$(manifest_for E-5678 pytest)"
    mkdir -p "${root}/.endless/tasks/E-0000"           # no verify.toml → ignored
    printf 'notes\n' > "${root}/.endless/tasks/README.md" # stray file → ignored

    assert_contains "discovers E-1234" "FOUND E-1234" discover "${root}"
    assert_contains "discovers E-5678" "FOUND E-5678" discover "${root}"
    assert_contains "finds exactly 2 suites" "COUNT 2" discover "${root}"
    assert_contains "parses the check's runner" 'runner=gotest' discover "${root}"
    assert_not_contains "ignores the manifest-less subdir E-0000" "E-0000" discover "${root}"
    rm -rf "${root}"

    # Empty project: no .endless/tasks dir → empty result, no error (COUNT 0).
    root=$(mktemp -d)
    out=$(discover "${root}" 2>&1)
    if [[ "${out}" == *"COUNT 0"* ]]; then
        report_pass "project with no suites → empty map, no error"
    else
        report_fail "project with no suites → empty map, no error" "COUNT 0" "${out}"
    fi
    rm -rf "${root}"

    # Missing required field → loud failure.
    root=$(mktemp -d)
    write_suite "${root}" "E-1234" 'schema = 1
[[check]]
runner = "gotest"
tests = ["TestX"]'
    assert_fails_with "missing required 'task' fails loudly" \
        "missing required field" discover "${root}"
    rm -rf "${root}"

    # No [[check]] entries → loud failure.
    root=$(mktemp -d)
    write_suite "${root}" "E-1234" 'schema = 1
task = "E-1234"'
    assert_fails_with "manifest with no checks fails loudly" \
        "declares no [[check]] entries" discover "${root}"
    rm -rf "${root}"

    # Unknown schema version → loud failure.
    root=$(mktemp -d)
    write_suite "${root}" "E-1234" 'schema = 2
task = "E-1234"
[[check]]
runner = "gotest"
tests = ["TestX"]'
    assert_fails_with "unknown schema version fails loudly" \
        "unknown manifest schema version" discover "${root}"
    rm -rf "${root}"

    # task id disagrees with its directory name → loud failure.
    root=$(mktemp -d)
    write_suite "${root}" "E-1234" "$(manifest_for E-9999 gotest)"
    assert_fails_with "task id / directory mismatch fails loudly" \
        "does not match suite directory" discover "${root}"
    rm -rf "${root}"
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

    # Compile the discovery harness once. A dot-prefixed temp dir under the
    # module keeps it importable (internal/verify is module-private) while
    # staying invisible to `./...` package patterns. Cleaned up on exit.
    HARNESS_DIR=$(mktemp -d "${repo_root}/tests/tasks/.e1602harness.XXXXXX") || exit 2
    trap 'rm -rf "${HARNESS_DIR}"' EXIT
    cat > "${HARNESS_DIR}/main.go" <<'GO'
package main

import (
	"fmt"
	"os"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/verify"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: harness <project-root>")
		os.Exit(2)
	}
	manifests, err := verify.Discover(dt.DirPath(os.Args[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "DISCOVER_ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("COUNT %d\n", len(manifests))
	for id, m := range manifests {
		fmt.Printf("FOUND %s checks=%d\n", id, len(m.Checks))
		for _, c := range m.Checks {
			fmt.Printf("  runner=%s fmt=%s\n", c.Runner, c.ResolvedFormat())
		}
	}
}
GO

    printf '%sE-1602 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  scope:   internal/verify (verify.toml schema + discovery)\n'
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_static
    test_dependency
    test_unit
    test_behavioral

    summary
}

main "$@"
