#!/usr/bin/env bash
#
# E-2014 verification script — CLAUDE.md is the minimized WHAT-only rewrite,
# every rule left in it is true, and nothing it dropped was lost.
#
# What changed. CLAUDE.md is loaded into every session's context in this repo
# unconditionally, so its length is a permanent per-session token tax. ED-1564
# fixed what it may hold: only what is true HERE and nowhere else, stated as
# WHAT the agent must do — no rationale, no history, no mechanism, no task ids.
# Workflow goes to `endless guide`, which is pulled on demand and reaches every
# project; rationale goes to an accepted decision. E-1817 audited the 440-line
# file against that rule and proposed the replacement this task applies.
#
# The failure this has to prove is closed is not "the file is long" — it is that
# a long file goes stale in ways nobody notices. E-1817 found three rules live in
# CLAUDE.md at once that named commands which no longer exist or steered an agent
# away from the supported path: `just install` "never from a worktree" (false
# since E-1036 made it worktree-scoped), `/usr/local/bin/endless-hook` and
# `bin/endless-hook` (consolidated into `endless-go`), and `endless-sandbox
# destroy` (same). Every check below that resolves a command is aimed at that
# class, not at the prose.
#
# Run from anywhere inside the worktree:
#   endless task verify E-2014
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: read-only. Nothing here writes to a database, a ledger, a cache or
# the filesystem. The one command that reaches the real ledger (`--db main`) is
# a `--help`, which resolves the flag without opening the DB.
#
# Fail-fast: `tests/test_claude_md_rules.py` runs FIRST. It carries the permanent
# invariants — the size budget, no task ids, no canonical block copied back in,
# every `just` recipe resolvable, and the direct-SQL file count kept honest — at
# a granularity this script cannot reach, and it outlives this script, which is a
# pre-land gate and retires when E-2014 lands.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

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
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}" "${RESET}"
        return 0
    fi
    printf '  %d passed, %s%d failed%s\n' "${PASS_COUNT}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    %s✗%s %s\n' "${RED}" "${RESET}" "$t"; done
    printf '\n'
    return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_absent DESC NEEDLE — the needle must not appear in CLAUDE.md
assert_absent() {
    if ! grep -qF -- "$2" "$MD"; then report_pass "$1"
    else report_fail "$1" "CLAUDE.md does not mention: $2" "$(grep -nF -- "$2" "$MD")"; fi
}

# ─── locate the worktree ────────────────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MD="$WT/CLAUDE.md"
EGO="$WT/bin/endless-go"

if [[ ! -f "$MD" ]]; then
    printf '%sSETUP FAILED%s: %s is missing.\n' "${RED}" "${RESET}" "$MD" >&2
    exit 2
fi
if [[ ! -x "$EGO" ]]; then
    printf '%sSETUP FAILED%s: %s not built. Run `just build` first.\n' \
        "${RED}" "${RESET}" "$EGO" >&2
    exit 2
fi

# The Python CLI, run from the worktree's own source rather than the global
# install, so this gates the candidate code and not what happens to be on PATH.
E() { ( cd "$WT" && uv run --project "$WT" endless "$@" ); }

# ─── 0. fail-fast unit layer ────────────────────────────────────────────────

section "The permanent invariants, unit level (fail-fast)"

if py_out="$(cd "$WT" && uv run --project "$WT" python -m pytest tests/test_claude_md_rules.py -q 2>&1)"; then
    report_pass "pytest tests/test_claude_md_rules.py"
else
    report_fail "pytest tests/test_claude_md_rules.py" "all tests pass" "$py_out"
    summary
    exit 1
fi

# ─── the shape of the file ──────────────────────────────────────────────────

section "What CLAUDE.md now is"

LINES="$(wc -l < "$MD" | tr -d ' ')"

# The size claim, measured against the version this branch replaced rather than
# hardcoded — the point is the cut, not a magic number.
FORK="$(cd "$WT" && git merge-base HEAD main 2>/dev/null)"
if [[ -n "$FORK" ]] && OLD_MD="$(cd "$WT" && git show "$FORK:CLAUDE.md" 2>/dev/null)"; then
    OLD_LINES="$(printf '%s\n' "$OLD_MD" | wc -l | tr -d ' ')"
    if (( OLD_LINES >= 300 && LINES * 5 < OLD_LINES )); then
        report_pass "the replacement is a >80% cut ($OLD_LINES → $LINES lines)"
    else
        report_fail "the replacement is a >80% cut" \
            "fork point >= 300 lines, HEAD < a fifth of it" \
            "fork point $OLD_LINES lines, HEAD $LINES lines"
    fi
