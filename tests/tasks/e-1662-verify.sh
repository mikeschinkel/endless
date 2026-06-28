#!/usr/bin/env bash
#
# E-1662 verification script.
#
# E-1662 builds on E-986's generic post-worktree-create hook runner
# (_run_post_worktree_create_hook in worktree_cmd.py, exercised by
# e-986-verify.sh). E-1662's delta is endless's OWN hook
# (.endless/hooks/post-worktree-create.sh): besides E-986's `just go-work-init`
# it now also runs `just build` and `just claude-settings-init`, so a self-dev
# worktree is actually BUILT and carries the per-worktree hook override instead
# of silently falling back to the global/main binary (E-1281/E-998).
#
# Checks:
#   - PRODUCT guard: no shipped endless Go/Python source shells out to `just`;
#     `just` lives ONLY in the .endless/hooks/post-worktree-create.sh wrapper.
#   - Hook content: endless's hook runs go-work-init + build + claude-settings-init.
#   - Functional E2E against THIS worktree: running the hook builds
#     bin/endless-go and installs a .claude/settings.json hook override whose
#     command points at this worktree's binary, preserving the XDG_CONFIG_HOME
#     env block.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1662-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. Shape follows the E-1572 sibling.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ───────────────────────────────────────────────────────────────

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
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}

# assert_true DESC -- CMD...  (CMD must exit zero)
assert_true() {
    local desc="$1"; shift 2   # drop desc and the literal --
    if "$@" >/dev/null 2>&1; then report_pass "${desc}"
    else report_fail "${desc}" "exit==0" "exit=$?"; fi
}

# ─── globals wired in main ───────────────────────────────────────────────────

REPO_ROOT=""
HOOK=""

# ─── checks ──────────────────────────────────────────────────────────────────

test_product_guard() {
    section "PRODUCT guard — no \`just\` in shipped source"

    # Go: nothing in cmd/ or internal/ may exec `just`.
    local go_hits
    go_hits=$(grep -rIn -E 'exec\.Command\("just"|"just"' "${REPO_ROOT}/cmd" "${REPO_ROOT}/internal" 2>/dev/null || true)
    assert_eq "no exec(\"just\") in Go (cmd/, internal/)" "" "${go_hits}"

    # Python: the hook runner must exec the script, not invoke `just` itself.
    local py_hits
    py_hits=$(grep -nE '"just"|'\''just'\''|subprocess.*just' "${REPO_ROOT}/src/endless/worktree_cmd.py" 2>/dev/null || true)
    assert_eq "no \`just\` invocation in worktree_cmd.py" "" "${py_hits}"
}

test_hook_content() {
    section "endless hook — bootstraps go.work + build + hook override"

    local hook_body
    hook_body=$(cat "${HOOK}" 2>/dev/null)
    assert_contains "hook runs go-work-init (E-986)" "just go-work-init" "${hook_body}"
    assert_contains "hook runs build (E-1662)" "just build" "${hook_body}"
    assert_contains "hook installs hook override (E-1662)" "just claude-settings-init" "${hook_body}"
    assert_true "hook is executable" -- test -x "${HOOK}"
}

test_functional_e2e() {
    section "functional E2E — run THIS worktree's hook (builds + hook override)"

    local out rc
    out=$("${HOOK}" "${REPO_ROOT}" 2>&1); rc=$?
    assert_eq "hook exits 0" "0" "${rc}"

    assert_true "bin/endless-go built and executable" -- test -x "${REPO_ROOT}/bin/endless-go"

    local settings="${REPO_ROOT}/.claude/settings.json"
    local hookcmd xdg
    hookcmd=$(python3 -c '
import json,sys
s=json.load(open(sys.argv[1]))
cmds=[h.get("command","") for ev in (s.get("hooks") or {}).values() for e in ev for h in e.get("hooks",[])]
print("\n".join(cmds))
' "${settings}" 2>/dev/null)
    xdg=$(python3 -c '
import json,sys
s=json.load(open(sys.argv[1]))
print((s.get("env") or {}).get("XDG_CONFIG_HOME",""))
' "${settings}" 2>/dev/null)

    assert_contains "hook override points at worktree binary" \
        "${REPO_ROOT}/bin/endless-go" "${hookcmd}"
    assert_contains "XDG_CONFIG_HOME env block preserved" \
        "sandboxes/$(basename "${REPO_ROOT}")" "${xdg}"
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    HOOK="${REPO_ROOT}/.endless/hooks/post-worktree-create.sh"
    [[ -f "${HOOK}" ]] || { printf 'ERROR: %s not found\n' "${HOOK}" >&2; exit 2; }
    command -v git     >/dev/null 2>&1 || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    command -v just    >/dev/null 2>&1 || { printf 'ERROR: just not on PATH\n' >&2; exit 2; }
    command -v python3 >/dev/null 2>&1 || { printf 'ERROR: python3 not on PATH\n' >&2; exit 2; }

    printf '%sE-1662 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo: %s\n  hook: %s\n' "${REPO_ROOT}" "${HOOK}"

    test_product_guard
    test_hook_content
    test_functional_e2e

    summary
}

main "$@"
