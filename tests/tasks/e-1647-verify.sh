#!/usr/bin/env bash
#
# E-1647 verification script — confirms the flat, type-agnostic respawn handoff
# template exists and renders correctly through the REAL render path.
#
# The change adds internal/templatecmd/templates/handoff/respawn.md.tmpl (a single
# template used for every reopen, regardless of task type) plus the Python render
# glue in src/endless/task_cmd.py:render_handoff(respawn=True, ...) that supplies
# three new restore-context vars: restore_case, prior_outcome, last_status_snapshot.
#
# This script drives the embedded template through `endless-go template render`
# (the same binary + code path render_handoff shells out to) — it does NOT
# re-implement templating. The render is pure (no DB reads, no seeding), so the
# self-dev sandbox is untouched and the script is fully re-runnable.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1647-verify.sh
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

TEMPLATE_REL="internal/templatecmd/templates/handoff/respawn.md.tmpl"

# Populated in main() once the repo root and binary are resolved.
ENDLESS_GO=""

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

# ─── render helper ──────────────────────────────────────────────────────────

# A complete var payload mirroring what render_handoff() supplies — every var
# the template can reference, so a successful render leaves no `<no value>`
# placeholder behind. The three E-1647 vars use distinct sentinel strings so
# each can be asserted present independently.
render_respawn() {
    printf '%s' '{
        "spawned_id": 1647,
        "label_prefix": "E-1647",
        "title": "Reopened task title",
        "spawner_task": 1644,
        "return_anchor": "%42",
        "worktree_path": "/abs/p",
        "branch": "task/1647-x",
        "child_count": 0,
        "children_state": "no children yet",
        "bg": false,
        "restore_case": "rebuilt-off-main",
        "prior_outcome": "PRIOR_OUTCOME_SENTINEL",
        "last_status_snapshot": "LAST_SNAPSHOT_SENTINEL"
    }' | "${ENDLESS_GO}" template render handoff/respawn 2>/tmp/e1647_render_err
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_template_exists() {
    section "Template — respawn.md.tmpl is present"
    if [[ -f "${TEMPLATE_REL}" ]]; then
        report_pass "${TEMPLATE_REL} exists"
    else
        report_fail "${TEMPLATE_REL} exists" "file not found at repo root"
    fi
}

test_render() {
    section "Render — real path substitutes all vars (no <no value> leaks)"

    local out rc
    out=$(render_respawn)
    rc=$?

    if [[ "${rc}" -ne 0 ]]; then
        report_fail "render exits 0" \
            "exit=${rc} | $(tail -3 /tmp/e1647_render_err 2>/dev/null | tr '\n' '⏎')"
        # Stash empty output so downstream content checks fail loudly too.
        out=""
    else
        report_pass "render exits 0"
    fi

    if [[ -n "${out}" ]] && ! grep -q '<no value>' <<<"${out}"; then
        report_pass "no <no value> placeholders remain"
    else
        report_fail "no <no value> placeholders remain" \
            "output contained <no value> or render produced nothing"
    fi

    # Each new E-1647 var substituted.
    if grep -q 'rebuilt-off-main' <<<"${out}"; then
        report_pass "restore_case substituted"
    else
        report_fail "restore_case substituted" "expected 'rebuilt-off-main' in output"
    fi
    if grep -q 'PRIOR_OUTCOME_SENTINEL' <<<"${out}"; then
        report_pass "prior_outcome substituted"
    else
        report_fail "prior_outcome substituted" "expected prior_outcome sentinel in output"
    fi
    if grep -q 'LAST_SNAPSHOT_SENTINEL' <<<"${out}"; then
        report_pass "last_status_snapshot substituted"
    else
        report_fail "last_status_snapshot substituted" "expected snapshot sentinel in output"
    fi

    section "Render — distinguishing content of the respawn template"

    # Lead instruction: /cd <abs worktree path> on its own line.
    if grep -qx '/cd /abs/p' <<<"${out}"; then
        report_pass "leads with '/cd /abs/p' line"
    else
        report_fail "leads with '/cd /abs/p' line" "no exact '/cd /abs/p' line in output"
    fi

    # The interrogative pivot — proves this is the respawn template, not a
    # type template (which drive toward completion without asking why).
    if grep -qi 'ask the user what their goal is for reopening' <<<"${out}"; then
        report_pass "contains the interrogative reopen pivot"
    else
        report_fail "contains the interrogative reopen pivot" \
            "missing the 'ask the user … goal … reopening' line"
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

    # Resolve the worktree-built endless-go (exercises candidate code, not the
    # global install). Build it on demand if missing.
    ENDLESS_GO="${repo_root}/bin/endless-go"
    if [[ ! -x "${ENDLESS_GO}" ]]; then
        if command -v just >/dev/null 2>&1; then
            just go >/dev/null 2>&1
        fi
        if [[ ! -x "${ENDLESS_GO}" ]]; then
            printf 'ERROR: bin/endless-go missing and could not be built (run: just go)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1647 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:        %s\n' "${repo_root}"
    printf '  endless-go: %s\n' "${ENDLESS_GO}"
    printf '  go:         %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_template_exists
    test_render

    summary
}

main "$@"
