#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2270 and records what was true when E-2270
# landed. Edit it only if you ARE E-2270. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2270 verification — an unfinished task linked as preceding another refuses
# the other's claim, spawn and prime, naming it; --out-of-order overrides that
# refusal alone; the auto-spawn and auto-prime jobs skip such a task.
#
#   endless task verify E-2270
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

if uv run pytest -q tests/test_precedes_gate.py tests/test_prime.py \
        tests/test_spawn_gate.py tests/test_advisory_relations.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: the precedes refusal, its override, and the gates beside it"
else
    report_fail "pytest precedes gate / prime / spawn gate" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

if go test ./internal/autospawnjob/ >"${TMP}/go.log" 2>&1; then
    report_pass "go test: auto-spawn and auto-prime skip a task with an unfinished predecessor"
else
    report_fail "go test ./internal/autospawnjob/" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── 2. end to end, through the candidate CLI in a throwaway project ─────────
section "2. claim refuses, names the predecessor, and changes nothing"

ENDLESS="${WT}/.venv/bin/endless"
[[ -x "${ENDLESS}" ]] || ( uv sync --quiet ) >/dev/null 2>&1
[[ -x "${ENDLESS}" ]] || setup_error "no candidate endless at ${ENDLESS}"

export XDG_CONFIG_HOME="${TMP}/config" XDG_CACHE_HOME="${TMP}/cache"
mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"
PROJ="${TMP}/proj"
mkdir -p "${PROJ}/.endless"
printf '{"name": "e2270-verify"}\n' >"${PROJ}/.endless/config.json"
git -C "${PROJ}" init -q -b main
git -C "${PROJ}" config user.email t@e.x
git -C "${PROJ}" config user.name t
git -C "${PROJ}" config commit.gpgsign false
printf 'x\n' >"${PROJ}/README"
git -C "${PROJ}" add -A
git -C "${PROJ}" commit -q -m init
cd "${PROJ}" || setup_error "cannot cd to ${PROJ}"
"${ENDLESS}" project register "${PROJ}" --infer --name e2270-verify --status active >/dev/null 2>&1 || true

add() {
    "${ENDLESS}" task add "$1" --description "$1 for the E-2270 suite." \
        --plan "# Plan" --complexity low --risk low "${@:2}" 2>&1 \
        | grep -oE 'Added E-[0-9]+' | head -1 | cut -d' ' -f2
}
status_of() {
    "${ENDLESS}" task show "$1" --json 2>/dev/null \
        | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])'
}

A="$(add "Fix the first thing")"
B="$(add "Fix the second thing" --preceded-by "${A}")"
[[ -n "${A}" && -n "${B}" ]] || setup_error "could not file the two tasks (A=${A} B=${B})"

OUT="$("${ENDLESS}" task claim "${B}" --unattended 2>&1)"
RC=$?
assert_eq "claim of the follower exits 1" "1" "${RC}"
assert_contains "it names the unfinished predecessor and its status" "${A}  submitted  Fix the first thing" "${OUT}"
assert_contains "it offers the override" "endless task claim ${B} --out-of-order" "${OUT}"
assert_eq "the follower's status did not move" "submitted" "$(status_of "${B}")"

section "3. --out-of-order claims anyway, still listing the predecessor"

OUT="$("${ENDLESS}" task claim "${B}" --unattended --out-of-order 2>&1)"
assert_eq "claim --out-of-order exits 0" "0" "$?"
assert_contains "it lists the predecessor" "Starting ${B} out of order: ${A} [submitted]" "${OUT}"
assert_eq "the follower is underway" "underway" "$(status_of "${B}")"

section "4. The other two verbs carry the flag"

for verb in spawn prime; do
    assert_contains "task ${verb} --help offers --out-of-order" "--out-of-order" \
        "$("${ENDLESS}" task "${verb}" --help 2>&1)"
done

summary
