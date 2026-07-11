#!/usr/bin/env bash
#
# E-1625 verification script — confirms the `just verify` self_dev recipe is a
# thin wrapper around the PRODUCT verb `endless task verify` (E-1603) and holds
# NO verification logic of its own (the just-is-dev-only constraint).
#
# The deliverable is a single Justfile recipe:
#
#     verify id="":
#         endless --db sandbox task verify {{ id }}
#
# All verification behavior — verify.toml discovery, per-run temp HOME/XDG
# isolation, running the suite, CTRF normalization, and the pass/fail exit code —
# lives in `endless task verify` and the endless-go runner it shells to. This
# script asserts the recipe's SHAPE statically, then (when the worktree toolchain
# is present) drives `just verify <bogus-id>` end-to-end to prove it delegates
# all the way to the runner and passes the runner's non-zero exit straight back.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1625-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

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

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    local desc="$1"
    local detail="$2"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "${detail}"
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

# ─── recipe body extraction ─────────────────────────────────────────────────

# The indented command lines of the `verify` recipe (everything after the
# `verify id=...:` header), with leading indentation stripped. One line each.
RECIPE_BODY=""
extract_recipe_body() {
    RECIPE_BODY=$(just --show verify 2>/dev/null \
        | awk '/^verify /{seen=1; next} seen && /^[[:space:]]+/{sub(/^[[:space:]]+/,""); print}')
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_recipe_exists() {
    section "Recipe — \`just verify\` is defined and listed"

    if just --show verify >/dev/null 2>&1; then
        report_pass "recipe 'verify' is defined in the Justfile"
    else
        report_fail "recipe 'verify' is defined in the Justfile" \
            "just --show verify failed"
    fi

    if just --list 2>/dev/null | grep -qE '^\s*verify\b'; then
        report_pass "recipe 'verify' appears in \`just --list\`"
    else
        report_fail "recipe 'verify' appears in \`just --list\`" "not listed"
    fi

    if just help 2>/dev/null | grep -q 'just verify'; then
        report_pass "\`just help\` documents the recipe"
    else
        report_fail "\`just help\` documents the recipe" "not in help menu"
    fi
}

test_wraps_product_verb() {
    section "Wrapping — body invokes \`endless task verify\` under --db sandbox"

    local normalized
    normalized=$(printf '%s' "${RECIPE_BODY}" | tr -s '[:space:]' ' ')

    if [[ "${normalized}" == *"endless --db sandbox task verify"* ]]; then
        report_pass "body calls 'endless --db sandbox task verify'"
    else
        report_fail "body calls 'endless --db sandbox task verify'" \
            "body: ${normalized:-<empty>}"
    fi

    # --db sandbox is what re-execs into worktree source AND selects
    # <worktree>/bin/endless-go (E-1510) — the self_dev routing.
    if [[ "${normalized}" == *"--db sandbox"* ]]; then
        report_pass "self_dev routing present (--db sandbox)"
    else
        report_fail "self_dev routing present (--db sandbox)" \
            "no --db sandbox in body"
    fi

    # The task id is forwarded positionally (empty -> product's own default).
    if [[ "${normalized}" == *'{{ id }}'* ]]; then
        report_pass "task id forwarded to the product verb ({{ id }})"
    else
        report_fail "task id forwarded to the product verb ({{ id }})" \
            "body: ${normalized:-<empty>}"
    fi
}

test_thin_wrapper() {
    section "Thin — recipe holds NO verification logic (just-is-dev-only)"

    local body_line_count
    body_line_count=$(printf '%s\n' "${RECIPE_BODY}" | grep -c '[^[:space:]]')

    if [[ "${body_line_count}" -eq 1 ]]; then
        report_pass "body is a single command (${body_line_count} line)"
    else
        report_fail "body is a single command" \
            "expected 1 command line, found ${body_line_count}"
    fi

    # No shell control flow / verification primitives leaking into the recipe.
    # These would signal logic that belongs in `endless task verify`, not here.
    local forbidden='if |for |while |case |&&|\|\||;|assert|normali[sz]e|ctrf|verify\.toml|python|sqlite|grep|jq'
    if printf '%s' "${RECIPE_BODY}" | grep -qE "${forbidden}"; then
        report_fail "no verification/normalization logic in the body" \
            "matched forbidden token(s): $(printf '%s' "${RECIPE_BODY}" | grep -oE "${forbidden}" | sort -u | tr '\n' ' ')"
    else
        report_pass "no verification/normalization logic in the body"
    fi
}

test_delegates_end_to_end() {
    section "Delegation — \`just verify <bogus>\` reaches the runner, passes exit through"

    # Live check needs the worktree toolchain: uv (Python re-exec) + a
    # verify-capable worktree endless-go. Absent either, this is an environment
    # gap, not a recipe failure — report and skip rather than false-fail.
    if ! command -v uv >/dev/null 2>&1; then
        printf '  %s(skipped: uv not on PATH — cannot re-exec worktree CLI)%s\n' "${DIM}" "${RESET}"
        return
    fi
    if [[ ! -x ./bin/endless-go ]] || ! ./bin/endless-go verify --help >/dev/null 2>&1; then
        printf '  %s(skipped: worktree bin/endless-go lacks `verify` — run `just build`)%s\n' "${DIM}" "${RESET}"
        return
    fi

    local out rc
    out=$(just verify E-9999999 2>&1)
    rc=$?

    if [[ "${rc}" -ne 0 ]]; then
        report_pass "non-zero exit propagates through the recipe (exit=${rc})"
    else
        report_fail "non-zero exit propagates through the recipe" \
            "expected non-zero, got 0"
    fi

    # The failure text is the RUNNER's own — proof the wrapper delegated the
    # whole way down to endless-go rather than deciding anything itself.
    if printf '%s' "${out}" | grep -q 'no verification suite found'; then
        report_pass "error originates in the endless-go runner (delegated)"
    else
        report_fail "error originates in the endless-go runner (delegated)" \
            "output tail: $(printf '%s' "${out}" | tail -2 | tr '\n' '⏎')"
    fi
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

    extract_recipe_body
    if [[ -z "${RECIPE_BODY//[[:space:]]/}" ]]; then
        printf 'ERROR: could not read the `verify` recipe body via `just --show verify`\n' >&2
        exit 2
    fi

    printf '%sE-1625 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  just:    %s\n' "$(just --version 2>&1 | awk '{print $2}')"

    test_recipe_exists
    test_wraps_product_verb
    test_thin_wrapper
    test_delegates_end_to_end

    summary
}

main "$@"
