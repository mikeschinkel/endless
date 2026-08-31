#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2000 and records what was true when E-2000
# landed. Edit it only if you ARE E-2000. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2000 verification — a session records lessons in its OWN worktree, and the
# log lives in .endless/, not .claude/.
#
# Run from anywhere inside the worktree:
#   endless task verify E-2000
#
# Single entry point (per E-1596).
#
#   1. The reported symptom, end to end, with REAL git worktrees: a session
#      recording a lesson must leave the main checkout clean, and the entry must
#      ride the task branch instead.
#   2. The log moved to .endless/LESSONS.md. It is an Endless artifact and a
#      self_dev-only one; .claude/ is the Claude Code harness's directory.
#   3. The fix carries ZERO product code. worktree_cmd.py must be byte-identical
#      to the base branch.
#   4. CLAUDE.md and the log's own header both say the same thing, and neither
#      still issues the instruction that caused this.
#
# Exit 0 on all-passed, 1 on any failure.
#
# Why real `git worktree add` rather than two directories: "modifies the main
# checkout's working tree" is a statement about a shared .git and a tracked
# path. Two unrelated repos cannot express it, and the whole defect is that the
# write reached ACROSS a worktree boundary into another checkout.
#
# Why step 1 asserts the OLD way still dirties main: it is the control. Without
# it, a "main is clean" pass proves nothing — main would also be clean if the
# test never wrote anything.
#
#   5. `merge=union` on the log behaves as claimed, measured against real git:
#      concurrent appends merge with no duplication, an identical entry is kept
#      once, and the one hazard (two branches editing the SAME line) is pinned
#      so it stays known rather than discovered.
#
# Sections 5's fixtures each build their branches from a PRISTINE main. An
# earlier revision of this suite did not: it rebuilt task/A against a main that
# had already absorbed task/A, so the fixture appended the same entry twice and
# the assertion counted two and concluded union duplicates entries. It passed,
# and it was wrong. That false measurement is what justified a per-task-file +
# fold mechanism in worktree_cmd.py, since removed. A test that constructs its
# own expected failure proves nothing about the code.
#
# What this suite does NOT do: run any other task's verify script. Project-wide
# regression is `go build/vet/test ./...` + `just test`.
#
# Model: .endless/tasks/e-1997/verify.sh.

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
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

REPO_ROOT=""
TMP_ROOT=""

# Where the log lives now, and where it used to. Literals on purpose: a move
# this suite has not been taught about should fail here rather than silently
# weaken every assertion below.
LESSONS_LOG=".endless/LESSONS.md"
RETIRED_LOG=".claude/LESSONS.md"

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
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

note() {
    printf '  %s•%s %s\n' "${DIM}" "${RESET}" "$1"
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

assert_text_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains '${needle}'" "${haystack:0:300}"
}

assert_file_contains() {
    local desc="$1" needle="$2" file="$3"
    if grep -qF -- "${needle}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} contains '${needle}'" "absent"
}

assert_file_lacks() {
    local desc="$1" needle="$2" file="$3"
    if ! grep -qF -- "${needle}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} WITHOUT '${needle}'" "still present"
}

# ─── helpers ────────────────────────────────────────────────────────────────

# A throwaway repo with one real worktree hanging off it, mirroring the
# .endless/worktrees/e-NNN layout. Echoes the main checkout's path.
make_repo() {
    local root="$1" main="$1/main"
    mkdir -p "${main}/.endless"
    git -C "${main}" init -q -b main
    git -C "${main}" config user.email "test@example.com"
    git -C "${main}" config user.name "Test"
    printf '# Lessons Learned\n\n## Entries\n\n### [2026-01-01] Existing\n- old\n' \
        > "${main}/${LESSONS_LOG}"
    # As the real repo does (.gitignore:21). Without it the worktree dir itself
    # shows up as untracked in main and every "main is clean" assertion below
    # would be measuring the fixture instead of the fix.
    printf '.endless/worktrees/\n' > "${main}/.gitignore"
    git -C "${main}" add -A
    git -C "${main}" commit -q -m base
    git -C "${main}" worktree add -q -b task/2000 "${main}/.endless/worktrees/e-2000" main
    printf '%s' "${main}"
}

