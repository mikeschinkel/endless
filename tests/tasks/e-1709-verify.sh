#!/usr/bin/env bash
#
# E-1709 verification script — `just land` rebuilds the worktree's endless-go
# up-front, before the steps that consume it, so a stale binary can't break the
# landing.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1709-verify.sh
#
# This is the single entry point for verifying E-1709 (per E-1596). The fix is
# an ORDERING change in the dev-only `just land` recipe: the worktree binary is
# now rebuilt (`just go`) BEFORE `endless db apply-change` and `endless worktree
# land` run it against the real DB. Both of those exec the worktree endless-go,
# which asserts tasktype.VerifyIntegrity on connect; a worktree rebased onto a
# newer main but not rebuilt would otherwise carry a stale enum and fail that
# check against the real DB (the E-1542 incident). Output: pass/fail per check,
# then a summary. Exit 0 on all-passed, 1 on any failure.
#
# What it does NOT cover (one irreducible manual case, NOT a step for the
# operator): the full stale-binary land against the *real* DB is the E-1542
# incident itself and irreversibly advances main, so it is not re-run here.
# Checks 2-4 together prove the fix instead: the recipe rebuilds the worktree
# binary up-front, that binary passes VerifyIntegrity, and the rebuild is
# ordered before every real-DB consumer — so the original failure has no
# remaining path.
#
# Model: tests/tasks/e-1542-verify.sh (the E-1596 reference shape).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
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
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_lt DESC LABEL_A A LABEL_B B  (integer A < B, both must be non-empty)
assert_lt() {
    local desc="$1" label_a="$2" a="$3" label_b="$4" b="$5"
    if [[ -z "${a}" || -z "${b}" ]]; then
        report_fail "${desc}" "${label_a} and ${label_b} both present" \
            "${label_a}=${a:-<missing>} ${label_b}=${b:-<missing>}"
        return
    fi
    if [[ "${a}" -lt "${b}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${label_a}(${a}) before ${label_b}(${b})" \
        "${label_a}=${a} is NOT before ${label_b}=${b}"
}

# ─── check 1: build + automated suites ──────────────────────────────────────

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # The enum whose VerifyIntegrity a stale binary tripped in the incident.
    assert_succeeds "go test ./internal/tasktype/..." \
        go test ./internal/tasktype/...
}

# ─── check 2: recipe ordering (static regression guard) ─────────────────────

test_recipe_ordering() {
    section "Recipe ordering — rebuild before apply-change & record-landing"

    local body code rebuild_ln apply_ln land_ln
    body=$(just --show land 2>/dev/null)
    # Drop comment-only lines so marker substrings in the doc comments don't
    # false-match; line numbers below are all relative to this filtered view.
    code=$(printf '%s\n' "${body}" | grep -vE '^[[:space:]]*#')

    rebuild_ln=$(printf '%s\n' "${code}" | grep -nF 'cd "$wt" && just go' | head -1 | cut -d: -f1)
    apply_ln=$(printf '%s\n' "${code}"   | grep -nF 'endless db apply-change' | head -1 | cut -d: -f1)
    land_ln=$(printf '%s\n' "${code}"    | grep -nF 'endless worktree land'   | head -1 | cut -d: -f1)

    if [[ -z "${rebuild_ln}" ]]; then
        report_fail "up-front worktree rebuild present in land recipe" \
            "a 'cd \"\$wt\" && just go' step exists" "no such step found"
    else
        report_pass "up-front worktree rebuild present in land recipe"
    fi

    assert_lt "rebuild runs before 'endless db apply-change'" \
        "rebuild" "${rebuild_ln}" "apply-change" "${apply_ln}"
    assert_lt "rebuild runs before 'endless worktree land'" \
        "rebuild" "${rebuild_ln}" "record-land" "${land_ln}"
}

# ─── check 3: recipe shell syntax ───────────────────────────────────────────

test_recipe_syntax() {
    section "Recipe syntax"
    if just --show land 2>/dev/null | bash -n 2>/tmp/e1709-bash-n.err; then
        report_pass "land recipe body parses (bash -n)"
    else
        report_fail "land recipe body parses (bash -n)" "no syntax errors" \
            "$(cat /tmp/e1709-bash-n.err)"
    fi
    rm -f /tmp/e1709-bash-n.err
}

# ─── check 4: the up-front build works and its binary passes integrity ──────

test_rebuild_works() {
    section "Up-front build works & binary passes VerifyIntegrity"

    # The exact command the recipe now runs up-front (cwd is the worktree).
    assert_succeeds "just go (recipe's up-front build)" just go

    if [[ -x bin/endless-go ]]; then
        report_pass "bin/endless-go built and executable"
    else
        report_fail "bin/endless-go built and executable" \
            "executable file at bin/endless-go" "missing or not executable"
    fi

    # Any endless-go command that opens the DB runs tasktype.VerifyIntegrity on
    # connect (internal/monitor/db.go). `tmux active-id` is the lightest such
    # command; the worktree binary self-routes to the sandbox DB from cwd
    # (E-1368), so this touches the sandbox, never the real ledger. Success
    # proves the freshly built binary the recipe uses passes the very integrity
    # check the stale binary failed.
    assert_succeeds "rebuilt binary opens DB (VerifyIntegrity passes)" \
        ./bin/endless-go tmux active-id
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

    if ! command -v just >/dev/null 2>&1; then
        printf 'ERROR: just not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1709 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (endless-go self-routes from cwd)\n'

    test_build_and_suites
    test_recipe_ordering
    test_recipe_syntax
    test_rebuild_works

    summary
}

main "$@"
