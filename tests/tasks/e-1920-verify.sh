#!/usr/bin/env bash
#
# E-1920 verification script — superseded and obsolete end states for decisions.
#
# The problem: a decision's vocabulary ended at accepted|rejected, so a rule
# that stopped governing years ago still read as current. ED-1472 was cited
# confidently in an E-1917 session while the user believed it had been
# overtaken, and nothing in the ledger could settle it either way.
#
# Two end states, split by WHY it stopped:
#   superseded — a newer decision took over. The successor is named by a
#                `supersedes` relation, so `decision show` and `decision list`
#                can print it. A status alone cannot carry a pointer, which is
#                why supersede writes two facts, not one.
#   obsolete   — it stopped applying with no replacement. `--reason` is
#                required: it is the only thing separating a rule retired
#                deliberately from one that quietly stopped being mentioned.
#
# Both are reachable ONLY from `accepted` — only an accepted decision governs,
# so only an accepted one can stop — which is what lets a single reversal
# (`reinstate`) be unambiguous, with one destination and no stored prior status.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1920-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, plus a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB, ledger, cache or
# log is touched. The end-to-end arms drive the real CLI, so every write goes
# through the Go event executor built into this worktree's bin/.
#
# Fail-fast: the Go executor tests and the Python CLI tests run FIRST. They pin
# the status guards, the payload validation and the renderer output at a
# granularity the shell cannot reach, so if they fail there is no point running
# the end-to-end checks.

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
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}
# assert_fails DESC NEEDLE COMMAND...  — the command must exit non-zero AND
# its message must name NEEDLE. A guard that refuses for the wrong reason is
# not a guard.
assert_fails() {
    local desc="$1" needle="$2"; shift 2
    local out rc
    out="$("$@" 2>&1)"; rc=$?
    if [[ $rc -eq 0 ]]; then
        report_fail "$desc" "a refusal mentioning: $needle" "succeeded: $out"
    elif [[ "$out" != *"$needle"* ]]; then
        report_fail "$desc" "a refusal mentioning: $needle" "$out"
    else
        report_pass "$desc"
    fi
}

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; EGO=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
# D: the decision surface under test.
D() { E decision "$@"; }
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }

# STATUS ID: the stored status of a decision, read from the DB rather than
# scraped from output — the row is what every other reader sees.
STATUS() { Q "SELECT status FROM decisions WHERE id=$1"; }
# SUPERSEDERS ID: ids of the decisions holding a `supersedes` row AT this one.
# coalesce, not a bare group_concat: over zero rows it returns NULL, which
# --tsv renders as the string "None" — an absent relation would then compare
# unequal to "" and the check would fail for the wrong reason.
SUPERSEDERS() {
    Q "SELECT coalesce(group_concat(source_decision_id),'') FROM decision_relations
        WHERE target_kind='decision' AND relation_type='supersedes'
          AND target_id=$1"
}
OBSOLETE_REASON() { Q "SELECT coalesce(obsolete_reason,'') FROM decisions WHERE id=$1"; }

# NEW_DECISION TITLE: add a decision, print its numeric id.
NEW_DECISION() {
    D add "$1" --description "seeded by e-1920-verify" 2>/dev/null \
        | grep -oE 'ED-[0-9]+' | head -1 | tr -d 'ED-'
}

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$TMP/xdg/endless"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go \
        --status active >/dev/null 2>&1

    [[ -n "$(Q "SELECT id FROM projects WHERE name='probe'")" ]] || return 1
    return 0
}

teardown_fixture() { [[ -n "$TMP" && -d "$TMP" ]] && rm -rf "$TMP"; }

# ─── locate the worktree + binary ───────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EGO="$WT/bin/endless-go"

if [[ ! -x "$EGO" ]]; then
    printf '%sSETUP FAILED%s: %s not built. Run `just build` first.\n' \
        "${RED}" "${RESET}" "$EGO" >&2
    exit 2
fi

# ─── 0. fail-fast unit layer ────────────────────────────────────────────────

section "Executor + CLI unit tests (fail-fast)"

if go_out="$(cd "$WT" && go test ./internal/events/ -run 'TestDecision' 2>&1)"; then
    report_pass "go test ./internal/events -run TestDecision"
