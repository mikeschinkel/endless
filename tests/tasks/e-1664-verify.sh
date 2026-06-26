#!/usr/bin/env bash
#
# E-1664 verification script — confirms a self_dev land deterministically uses
# the WORKTREE's endless-go binary for the record-landing emit, in CODE (not via
# the Justfile PATH hack E-1660 used), and fails loudly when that build is
# missing instead of silently sliding to the stale global.
#
# Background: landing a branch that ADDS a mirrored-enum value (a new task_types
# row) applies the schema change, then `endless worktree land` emits the
# task.landed event. Under --db main `_resolve_endless_go()` used to resolve the
# GLOBAL binary (not yet rebuilt), whose tasktype enum lacked the new constant —
# tripping monitor.DB()'s VerifyIntegrity. E-1660 patched this per-call in the
# Justfile (PATH-prepend the worktree bin). E-1664 makes it an invariant of the
# land itself: `land_worktree` resolves the worktree binary via
# `_resolve_land_endless_go` and threads it through `_record_landing` ->
# `emit_event` -> `_resolve_endless_go(override=...)`; a missing build is a loud
# error. The Justfile land call is now plain.
#
# Checks (no destructive real land):
#   1. Gate still armed (don't weaken it): an emit against a DB carrying a
#      task_types row with NO matching enum constant MUST fail with the integrity
#      error. This is the exact failure the bug produced.
#   2. Matching-enum passes: the same emit against a schema-seeded DB gets PAST
#      the integrity gate (it may fail later on an unrelated FK — fine).
#   3. Override honored: `_resolve_endless_go(override=X)` returns X when valid,
#      raises loudly when X is missing/non-exec, and ignores it (default
#      resolution) when None.
#   4. Land invariant: `_resolve_land_endless_go` returns the worktree binary for
#      a self_dev project when present, raises a loud "build it" error when the
#      build is missing, and returns None for a non-self_dev project.
#   5. Justfile regression guard: the `land` recipe invokes
#      `endless worktree land "$tid"` plainly — no PATH-prepend wrapper around it.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1664-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# A task_types id that no Go enum constant will ever match — stands in for "a
# new mirrored-enum row the stale binary doesn't know about yet".
PHANTOM_ID=99
PHANTOM_SLUG="phantom"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
}

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    local desc="$1"
    local expected="$2"
    local actual="$3"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "${expected}"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "${actual}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("${desc}")
}

