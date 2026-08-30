#!/usr/bin/env bash
#
# E-1737 verification — --db must be REJECTED in a project that is not self-dev,
# instead of silently mkdir'ing a stray sandbox endless.db in the cache.
#
# Run from inside the worktree:
#   ./tests/tasks/e-1737-verify.sh
#
# What it proves:
#   1. FIX — in a non-self_dev project (its worktree AND its main checkout),
#      apply_db_choice() rejects BOTH --db main and --db sandbox with the plain,
#      ticket-free refusal, and creates NO stray endless.db on disk.
#   2. NO REGRESSION — in a self_dev project, --db sandbox (in a worktree) and
#      --db main (worktree or main checkout) still resolve correctly.
#   3. DECOUPLING — default_db_to_main() (the forced-main pin used by
#      land/backup/apply-change, which run in downstream non-self_dev projects)
#      still pins main and is NOT blocked by the new --db self-dev gate.
#   4. END-TO-END — the committed regression tests in tests/test_db_gate.py pass.
#
# Safe: every DB write is confined to a throwaway XDG_CACHE_HOME under a mktemp
# dir that is removed on exit. Nothing touches the real or sandbox ledgers.

set -u

# ─── output ──────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()    { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass(){ printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT+1)); }
report_fail(){
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT+1)); FAILED_TESTS+=("$1")
}
summary(){
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
    printf '\n'; return 1
}

# ─── environment ─────────────────────────────────────────────────────────────

# Worktree root (this script lives at <root>/tests/tasks/).
WORKTREE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${WORKTREE_ROOT}" || { echo "cannot cd to worktree root"; exit 1; }

# Run the WORKTREE's candidate source, not the global `endless` (which is main's
# editable install). PYTHONPATH points imports at this worktree's src/; the venv
# python supplies the dependencies (click, etc.). Materialize the venv if a fresh
# worktree hasn't built one yet.
PY="${WORKTREE_ROOT}/.venv/bin/python"
if [[ ! -x "${PY}" ]]; then
    ( cd "${WORKTREE_ROOT}" && uv run python -c 'pass' ) >/dev/null 2>&1
fi
if [[ ! -x "${PY}" ]]; then
    echo "no worktree venv python at ${PY}; run 'uv run python -c pass' first"; exit 1
fi
export PYTHONPATH="${WORKTREE_ROOT}/src"

# Throwaway fixture: a non-self_dev ("down") project and a self_dev ("sd")
# project, each with a worktree-shaped dir, plus an isolated cache so any stray
# DB lands here and is asserted-absent / cleaned up.
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT
export XDG_CACHE_HOME="${TMP_ROOT}/cache"

DOWN_ROOT="${TMP_ROOT}/down"
DOWN_WT="${DOWN_ROOT}/.endless/worktrees/e-999"
mkdir -p "${DOWN_WT}"
printf '{"self_dev": false}\n' > "${DOWN_ROOT}/.endless/config.json"

SD_ROOT="${TMP_ROOT}/sd"
SD_WT="${SD_ROOT}/.endless/worktrees/e-111"
mkdir -p "${SD_WT}"
printf '{"self_dev": true}\n' > "${SD_ROOT}/.endless/config.json"

MAIN_CFG="${HOME}/.config/endless"

# pyrun <cwd> <python-source> — run the worktree source with a chosen cwd (which
# is what apply_db_choice keys its project resolution off of).
pyrun(){ ( cd "$1" && "${PY}" -c "$2" 2>&1 ); }

# ─── 1. the fix: non-self_dev projects reject --db, create no stray DB ────────

section "1. Fix — non-self_dev project rejects --db (both values), no stray DB"

# --db sandbox in a non-self_dev worktree: was the bug (silent stray sandbox DB).
# On (correct) rejection nothing is opened; on a regression that ACCEPTS, we open
# the DB so the stray file appears under XDG_CACHE_HOME and the find check fails.
out="$(pyrun "${DOWN_WT}" '
from endless import config
try:
    config.apply_db_choice("sandbox")
except ValueError as e:
    print("REJECTED", e)
else:
    from endless import db
    try:
        db.get_db()
    except Exception:
        pass
    print("ACCEPTED", config.DB_PATH, config.DB_PATH.exists())
')"
if [[ "${out}" == REJECTED*self-dev* ]]; then
    report_pass "--db sandbox rejected in a non-self_dev worktree (the bug)"
