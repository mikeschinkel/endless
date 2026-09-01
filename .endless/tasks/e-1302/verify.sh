#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1302 and records what was true when E-1302
# landed. Edit it only if you ARE E-1302. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1302 verification — `endless task id` / `endless tmux task`.
#
# Before: the DB-backed session→task binding was reachable only as
# `endless-go tmux active-id` — deliberately undocumented plumbing for the
# tmux menus, absent from `endless --help`, and named after the mechanism
# rather than the question. Anyone who wanted the id had to know the Go
# binary existed and that a subcommand called `active-id` lived under `tmux`.
# After: `endless task id` is the user-facing verb ("which task am I on?"),
# with `endless tmux task` as an alias for whoever reaches for tmux first.
# Both print one bare `E-NNNN` line and exit non-zero when there is no task.
#
# Run from inside the worktree (esu puts you there):
#   endless task verify E-1302
#
# What it proves:
#   1. FAIL-FAST unit gate: this task's Python suite and the CLI-surface
#      suites it could break are green.
#   2. Both spellings exist, are listed in their groups' help, and resolve to
#      the SAME command object — an alias, not a second implementation.
#   3. The help states the contract a caller scripts against.
#   4. FUNCTIONAL, against the real binary and the real session binding:
#      a. `endless task id` agrees with `endless-go tmux active-id`, the
#         source it promotes — including when both say "no task";
#      b. stdout is exactly one bare `E-NNNN` line, nothing else, so
#         `$(endless task id)` composes;
#      c. `endless tmux task` produces byte-identical output;
#      d. no task → exit 1, EMPTY stdout, reason on stderr;
#      e. outside tmux the reason says so, instead of blaming an unclaimed
#         task;
#      f. the id round-trips: `endless task show "$(endless task id)"`.
#   5. Python did not grow a seventh SQLite reader to do it.
#   6. The guide documents the verb where a reader would look for it, and no
#      stale `endless-tmux active-id` spelling survives in the guide.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

WRAPPER="${WT}/src/endless/tmux_cmd.py"
CLI="${WT}/src/endless/cli.py"
TASKS_GUIDE="${WT}/docs/guide/tasks.md"
REF_GUIDE="${WT}/docs/guide/reference.md"
SESSIONS_GUIDE="${WT}/docs/guide/sessions.md"
GO_BIN="${WT}/bin/endless-go"

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
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    [[ -n "${3:-}" ]] && printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
    return 0
}

report_skip() {
    printf '  %s–%s %s %s(%s)%s\n' "${DIM}" "${RESET}" "$1" "${DIM}" "$2" "${RESET}"
}

setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

for f in "${WRAPPER}" "${CLI}" "${TASKS_GUIDE}" "${REF_GUIDE}" "${SESSIONS_GUIDE}"; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# If the surface's own suite is red, every functional assertion below is a
# derived symptom. Stop here rather than printing a wall of consequences.
section "1. Unit gate (fail-fast)"

if uv run pytest -q tests/test_task_id_cmd.py tests/test_cli.py \
        tests/test_guide_map.py tests/test_go_cli_parity.py \
        >/tmp/e1302-py.log 2>&1; then
    report_pass "pytest (task id surface, CLI tree, guide map, Go/Python parity)"
else
    report_fail "pytest" "pass" "failed — see /tmp/e1302-py.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# ── 2. both spellings exist, and are one command ────────────────────────────
section "2. Surface: two spellings, one command"

if uv run endless task --help 2>/dev/null | grep -qE '^\s+id\b'; then
    report_pass "\`id\` is listed in \`endless task --help\`"
else
    report_fail "\`id\` is listed in \`endless task --help\`" "an 'id' row" "absent"
fi

if uv run endless tmux --help 2>/dev/null | grep -qE '^\s+task\b'; then
    report_pass "\`task\` is listed in \`endless tmux --help\`"
else
    report_fail "\`task\` is listed in \`endless tmux --help\`" "a 'task' row" "absent"
fi

# The alias must be the same object, or the two can drift apart silently.
if uv run python -c '
import sys
from endless.cli import main
a = main.commands["task"].commands.get("id")
b = main.commands["tmux"].commands.get("task")
sys.exit(0 if a is not None and a is b else 1)' 2>/dev/null; then
    report_pass "\`tmux task\` IS \`task id\` (same command object, cannot drift)"