else
    report_fail "go test ./internal/events -run TestDecision" "all tests pass" "$go_out"
    summary
    exit 1
fi

if py_out="$(cd "$WT" && uv run --project "$WT" python -m pytest \
        tests/test_decision_cmd.py -q 2>&1)"; then
    report_pass "pytest tests/test_decision_cmd.py"
else
    report_fail "pytest tests/test_decision_cmd.py" "all tests pass" "$py_out"
    summary
    exit 1
fi

# The schema change file must be syntactically sound and self-contained; it is
# the only thing that brings an EXISTING ledger up to the new column, and it
# runs exactly once, at land time, where a compile error is expensive.
if vet_out="$(cd "$WT" && go vet \
        ./internal/schema/changes/e-1920-add-decisions-obsolete-reason.go 2>&1)"; then
    report_pass "go vet the obsolete_reason change file"
else
    report_fail "go vet the obsolete_reason change file" "clean" "$vet_out"
    summary
    exit 1
fi

# ─── end-to-end ─────────────────────────────────────────────────────────────

if ! setup_fixture; then
    printf '%sSETUP FAILED%s: could not build the isolated fixture.\n' "${RED}" "${RESET}" >&2
    teardown_fixture
    exit 2
fi
trap teardown_fixture EXIT

OLD="$(NEW_DECISION 'Original rule that will be overtaken')"
NEW="$(NEW_DECISION 'Replacement rule that takes over')"
GONE="$(NEW_DECISION 'Rule whose subject is deleted')"
# LIVE stays accepted for the whole run. The guard arm needs a decision that
# is genuinely still governing to aim at; reusing one the earlier arms retired
# would trip a different guard first and the check would pass vacuously.
LIVE="$(NEW_DECISION 'Rule that stays in force throughout')"

if [[ -z "$OLD" || -z "$NEW" || -z "$GONE" || -z "$LIVE" ]]; then
    printf '%sSETUP FAILED%s: could not seed decisions (%s/%s/%s/%s).\n' \
        "${RED}" "${RESET}" "$OLD" "$NEW" "$GONE" "$LIVE" >&2
    exit 2
fi

section "Arm 1 — supersede records BOTH facts"

# The precondition the whole feature rests on: only an accepted decision can
# stop governing. Retire it before it is accepted and the guard must fire.
assert_fails "a proposed decision cannot be superseded" "never accepted" \
    D supersede "ED-$OLD" --by "ED-$NEW"
assert_fails "...nor obsoleted" "never accepted" \
    D obsolete "ED-$OLD" --reason "x"

D accept "ED-$OLD" >/dev/null 2>&1
D accept "ED-$GONE" >/dev/null 2>&1
D accept "ED-$LIVE" >/dev/null 2>&1
assert_eq "the decision is accepted and governing" "accepted" "$(STATUS "$OLD")"

sup_out="$(D supersede "ED-$OLD" --by "ED-$NEW" 2>&1)"
assert_eq "supersede sets the status" "superseded" "$(STATUS "$OLD")"
assert_eq "...and names the successor in decision_relations" "$NEW" "$(SUPERSEDERS "$OLD")"
assert_contains "...and says so on stdout" "accepted → superseded" "$sup_out"

# The successor is untouched — supersession is a statement about the OLD one.
assert_eq "the superseding decision keeps its own status" "proposed" "$(STATUS "$NEW")"

section "Arm 2 — the successor is recoverable from every read surface"

# This is the whole point of the task. A `superseded` row whose replacement
# cannot be named is exactly the dead end ED-1472 left behind.
show_llm="$(D show "ED-$OLD" --llm 2>&1)"
assert_contains "decision show --llm reports the status" "status=superseded" "$show_llm"
assert_contains "...and names the successor" "superseded_by=ED-$NEW" "$show_llm"

show_human="$(D show "ED-$OLD" 2>&1)"
assert_contains "decision show gives the successor its own line" "Superseded:" "$show_human"
assert_contains "...naming the replacement" "ED-$NEW" "$show_human"

show_json="$(D show "ED-$OLD" --json 2>&1)"
json_sup="$(printf '%s' "$show_json" | python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["superseded_by"]))')"
assert_eq "decision show --json carries superseded_by" "ED-$NEW" "$json_sup"

