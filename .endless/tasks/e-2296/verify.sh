#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2296 and records what was true when E-2296
# landed. Edit it only if you ARE E-2296. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2296 verification — a task with a live blocker refuses its claim, spawn
# and prime, naming every blocker and pointing at task unblock; --out-of-order
# does not cover it; unblocking releases it.
#
#   endless task verify E-2296
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"
TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
TMP="$(cd "${TMP}" && pwd -P)"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if uv run pytest -q tests/test_blocks_gate.py tests/test_precedes_gate.py \
        tests/test_prime.py tests/test_spawn_gate.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: the blocks refusal and the gates beside it"
else
    report_fail "pytest blocks gate / precedes gate / prime / spawn gate" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. end to end, through the candidate CLI in a throwaway project ─────────
section "2. claim refuses a blocked task, names the blocker, and changes nothing"

ENDLESS="${WT}/.venv/bin/endless"
[[ -x "${ENDLESS}" ]] || ( uv sync --quiet ) >/dev/null 2>&1
[[ -x "${ENDLESS}" ]] || setup_error "no candidate endless at ${ENDLESS}"

export XDG_CONFIG_HOME="${TMP}/config" XDG_CACHE_HOME="${TMP}/cache"
mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"
PROJ="${TMP}/proj"
mkdir -p "${PROJ}/.endless"
printf '{"name": "e2296-verify"}\n' >"${PROJ}/.endless/config.json"
git -C "${PROJ}" init -q -b main
git -C "${PROJ}" config user.email t@e.x
git -C "${PROJ}" config user.name t
git -C "${PROJ}" config commit.gpgsign false
printf 'x\n' >"${PROJ}/README"
git -C "${PROJ}" add -A
git -C "${PROJ}" commit -q -m init
cd "${PROJ}" || setup_error "cannot cd to ${PROJ}"
"${ENDLESS}" project register "${PROJ}" --infer --name e2296-verify --status active >/dev/null 2>&1 || true

add() {
    "${ENDLESS}" task add "$1" --description "$1 for the blocking suite." \
        --plan "# Plan" --complexity low --risk low "${@:2}" >"${TMP}/add.log" 2>&1 || cp "${TMP}/add.log" "${TMP}/add-failed.log"
    grep -oE 'Added E-[0-9]+' "${TMP}/add.log" | head -1 | cut -d' ' -f2
}
status_of() {
    "${ENDLESS}" task show "$1" --json 2>/dev/null \
        | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])'
}

A="$(add "Fix the first thing")"
[[ -n "${A}" ]] || setup_error "could not file the blocker: $(tail -8 "${TMP}/add.log")"
B="$(add "Fix the second thing" --blocked-by "${A}")"
[[ -n "${B}" ]] || setup_error "could not file the blocked task: $(tail -8 "${TMP}/add-failed.log" 2>/dev/null)"

OUT="$("${ENDLESS}" task claim "${B}" --unattended 2>&1)"
RC=$?
assert_eq "claim of the blocked task exits 1" "1" "${RC}"
assert_contains "it names the blocker and its status" "${A}  submitted  Fix the first thing" "${OUT}"
assert_contains "it points at task unblock" "endless task unblock ${B} --by ${A}" "${OUT}"
assert_not_contains "it offers no override" "--out-of-order" "${OUT}"
assert_eq "the blocked task's status did not move" "submitted" "$(status_of "${B}")"

section "3. --out-of-order does not get past a block"

OUT="$("${ENDLESS}" task claim "${B}" --unattended --out-of-order 2>&1)"
assert_eq "claim --out-of-order still exits 1" "1" "$?"
assert_contains "it still names the blocker" "${A}  submitted" "${OUT}"
assert_eq "the blocked task's status still did not move" "submitted" "$(status_of "${B}")"

section "4. Unblocking releases the task"

"${ENDLESS}" task unblock "${B}" --by "${A}" >"${TMP}/unblock.log" 2>&1 \
    || setup_error "task unblock failed: $(tail -8 "${TMP}/unblock.log")"
OUT="$("${ENDLESS}" task claim "${B}" --unattended 2>&1)"
assert_eq "claim after unblock exits 0" "0" "$?"
assert_eq "the task is underway" "underway" "$(status_of "${B}")"

summary
