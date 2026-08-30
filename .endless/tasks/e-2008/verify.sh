#!/usr/bin/env bash
#
# E-2008 verification script — an empty --<field>-file is refused, never written.
#
# The bug: every `--<field>-file` flag on the task/decision/epic verbs wrote
# whatever the file held. A path that came back empty — a failed extraction, a
# sed that matched nothing — silently REPLACED the existing description, text,
# analysis or outcome with nothing, and the command printed a normal success
# line. Observed on E-1817: an analysis was round-tripped through `task show`,
# the extraction produced a zero-byte file, and `task update --analysis-file`
# wrote it over 3.5KB. The content survived only because the session still had
# it in context; from a fresh session it was gone.
#
# The fix: zero bytes is never a legitimate value for these fields, so an empty
# or whitespace-only file is refused UNCONDITIONALLY at cli._resolve_content_flag
# — the single choke point every inline/file pair already funnels through. There
# is deliberately NO --force: --force is precisely the flag a mistaken caller
# appends after reading a refusal, which would restore the failure mode with an
# audit trail claiming it was intended. Emptying a field is instead an explicit,
# field-named act — `--clear <field>` on the three `update` verbs — that a failed
# pipeline cannot reach by accident.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-2008-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Fail-fast: tests/test_content_flag_gate.py runs FIRST. It pins the predicate
# at a granularity the shell cannot reach — the zero-bytes/whitespace split, the
# clearable-vs-not recovery line, the --clear conflict rule — so if it fails
# there is no point running the end-to-end checks.
#
# What the shell adds that the unit tests cannot: that the refusal is wired into
# every real CLI surface, and above all that the field's PRIOR CONTENT IS STILL
# THERE afterward. "Raises a ClickException" is not the property that was
# broken; "did not silently blank 3.5KB" is.
#
# Isolation: a throwaway git repo under a temp dir with its own
# XDG_CONFIG_HOME / XDG_CACHE_HOME, so no real, sandbox or cached DB is touched.

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
# assert_lacks DESC NEEDLE HAYSTACK
assert_lacks() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
}

# ─── locate the worktree ────────────────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if ! command -v uv >/dev/null 2>&1; then
    printf '%sSETUP FAILED%s: uv not on PATH.\n' "${RED}" "${RESET}" >&2
    exit 2
fi

# ─── 0. fail-fast unit layer ────────────────────────────────────────────────

section "Content-flag gate unit tests (fail-fast)"

if py_out="$(cd "$WT" && uv run --project "$WT" python -m pytest \
        tests/test_content_flag_gate.py -q 2>&1)"; then
    report_pass "pytest tests/test_content_flag_gate.py"
else
    report_fail "pytest tests/test_content_flag_gate.py" "all tests pass" "$py_out"
    summary
    exit 1
fi

# ─── fixture ────────────────────────────────────────────────────────────────

TMP=""; PROJ=""

E() { ( cd "$PROJ" && uv run --project "$WT" endless "$@" ); }
Q() { E sql "$1" --tsv 2>/dev/null; }

# RUN <desc-var-prefix> …: capture combined output and exit code of an E call.
# Sets OUT and RC.
RUN() { OUT="$(E "$@" 2>&1)"; RC=$?; return 0; }

# NEW_TASK <title> [args…]: create a task, echo its numeric id.
NEW_TASK() {
    local title="$1"; shift
    E task add "$title" "$@" 2>&1 | grep -oE 'E-[0-9]+' | head -1 | tr -d 'E-'
}

setup_fixture() {
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    PROJ="$TMP/probe"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    mkdir -p "$PROJ" "$TMP/xdg/endless" "$TMP/files"

    # The three shapes an extraction can hand back. `real` is the control: a
    # file with content must still load, or the guard is just breaking the flag.
    : > "$TMP/files/empty.md"
    printf '   \n\n\t\n'          > "$TMP/files/whitespace.md"
    printf 'genuine loaded content\n' > "$TMP/files/real.md"

    git -C "$PROJ" init -q
    git -C "$PROJ" config user.email verify@test
    git -C "$PROJ" config user.name verify
    git -C "$PROJ" commit -q --allow-empty -m "initial commit"

    E project register "$PROJ" --name probe --label Probe --desc d --lang Go \
        --status active >/dev/null 2>&1

    [[ -n "$(Q "SELECT id FROM projects WHERE name='probe'")" ]] || return 1
    return 0
}

teardown_fixture() { [[ -n "$TMP" && -d "$TMP" ]] && rm -rf "$TMP"; }

if ! setup_fixture; then
    printf '%sSETUP FAILED%s: could not build the isolated fixture.\n' \
        "${RED}" "${RESET}" >&2
    teardown_fixture
    exit 2
fi
trap teardown_fixture EXIT

# ─── 1. the regression: content survives an empty file ──────────────────────

section "task update — an empty file is refused and the field is untouched"

TID="$(NEW_TASK "Test the empty analysis file refusal")"
if [[ -z "$TID" ]]; then
    printf '%sSETUP FAILED%s: could not create the probe task.\n' \
        "${RED}" "${RESET}" >&2
    exit 2
fi

E task update "$TID" --analysis-file "$TMP/files/real.md" >/dev/null 2>&1
assert_eq "control: a file WITH content loads (the guard is not just breaking the flag)" \
    "genuine loaded content" "$(Q "SELECT trim(analysis) FROM live_tasks WHERE id=$TID")"

RUN task update "$TID" --analysis-file "$TMP/files/empty.md"
assert_eq "zero-byte --analysis-file exits non-zero" \
    "1" "$([[ $RC -ne 0 ]] && echo 1 || echo 0)"