else
    report_fail "the replacement is a >80% cut" \
        "CLAUDE.md readable at the fork point with main" "could not resolve it"
fi

# The six sections ED-1564 leaves standing, in order. A seventh means something
# was added — which the file's own first rule forbids without Mike's say-so.
assert_eq "exactly the six approved sections remain, in order" \
"## The ledger is durable state
## Python and Go
## Build
## Your worktree and its database
## Memory is OFF here
## PRODUCT" \
"$(grep '^## ' "$MD")"

assert_contains "it opens by pointing at the guide" \
    "Run \`endless guide\` first." "$(head -5 "$MD")"
assert_contains "...and forbids growing itself without permission" \
    "Do not add anything to CLAUDE.md without explicit permission" "$(head -8 "$MD")"

# ─── what it no longer says ─────────────────────────────────────────────────

section "The three stale rules E-1817 found are gone"

assert_absent "no \`endless-hook\` binary (consolidated into endless-go)" "endless-hook"
assert_absent "no \`endless-sandbox\` binary (same)" "endless-sandbox"
assert_absent "no 'run just install from the main checkout, never from a worktree'" \
    "never from a worktree"

section "...along with the mechanism prose that belongs elsewhere"

assert_absent "no by-hand \`git worktree add\` recipe" "git worktree add"
assert_absent "no \`uv tool install\` incantation" "uv tool install -e"
assert_absent "no duplicated canonical block" "<!-- BEGIN canonical:"

# ─── every command it still names resolves ──────────────────────────────────

section "Every command CLAUDE.md still names resolves"

# `just` recipes are covered statically by the pytest module; these are the
# commands only a running binary can answer for.
guide_out="$(E guide 2>&1)"
assert_contains "\`endless guide\` runs" "Using Endless in a Claude Code Session" "$guide_out"

claim_out="$(E task claim --help 2>&1)"
assert_contains "\`endless task claim\` exists" "Usage: endless task claim" "$claim_out"

event_out="$("$EGO" event 2>&1)"
assert_contains "\`endless-go event emit\` exists" "emit" "$event_out"

# ─── the rules are true ─────────────────────────────────────────────────────

section "The ledger is durable state — as stated"

ledger_tracked="$(cd "$WT" && git ls-files '.endless/db-ledger/*.jsonl' | head -1)"
assert_contains "\`.endless/db-ledger/*.jsonl\` exists and is tracked by git" \
    ".endless/db-ledger/" "$ledger_tracked"
assert_eq "\`.endless/verbs.jsonl\` exists and is tracked by git" \
    ".endless/verbs.jsonl" "$(cd "$WT" && git ls-files '.endless/verbs.jsonl')"
assert_eq "\`.endless/\` is not gitignored" \
    "" "$(cd "$WT" && git check-ignore .endless/verbs.jsonl 2>&1)"

section "Python and Go — as stated"

assert_eq "writes go through the event pipeline, from Python" \
    "1" "$(grep -c '"event", "emit"' "$WT/src/endless/event_bridge.py")"

section "Your worktree and its database — as stated"

# The rule an agent gets wrong most expensively: a bare `endless` command in a
# self-dev worktree does NOT reach the real ledger, and the refusal has to name
# the flag that does.
bare_out="$(E task next 2>&1)"
assert_contains "a bare \`endless\` command in this worktree refuses to touch the real ledger" \
    "requires an explicit --db value" "$bare_out"
assert_contains "...and the refusal names \`--db main\`" "--db main" "$bare_out"

db_help="$(E task next --db main --help 2>&1)"
assert_contains "\`--db main\` is accepted in any position" \
    "Usage: endless task next" "$db_help"

section "Build — as stated"

assert_eq "\`just build\` puts binaries in ./bin/" \
    "endless-go" "$(basename "$EGO")"

# ─── nothing was lost, only relocated ───────────────────────────────────────

section "What CLAUDE.md dropped is still reachable in \`endless guide\`"

# ED-1564's load-bearing half: the cut is only safe if the guide already carries
# the workflow the file used to restate. Each topic below had a section in the
# 440-line CLAUDE.md.
assert_contains "the status lifecycle" "stateDiagram-v2" "$guide_out"
assert_contains "worktrees and landing" "worktree land" "$(E guide orchestration 2>&1)"
assert_contains "the ledger's durability" "durable state" "$(E guide reference 2>&1)"

summary