else
    report_fail "--db sandbox rejected in a non-self_dev worktree" \
        "REJECTED ... self-dev ..." "${out}"
fi

# --db main in the same non-self_dev worktree.
out="$(pyrun "${DOWN_WT}" '
from endless import config
try:
    config.apply_db_choice("main")
    print("ACCEPTED", config.RESOLVED_CONFIG_DIR)
except ValueError as e:
    print("REJECTED", e)
')"
if [[ "${out}" == REJECTED*self-dev* ]]; then
    report_pass "--db main rejected in a non-self_dev worktree"
else
    report_fail "--db main rejected in a non-self_dev worktree" \
        "REJECTED ... self-dev ..." "${out}"
fi

# --db main from the non-self_dev project's MAIN checkout (not just worktrees).
out="$(pyrun "${DOWN_ROOT}" '
from endless import config
try:
    config.apply_db_choice("main")
    print("ACCEPTED", config.RESOLVED_CONFIG_DIR)
except ValueError as e:
    print("REJECTED", e)
')"
if [[ "${out}" == REJECTED*self-dev* ]]; then
    report_pass "--db main rejected in a non-self_dev main checkout"
else
    report_fail "--db main rejected in a non-self_dev main checkout" \
        "REJECTED ... self-dev ..." "${out}"
fi

# Filesystem proof: no endless.db was created anywhere under the temp cache.
stray="$(find "${XDG_CACHE_HOME}" -name endless.db 2>/dev/null)"
if [[ -z "${stray}" ]]; then
    report_pass "no stray endless.db created in the cache"
else
    report_fail "no stray endless.db created in the cache" "(none)" "${stray}"
fi

# ─── 2. no regression: self_dev projects still resolve --db ───────────────────

section "2. No regression — self_dev project still resolves --db main | sandbox"

out="$(pyrun "${SD_WT}" '
from endless import config
config.apply_db_choice("sandbox")
print(config.RESOLVED_CONFIG_DIR)
')"
if [[ "${out}" == *"sandboxes/e-111/endless" ]]; then
    report_pass "--db sandbox resolves the worktree sandbox in a self_dev worktree"
else
    report_fail "--db sandbox resolves the sandbox in a self_dev worktree" \
        "...sandboxes/e-111/endless" "${out}"
fi

out="$(pyrun "${SD_WT}" '
from endless import config
config.apply_db_choice("main")
print(config.RESOLVED_CONFIG_DIR)
')"
if [[ "${out}" == "${MAIN_CFG}" ]]; then
    report_pass "--db main resolves the real ledger in a self_dev worktree"
else
    report_fail "--db main resolves the real ledger in a self_dev worktree" \
        "${MAIN_CFG}" "${out}"
fi

out="$(pyrun "${SD_ROOT}" '
from endless import config
config.apply_db_choice("main")
print(config.RESOLVED_CONFIG_DIR)
')"
if [[ "${out}" == "${MAIN_CFG}" ]]; then
    report_pass "--db main resolves the real ledger in a self_dev main checkout"
else
    report_fail "--db main resolves the real ledger in a self_dev main checkout" \
        "${MAIN_CFG}" "${out}"
fi

# ─── 3. decoupling: forced-main pin is not blocked by the gate ────────────────

section "3. Decoupling — default_db_to_main() pins main in a non-self_dev worktree"

out="$(pyrun "${DOWN_WT}" '
from endless import config
config.default_db_to_main()   # must NOT raise the --db self-dev refusal
print(config.RESOLVED_CONFIG_DIR)
')"
if [[ "${out}" == "${MAIN_CFG}" ]]; then
    report_pass "default_db_to_main() pins main (land/backup/apply-change stay usable downstream)"
else
    report_fail "default_db_to_main() pins main in a non-self_dev worktree" \
        "${MAIN_CFG}" "${out}"
fi

# ─── 4. committed regression tests ───────────────────────────────────────────

section "4. Regression suite — tests/test_db_gate.py"

test_out="$(cd "${WORKTREE_ROOT}" && uv run pytest tests/test_db_gate.py -q 2>&1)"
test_rc=$?
last_line="$(printf '%s\n' "${test_out}" | tail -1)"
if [[ "${test_rc}" -eq 0 ]]; then
    report_pass "tests/test_db_gate.py passes (${last_line})"
else
    report_fail "tests/test_db_gate.py passes" "exit 0" "exit=${test_rc} | ${last_line}"
fi

summary
