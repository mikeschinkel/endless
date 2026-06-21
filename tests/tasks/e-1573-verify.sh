#!/usr/bin/env bash
#
# E-1573 verification script — confirms the orchestration guide's spawn
# write-up was rewritten to cover the shipped spawn/coordinator/bg-dispatch
# patterns: foreground vs background dispatch, per-type handoff variants, the
# --bg flow, both attach verbs, the epic coordinator pattern with its six
# children-state modes, the soft throttle warning, the bg-agent session
# lifecycle, and handoff-template customization. Also confirms the stale
# single-file handoff reference is gone, the guide-map cross-reference lists
# the new verbs, the map stays valid, and the focused guide-map unit test
# still passes.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1573-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. This task is documentation-only — the script makes NO DB writes and
# needs no sandbox; it reads rendered guide output, runs `just guide-check`,
# and runs the guide-map unit test.
#
# NOTE: it renders via `uv run endless ...` (worktree source) on purpose — the
# globally-installed `endless` reads docs from the MAIN checkout, so it would
# show the pre-rewrite content until this branch lands.
#
# Shape borrowed from the E-1574 docs-only verify script; this is a per-task
# verify script, not a deliverable of the verify-suite formalization epic.

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

# Captured once in main(): the rendered `endless guide orchestration`, the
# `endless guide` index, the `endless guide --list` slug set, and the spawn
# section sliced out of the orchestration page.
GUIDE_ORCH=""
GUIDE_INDEX=""
GUIDE_LIST=""
SPAWN_SECTION=""
COORDINATOR_SECTION=""

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

