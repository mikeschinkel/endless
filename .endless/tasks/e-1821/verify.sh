#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1821 and records what was true when E-1821
# landed. Edit it only if you ARE E-1821. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1821 verification — the `esm` shell helper for `endless session monitor`.
#
# Run from inside the worktree (esu puts you there):
#   endless task verify E-1821
#
# What it proves:
#   1. `endless shell-init` (from THIS worktree's source) emits an `esm()`
#      helper that wraps `session monitor`, routes through `_endless_run`, and
#      never shells out to a bare `endless` (worktree-routing invariant).
#   2. The snippet sources cleanly under `set -u` (nounset) — esm must not
#      reintroduce an unguarded ${VAR} read.
#   3. Behaviorally, `esm <args>` forwards exactly `session monitor <args>` to
#      the routing helper — verified by stubbing `_endless_run` and capturing
#      what esm hands it (arg passthrough for --all/--tree, no `eval`).
#   4. The pytest suite tests/test_shell_init.py still passes (regression).
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

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

die() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

# ─── setup: render the snippet from THIS worktree's source ───────────────────

section "Setup — render shell-init from worktree source"
SNIPPET="$(cd "${WT}" && uv run endless shell-init 2>/dev/null)" \
    || die "uv run endless shell-init failed"
[[ -n "${SNIPPET}" ]] || die "shell-init produced empty output"
SNIP_FILE="$(mktemp "/tmp/e1821-snippet.XXXXXX.sh")" || die "mktemp failed"
printf '%s\n' "${SNIPPET}" > "${SNIP_FILE}"
printf '  rendered snippet (%d lines)\n' "$(wc -l < "${SNIP_FILE}" | tr -d ' ')"

# ─── static checks: esm is present and wired correctly ──────────────────────

section "Static — esm() is defined and routes via _endless_run"

if [[ "${SNIPPET}" == *"esm()"* ]]; then
    report_pass "esm() helper is present in the snippet"
else
    report_fail "esm() presence" "snippet contains 'esm()'" "not found"
fi

# Extract just the esm function body (from 'esm()' to its closing '}').
ESM_BODY="${SNIPPET##*esm() \{}"
ESM_BODY="${ESM_BODY%%$'\n}'*}"

if [[ "${ESM_BODY}" == *"session monitor"* ]]; then
    report_pass "esm body invokes 'session monitor'"
else
    report_fail "esm subcommand" "body contains 'session monitor'" "${ESM_BODY}"
fi

if [[ "${ESM_BODY}" == *"_endless_run"* ]]; then
    report_pass "esm routes through _endless_run (worktree-aware CLI)"
else
    report_fail "esm routing" "body contains '_endless_run'" "${ESM_BODY}"
fi

# The worktree-routing invariant: no helper may shell out to a bare `endless`.
if [[ "${ESM_BODY}" != *'$(endless '* ]]; then
    report_pass "esm does not shell out to a bare \$(endless ...)"
else
    report_fail "esm bare-endless" "no '\$(endless ' in body" "${ESM_BODY}"
fi

# monitor prints frames, not shell code — esm must NOT eval its output.
if [[ "${ESM_BODY}" != *"eval"* ]]; then
    report_pass "esm runs in the foreground (no eval — monitor isn't shell code)"
else
    report_fail "esm eval" "no 'eval' in body" "${ESM_BODY}"
fi

# ─── nounset: the snippet sources cleanly under set -u ──────────────────────

section "Robustness — snippet sources under 'set -u' and defines esm"
if OUT="$(zsh -c "set -u; source '${SNIP_FILE}'; typeset -f esm >/dev/null && echo DEFINED" 2>&1)" \
    && [[ "${OUT}" == *DEFINED* ]]; then
    report_pass "sources under nounset; esm is defined (no unbound-variable error)"
else
    report_fail "nounset source" "esm DEFINED, no error" "${OUT}"
fi

# ─── behavioral: esm forwards its args to 'session monitor' verbatim ────────

section "Behavior — esm <args> forwards 'session monitor <args>'"

# Stub _endless_run to echo exactly what esm hands it, then drive esm with a
# couple of arg shapes. This exercises the real esm function body (arg
# passthrough, "$@" quoting) without launching the live 2s dashboard loop.
run_esm() { # ARGS... -> echoes the command esm forwarded to _endless_run
    zsh -c "
        set -u
        source '${SNIP_FILE}'
        _endless_run() { printf '%s\n' \"\$*\"; }   # override the router
        esm \"\$@\"
    " esm-driver "$@"
}

OUT="$(run_esm)"
if [[ "${OUT}" == "session monitor" ]]; then
    report_pass "bare 'esm' -> 'session monitor'"
else
    report_fail "esm passthrough (no args)" "session monitor" "${OUT}"
fi

OUT="$(run_esm --all)"
if [[ "${OUT}" == "session monitor --all" ]]; then
    report_pass "'esm --all' -> 'session monitor --all'"
else
    report_fail "esm passthrough (--all)" "session monitor --all" "${OUT}"
fi

OUT="$(run_esm --tree)"
if [[ "${OUT}" == "session monitor --tree" ]]; then
    report_pass "'esm --tree' -> 'session monitor --tree'"
else
    report_fail "esm passthrough (--tree)" "session monitor --tree" "${OUT}"
fi

# ─── regression: the pytest shell-init suite still passes ───────────────────

section "Regression — tests/test_shell_init.py"
if ( cd "${WT}" && uv run pytest tests/test_shell_init.py -q ) >/tmp/e1821-pytest.log 2>&1; then
    report_pass "tests/test_shell_init.py passes"
else
    report_fail "pytest test_shell_init.py" "all tests pass" "$(tail -5 /tmp/e1821-pytest.log)"
fi

# ─── cleanup + summary ──────────────────────────────────────────────────────

rm -f "${SNIP_FILE}" /tmp/e1821-pytest.log

printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
if [[ "${FAIL_COUNT}" -eq 0 ]]; then
    printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
    exit 0
fi
printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
printf '\n'
exit 1