assert_contains "…and says it loaded no content" "loaded no content" "$OUT"
assert_contains "…and names the offending path" "$TMP/files/empty.md" "$OUT"
assert_contains "…and reports the size, so the failed step is findable" "0 bytes" "$OUT"
assert_eq "THE REGRESSION: the existing analysis is still there" \
    "genuine loaded content" "$(Q "SELECT trim(analysis) FROM live_tasks WHERE id=$TID")"

RUN task update "$TID" --analysis-file "$TMP/files/whitespace.md"
assert_contains "whitespace-only file is refused too (equally a failed pipeline)" \
    "all whitespace" "$OUT"
assert_eq "…and the analysis survives that too" \
    "genuine loaded content" "$(Q "SELECT trim(analysis) FROM live_tasks WHERE id=$TID")"

# ─── 2. the whole family, not just --analysis-file ──────────────────────────

section "Every --<field>-file on every verb that has one"

for field in description text analysis outcome; do
    RUN task update "$TID" "--${field}-file" "$TMP/files/empty.md"
    assert_contains "task update --${field}-file refused" \
        "--${field}-file loaded no content" "$OUT"
done

RUN task add "Test the add-side empty description refusal" \
    --description-file "$TMP/files/empty.md"
assert_contains "task add --description-file refused" "loaded no content" "$OUT"
assert_lacks "…and does NOT offer --clear (nothing to clear on a create)" \
    "--clear" "$OUT"

RUN task confirm "$TID" --outcome-file "$TMP/files/empty.md"
assert_contains "task confirm --outcome-file refused" "loaded no content" "$OUT"

EID="$(NEW_TASK "Test the epic-side empty file refusal")"
E epic update "$EID" --text-file "$TMP/files/real.md" >/dev/null 2>&1
RUN epic update "$EID" --text-file "$TMP/files/empty.md"
assert_contains "epic update --text-file refused" "loaded no content" "$OUT"
assert_eq "…and the epic's text survives" \
    "genuine loaded content" "$(Q "SELECT trim(text) FROM live_tasks WHERE id=$EID")"

DID="$(E decision add "Refuse an empty description file on a decision" \
        --description "original decision description" 2>&1 \
      | grep -oE 'ED-[0-9]+' | head -1 | tr -d 'ED-')"
RUN decision update "$DID" --description-file "$TMP/files/empty.md"
assert_contains "decision update --description-file refused" "loaded no content" "$OUT"
assert_eq "…and the decision's description survives" \
    "original decision description" \
    "$(Q "SELECT description FROM decisions WHERE id=$DID")"

# ─── 3. no --force, and --force does not defeat it ──────────────────────────

section "No 'just do it' escape hatch"

RUN task update "$TID" --analysis-file "$TMP/files/empty.md"
assert_lacks "the refusal never suggests --force" "--force" "$OUT"

# `task update` HAS a --force flag (it bypasses title validation). It must not
# be mistaken for a way through this guard: an agent that reads "refused" and
# reaches for the --force already on the command is exactly the caller the
# no-escape-hatch decision was made against.
RUN task update "$TID" --analysis-file "$TMP/files/empty.md" --force
assert_contains "the unrelated --force does not let an empty file through" \
    "loaded no content" "$OUT"
assert_eq "…and the analysis is still intact after that attempt" \
    "genuine loaded content" "$(Q "SELECT trim(analysis) FROM live_tasks WHERE id=$TID")"

# ─── 4. --clear <field> — the named way out ─────────────────────────────────

section "--clear <field> is the deliberate erase"

RUN task update "$TID" --analysis-file "$TMP/files/empty.md"
assert_contains "the refusal names --clear, so the way out is discoverable here" \
    "--clear analysis" "$OUT"

RUN task update "$TID" --clear analysis
assert_eq "--clear analysis succeeds" "0" "$RC"
assert_eq "…and the field really is empty" \
    "" "$(Q "SELECT analysis FROM live_tasks WHERE id=$TID")"

E task update "$TID" --analysis-file "$TMP/files/real.md" >/dev/null 2>&1
RUN task update "$TID" --clear analysis --analysis-file "$TMP/files/real.md"
assert_contains "--clear <field> alongside that field's --<field>-file is refused" \
    "conflicts with --analysis/--analysis-file" "$OUT"
assert_eq "…and nothing was written by the refused command" \
    "genuine loaded content" "$(Q "SELECT trim(analysis) FROM live_tasks WHERE id=$TID")"

RUN task update "$TID" --clear analysis --analysis "inline instead"
assert_contains "…same for the inline --<field>" \
    "conflicts with --analysis/--analysis-file" "$OUT"

RUN task update "$TID" --clear title
assert_contains "--clear only accepts the content fields" \
    "is not one of 'description', 'text', 'analysis', 'outcome'" "$OUT"

RUN decision update "$DID" --clear description
assert_eq "decision update --clear description succeeds" "0" "$RC"
assert_eq "…and the decision description is empty" \
    "" "$(Q "SELECT description FROM decisions WHERE id=$DID")"

# ─── 5. the inline empty-string form is unchanged ───────────────────────────

section "Inline --<field> '' still clears (it names the field; a pipeline cannot reach it)"

E task update "$TID" --analysis-file "$TMP/files/real.md" >/dev/null 2>&1
RUN task update "$TID" --analysis ""
assert_eq "inline --analysis '' still succeeds" "0" "$RC"
assert_eq "…and still empties the field" \
    "" "$(Q "SELECT analysis FROM live_tasks WHERE id=$TID")"

summary