summary() {
    printf '\n%sSummary%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── helpers ────────────────────────────────────────────────────────────────

REPO_ROOT=""
WT_BIN=""
LEDGER_REPO=""
PY_OUT=""
PY_RC=0

# Build a throwaway git repo to serve as --project-root so the event's ledger
# auto-commit (which refuses to land on a linked worktree, E-1309) succeeds and
# the emit proceeds to the DB projection where VerifyIntegrity runs.
make_ledger_repo() {
    LEDGER_REPO=$(mktemp -d)
    git -C "${LEDGER_REPO}" init -q
    git -C "${LEDGER_REPO}" config user.email verify@e1664
    git -C "${LEDGER_REPO}" config user.name e1664-verify
    git -C "${LEDGER_REPO}" commit -q --allow-empty -m init
    mkdir -p "${LEDGER_REPO}/.endless"
}

# seed_db <config-dir> [inject-phantom] — create a fresh schema-seeded DB at
# <config-dir>/endless/endless.db; if a second arg is given, inject a
# task_types row with no matching enum constant.
seed_db() {
    local cfg="$1"
    local inject="${2:-}"
    mkdir -p "${cfg}/endless"
    sqlite3 "${cfg}/endless/endless.db" < "${REPO_ROOT}/internal/schema/schema.sql" >/dev/null 2>&1
    if [[ -n "${inject}" ]]; then
        sqlite3 "${cfg}/endless/endless.db" \
            "INSERT INTO task_types (id,slug,label) VALUES (${PHANTOM_ID},'${PHANTOM_SLUG}','Phantom');"
    fi
}

# emit <config-dir> — run the same event the record-landing step writes, against
# the DB under <config-dir>, using the WORKTREE binary. Echoes combined output.
emit() {
    local cfg="$1"
    "${WT_BIN}" event emit --kind task.landed --project endless \
        --entity-type task --entity-id 1664 \
        --project-root "${LEDGER_REPO}" --config-dir "${cfg}/endless" \
        --actor-kind system --actor-id "verify@e1664" --node-id "abcd" \
        --payload '{"branch":"e-1664-verify","merge_commit_sha":"deadbeef"}' 2>&1
}

# run_py <snippet> — execute a Python snippet with the worktree on PATH (so the
# default endless-go resolution finds a real binary). Sets PY_OUT and PY_RC. A
# snippet signals success by printing "OK" and exiting 0; any AssertionError /
# SystemExit / exception is a failure.
run_py() {
    PY_OUT=$(cd "${REPO_ROOT}" && PATH="${REPO_ROOT}/bin:${PATH}" uv run python -c "$1" 2>&1)
    PY_RC=$?
}

# ─── 1. gate still armed (don't weaken it) ──────────────────────────────────

test_gate_armed() {
    section "Integrity gate — a DB row with no matching enum constant is REFUSED"

    local cfg
    cfg=$(mktemp -d)
    seed_db "${cfg}" inject
    local out
    out=$(emit "${cfg}")
    local desc="record-landing emit fails closed on a drifted task_types row"
    if [[ "${out}" == *"integrity check"* && "${out}" == *"no matching enum constant"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" \
            "output contains 'integrity check' AND 'no matching enum constant'" \
            "${out}"
    fi
}

# ─── 2. matching enum passes the gate ───────────────────────────────────────

test_matching_passes() {
    section "Matching enum — record-landing emit gets PAST the integrity gate"

    local cfg
    cfg=$(mktemp -d)
    seed_db "${cfg}"   # no phantom row; all task_types match the binary's enum
    local out
    out=$(emit "${cfg}")
    local desc="emit clears the integrity gate when the binary enum matches the DB"
    if [[ "${out}" != *"integrity check"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" \
            "output does NOT contain 'integrity check'" \
            "${out}"
    fi
}

# ─── 3. _resolve_endless_go(override=...) ───────────────────────────────────

test_override_honored() {
    section "_resolve_endless_go — explicit override wins, missing fails loudly"

    run_py '
import os, tempfile
import click
from endless import event_bridge as eb

fd, p = tempfile.mkstemp(suffix="-endless-go")
os.close(fd); os.chmod(p, 0o755)

got = eb._resolve_endless_go(override=p)
assert got == p, f"valid override not returned verbatim: {got!r}"

try:
    eb._resolve_endless_go(override="/nonexistent/endless-go-xyz")
    raise AssertionError("missing override did not raise")
except click.ClickException:
    pass

got = eb._resolve_endless_go(override=None)
assert got != p, "override=None used the pinned path"
assert got.endswith("endless-go"), f"override=None resolved unexpectedly: {got!r}"

os.unlink(p)
print("OK")
'
    local desc="override returned when valid; loud error when missing; ignored when None"
    if [[ "${PY_RC}" -eq 0 && "${PY_OUT}" == *"OK"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "Python checks print OK and exit 0" "${PY_OUT}"
    fi
}

# ─── 4. _resolve_land_endless_go invariant ──────────────────────────────────

test_land_invariant() {
    section "_resolve_land_endless_go — self_dev uses worktree bin or fails loudly"

    run_py '
import os, json, tempfile
from pathlib import Path
import click
from endless import worktree_cmd as wc

base = Path(tempfile.mkdtemp())

# self_dev project with a built worktree binary
proj = base / "proj"
(proj / ".endless").mkdir(parents=True)
(proj / ".endless" / "config.json").write_text(json.dumps({"self_dev": True}))
wt = proj / ".endless" / "worktrees" / "e-1664"
(wt / "bin").mkdir(parents=True)
binp = wt / "bin" / "endless-go"
binp.write_text("#!/bin/sh\n"); os.chmod(binp, 0o755)

got = wc._resolve_land_endless_go(wt, proj)
assert got == str(binp), f"self_dev+present: expected {binp}, got {got!r}"

# self_dev project, binary missing -> loud error mentioning the build
os.remove(binp)
try:
    wc._resolve_land_endless_go(wt, proj)
    raise AssertionError("self_dev+missing did not raise")
except click.ClickException as e:
    msg = e.message.lower()
    assert "build" in msg, f"missing-binary error lacks build hint: {e.message!r}"

# non-self_dev project -> None (global is correct; no worktree binary exists)
proj2 = base / "proj2"
(proj2 / ".endless").mkdir(parents=True)
(proj2 / ".endless" / "config.json").write_text(json.dumps({}))
wt2 = proj2 / ".endless" / "worktrees" / "e-1664"
(wt2 / "bin").mkdir(parents=True)
assert wc._resolve_land_endless_go(wt2, proj2) is None, "non-self_dev should be None"

print("OK")
'
    local desc="self_dev+present -> wt bin; self_dev+missing -> loud error; non-self_dev -> None"
    if [[ "${PY_RC}" -eq 0 && "${PY_OUT}" == *"OK"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "Python checks print OK and exit 0" "${PY_OUT}"
    fi
}

# ─── 5. Justfile no longer PATH-prepends the land call ──────────────────────

test_justfile_plain_land() {
    section "Justfile — the land recipe calls 'endless worktree land' plainly"

    local recipe
    recipe=$(just --show land 2>/dev/null)

    local desc1="land invokes 'endless worktree land \"\$tid\"' without a PATH wrapper"
    if [[ "${recipe}" == *'endless worktree land "$tid"'* \
          && "${recipe}" != *'PATH="$wt/bin:$PATH" endless worktree land'* ]]; then
        report_pass "${desc1}"
    else
        report_fail "${desc1}" \
            'plain `endless worktree land "$tid"`, no `PATH="$wt/bin:$PATH"` before it' \
            "$(printf '%s' "${recipe}" | grep -n 'worktree land' || echo '<no match>')"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${LEDGER_REPO}" && -d "${LEDGER_REPO}" ]] && rm -rf "${LEDGER_REPO}"
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    local missing=0
    command -v sqlite3 >/dev/null 2>&1 || { printf 'ERROR: sqlite3 not on PATH\n' >&2; missing=1; }
    command -v just >/dev/null 2>&1 || { printf 'ERROR: just not on PATH\n' >&2; missing=1; }
    command -v git >/dev/null 2>&1 || { printf 'ERROR: git not on PATH\n' >&2; missing=1; }
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; missing=1; }
    [[ "${missing}" -eq 0 ]] || exit 2

    WT_BIN="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${WT_BIN}" ]]; then
        printf 'ERROR: worktree endless-go not built (run: just build)\n' >&2
        exit 2
    fi

    trap cleanup EXIT
    make_ledger_repo

    printf '%sE-1664 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${REPO_ROOT}"
    printf '  binary:  %s\n' "${WT_BIN}"

    test_gate_armed
    test_matching_passes
    test_override_honored
    test_land_invariant
    test_justfile_plain_land

    summary
}

main "$@"
