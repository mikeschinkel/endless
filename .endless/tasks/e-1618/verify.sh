#!/usr/bin/env bash
#
# E-1618 verification script — exercises the verify.toml [[check]] list: the
# first-class runner registry (gotest/pytest), tests/paths -> native-filter
# translation, format inference, raw-command fallback, per-check validation, and
# the bare-clone run.sh emission (Go package internal/verify, revising E-1602).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1618-verify.sh
#
# E-1618 ships no CLI surface yet (the runner that executes the checks is
# E-1603), so this verifies the Go package three ways:
#   1. Static analysis — builds, vet-clean, gofmt-clean, whole module compiles.
#   2. Unit tests       — the *_test.go suite, named per acceptance criterion.
#   3. Behavioral proof — a throwaway `main` (compiled fresh, independent of the
#                         *_test.go files) runs verify.Discover + RenderRunScript
#                         against real on-disk [[check]] fixtures and asserts the
#                         observable results.
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

# render ROOT — run the compiled harness against project root ROOT (prints the
# discovered checks, their resolved commands/formats, and the run.sh emission).
render() {
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

    assert_succeeds "mixed gotest+pytest+bats manifest parses; minimal parses" \
        go test -run '^TestParseManifest_Valid' -count=1 ./internal/verify/
    assert_succeeds "per-check validation matrix (tests-on-raw / raw-no-command / first-class conflicts)" \
        go test -run '^TestParseManifest_CheckValidation$' -count=1 ./internal/verify/
    assert_succeeds "consistent first-class variants (matching format / command escape hatch / paths-only) accepted" \
        go test -run '^TestParseManifest_FirstClassConsistentVariants$' -count=1 ./internal/verify/
    assert_succeeds "no [[check]] entries fails loudly" \
        go test -run '^TestParseManifest_NoChecks$' -count=1 ./internal/verify/
    assert_succeeds "top-level key misplaced under [[check]] rejected (TOML ordering)" \
        go test -run '^TestParseManifest_TopLevelKeyAfterCheckRejected$' -count=1 ./internal/verify/
    assert_succeeds "Check translation/format/command resolution (gotest anchoring, pytest nodeids, paths)" \
        go test -run '^TestCheck' -count=1 ./internal/verify/
    assert_succeeds "RenderRunScript emits setup -> checks -> teardown, excludes seed/needs" \
        go test -run '^TestRenderRunScript' -count=1 ./internal/verify/
    assert_succeeds "Merge layers project beneath task (setup/teardown/seed append, needs default)" \
        go test -run '^TestMerge' -count=1 ./internal/verify/
    assert_succeeds "Discover merges project config and validates effective checks" \
        go test -run '^TestDiscover' -count=1 ./internal/verify/
    assert_succeeds "full internal/verify package suite is green" \
        go test -count=1 ./internal/verify/
}

# ─── 3: behavioral proof (independent of *_test.go) ──────────────────────────

test_behavioral() {
    section "3 — Behavioral proof (fresh binary vs. real [[check]] fixtures)"

    local root

    # A verification composing three runners: a first-class gotest (structured
    # tests + paths), a first-class pytest (nodeids), and a raw bats command.
    root=$(mktemp -d)
    write_suite "${root}" "E-1234" 'schema   = 1
task     = "E-1234"
setup    = ["just build"]
teardown = ["echo done"]
seed     = ["fixtures/baseline.json"]

[[check]]
runner = "gotest"
tests  = ["TestFoo", "TestBar"]
paths  = ["./internal/verify/..."]

[[check]]
runner = "pytest"
tests  = ["tests/test_x.py::test_a"]

[[check]]
runner  = "bats"
command = "bats ./.endless/tasks/E-1234/cli.bats"
format  = "tap"'

    assert_contains "discovers the suite with 3 checks" "FOUND E-1234 checks=3" render "${root}"
    assert_contains "gotest tests/paths translate to an anchored -run filter" \
        "cmd=<go test -run '^(TestFoo|TestBar)\$' ./internal/verify/...>" render "${root}"
    assert_contains "gotest format inferred" "fmt=gotest-json" render "${root}"
    assert_contains "pytest nodeids translate to positional args" \
        "cmd=<pytest tests/test_x.py::test_a>" render "${root}"
    assert_contains "pytest format inferred" "fmt=pytest-json" render "${root}"
    assert_contains "raw bats command is literal with declared tap format" \
        "cmd=<bats ./.endless/tasks/E-1234/cli.bats> fmt=tap" render "${root}"

    # run.sh emission: setup -> checks -> teardown via trap; seed excluded.
    assert_contains "run.sh wires the teardown trap" "trap teardown EXIT" render "${root}"
    assert_contains "run.sh includes the setup step" "just build" render "${root}"
    assert_contains "run.sh includes the resolved gotest check" \
        "go test -run '^(TestFoo|TestBar)\$' ./internal/verify/..." render "${root}"
    assert_not_contains "run.sh excludes Endless-only seed" "fixtures/baseline.json" render "${root}"
    rm -rf "${root}"

    # tests on a non-first-class runner fails loudly.
    root=$(mktemp -d)
    write_suite "${root}" "E-2000" 'schema = 1
task   = "E-2000"

[[check]]
runner = "bats"
tests  = ["whatever"]'
    assert_fails_with "tests on a raw runner fails loudly" \
        "tests is only valid on a first-class runner" render "${root}"
    rm -rf "${root}"

    # raw runner without a command fails loudly.
    root=$(mktemp -d)
    write_suite "${root}" "E-3000" 'schema = 1
task   = "E-3000"

[[check]]
runner = "bats"'
    assert_fails_with "raw runner without command fails loudly" \
        "non-first-class runner requires command" render "${root}"
    rm -rf "${root}"

    # first-class with a mismatched explicit format fails loudly.
    root=$(mktemp -d)
    write_suite "${root}" "E-4000" 'schema = 1
task   = "E-4000"

[[check]]
runner = "gotest"
tests  = ["TestX"]
format = "tap"'
    assert_fails_with "first-class mismatched format fails loudly" \
        "declared format does not match" render "${root}"
    rm -rf "${root}"

    # a manifest with no checks fails loudly.
    root=$(mktemp -d)
    write_suite "${root}" "E-5000" 'schema = 1
task   = "E-5000"'
    assert_fails_with "manifest with no checks fails loudly" \
        "declares no [[check]] entries" render "${root}"
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

    # Compile the harness once. A dot-prefixed temp dir under the module keeps it
    # importable (internal/verify is module-private) while staying invisible to
    # `./...` package patterns. Cleaned up on exit.
    HARNESS_DIR=$(mktemp -d "${repo_root}/tests/tasks/.e1618harness.XXXXXX") || exit 2
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
		for i, c := range m.Checks {
			fmt.Printf("  CHECK #%d runner=%s cmd=<%s> fmt=%s\n",
				i, c.Runner, c.ResolvedCommand(), c.ResolvedFormat())
		}
		fmt.Printf("RUNSCRIPT-BEGIN %s\n%sRUNSCRIPT-END\n", id, verify.RenderRunScript(m))
	}
}
GO

    printf '%sE-1618 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  scope:   internal/verify ([[check]] list + first-class translation + run.sh emit)\n'
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_static
    test_unit
    test_behavioral

    summary
}

main "$@"
