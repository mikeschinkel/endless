#!/usr/bin/env bash
#
# E-1695 verification script — confirms the epic handoff template no longer
# tells the coordinator to dispatch children itself (and no longer via `--bg`),
# and instead advises the user to spawn each child.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1695-verify.sh
#
# The change is to an embedded Go template (internal/templatecmd/templates/
# handoff/epic.md.tmpl). For self-dev projects the renderer reads the EMBEDDED
# source, so this script rebuilds bin/endless-go first, then renders the epic
# handoff (foreground AND background variants) and asserts on the text. The
# dispatch guidance lives outside the {{if .bg}} blocks, so both renders must
# satisfy the same assertions.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error (not a git worktree / build failed / go missing).
#
# Ad-hoc per-task verify script, following the E-1596 convention and modeled on
# tests/tasks/e-1577-verify.sh.

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

# ─── assertions (operate on a captured render, passed as the last arg) ───────

# assert_contains DESC PATTERN RENDER
assert_contains() {
    local desc="$1"
    local pattern="$2"
    local render="$3"
    if [[ "${render}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "render contains: ${pattern}" "${render}"
}

# assert_not_contains DESC PATTERN RENDER
assert_not_contains() {
    local desc="$1"
    local pattern="$2"
    local render="$3"
    if [[ "${render}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "render does NOT contain: ${pattern}" "${render}"
}

# ─── render helper ──────────────────────────────────────────────────────────

# render_epic BG  → prints the rendered epic handoff to stdout.
# BG is the literal JSON boolean `true` or `false`.
render_epic() {
    local bg="$1"
    printf '%s' "{
        \"spawned_id\": 9999,
        \"label_prefix\": \"E-8888/E-9999\",
        \"title\": \"Test epic\",
        \"spawner_task\": 7777,
        \"return_anchor\": \"%1\",
        \"worktree_path\": \"/tmp/wt/e-9999\",
        \"branch\": \"task/9999-test\",
        \"bg\": ${bg}
    }" | ./bin/endless-go template render handoff/epic
}

# ─── checks ─────────────────────────────────────────────────────────────────

# Run the full assertion set against one render variant.
check_render() {
    local variant="$1"     # "foreground" | "background"
    local render="$2"

    section "Epic handoff (${variant}) — coordinator advises, does not dispatch"

    # The coordinator no longer auto-dispatches children.
    assert_not_contains "no '--bg' child dispatch" \
        "spawn --bg" "${render}"
    assert_not_contains "no 'dispatcher: fan out' framing" \
        "dispatcher: fan out" "${render}"
    assert_not_contains "role summary drops 'dispatch children'" \
        "dispatch children" "${render}"

    # The advise-only guidance is present.
    assert_contains "role summary tells user to spawn" \
        "tell the user which children to spawn" "${render}"
    assert_contains "'ready' mode hands spawn to the user" \
        "let the user spawn each child when ready" "${render}"
    assert_contains "'ready' mode: don't spawn them yourself" \
        "Don't spawn them yourself" "${render}"
    assert_contains "'ready' mode still names the foreground spawn command" \
        "endless task spawn <child-id>" "${render}"

    # No regression: all six operational-mode labels still present.
    assert_contains "mode label: Zero children" \
        "Zero children" "${render}"
    assert_contains "mode label: All \`unplanned\`" \
        "All \`unplanned\`" "${render}"
    assert_contains "mode label: All \`ready\`" \
        "All \`ready\`" "${render}"
    assert_contains "mode label: All \`underway\`" \
        "All \`underway\`" "${render}"
    assert_contains "mode label: All terminal" \
        "All terminal" "${render}"
    assert_contains "mode label: Mixed" \
        "Mixed" "${render}"
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

    printf '%sE-1695 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"

    # Re-embed the current template so the render reflects source, not a stale
    # binary.
    printf '  build:   go build -o bin/endless-go ./cmd/endless-go\n'
    if ! go build -o bin/endless-go ./cmd/endless-go; then
        printf 'ERROR: build failed\n' >&2
        exit 2
    fi

    local fg_render bg_render
    fg_render=$(render_epic false)
    bg_render=$(render_epic true)

    check_render "foreground" "${fg_render}"
    check_render "background" "${bg_render}"

    summary
}

main "$@"