else
    report_fail "\`tmux task\` IS \`task id\`" "the same object" "different or missing"
fi

# ── 3. the help states the scripting contract ───────────────────────────────
section "3. Help states the contract"

help_out=$(uv run endless task id --help 2>/tmp/e1302-help.log) \
    || setup_error "could not run 'endless task id --help' (see /tmp/e1302-help.log)"
# Click rewraps help to the terminal width, so a phrase can straddle a newline.
help_flat=$(tr '\n' ' ' <<<"${help_out}" | tr -s ' ')

while IFS='|' read -r needle label; do
    if grep -qF -- "${needle}" <<<"${help_flat}"; then
        report_pass "help ${label}"
    else
        report_fail "help ${label}" "'${needle}' in --help" "absent"
    fi
done <<'EOF'
E-NNNN|names the output shape
diagnostic on stderr — never on stdout|states where diagnostics go
Exits 1|states the no-task exit code
endless tmux task|names the alias
EOF

# ── 4. functional, against the real binary ──────────────────────────────────
section "4a. Agreement with the source it promotes"

[[ -x "${GO_BIN}" ]] || setup_error "missing ${GO_BIN} — run \`just build\` first"

go_out=$("${GO_BIN}" tmux active-id 2>/dev/null)
go_rc=$?
py_out=$(uv run endless task id 2>/tmp/e1302-id.log)
py_rc=$?

if [[ "${py_rc}" == "${go_rc}" && "${py_out}" == "${go_out}" ]]; then
    report_pass "\`task id\` agrees with \`endless-go tmux active-id\` (rc=${py_rc}, out='${py_out}')"
else
    report_fail "\`task id\` agrees with \`endless-go tmux active-id\`" \
        "rc=${go_rc} out='${go_out}'" "rc=${py_rc} out='${py_out}'"
fi

section "4b-c. Output shape, and the alias"

if (( py_rc == 0 )); then
    if [[ "${py_out}" =~ ^E-[0-9]+$ ]]; then
        report_pass "stdout is exactly one bare id: ${py_out}"
    else
        report_fail "stdout is exactly one bare id" "E-NNNN and nothing else" "${py_out}"
    fi

    # Not just "matches" — literally one line. A banner or a trailing hint
    # would still match the regex above once the shell strips it.
    line_count=$(uv run endless task id 2>/dev/null | wc -l | tr -d ' ')
    if [[ "${line_count}" == "1" ]]; then
        report_pass "stdout is exactly one line"
    else
        report_fail "stdout is exactly one line" "1" "${line_count}"
    fi

    alias_out=$(uv run endless tmux task 2>/dev/null)
    if [[ "${alias_out}" == "${py_out}" ]]; then
        report_pass "\`tmux task\` output is byte-identical: ${alias_out}"
    else
        report_fail "\`tmux task\` output is byte-identical" "${py_out}" "${alias_out}"
    fi

    section "4f. The id round-trips into another verb"
    if uv run endless task show "${py_out}" --db main >/tmp/e1302-show.log 2>&1; then
        report_pass "\`endless task show \"\$(endless task id)\"\` resolves the task"
    else
        report_fail "\`task show \"\$(task id)\"\` resolves" "exit 0" \
            "$(tail -2 /tmp/e1302-show.log)"
    fi
else
    report_skip "output-shape assertions" "this shell's session holds no task"
    report_skip "round-trip assertion" "this shell's session holds no task"
fi

section "4d-e. No task: exit code, empty stdout, reason on stderr"

# A pane that cannot be bound to any session — the no-task path, reachable
# without disturbing the real binding.
bogus_out=$(uv run endless task id --pane '%e1302verify' 2>/tmp/e1302-none.log)
bogus_rc=$?

if (( bogus_rc != 0 )); then
    report_pass "no task → exit ${bogus_rc} (non-zero)"
else
    report_fail "no task → non-zero exit" "non-zero" "0"
fi

if [[ -z "${bogus_out}" ]]; then
    report_pass "no task → EMPTY stdout, so a failed \$( ) capture is empty"
else
    report_fail "no task → empty stdout" "nothing" "${bogus_out}"
fi

if grep -qF -- "no active task" /tmp/e1302-none.log; then
    report_pass "no task → the reason is on stderr"
else
    report_fail "no task → the reason is on stderr" "'no active task'" \
        "$(head -1 /tmp/e1302-none.log)"
