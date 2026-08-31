#!/usr/bin/env bash
#
# E-1533 verification — `endless decision update <id>` edits a decision's
# title and/or description IN PLACE (no new ID, no rejected row), re-emitting
# the `.endless/decisions/ED-NNN.md` mirror from the new DB content.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1533
#
# Strategy (mirrors e-1747-verify.sh): build a FULLY ISOLATED throwaway
# environment (temp XDG_CONFIG_HOME + fresh DB, temp git project, a real linked
# worktree) and drive the CANDIDATE Python CLI (`.venv/bin/endless`) + the
# worktree-built `bin/endless-go`. Nothing touches the real endless repo, the
# real ledger, or this worktree's own branch — teardown is `rm -rf`.
#
# What it checks:
#   1. update --title           -> title changes in place, SAME ED-id, status
#                                  unchanged; NO new decision, NO rejected row.
#   2. update --description      -> description changes; mirror rewritten and
#                                  committed as "Endless: update decision ED-N".
#   3. update --description-file -> description loaded from a file.
#   4. editable in any status    -> a rejected decision's title can be corrected.
#   5. guards: no flags, empty title, "record that ..." title, unknown id.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any
# failure, 2 setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_contains DESC PATTERN CMD...
assert_contains() {
    local desc="$1" pat="$2"; shift 2
    local out
    out=$("$@" 2>&1)
    if [[ "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pat}" "${out}"
}

# assert_not_contains DESC PATTERN CMD...
assert_not_contains() {
    local desc="$1" pat="$2"; shift 2
    local out
    out=$("$@" 2>&1)
    if [[ "${out}" != *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output does NOT contain: ${pat}" "${out}"
}

# assert_file_has DESC FILE PATTERN — file exists and contains PATTERN.
assert_file_has() {
    local desc="$1" file="$2" pat="$3"
    if [[ ! -f "${file}" ]]; then
        report_fail "${desc}" "file exists: ${file}" "absent"; return
    fi
    if grep -q -- "${pat}" "${file}"; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${pat}" "$(cat "${file}")"
}

# assert_head_subject DESC GITDIR SUBJECT — HEAD commit subject equals SUBJECT.
assert_head_subject() {
    local desc="$1" gitdir="$2" want="$3" got
    got=$(git -C "${gitdir}" log --format=%s -n 1 2>&1)
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "HEAD subject == ${want}" "${got}"
}

# assert_eq DESC WANT GOT
assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${want}" == "${got}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "== ${want}" "${got}"
}

# assert_refused DESC PATTERN CMD... — CMD exits non-zero AND output has PATTERN.
assert_refused() {
    local desc="$1" pat="$2"; shift 2
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${out}" == *"${pat}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "exit != 0 AND output contains: ${pat}" \
        "exit=${rc} | ${out}"
}

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=""
WORK=""
PROJ=""
WT=""
EN=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

# en — the CANDIDATE Python CLI run from the temp worktree cwd so decision
# mirrors ride the worktree branch and project-from-cwd resolves to our project.
en() { ( cd "${WT}" && "${EN}" "$@" ); }

# did_of OUTPUT — extract the first ED-NNN id from CLI output.
did_of() { printf '%s\n' "$1" | grep -oE 'ED-[0-9]+' | head -1; }

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${REPO_ROOT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${REPO_ROOT}" >&2; exit 2; }
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT

    export XDG_CONFIG_HOME="${WORK}/config"
    export XDG_CACHE_HOME="${WORK}/cache"
    export ENDLESS_AUTO_MIGRATE=1
    export PATH="${REPO_ROOT}/bin:${PATH}"
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    : > "${PROJ}/README.md"
    git -C "${PROJ}" add README.md
    git -C "${PROJ}" commit -q -m "init"

    ( cd "${PROJ}" && "${EN}" register "${PROJ}" --infer --name verify1533 \
        --status active ) >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }

    # A linked worktree so decision mirrors have somewhere to ride (and commit).
    WT="${PROJ}/.endless/worktrees/e-1"
    git -C "${PROJ}" worktree add -q -b "task/1" "${WT}" main 2>/dev/null || {
        printf 'ERROR: creating worktree failed\n' >&2; exit 2; }
    mkdir -p "${WT}/.endless"
    printf '{"kind":"task","base_branch":"main","branch":"task/1"}\n' \
        > "${WT}/.endless/worktree.json"
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_title_in_place() {
    section "1 — update --title edits in place (same id, no new/rejected row)"

    local out did
    out=$(en decision add "Original title" --project verify1533 2>&1)
    did=$(did_of "${out}")
    [[ -z "${did}" ]] && { report_fail "seed decision returns ED-id" "ED-NNN" "${out}"; return; }

    local before_count
    before_count=$(en decision list --project verify1533 --llm 2>/dev/null | grep -cE '^ED-')

    assert_contains "update --title succeeds" "Updated ${did}" \
        en decision update "${did}" --title "Corrected title in place"

    assert_contains "new title shows on the SAME id" "Corrected title in place" \
        en decision show "${did}"
    assert_not_contains "old title is gone" "Original title" \
        en decision show "${did}"
    assert_contains "status stays proposed (no reject dance)" "proposed" \
        en decision show "${did}"

    local after_count
    after_count=$(en decision list --project verify1533 --llm 2>/dev/null | grep -cE '^ED-')
    assert_eq "no new decision row created" "${before_count}" "${after_count}"
    assert_not_contains "no rejected row introduced" "rejected" \
        en decision list --project verify1533 --llm
}

check_description_and_mirror() {
    section "2 — update --description rewrites DB + the .md mirror"

    local out did
    out=$(en decision add "Body decision" \
        --description "ORIGINAL-BODY-MARKER" --project verify1533 2>&1)
    did=$(did_of "${out}")
    [[ -z "${did}" ]] && { report_fail "seed decision returns ED-id" "ED-NNN" "${out}"; return; }

    assert_contains "update --description succeeds" "Updated ${did}" \
        en decision update "${did}" --description "UPDATED-BODY-MARKER"

    assert_contains "new body shows via decision show (DB is source of truth)" \
        "UPDATED-BODY-MARKER" en decision show "${did}"

    local num="${did#ED-}"
    assert_file_has "mirror rewritten to new body" \
        "${WT}/.endless/decisions/ED-${num}.md" "UPDATED-BODY-MARKER"
    assert_not_contains "mirror no longer has the old body" "ORIGINAL-BODY-MARKER" \
        cat "${WT}/.endless/decisions/ED-${num}.md"
    assert_head_subject "mirror rewrite committed as an update" \
        "${WT}" "Endless: update decision ED-${num}"
}

check_description_file() {
    section "3 — update --description-file loads from a file"

    local out did dfile
    out=$(en decision add "File-body decision" --project verify1533 2>&1)
    did=$(did_of "${out}")
    [[ -z "${did}" ]] && { report_fail "seed decision returns ED-id" "ED-NNN" "${out}"; return; }

    # No trailing newline: description is single-line (validate_description
    # rejects embedded newlines, same guard as `decision add`).
    dfile="${WORK}/body.txt"
    printf 'BODY-FROM-FILE-MARKER' > "${dfile}"
    assert_contains "update --description-file succeeds" "Updated ${did}" \
        en decision update "${did}" --description-file "${dfile}"
    assert_contains "file body shows via decision show" "BODY-FROM-FILE-MARKER" \
        en decision show "${did}"
}

check_any_status() {
    section "4 — editable in any status (a rejected decision's wording)"

    local out did
    out=$(en decision add "Decision to reject then edit" --project verify1533 2>&1)
    did=$(did_of "${out}")
    [[ -z "${did}" ]] && { report_fail "seed decision returns ED-id" "ED-NNN" "${out}"; return; }

    en decision reject "${did}" --reason "wrong approach" >/dev/null 2>&1
    assert_contains "rejected decision title still editable" "Updated ${did}" \
        en decision update "${did}" --title "Corrected wording after reject"
    assert_contains "corrected title shows on the rejected decision" \
        "Corrected wording after reject" en decision show "${did}"
}

check_guards() {
    section "5 — input guards"

    local out did
    out=$(en decision add "Guard decision" --project verify1533 2>&1)
    did=$(did_of "${out}")
    [[ -z "${did}" ]] && { report_fail "seed decision returns ED-id" "ED-NNN" "${out}"; return; }

    assert_refused "no flags is refused" "Nothing to update" \
        en decision update "${did}"
    assert_refused "empty title is refused" "may not be empty" \
        en decision update "${did}" --title "   "
    assert_refused "'record that ...' title is refused" "state the decision" \
        en decision update "${did}" --title "record that we chose X"
    assert_refused "unknown decision id is refused" "No decision found" \
        en decision update ED-9999 --title "nope"
}

# ─── main ───────────────────────────────────────────────────────────────────

# Fail-fast: the Python unit tests for update_decision (validation guards +
# emitted-payload shape) gate the slower end-to-end checks below.
check_unit_tests() {
    section "0 — decision_cmd unit tests (fail-fast gate)"
    local out rc
    out=$( ( cd "${REPO_ROOT}" && uv run pytest tests/test_decision_cmd.py -q ) 2>&1 )
    rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "tests/test_decision_cmd.py passes"
        return 0
    fi
    report_fail "tests/test_decision_cmd.py passes" "pytest exit == 0" \
        "exit=${rc}\n${out}"
    summary
    exit 1
}

main() {
    setup

    printf '%sE-1533 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s/bin/endless-go\n' "${REPO_ROOT}"
    printf '  env:     isolated (%s)\n' "${WORK}"

    check_unit_tests
    check_title_in_place
    check_description_and_mirror
    check_description_file
    check_any_status
    check_guards

    summary
}

main "$@"