# Porcelain status of the MAIN checkout, whitespace-trimmed.
main_status() {
    git -C "$1" status --porcelain | tr -d '[:space:]'
}

# ─── tests ──────────────────────────────────────────────────────────────────

test_symptom() {
    section "Symptom — a worktree session must not reach into main"
    local main wt
    main=$(make_repo "${TMP_ROOT}/sym")
    wt="${main}/.endless/worktrees/e-2000"

    # Control: the OLD instruction. A session in the worktree appends to the
    # main checkout's tracked log, exactly as CLAUDE.md used to direct.
    printf '\n### [2026-08-20] Old way\n- appended from a worktree\n' >> "${main}/${LESSONS_LOG}"
    assert_eq "control: the old instruction DOES dirty main" \
        "M.endless/LESSONS.md" "$(main_status "${main}")"
    git -C "${main}" checkout -q -- "${LESSONS_LOG}"
    assert_eq "control reverted; main clean again" "" "$(main_status "${main}")"

    # The fix: the session appends to its OWN worktree's copy.
    printf '\n### [2026-08-20] New way\n- recorded in the worktree\n' >> "${wt}/${LESSONS_LOG}"
    assert_eq "recording a lesson leaves the main checkout clean" \
        "" "$(main_status "${main}")"

    git -C "${wt}" add -A
    git -C "${wt}" commit -q -m "E-2000: lesson"
    assert_eq "the lesson rides the task branch, not main" \
        "${LESSONS_LOG}" \
        "$(git -C "${main}" diff --name-only main task/2000)"

    git -C "${main}" merge -q --ff-only task/2000
    assert_file_contains "landing carries it to main" \
        "recorded in the worktree" "${main}/${LESSONS_LOG}"
    assert_file_contains "pre-existing entries survive" \
        "### [2026-01-01] Existing" "${main}/${LESSONS_LOG}"
    assert_eq "main clean after the land" "" "$(main_status "${main}")"
}

test_log_location() {
    section "Location — the log is an Endless artifact, not a harness one"
    assert_eq "${LESSONS_LOG} exists" "yes" \
        "$([[ -f "${REPO_ROOT}/${LESSONS_LOG}" ]] && echo yes || echo no)"
    assert_eq "${RETIRED_LOG} is gone" "yes" \
        "$([[ ! -e "${REPO_ROOT}/${RETIRED_LOG}" ]] && echo yes || echo no)"
    assert_eq "the move preserved history (tracked at the new path)" \
        "${LESSONS_LOG}" \
        "$(git -C "${REPO_ROOT}" ls-files "${LESSONS_LOG}")"
    assert_eq "the retired path is tracked nowhere" "" \
        "$(git -C "${REPO_ROOT}" ls-files "${RETIRED_LOG}")"
    # The per-task lesson files an earlier revision of this task introduced.
    assert_eq "no per-task lesson files left behind" "" \
        "$(git -C "${REPO_ROOT}" ls-files '.claude/lessons' '.endless/lessons')"
}

test_no_product_code() {
    section "Cost — the fix carries no product code"
    local base diff
    base=$(git -C "${REPO_ROOT}" merge-base HEAD main 2>/dev/null)
    if [[ -z "${base}" ]]; then
        report_fail "resolve the base commit" "a merge-base with main" "none"
        return
    fi
    diff=$(git -C "${REPO_ROOT}" diff --name-only "${base}" -- src/ internal/ cmd/)
    assert_eq "src/, internal/ and cmd/ are untouched" "" "${diff}"
}

test_claude_md() {
    section "CLAUDE.md — the instruction that caused this is gone"
    local md="${REPO_ROOT}/CLAUDE.md"
    assert_file_lacks "no longer says to append to the main checkout" \
        "the *main checkout*, always, even" "${md}"
    assert_file_lacks "the invented worktree-drop rationale is gone" \
        "would be destroyed when the worktree is dropped" "${md}"
    assert_file_contains "names the worktree-local path" \
        "<worktree>/.endless/LESSONS.md" "${md}"
    assert_file_contains "says why it is .endless and not .claude" \
        "It lives in \`.endless/\`, not \`.claude/\`" "${md}"
    assert_file_contains "points at the union attribute" \
        "merge=union" "${md}"
    assert_file_contains "names union's same-line hazard" \
        "*same-line* edit" "${md}"
    assert_file_contains "withholds union from the DB ledger" \
        "db-ledger" "${md}"
    assert_file_contains "retired path is marked retired" \
        "is the retired location" "${md}"
}