fi

# The two causes of "no task" are different problems with different fixes;
# the message has to say which one it is.
if grep -qF -- "task claim" /tmp/e1302-none.log; then
    report_pass "in tmux, the message points at \`task claim\`"
else
    report_fail "in tmux, the message points at \`task claim\`" \
        "'task claim' in the message" "$(head -1 /tmp/e1302-none.log)"
fi

env -u TMUX -u TMUX_PANE uv run endless tmux task >/tmp/e1302-notmux-out.log 2>/tmp/e1302-notmux.log
notmux_rc=$?

if (( notmux_rc != 0 )) && [[ ! -s /tmp/e1302-notmux-out.log ]]; then
    report_pass "outside tmux → non-zero exit, empty stdout"
else
    report_fail "outside tmux → non-zero exit, empty stdout" \
        "rc!=0 and no stdout" "rc=${notmux_rc}, stdout=$(cat /tmp/e1302-notmux-out.log)"
fi

if grep -qF -- "not inside tmux" /tmp/e1302-notmux.log; then
    report_pass "outside tmux → the message says so, not 'claim a task'"
else
    report_fail "outside tmux → the message says so" \
        "'not inside tmux'" "$(head -1 /tmp/e1302-notmux.log)"
fi

# The alias must name itself, not the primary spelling, or the message sends
# the reader to a command they did not type.
if grep -qF -- "endless tmux task:" /tmp/e1302-notmux.log; then
    report_pass "the message names the spelling the user typed"
else
    report_fail "the message names the spelling the user typed" \
        "'endless tmux task:'" "$(head -1 /tmp/e1302-notmux.log)"
fi

# ── 5. no new Python SQLite reader ──────────────────────────────────────────
# CLAUDE.md: Go owns database access; Python still reads SQLite in six files
# and must not grow a seventh. The whole point of shelling to the Go binary is
# that this verb does not become one.
section "5. Python did not grow a seventh SQLite reader"

if grep -qE '\b(sqlite3|import sqlite)\b' "${WRAPPER}"; then
    report_fail "the wrapper does not touch SQLite" "no sqlite3 import" "present"
else
    report_pass "the wrapper does not touch SQLite"
fi

if grep -qF -- 'endless-go' "${WRAPPER}" && \
   grep -qF -- '"tmux", "active-id"' "${WRAPPER}"; then
    report_pass "the read is delegated to \`endless-go tmux active-id\`"
else
    report_fail "the read is delegated to \`endless-go tmux active-id\`" \
        "the shellout in ${WRAPPER##*/}" "absent"
fi

# ── 6. guide ────────────────────────────────────────────────────────────────
section "6. Guide"

if grep -qF -- 'endless task id' "${TASKS_GUIDE}"; then
    report_pass "the tasks section documents \`endless task id\`"
else
    report_fail "the tasks section documents \`endless task id\`" "the command" "absent"
fi

if grep -qF -- 'endless tmux task' "${REF_GUIDE}"; then
    report_pass "the reference section lists the \`tmux task\` alias"
else
    report_fail "the reference section lists the \`tmux task\` alias" \
        "the alias" "absent"
fi

if grep -qF -- 'endless task id' "${SESSIONS_GUIDE}"; then
    report_pass "the 'who am I' discovery answer names the verb"
else
    report_fail "the 'who am I' discovery answer names the verb" "the command" "absent"
fi

# The guide pointed at a binary that has not been spelled that way since
# E-1367 folded it into `endless-go tmux`. Promoting the verb is the moment
# to stop teaching the old name.
stale=$(grep -rn 'endless-tmux' "${WT}/docs/guide/" 2>/dev/null || true)
if [[ -z "${stale}" ]]; then
    report_pass "no stale \`endless-tmux\` spelling left in the guide"
else
    report_fail "no stale \`endless-tmux\` spelling in the guide" "no matches" "${stale}"
fi

# ── summary ─────────────────────────────────────────────────────────────────
section "Summary"
printf '  %s%d passed%s, %s%d failed%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" \
    "$([[ ${FAIL_COUNT} -gt 0 ]] && printf '%s' "${RED}")" "${FAIL_COUNT}" "${RESET}"

if (( FAIL_COUNT > 0 )); then
    printf '\n  Failed:\n'
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    exit 1
fi
exit 0
