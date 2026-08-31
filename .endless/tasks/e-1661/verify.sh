#!/usr/bin/env bash
#
# E-1661 verification script — a failing hook exits 2, so the AGENT sees it.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1661-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# ─── what this proves ───────────────────────────────────────────────────────
#
# Claude Code grades a hook by its exit code, and only exit 2 is fed back into
# the model's context: on PreToolUse it blocks the call and hands the model
# stderr, on PostToolUse it hands the model stderr after the fact. Every other
# non-zero code is a "non-blocking error" — a notice to the USER, and the turn
# continues. Every hook failure used to exit 1, so an agent instructed to stop
# on errors could not: Endless would stop recording the session, say so to a
# human who was probably not looking, and the agent would keep working against
# a database that had stopped tracking it.
#
# The BUG, reproducible on the binary before this branch: seed a DB, diverge its
# task_types enum mirror, fire PostToolUse — the hook prints the integrity
# failure and exits 1. Invisible to the agent. Step 3 below is that exact
# reproduction with the expectation flipped to 2.
#
# ─── why the checks pass --config-dir ───────────────────────────────────────
#
# A production hook is pinned by PinMainDB (cmd/endless-go/main.go), and E-1818
# makes a pinned open SCHEMA-PASSIVE: schema.SQL is not applied and the enum
# integrity gates do not run at all, so a pinned binary cannot fail-close on a
# real DB it does not own. --config-dir routes this binary at a throwaway DB it
# DOES own, which is the only way to reach those gates — and, not incidentally,
# the only way to run this script without writing to the real ledger.
#
# Step 4 covers the failure a real pinned session still hits, and the one that
# replaced the integrity gate as the live stale-binary symptom: the open
# succeeds and the first query naming a column the deployed schema lacks fails.
#
# ─── why Stop is exempt ─────────────────────────────────────────────────────
#
# On Stop, exit 2 does not mean "tell the agent" — it means "refuse to let the
# turn end", and the harness answers by starting another model turn at once. No
# user input, no cap. A hook that exits 2 on every Stop because the database is
# unreachable therefore loops the session until somebody hits Esc, and the model
# is never told why (Stop's stderr goes to the user). The Stop gate's usual loop
# guard is a counter in the database, which is exactly what is missing. So Stop
# keeps the non-blocking exit, and steps 3 and 4 assert that it does.
#
# ─── isolation ──────────────────────────────────────────────────────────────
#
# One mktemp -d holds the throwaway XDG_CONFIG_HOME (so even hook.log lands
# inside it), three separate DBs, and the fixture directory used as the hook's
# cwd. The EXIT trap removes it. No real DB, ledger, cache or log is touched.
#
# Fail-fast: the Go unit layer runs FIRST. It pins the grading table event by
# event, and pins the deferred tag in runClaude that the whole feature hangs
# from — a regression the shell can only observe one event at a time.

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
note()        { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }
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
    printf '  %d passed, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${PASS_COUNT}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "$t"; done
    printf '\n'
    return 1
}
setup_failed() {
    printf '%sSETUP FAILED%s: %s\n' "${RED}" "${RESET}" "$1" >&2
    exit 2
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
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
}

# ─── locate the worktree + binary ───────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)" || setup_failed "cannot locate the worktree"
EGO="$WT/bin/endless-go"

command -v sqlite3 >/dev/null 2>&1 || setup_failed "sqlite3 is not on PATH; the fixtures are built with it"

# Build only when something is actually stale — the grading lives in the binary,
# so a stale one would verify the wrong code.
if [[ ! -x "$EGO" ]] || [[ -n "$(find "$WT" -name '*.go' -not -path '*/vendor/*' -newer "$EGO" 2>/dev/null | head -1)" ]]; then
    ( cd "$WT" && just build ) >/dev/null 2>&1 || setup_failed "just build"
fi
[[ -x "$EGO" ]] || setup_failed "$EGO not built"

printf '%sE-1661 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
printf '  worktree: %s\n  binary:   %s\n' "$WT" "$EGO"

# ─── 1. fail-fast unit layer ────────────────────────────────────────────────

