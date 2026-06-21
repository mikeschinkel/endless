#!/usr/bin/env bash
#
# E-1611 verification script — exercises the verify manifest's `setup` field and
# the project-level .endless/verify.toml config layering (Go package
# internal/verify, building on E-1602).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1611-verify.sh
#
# E-1611 ships no CLI surface yet (the runner that executes `setup` is E-1603),
# so this verifies the Go package three ways:
#   1. Static analysis — builds, vet-clean, gofmt-clean, whole module compiles.
#   2. Unit tests       — the *_test.go suite, named per acceptance criterion.
#   3. Behavioral proof — a throwaway `main` (compiled fresh, independent of the
#                         *_test.go files) runs verify.Discover against real
#                         on-disk fixtures: a project-level .endless/verify.toml
#                         merged beneath per-task manifests, asserting the
#                         effective setup ordering, inherited defaults, and loud
#                         failure on an invalid project config.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on an environment problem.
#
# REQUIRES the worktree's go.work (absolute-path replaces). If builds fail with
# "replacement directory ../go-pkgs/... does not exist", run `just go-work-init`.
#
# Per-task verify script (convention from E-1596 / E-1602), not a deliverable of
# the epic itself.

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

# ─── fixtures ───────────────────────────────────────────────────────────────

# write_suite ROOT ID CONTENT — writes ROOT/.endless/tasks/ID/verify.toml.
write_suite() {
    local root="$1" id="$2" content="$3"
    mkdir -p "${root}/.endless/tasks/${id}"
    printf '%s' "${content}" > "${root}/.endless/tasks/${id}/verify.toml"
}

# write_project_config ROOT CONTENT — writes ROOT/.endless/verify.toml.
write_project_config() {
    local root="$1" content="$2"
    mkdir -p "${root}/.endless"
    printf '%s' "${content}" > "${root}/.endless/verify.toml"
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

# ─── 2: unit tests, mapped to acceptance criteria ────────────────────────────

test_unit() {
    section "2 — Unit tests (per acceptance criterion)"

    assert_succeeds "manifest carries the new 'setup' field (parse round-trip)" \
        go test -run '^TestParseManifest_ValidFull$' -count=1 ./internal/verify/
    assert_succeeds "project-level verify.toml parses; missing schema / bad format / per-task keys fail loudly" \
        go test -run '^TestParseProjectConfig' -count=1 ./internal/verify/
    assert_succeeds "Merge layers project beneath task (setup/seed append, format/needs defaults)" \
        go test -run '^TestMerge' -count=1 ./internal/verify/
    assert_succeeds "Discover merges project config and validates the effective manifest" \
        go test -run '^TestDiscover' -count=1 ./internal/verify/
    assert_succeeds "full internal/verify package suite is green" \
        go test -count=1 ./internal/verify/
}

# ─── 3: behavioral proof (independent of *_test.go) ──────────────────────────

test_behavioral() {
    section "3 — Behavioral proof (fresh binary vs. real on-disk layering)"

    local root out

    # Project-level config supplies a default format and a shared first setup
    # step; the per-task manifest omits format (inherits) and appends its own
    # setup step. The effective manifest must reflect project-then-task order.
    root=$(mktemp -d)
    write_project_config "${root}" 'schema = 1
format = "gotest-json"
setup  = ["just build", ".endless/verify/schema-init.sh"]
needs  = ["docker"]'
    write_suite "${root}" "E-1234" 'schema = 1
task   = "E-1234"
runner = "go test ./.endless/tasks/E-1234/..."
setup  = ["task-prep.sh"]'

    assert_contains "merges project + task setup, project first" \
        'setup=[just build .endless/verify/schema-init.sh task-prep.sh]' discover "${root}"
    assert_contains "task inherits the project default format" \
        'format=gotest-json' discover "${root}"
    assert_contains "task inherits the project default needs" \
        'needs=[docker]' discover "${root}"
    rm -rf "${root}"

    # A task that sets its own format overrides the project default, and a task
    # needs = [] explicitly overrides the project default down to none.
    root=$(mktemp -d)
    write_project_config "${root}" 'schema = 1
format = "gotest-json"
needs  = ["docker"]'
    write_suite "${root}" "E-2000" 'schema = 1
task   = "E-2000"
runner = "go test ./..."
format = "tap"
needs  = []'
    assert_contains "task format overrides project default" \
        'format=tap' discover "${root}"
    assert_contains "task needs = [] overrides project default to none" \
        'needs=[]' discover "${root}"
    rm -rf "${root}"

    # No project config: a per-task manifest omitting format is incomplete and
    # fails loudly (no layer supplies the default).
    root=$(mktemp -d)
    write_suite "${root}" "E-3000" 'schema = 1
task   = "E-3000"
runner = "go test ./..."'
    assert_fails_with "missing format with no project default fails loudly" \
        "missing required field" discover "${root}"
    rm -rf "${root}"

    # An invalid project-level verify.toml fails discovery loudly.
    root=$(mktemp -d)
    write_project_config "${root}" 'schema = 1
format = "junit-xml"'
    write_suite "${root}" "E-4000" "$(printf 'schema = 1\ntask = "E-4000"\nrunner = "go test ./..."\nformat = "tap"\n')"
    assert_fails_with "invalid project config fails loudly" \
        "invalid project-level .endless/verify.toml" discover "${root}"
    rm -rf "${root}"

    # A per-task key placed in the project config is rejected as unknown.
    root=$(mktemp -d)
    write_project_config "${root}" 'schema = 1
runner = "go test ./..."'
    write_suite "${root}" "E-5000" "$(printf 'schema = 1\ntask = "E-5000"\nrunner = "go test ./..."\nformat = "tap"\n')"
    assert_fails_with "per-task 'runner' in project config rejected" \
        "unknown keys" discover "${root}"
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
    HARNESS_DIR=$(mktemp -d "${repo_root}/tests/tasks/.e1611harness.XXXXXX") || exit 2
    trap 'rm -rf "${HARNESS_DIR}"' EXIT
    cat > "${HARNESS_DIR}/main.go" <<'GO'
package main

import (
	"fmt"
	"os"
	"strings"

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
		fmt.Printf("FOUND %s runner=%q format=%s setup=[%s] seed=[%s] needs=[%s]\n",
			id, m.Runner, m.Format,
			strings.Join(m.Setup, " "),
			strings.Join(m.Seed, " "),
			strings.Join(m.Needs, " "))
	}
}
GO

    printf '%sE-1611 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  scope:   internal/verify (setup field + project-level config layering)\n'
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_static
    test_unit
    test_behavioral

    summary
}

main "$@"