test_log_header() {
    section "LESSONS.md header — agrees with CLAUDE.md"
    local log="${REPO_ROOT}/${LESSONS_LOG}"
    # Header only: everything above the Format block. Entries below it quote the
    # old wording verbatim (that is what a lesson about it looks like), so a
    # whole-file assertion would fail on its own history.
    local header="${TMP_ROOT}/header.md"
    sed -n '1,/^## Format/p' "${log}" > "${header}"
    assert_file_lacks "no longer tells sessions to read it at session start" \
        "Review this file at the start of each session" "${header}"
    assert_file_contains "says write-only" "write-only" "${header}"
    assert_file_contains "says do not read" "**Do NOT read this file**" "${header}"
    assert_file_contains "says to write to your own worktree's copy" \
        "Write to your own worktree's copy" "${header}"
    assert_file_contains "explains that concurrent appends self-merge" \
        "merge=union" "${header}"
    assert_file_contains "names union's same-line hazard" \
        "same-line edit" "${header}"
}

# fresh_log_repo DIR -> a repo with the log, the union attribute, and NO
# branches. Callers build their branches from a pristine main.
#
# Rebuilding branches against a main that has ALREADY absorbed one of them is
# how the first version of this suite convinced itself union duplicates entries:
# the fixture appended the same entry twice and the assertion dutifully counted
# two. Every case below starts from a clean main for that reason.
fresh_log_repo() {
    local main="$1"
    mkdir -p "${main}/.endless"
    git -C "${main}" init -q -b main
    git -C "${main}" config user.email "test@example.com"
    git -C "${main}" config user.name "Test"
    printf '# Lessons Learned\n\nHEADER LINE\n\n## Entries\n\n### [2026-01-01] Old\n- one\n' \
        > "${main}/${LESSONS_LOG}"
    printf '%s merge=union\n' "${LESSONS_LOG}" > "${main}/.gitattributes"
    git -C "${main}" add -A
    git -C "${main}" commit -q -m base
}

# land_b_onto_a MAIN -> "CLEAN" or "CONFLICT", having rebased task/B onto a main
# that has fast-forwarded task/A. Leaves the rebased tree in place on success.
land_b_onto_a() {
    local main="$1"
    git -C "${main}" checkout -q main
    git -C "${main}" merge -q --ff-only task/A
    git -C "${main}" checkout -q task/B
    if git -C "${main}" rebase main >/dev/null 2>&1; then
        printf 'CLEAN'
    else
        git -C "${main}" rebase --abort >/dev/null 2>&1
        printf 'CONFLICT'
    fi
}

test_union_appends() {
    section "Union — concurrent appends, the common case"
    local main="${TMP_ROOT}/u1/main"
    fresh_log_repo "${main}"
    local b
    for b in A B; do
        git -C "${main}" checkout -q -b "task/${b}" main
        printf '\n### [2026-08-20] From %s\n- %s\n' "${b}" "${b}" >> "${main}/${LESSONS_LOG}"
        git -C "${main}" add -A
        git -C "${main}" commit -q -m "${b}"
    done
    assert_eq "two branches appending different entries rebase cleanly" \
        "CLEAN" "$(land_b_onto_a "${main}")"
    assert_eq "entry A survives exactly once" "1" \
        "$(grep -c 'From A' "${main}/${LESSONS_LOG}")"
    assert_eq "entry B survives exactly once" "1" \
        "$(grep -c 'From B' "${main}/${LESSONS_LOG}")"
}

