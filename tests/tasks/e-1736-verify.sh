#!/usr/bin/env bash
#
# E-1736 verification — worktree land refuses when the task branch's
# commits touch .endless/db-ledger/.
#
# Ledger entries are auto-committed on the main checkout only (the ED-1525
# routing policy); a branch-side commit under .endless/db-ledger/ would ride
# the land rebase into main and corrupt the shared DB history. Land adds a
# deterministic gate (Step 3.75) that runs AFTER Step 3.7's orphan-drop and
# refuses anything still touching the ledger dir.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1736-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Why the gate is exercised at the helper level, not via full `endless
# worktree land`: land_worktree() opens with config.default_db_to_main() and
# resolves the REAL project root, so it cannot be driven against a throwaway
# repo without touching the real DB/main checkout. The gate's decision is a
# pure function of git state — _ledger_touching_commits(worktree, base) — so
# the sections below construct the exact branch shapes land would see at the
# gate (running Step 3.7's _drop_orphan_amendable_commits first where land
# does) and assert the helper's verdict. Sections A–D cover the four shapes;
# section E runs the pytests as a no-regression check.
#
# Everything runs isolated: throwaway git repos under mktemp, cleaned up on
# exit. No real DB, ledger, or main checkout is touched.

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# ─── git fixture helpers ─────────────────────────────────────────────────────

TMP=""

git_q()  { git -C "$1" "${@:2}" >/dev/null 2>&1; }

# commit REPO MSG REL_PATH CONTENT — write one file and commit it.
commit() {
    local repo="$1" msg="$2" rel="$3" content="$4"
    mkdir -p "$(dirname "$repo/$rel")"
    printf '%s' "$content" > "$repo/$rel"
    git_q "$repo" add -A
    git_q "$repo" commit -q -m "$msg"
}

# new_case NAME — make a fresh main repo + task worktree forked from main.
# Sets globals MAIN and WT for the caller. Each case gets its own subdir so
# worktrees never collide.
new_case() {
    local name="$1"
    local root="$TMP/$name"
    MAIN="$root/main"
    WT="$root/wt"
    mkdir -p "$MAIN"
    git_q "$MAIN" init -q -b main
    git_q "$MAIN" config user.email verify@e1736
    git_q "$MAIN" config user.name e1736-verify
    commit "$MAIN" "init" "README.md" $'init\n'
    git_q "$MAIN" worktree add -q "$WT" -b "task/$name" main
}

# amend_ledger REPO REL EXTRA — simulate canAmend rewriting the ledger commit:
# append to the file and `git commit --amend` (SHA changes, subject stays).
amend_ledger() {
    local repo="$1" rel="$2" extra="$3"
    printf '%s' "$(cat "$repo/$rel")$extra" > "$repo/$rel"
    git_q "$repo" add -A
    git_q "$repo" commit -q --amend --no-edit
}

LEDGER_SUBJECT="Endless: record ledger entry"