section "1. The grading table and its wiring, unit level (fail-fast)"

if go_out="$(cd "$WT" && go test ./internal/hookcmd/ \
        -run 'HookExitCode|TaggedWithEvent|RunClaude_TagsItsFailures|HaltNotice' 2>&1)"; then
    report_pass "go test ./internal/hookcmd -run '…HookExitCode|RunClaude_TagsItsFailures…'"
else
    report_fail "go test ./internal/hookcmd -run '…HookExitCode|RunClaude_TagsItsFailures…'" \
        "all tests pass" "$go_out"
    summary
    exit 1
fi

# ─── fixture ────────────────────────────────────────────────────────────────

BASE=""
cleanup() { [[ -n "$BASE" && -d "$BASE" ]] && rm -rf "$BASE"; }
trap cleanup EXIT

BASE="$(mktemp -d)" || setup_failed "mktemp -d"
CWD="$BASE/project"          # the directory the hook is told it is running in
mkdir -p "$CWD" || setup_failed "mkdir fixture project dir"

# HOOK <cfgdir> <event> — run the real hook binary against one throwaway DB.
# Echoes stderr; the caller reads $? for the exit code.
#
# CLAUDE_CODE_ENTRYPOINT=cli is REQUIRED, not decoration (E-1962): the hook is
# gated on the harness and returns immediately — exit 0, no output — when the
# environment is not a supported agent host, and a bare shell is not one.
# TMUX_PANE and ENDLESS_SESSION_ID are stripped so the probe cannot read the
# live session running this script and bind itself to that session's task.
# XDG_CONFIG_HOME is redirected because hook.log is opened at package init,
# before --config-dir is consumed.
HOOK() {
    local cfg="$1" event="$2"
    printf '{"session_id":"e1661-verify","cwd":"%s","hook_event_name":"%s","tool_name":"Bash","source":"startup","prompt":"probe"}' \
        "$CWD" "$event" \
    | ( cd "$CWD" && env -u TMUX_PANE -u ENDLESS_SESSION_ID \
            CLAUDE_CODE_ENTRYPOINT=cli XDG_CONFIG_HOME="$cfg" \
            "$EGO" --config-dir "$cfg/endless" hook claude 2>&1 >/dev/null )
}

# HOOK_RC <cfgdir> <event> — the exit code alone.
HOOK_RC() { HOOK "$1" "$2" >/dev/null 2>&1; }

# seed_db <cfgdir> — create a throwaway config dir and let the binary build the
# schema in it, exactly as a first run on a fresh install would.
seed_db() {
    local cfg="$1"
    mkdir -p "$cfg/endless" || return 1
    HOOK_RC "$cfg" PostToolUse
}

CFG_OK="$BASE/ok"
CFG_ENUM="$BASE/enum-drift"
CFG_SCHEMA="$BASE/schema-drift"

seed_db "$CFG_OK"     || setup_failed "seeding the healthy fixture DB"
seed_db "$CFG_ENUM"   || setup_failed "seeding the enum-drift fixture DB"
seed_db "$CFG_SCHEMA" || setup_failed "seeding the schema-drift fixture DB"

# ─── 2. a healthy database changes nothing ──────────────────────────────────

section "2. Healthy database — every event still exits 0"

for ev in SessionStart UserPromptSubmit PreToolUse PostToolUse Stop SessionEnd; do
    HOOK_RC "$CFG_OK" "$ev"
    assert_eq "$ev exits 0 on a healthy DB" "0" "$?"
done

assert_not_contains "a healthy hook prints no halt notice" \
    "STOP. Do not continue" "$(HOOK "$CFG_OK" PostToolUse)"

# ─── 3. enum drift — the reported bug ───────────────────────────────────────

section "3. Enum drift — the fail-closed integrity gate reaches the agent"