test_union_identical_entry() {
    section "Union — the same entry recorded on both branches"
    local main="${TMP_ROOT}/u2/main"
    fresh_log_repo "${main}"
    local b
    for b in A B; do
        git -C "${main}" checkout -q -b "task/${b}" main
        printf '\n### [2026-08-20] Same lesson\n- identical text\n' >> "${main}/${LESSONS_LOG}"
        git -C "${main}" add -A
        git -C "${main}" commit -q -m "${b}"
    done
    assert_eq "rebases cleanly" "CLEAN" "$(land_b_onto_a "${main}")"
    # Not a conflict at all: both sides made the identical change, so git takes
    # it once. This is the assertion an earlier revision of this suite got
    # backwards, and the reason the whole per-task-file mechanism was built.
    assert_eq "kept once, not duplicated" "1" \
        "$(grep -c 'Same lesson' "${main}/${LESSONS_LOG}")"
}

test_union_same_line_hazard() {
    section "Union — its one real hazard, pinned so it stays known"
    local main="${TMP_ROOT}/u3/main"
    fresh_log_repo "${main}"
    git -C "${main}" checkout -q -b task/A main
    sed -i "" 's/HEADER LINE/HEADER FROM A/' "${main}/${LESSONS_LOG}"
    git -C "${main}" add -A && git -C "${main}" commit -q -m A
    git -C "${main}" checkout -q -b task/B main
    sed -i "" 's/HEADER LINE/HEADER FROM B/' "${main}/${LESSONS_LOG}"
    git -C "${main}" add -A && git -C "${main}" commit -q -m B

    assert_eq "same-line edits do NOT conflict under union" \
        "CLEAN" "$(land_b_onto_a "${main}")"
    # Both survive, silently, one after the other. Appends are safe; editing the
    # header concurrently is the case to look at what landed.
    assert_eq "...both versions of the line survive (the documented hazard)" \
        "1" "$(grep -c 'HEADER FROM A' "${main}/${LESSONS_LOG}")"
    assert_eq "...including the other one" "1" \
        "$(grep -c 'HEADER FROM B' "${main}/${LESSONS_LOG}")"
}

test_attribute_scoping() {
    section "Attribute — applied to the log, withheld from the ledger"
    local got
    got=$(git -C "${REPO_ROOT}" check-attr merge -- "${LESSONS_LOG}")
    assert_text_contains "the log gets merge=union" "merge: union" "${got}"

    # verbs.jsonl is the precedent this follows (E-1268).
    got=$(git -C "${REPO_ROOT}" check-attr merge -- .endless/verbs.jsonl)
    assert_text_contains "verbs.jsonl still has it" "merge: union" "${got}"

    # The ledger must NOT: concatenating both sides would duplicate DB mutation
    # records. It avoids conflicts by sharding filenames per machine instead.
    got=$(git -C "${REPO_ROOT}" check-attr merge -- .endless/db-ledger/db-entries-0000-000001.jsonl)
    assert_text_contains "the DB ledger does NOT" "merge: unspecified" "${got}"

    # check-attr passes on a mangled comment, so it cannot see a truncated or
    # duplicated block. This caught a real one: the rule was re-applied by
    # copying a fixed line count off the tail of the file, which clipped the
    # first two lines of its own comment and left it starting mid-sentence.
    local block
    block=$(awk '/^\.endless\/verbs\.jsonl/{f=1;next} f' "${REPO_ROOT}/.gitattributes")
    assert_text_contains "the log's rule is introduced by its own comment" \
        "# E-2000:" "$(printf '%s' "${block}" | head -1)"
    assert_eq "no duplicated sentence fragments in the block" "0" \
        "$(printf '%s' "${block}" | grep -c 'Measured.*Measured')"
    assert_eq "every non-blank line before the rule is a comment" "0" \
        "$(printf '%s' "${block}" | sed '$d' | grep -cv '^#')"
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]] && rm -rf "${TMP_ROOT}"
    return 0
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/e-2000.XXXXXX") || exit 2
    trap cleanup EXIT

    printf '%sE-2000 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:      %s\n' "${REPO_ROOT}"
    printf '  scratch:  %s\n' "${TMP_ROOT}"

    test_symptom
    test_log_location
    test_no_product_code
    test_claude_md
    test_log_header
    test_union_appends
    test_union_identical_entry
    test_union_same_line_hazard
    test_attribute_scoping

    summary
}

main "$@"