# gate_count WT BASE — number of ledger-touching commits the land gate sees.
# Invokes the real helper the gate uses, so this is the production verdict.
gate_count() {
    ( cd "$REPO_ROOT" && uv run python -c "
import sys
from pathlib import Path
from endless.worktree_cmd import _ledger_touching_commits
print(len(_ledger_touching_commits(Path('$1'), '$2')))
" 2>/dev/null )
}

# step37_then_gate_count WT BASE — run Step 3.7's orphan-drop (exactly as land
# does) and THEN report the gate's offender count. This is the sequence land
# executes, so it proves the gate's verdict on the post-3.7 branch shape.
step37_then_gate_count() {
    ( cd "$REPO_ROOT" && uv run python -c "
from pathlib import Path
from endless.worktree_cmd import (
    _drop_orphan_amendable_commits, _ledger_touching_commits,
)
wt, base = Path('$1'), '$2'
_drop_orphan_amendable_commits(wt, base)
print(len(_ledger_touching_commits(wt, base)))
" 2>/dev/null )
}

# ─── section A: clean branch — no false positive ─────────────────────────────

test_clean_branch() {
    section "A. Clean branch — gate reports no offenders"
    new_case clean
    commit "$WT" "user work" "hello.txt" $'hello\n'
    assert_eq "user-only branch: gate count is 0" "0" "$(gate_count "$WT" main)"
}

# ─── section B: standalone branch-side ledger commit — refused ───────────────

test_standalone_ledger() {
    section "B. Branch-side ledger commit — gate reports 1 offender (refusal)"
    new_case standalone
    commit "$WT" "user work" "hello.txt" $'hello\n'
    commit "$WT" "$LEDGER_SUBJECT" ".endless/db-ledger/x.jsonl" $'{"a":1}\n'
    assert_eq "branch-side ledger commit: gate count is 1" \
        "1" "$(gate_count "$WT" main)"
}

# ─── section C: orphan false-positive — dropped by Step 3.7, gate clean ──────

test_orphan_no_false_positive() {
    section "C. Orphan ledger commit (E-1342 shape) — Step 3.7 drops it, gate clean"
    new_case orphan
    # Recreate: main has a ledger commit, branch forks off it, main amends it
    # so the branch's base copy becomes an orphan touching the ledger.
    # (new_case forked at plain init; redo the fork off a ledger tip.)
    local root="$TMP/orphan"
    rm -rf "$root"; mkdir -p "$root/main"
    MAIN="$root/main"; WT="$root/wt"
    git_q "$MAIN" init -q -b main
    git_q "$MAIN" config user.email verify@e1736
    git_q "$MAIN" config user.name e1736-verify
    commit "$MAIN" "init" "README.md" $'init\n'
    commit "$MAIN" "$LEDGER_SUBJECT" ".endless/db-ledger/x.jsonl" $'{"a":1}\n'
    git_q "$MAIN" worktree add -q "$WT" -b task/orphan main
    commit "$WT" "user work" "hello.txt" $'hello\n'
    amend_ledger "$MAIN" ".endless/db-ledger/x.jsonl" $'{"a":2}\n'

    # Before Step 3.7 the orphan is a ledger-touching commit in main..HEAD.
    assert_eq "pre-3.7: orphan appears as 1 ledger commit" \
        "1" "$(gate_count "$WT" main)"
    # After Step 3.7's drop (what land runs first), the gate must see none.
    assert_eq "post-3.7: gate count is 0 (no false positive)" \
        "0" "$(step37_then_gate_count "$WT" main)"
}

# ─── section D: mid-branch ledger commit — survives 3.7, caught by gate ──────

test_mid_branch_ledger() {
    section "D. Mid-branch ledger commit — survives Step 3.7, gate catches it"
    new_case midbranch
    commit "$WT" "user work" "hello.txt" $'hello\n'
    commit "$WT" "$LEDGER_SUBJECT" ".endless/db-ledger/x.jsonl" $'{"a":1}\n'
    # Step 3.7 breaks at the base user commit and drops nothing; the gate
    # must still refuse the mid-branch ledger commit.
    assert_eq "post-3.7: gate count is 1 (mid-branch ledger caught)" \
        "1" "$(step37_then_gate_count "$WT" main)"
}

# ─── section E: pytest no-regression ─────────────────────────────────────────

test_pytests() {
    section "E. pytest — new gate suite + orphan-drop no-regression"
    local out rc
    out=$( cd "$REPO_ROOT" && uv run pytest -q \
        tests/test_worktree_land_dbledger_gate.py \
        tests/test_worktree_land_orphan_drop.py 2>&1 )
    rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "gate + orphan-drop pytests pass"
    else
        report_fail "gate + orphan-drop pytests" "exit 0" "exit=$rc"$'\n'"$out"
    fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${REPO_ROOT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    command -v git >/dev/null || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    command -v uv  >/dev/null || { printf 'ERROR: uv not on PATH\n'  >&2; exit 2; }

    printf '%sE-1736 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd: %s\n' "${REPO_ROOT}"

    TMP="$(mktemp -d)" || { printf 'ERROR: mktemp failed\n' >&2; exit 2; }

    test_clean_branch
    test_standalone_ledger
    test_orphan_no_false_positive
    test_mid_branch_ledger
    test_pytests

    # Worktrees were created under $TMP; prune their registrations before
    # blowing the dir away so no stale `git worktree` entries linger.
    find "$TMP" -maxdepth 2 -name main -type d 2>/dev/null | while read -r m; do
        git -C "$m" worktree prune >/dev/null 2>&1 || true
    done
    rm -rf "$TMP"
    summary
}

main "$@"
