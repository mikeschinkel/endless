#!/usr/bin/env bash
#
# E-1702 verification script — confirms the brainstorm handoff template opens
# with an open-ended discussion instead of cueing the AskUserQuestion tool.
#
# The change lives in
# internal/templatecmd/templates/handoff/brainstorm.md.tmpl:
#   - the opening "Open by interviewing the requester — ask questions first"
#     became "Open with an open-ended discussion, not a questionnaire" plus an
#     explicit "Do NOT use the AskUserQuestion tool or lead with a list of
#     discrete questions";
#   - step 3's "start the interview" became "open the discussion".
#
# This script checks the source template AND the rendered output of a freshly
# built endless-go (the template is go:embed'd, so a spawn uses the compiled-in
# copy — building fresh is the truest end-to-end check).
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1702-verify.sh
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

TEMPLATECMD_PKG="./internal/templatecmd/"
TEMPLATE_FILE="internal/templatecmd/templates/handoff/brainstorm.md.tmpl"
TEMPLATE_NAME="handoff/brainstorm.md"

# Rendered output of the built binary, captured once in main() and reused by the
# render checks.
RENDERED=""

# Path to the freshly built binary; global so the EXIT trap can clean it up
# without tripping `set -u` once main()'s locals are out of scope.
BIN=""

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

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_cmd DESC CMD [ARGS...]
#   Pass if CMD exits 0. On failure, report the tail of its combined output.
assert_cmd() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_has DESC HAYSTACK NEEDLE
#   Pass if NEEDLE (a fixed string) is present in HAYSTACK.
assert_has() {
    local desc="$1"
    local haystack="$2"
    local needle="$3"
    if printf '%s' "${haystack}" | grep -qF -- "${needle}"; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "expected to find: ${needle}"
}

# assert_lacks DESC HAYSTACK NEEDLE
#   Pass if NEEDLE (a fixed string) is absent from HAYSTACK.
assert_lacks() {
    local desc="$1"
    local haystack="$2"
    local needle="$3"
    if printf '%s' "${haystack}" | grep -qF -- "${needle}"; then
        report_fail "${desc}" "unexpectedly found: ${needle}"
        return
    fi
    report_pass "${desc}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_source() {
    section "Source — the template file carries the reworded opening"

    local src
    src=$(cat "${TEMPLATE_FILE}")

    assert_lacks "old 'Open by interviewing the requester' cue is gone" \
        "${src}" "Open by interviewing the requester"
    assert_lacks "old 'start the interview' step is gone" \
        "${src}" "start the interview"
    assert_has "new open-ended-discussion opening is present" \
        "${src}" "Open with an open-ended discussion, not a questionnaire"
    assert_has "explicit AskUserQuestion warn-off is present" \
        "${src}" "Do NOT use the AskUserQuestion tool"
    assert_has "step 3 now says 'open the discussion'" \
        "${src}" "open the discussion"
}

test_render() {
    section "Render — freshly built binary emits the new opening"

    assert_has "rendered opening reads as an open-ended discussion" \
        "${RENDERED}" "Open with an open-ended discussion, not a questionnaire"
    assert_has "rendered text warns off AskUserQuestion" \
        "${RENDERED}" "Do NOT use the AskUserQuestion tool"
    assert_has "rendered step 3 says 'open the discussion'" \
        "${RENDERED}" "open the discussion"
    assert_lacks "rendered text no longer says 'interviewing the requester'" \
        "${RENDERED}" "interviewing the requester"
}

test_no_regression() {
    section "Regression — templatecmd suite stays green"

    assert_cmd "internal/templatecmd full suite passes" \
        go test -count=1 "${TEMPLATECMD_PKG}"
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

    # Worktrees need a go.work pointing at the local go-pkgs/ modules; without it
    # the replace directives resolve at the wrong depth and the build fails.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        if command -v just >/dev/null 2>&1; then
            just go-work-init >/dev/null 2>&1
        fi
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1702 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    # Build a fresh binary so the render reflects the CURRENT embedded template,
    # then render the brainstorm handoff once for the render checks.
    BIN=$(mktemp -t endless-go.XXXXXX) || exit 2
    trap 'rm -f "${BIN}"' EXIT
    if ! go build -o "${BIN}" ./cmd/endless-go 2>/tmp/e-1702-build.err; then
        printf '\n%sERROR:%s endless-go failed to build:\n' "${RED}${BOLD}" "${RESET}" >&2
        tail -5 /tmp/e-1702-build.err >&2
        exit 2
    fi

    local vars
    vars='{"label_prefix":"Task","title":"Verify E-1702","bg":false,"spawner_task":"0","return_anchor":"main:0","worktree_path":"/tmp/wt","branch":"e-1702","spawned_id":"1702","child_count":0}'
    RENDERED=$(printf '%s' "${vars}" | "${BIN}" template render "${TEMPLATE_NAME}" 2>/tmp/e-1702-render.err)
    if [[ -z "${RENDERED}" ]]; then
        printf '\n%sERROR:%s template render produced no output:\n' "${RED}${BOLD}" "${RESET}" >&2
        tail -5 /tmp/e-1702-render.err >&2
        exit 2
    fi

    test_source
    test_render
    test_no_regression

    summary
}

main "$@"