list_llm="$(D list --llm 2>&1)"
assert_contains "decision list annotates the retired row inline" \
    "superseded (by ED-$NEW)" "$list_llm"

section "Arm 3 — obsolete records what went away"

assert_fails "obsolete without a reason is refused" "reason" \
    D obsolete "ED-$GONE"

obs_out="$(D obsolete "ED-$GONE" --reason "the subsystem it governed was deleted" 2>&1)"
assert_eq "obsolete sets the status" "obsolete" "$(STATUS "$GONE")"
assert_eq "...and stores the reason on the row" \
    "the subsystem it governed was deleted" "$(OBSOLETE_REASON "$GONE")"
assert_contains "...and says so on stdout" "accepted → obsolete" "$obs_out"

obs_show="$(D show "ED-$GONE" 2>&1)"
assert_contains "decision show renders the reason" "the subsystem it governed was deleted" \
    "$obs_show"

# An obsolete decision has no successor — that is the distinction from
# superseded, and the renderer must not invent one.
obs_llm="$(D show "ED-$GONE" --llm 2>&1)"
assert_not_contains "an obsolete decision names no successor" "superseded_by=" "$obs_llm"
assert_contains "...but carries its reason for the LLM surface too" \
    "obsolete_reason=" "$obs_llm"

section "Arm 4 — the guards refuse, and say what to do instead"

assert_fails "an already-retired decision cannot be retired again" "already" \
    D supersede "ED-$OLD" --by "ED-$NEW"
assert_fails "a decision cannot supersede itself" "itself" \
    D supersede "ED-$OLD" --by "ED-$OLD"
assert_fails "a retired decision cannot supersede anything" "does not govern" \
    D supersede "ED-$LIVE" --by "ED-$GONE"

# The two reversals do not overlap, and each points at the other. Getting this
# wrong would let `reconsider` silently discard the `accepted` that a mis-aimed
# supersede should return to.
assert_fails "reconsider refuses a retired decision, pointing at reinstate" "reinstate" \
    D reconsider "ED-$OLD"
assert_fails "reinstate refuses a live decision, pointing at reconsider" "reconsider" \
    D reinstate "ED-$LIVE"

section "Arm 5 — reinstate undoes a retirement completely"

rein_out="$(D reinstate "ED-$OLD" 2>&1)"
assert_eq "reinstate returns a superseded decision to accepted, not proposed" \
    "accepted" "$(STATUS "$OLD")"
assert_contains "...and says which end state it undid" "superseded → accepted" "$rein_out"

# The relation must go with it. A decision back in force still carrying a
# successor is the same contradiction, inverted.
assert_eq "...and retires the supersedes relation" "" "$(SUPERSEDERS "$OLD")"

back_llm="$(D show "ED-$OLD" --llm 2>&1)"
assert_not_contains "the reinstated decision names no successor" "superseded_by=" "$back_llm"

D reinstate "ED-$GONE" >/dev/null 2>&1
assert_eq "reinstate returns an obsolete decision to accepted" "accepted" "$(STATUS "$GONE")"
assert_eq "...and clears the stored reason" "" "$(OBSOLETE_REASON "$GONE")"

# Round-trip: a reinstated decision must be genuinely re-retireable, not stuck
# in a half-state the forward verbs then refuse.
D supersede "ED-$OLD" --by "ED-$NEW" >/dev/null 2>&1
assert_eq "a reinstated decision can be retired again" "superseded" "$(STATUS "$OLD")"
assert_eq "...with the successor recorded afresh" "$NEW" "$(SUPERSEDERS "$OLD")"

assert_eq "every refusal above left its subject untouched" "accepted" "$(STATUS "$LIVE")"

section "Arm 6 — the vocabulary is wired through the relation surface"

# `supersedes` has to be a first-class link type, not a string the status verb
# happens to write: `decision link` is how a user records one by hand, and
# `decision show` reads it through the same path as every other relation.
assert_contains "supersedes is offered by decision link --help" "supersedes" \
    "$(D link --help 2>&1)"

# It is a statement about which RULE governs, so it is meaningless toward a
# task and must be refused there.
assert_fails "supersedes is refused toward a task" "not legal" \
    D link "ED-$NEW" --to "E-1" --type supersedes

summary