# A row whose id no Go enum constant claims. Chosen deliberately over editing an
# existing row: schema.SQL's seed UPSERTs the ids it knows about on every open,
# so a mutated id=1 would be reconciled away before the gate ever saw it. An
# extra id survives the reconcile, which is also how a stale binary meets a
# database seeded by a newer one that added a task type.
sqlite3 "$CFG_ENUM/endless/endless.db" \
    "INSERT INTO task_types (id, slug, label) VALUES (99, 'bogus', 'Bogus');" \
    || setup_failed "diverging task_types in the fixture DB"

ENUM_ERR="$(HOOK "$CFG_ENUM" PostToolUse)"

assert_contains "the gate still fails closed and names the table" \
    "task_types integrity check" "$ENUM_ERR"

# THE REGRESSION. Before this branch every one of these exited 1, which Claude
# Code treats as a non-blocking error: shown to the user, never injected into
# the agent's context.
for ev in PreToolUse PostToolUse UserPromptSubmit SessionStart SessionEnd PreCompact; do
    HOOK_RC "$CFG_ENUM" "$ev"
    assert_eq "$ev exits 2 (blocking) on a drifted DB" "2" "$?"
done

HOOK_RC "$CFG_ENUM" Stop
assert_eq "Stop stays 1 — exit 2 there loops the turn with no DB-backed guard" "1" "$?"

assert_not_contains "...and Stop does not print the halt notice either" \
    "STOP. Do not continue" "$(HOOK "$CFG_ENUM" Stop)"

# The notice is the actionable half required by the plan: the error names the
# drifted table, the notice names the remedy and tells the agent to stop.
assert_contains "the notice halts the agent" "STOP. Do not continue" "$ENUM_ERR"
assert_contains "the notice names the binary" "hook binary:" "$ENUM_ERR"
assert_contains "the notice names the database" "database:" "$ENUM_ERR"
assert_contains "the notice names the stale-binary remedy" "Reinstall or rebuild" "$ENUM_ERR"

# PRODUCT: the remedy has to read correctly on a machine that is not this one,
# for a project that is not Endless. A build recipe from this repo would not.
for recipe in "just install" "just build" "uv tool"; do
    assert_not_contains "the remedy does not assume this checkout ('$recipe')" \
        "$recipe" "$ENUM_ERR"
done

# ─── 4. schema drift — what a real pinned session actually hits ─────────────

section "4. Schema drift — a query the deployed schema cannot answer"

# The live stale-binary symptom since E-1818. A pinned hook opens the real DB
# schema-passive, so it no longer fails closed at open; it opens cleanly and
# then dies on the first statement naming a column the deployed schema lacks.
# Same invisibility, different origin — and it is NOT an integrity error, so
# grading on the DB layer alone would have missed it entirely.
sqlite3 "$CFG_SCHEMA/endless/endless.db" \
    "ALTER TABLE sessions DROP COLUMN last_activity;" \
    || setup_failed "diverging the sessions schema in the fixture DB"

SCHEMA_ERR="$(HOOK "$CFG_SCHEMA" PostToolUse)"

assert_contains "the failure is a plain query error, not an integrity gate" \
    "no column named last_activity" "$SCHEMA_ERR"
# Read the LOGGED error alone — the notice appended after it discusses integrity
# checks as one of the two things a stale binary breaks, which is guidance, not
# a classification of this failure.
assert_not_contains "...so there is no integrity-check text to grade on" \
    "integrity check" "$(printf '%s\n' "$SCHEMA_ERR" | head -1)"

for ev in PreToolUse PostToolUse; do
    HOOK_RC "$CFG_SCHEMA" "$ev"
    assert_eq "$ev exits 2 on a schema-drifted DB" "2" "$?"
done

HOOK_RC "$CFG_SCHEMA" Stop
assert_eq "Stop stays 1 on a schema-drifted DB too" "1" "$?"

assert_contains "the notice fires for this failure as well" \
    "STOP. Do not continue" "$SCHEMA_ERR"

# ─── 5. the real ledger was never in play ───────────────────────────────────

section "5. Isolation"

note "every DB written above lives under ${BASE}"
assert_eq "the fixture DBs exist where the trap will remove them" "3" \
    "$(find "$BASE" -name 'endless.db' | wc -l | tr -d ' ')"

summary