# assert_haystack_contains DESC HAYSTACK PATTERN
#   Pass if HAYSTACK contains the literal substring PATTERN.
assert_haystack_contains() {
    local desc="$1"
    local haystack="$2"
    local pattern="$3"
    if [[ "${haystack}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "<not found in rendered output>"
}

# assert_haystack_lacks DESC HAYSTACK PATTERN
#   Pass if HAYSTACK does NOT contain the literal substring PATTERN.
assert_haystack_lacks() {
    local desc="$1"
    local haystack="$2"
    local pattern="$3"
    if [[ "${haystack}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" \
        "found '${pattern}' in rendered output"
}

# assert_no_task_ids DESC HAYSTACK
#   Pass if HAYSTACK contains no E-NNN ticket id (shipped-doc constraint).
assert_no_task_ids() {
    local desc="$1"
    local haystack="$2"
    local hits
    hits=$(printf '%s\n' "${haystack}" | grep -oE 'E-[0-9]+' | sort -u | tr '\n' ' ')
    if [[ -z "${hits}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "no E-NNN ids in the added prose" "found: ${hits}"
}

# assert_exit_zero DESC CMD [ARGS...]
assert_exit_zero() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# ─── 1: the new spawn subsections render ─────────────────────────────────────

test_subsections_render() {
    section "1 — 'guide orchestration' renders the rewritten spawn subsections"

    assert_haystack_contains "Foreground vs background subsection" \
        "${GUIDE_ORCH}" "Foreground vs background"
    assert_haystack_contains "Per-type handoff variants subsection" \
        "${GUIDE_ORCH}" "Per-type handoff variants"
    assert_haystack_contains "Background-agent dispatch subsection" \
        "${GUIDE_ORCH}" "Background-agent dispatch"
    assert_haystack_contains "Attach verbs subsection" \
        "${GUIDE_ORCH}" "Attach verbs"
    assert_haystack_contains "Coordinator pattern for epics subsection" \
        "${GUIDE_ORCH}" "Coordinator pattern for epics"
    assert_haystack_contains "Throttle warning subsection" \
        "${GUIDE_ORCH}" "Throttle warning"
    assert_haystack_contains "Session lifecycle subsection" \
        "${GUIDE_ORCH}" "Session lifecycle (background agents)"
    assert_haystack_contains "Customizing handoff templates subsection" \
        "${GUIDE_ORCH}" "Customizing handoff templates"
}

# ─── 2: foreground vs background distinction ─────────────────────────────────

test_foreground_vs_background() {
    section "2 — foreground vs background dispatch is spelled out"

    assert_haystack_contains "background runs under the Anthropic supervisor" \
        "${SPAWN_SECTION}" "Anthropic supervisor"
    assert_haystack_contains "bg dies on machine shutdown / claude stop" \
        "${SPAWN_SECTION}" '`claude stop`'
    assert_haystack_contains "--bg flow invokes the CLI background mode" \
        "${SPAWN_SECTION}" "spawn <id> --bg"
}

# ─── 3: per-type handoff variants + fallback ─────────────────────────────────

test_per_type_variants() {
    section "3 — per-type handoff variants (task/bug/research/epic + fallback)"

    assert_haystack_contains "bug variant frames reproduce-first" \
        "${SPAWN_SECTION}" "reproduce the bug first"
    assert_haystack_contains "research variant frames findings-as-deliverable" \
        "${SPAWN_SECTION}" "findings *are* the deliverable"
    assert_haystack_contains "unknown type falls back to the task variant" \
        "${SPAWN_SECTION}" 'falls back to the `task` variant'
    assert_haystack_contains "templates referenced by per-type path" \
        "${SPAWN_SECTION}" "handoff/<type>.md.tmpl"
}

# ─── 4: attach verbs + in-session safeguard ──────────────────────────────────

test_attach_verbs() {
    section "4 — both attach verbs documented, with the in-session safeguard"

    assert_haystack_contains "spawn --attach opens a new tmux window" \
        "${SPAWN_SECTION}" "endless task spawn --attach"
    assert_haystack_contains "task attach replaces the current process" \
        "${SPAWN_SECTION}" "endless task attach"
    assert_haystack_contains "safeguard names process replacement" \
        "${SPAWN_SECTION}" "replaces the current process"
    assert_haystack_contains "detaching leaves the agent running" \
        "${SPAWN_SECTION}" "Detaching"
}

# ─── 5: coordinator pattern names all six children-state modes ────────────────

test_coordinator_modes() {
    section "5 — coordinator pattern names all six children-state modes"

    assert_haystack_contains "coordinator does not implement directly" \
        "${COORDINATOR_SECTION}" "does **not** implement"
    assert_haystack_contains "mode: zero children" \
        "${COORDINATOR_SECTION}" "Zero children"
    assert_haystack_contains "mode: all needs_plan" \
        "${COORDINATOR_SECTION}" 'All `needs_plan`'
    assert_haystack_contains "mode: all ready" \
        "${COORDINATOR_SECTION}" 'All `ready`'
    assert_haystack_contains "mode: all in_progress" \
        "${COORDINATOR_SECTION}" 'All `in_progress`'
    assert_haystack_contains "mode: all terminal" \
        "${COORDINATOR_SECTION}" "All terminal"
    assert_haystack_contains "mode: mixed" \
        "${COORDINATOR_SECTION}" "Mixed"
}

# ─── 6: throttle warning is soft + config-keyed ──────────────────────────────

test_throttle_warning() {
    section "6 — throttle warning is soft, config-keyed, non-blocking"

    assert_haystack_contains "config key named" \
        "${SPAWN_SECTION}" "bg_throttle_warn"
    assert_haystack_contains "warning never blocks" \
        "${SPAWN_SECTION}" "never blocks"
    assert_haystack_contains "sweet-spot guidance" \
        "${SPAWN_SECTION}" "3–5 parallel agents"
}

# ─── 7: session lifecycle — survives / dies ──────────────────────────────────

test_session_lifecycle() {
    section "7 — bg session lifecycle: survives vs dies, no recovery commands"

    assert_haystack_contains "survives machine sleep (versioned)" \
        "${SPAWN_SECTION}" "v2.1.142+"
    assert_haystack_contains "stops after ~1h idle when unattached" \
        "${SPAWN_SECTION}" "idle"
    # Recovery verbs are filed but unshipped — they must NOT appear.
    assert_haystack_lacks "no unshipped recovery command (respawn)" \
        "${SPAWN_SECTION}" "claude respawn"
}

# ─── 8: handoff-template customization / override mechanism ───────────────────

test_template_customization() {
    section "8 — handoff-template customization and override precedence"

    assert_haystack_contains "materialized per-type path" \
        "${SPAWN_SECTION}" ".endless/templates/handoff/<type>.md.tmpl"
    assert_haystack_contains "per-developer .local.tmpl override" \
        "${SPAWN_SECTION}" ".local.tmpl"
    assert_haystack_contains "debug render command" \
        "${SPAWN_SECTION}" "endless internal template render"
}

# ─── 9: stale single-file handoff reference is gone ──────────────────────────

test_stale_reference_gone() {
    section "9 — the old single-file handoff path no longer appears"

    assert_haystack_lacks "no docs/templates/handoff.md in orchestration" \
        "${GUIDE_ORCH}" "docs/templates/handoff.md"
}

# ─── 10: shipped-doc constraint — no E-NNN ids in the spawn section ───────────

test_no_task_ids() {
    section "10 — rewritten spawn section carries no internal task ids"

    assert_no_task_ids "spawn section is id-free (shipped-doc rule)" \
        "${SPAWN_SECTION}"
}

# ─── 11: cross-reference index lists the spawn/attach verbs ───────────────────

test_index_rows() {
    section "11 — 'guide' index lists task spawn and task attach (orchestration)"

    assert_haystack_contains "task spawn maps to orchestration" \
        "${GUIDE_INDEX}" 'task spawn` | orchestration'
    assert_haystack_contains "task attach maps to orchestration" \
        "${GUIDE_INDEX}" 'task attach` | orchestration'
}

# ─── 12: slug set unchanged — orchestration still a section ───────────────────

test_slug_present() {
    section "12 — 'guide --list' still exposes the orchestration slug"

    assert_haystack_contains "orchestration slug present" \
        "${GUIDE_LIST}" "orchestration"
}

# ─── 13: guide map valid + focused unit test green ───────────────────────────

test_map_and_tests() {
    section "13 — guide map validates and the guide-map unit test passes"

    assert_exit_zero "just guide-check exits 0" just guide-check
    assert_exit_zero "guide-map unit test passes" \
        uv run pytest tests/test_guide_map.py -q
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

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1573 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  checks:  rendered guide output + guide-check + unit test (no DB writes)\n'

    # Capture rendered guide output once. uv run uses the worktree source so the
    # guide command renders THIS worktree's docs/guide/*.md (the global tool
    # reads the main checkout). --db main is harmless: guide reads from disk.
    GUIDE_ORCH=$(uv run endless guide orchestration --db main 2>&1)
    GUIDE_INDEX=$(uv run endless guide --db main 2>&1)
    GUIDE_LIST=$(uv run endless guide --list --db main 2>&1)

    # Slice the "Spawning another Claude session" section so content and id-free
    # checks scope to the rewrite, not the whole page (which also covers
    # worktrees/shell-helpers/channels).
    SPAWN_SECTION=$(printf '%s\n' "${GUIDE_ORCH}" \
        | awk '/^## Spawning another Claude session/{f=1} f{print} f&&/^## Inter-session channels/{exit}')

    # Slice the coordinator subsection for the six-mode check.
    COORDINATOR_SECTION=$(printf '%s\n' "${SPAWN_SECTION}" \
        | awk '/^### Coordinator pattern for epics/{f=1} f{print} f&&/^### Throttle warning/{exit}')

    test_subsections_render
    test_foreground_vs_background
    test_per_type_variants
    test_attach_verbs
    test_coordinator_modes
    test_throttle_warning
    test_session_lifecycle
    test_template_customization
    test_stale_reference_gone
    test_no_task_ids
    test_index_rows
    test_slug_present
    test_map_and_tests

    summary
}

main "$@"
