#!/usr/bin/env bash
#
# E-2051 verification — LESSONS.md follows the verbs.jsonl model.
#
# Before: sessions appended corrections to their OWN worktree's
# `.endless/LESSONS.md` and committed them on the task branch, which re-dirtied
# an already-landed branch every time and made the session read unsettled again.
# After: sessions append to the MAIN checkout's copy, never commit it, and
# `worktree land`'s existing auto-commit sweep records it — exactly what
# `.endless/verbs.jsonl` already does.
#
# Run from inside the worktree (esu puts you there):
#   endless task verify E-2051
#
# What it proves:
#   1. FAIL-FAST unit gate: the affected Go + Python unit suites pass.
#   2. Python AUTO_COMMIT_GLOBS carries ".endless/LESSONS.md" (and still carries
#      the pre-existing entries — the addition is additive, not a swap).
#   3. The Go mirror (internal/monitor.AutoManagedStatusGlobs) carries it too,
#      and the two lists agree entry-for-entry.
#   4. CLAUDE.md states the new rule (main checkout, never commit) and no longer
#      teaches the worktree-copy / commit-on-branch convention.
#   5. No stale worktree-copy or commit-on-branch wording survives anywhere in
#      the tracked docs/source sweep.
#   6. The guide's auto-commit table lists the new path (doc/code sync, the same
#      invariant .endless/tasks/e-1870/verify.sh pins).
#   7. FUNCTIONAL, in a throwaway repo with no real DB or ledger writes: a
#      modified `.endless/LESSONS.md` on the main checkout partitions as
#      endless-managed (auto-commit), not as user work that would make land
#      refuse.
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

PY_SRC="${WT}/src/endless/worktree_cmd.py"
GO_SRC="${WT}/internal/monitor/worktree_anomalies.go"
CLAUDE_MD="${WT}/CLAUDE.md"
ORCH="${WT}/docs/guide/orchestration.md"

LESSONS_PATH=".endless/LESSONS.md"

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
}

setup_error() { printf 'SETUP ERROR: %s\n' "$1" >&2; exit 2; }

TMP_E2051=""
cleanup() { [[ -n "${TMP_E2051}" ]] && rm -rf "${TMP_E2051}"; }
trap cleanup EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# If the unit suites that own these two mirrors are red, every assertion below
# is noise. Stop here rather than printing a wall of derived failures.
section "1. Unit gate (fail-fast)"

if go test ./internal/monitor/ -run 'TestIsAutoManagedPath|TestUserStatusPaths' >/tmp/e2051-go.log 2>&1; then
    report_pass "go test ./internal/monitor (auto-managed path globs)"
else
    report_fail "go test ./internal/monitor" "pass" "failed — see /tmp/e2051-go.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

if uv run pytest -q tests/test_worktree_land_dedup.py tests/test_worktree_land_modified_guard.py \
        tests/test_worktree_land_conflict_msg.py >/tmp/e2051-py.log 2>&1; then
    report_pass "pytest (land glob registry, modified guard, conflict message)"
else
    report_fail "pytest land suites" "pass" "failed — see /tmp/e2051-py.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# ── 2. Python glob registry ─────────────────────────────────────────────────
section "2. Python AUTO_COMMIT_GLOBS"

py_globs=$(uv run python -c \
    'from endless.worktree_cmd import AUTO_COMMIT_GLOBS; print("\n".join(AUTO_COMMIT_GLOBS))' \
    2>/tmp/e2051-pyimp.log) \
    || setup_error "could not import AUTO_COMMIT_GLOBS (see /tmp/e2051-pyimp.log)"

if grep -qxF -- "${LESSONS_PATH}" <<<"${py_globs}"; then
    report_pass "AUTO_COMMIT_GLOBS carries '${LESSONS_PATH}'"
else
    report_fail "AUTO_COMMIT_GLOBS carries '${LESSONS_PATH}'" \
        "the path listed" "$(tr '\n' ' ' <<<"${py_globs}")"
fi

# The addition must not have displaced what was already there.
for keep in ".endless/verbs.jsonl" ".endless/db-ledger/*.jsonl"; do
    if grep -qxF -- "${keep}" <<<"${py_globs}"; then
        report_pass "AUTO_COMMIT_GLOBS still carries '${keep}'"
    else
        report_fail "AUTO_COMMIT_GLOBS still carries '${keep}'" "present" "missing"
    fi
done

# Runtime cross-check: the matcher, not just the tuple literal.
if uv run python -c \
    'import sys
from endless.worktree_cmd import _is_auto_commit_path
sys.exit(0 if _is_auto_commit_path(".endless/LESSONS.md") else 1)' 2>/dev/null; then
    report_pass "_is_auto_commit_path('${LESSONS_PATH}') is True"
else
    report_fail "_is_auto_commit_path('${LESSONS_PATH}')" "True" "False"
fi

# ── 3. the Go mirror agrees ─────────────────────────────────────────────────
# worktree_cmd.py's comment says these two lists are mirrors; `worktree check`
# and `session status` partition against the Go one. A one-sided edit means a
# clean worktree still reads dirty.
section "3. Go mirror (internal/monitor.AutoManagedStatusGlobs)"

go_globs=$(awk '/^var AutoManagedStatusGlobs = \[\]string\{/,/^\}/' "${GO_SRC}" \
    | grep -oE '"[^"]+"' | tr -d '"')
[[ -n "${go_globs}" ]] || setup_error "could not extract AutoManagedStatusGlobs from ${GO_SRC}"

if grep -qxF -- "${LESSONS_PATH}" <<<"${go_globs}"; then
    report_pass "AutoManagedStatusGlobs carries '${LESSONS_PATH}'"
else
    report_fail "AutoManagedStatusGlobs carries '${LESSONS_PATH}'" \
        "the path listed" "$(tr '\n' ' ' <<<"${go_globs}")"
fi

if [[ "$(sort <<<"${py_globs}")" == "$(sort <<<"${go_globs}")" ]]; then
    report_pass "Python and Go glob lists agree entry-for-entry"
else
    report_fail "Python and Go glob lists agree" \
        "$(sort <<<"${py_globs}" | tr '\n' ' ')" \
        "$(sort <<<"${go_globs}" | tr '\n' ' ')"
fi

# ── 4. CLAUDE.md carries the new rule ───────────────────────────────────────
section "4. CLAUDE.md rule"

if grep -qF -- "in the main checkout" "${CLAUDE_MD}"; then
    report_pass "CLAUDE.md sends lesson appends to the main checkout"
else
    report_fail "CLAUDE.md sends lesson appends to the main checkout" \
        "\"in the main checkout\" in the Memory-is-OFF section" "not found"
fi

if grep -qF -- "Never commit it." "${CLAUDE_MD}"; then
    report_pass "CLAUDE.md forbids committing the lessons file"
else
    report_fail "CLAUDE.md forbids committing the lessons file" \
        "a \"Never commit it.\" rule" "not found"
fi

if grep -qF -- "worktree land\` auto-commits it" "${CLAUDE_MD}"; then
    report_pass "CLAUDE.md names land's auto-commit as what records it"
else
    report_fail "CLAUDE.md names land's auto-commit" \
        "\"\`endless worktree land\` auto-commits it\"" "not found"
fi

# The two rules the task exists to delete.
if grep -qF -- "in your own worktree" "${CLAUDE_MD}"; then
    report_fail "CLAUDE.md drops the worktree-copy convention" \
        "no \"in your own worktree\"" "still present"
else
    report_pass "CLAUDE.md drops the worktree-copy convention"
fi

if grep -qF -- "commit it on your branch" "${CLAUDE_MD}"; then
    report_fail "CLAUDE.md drops commit-on-branch for lessons" \
        "no \"commit it on your branch\"" "still present"
else
    report_pass "CLAUDE.md drops commit-on-branch for lessons"
fi

# The surrounding rules the plan says to keep.
if grep -qF -- "Never read that file." "${CLAUDE_MD}"; then
    report_pass "CLAUDE.md keeps the never-read rule"
else
    report_fail "CLAUDE.md keeps the never-read rule" "\"Never read that file.\"" "missing"
fi

if grep -qF -- 'Name the full path you wrote to' "${CLAUDE_MD}"; then
    report_pass "CLAUDE.md keeps the name-the-path rule"
else
    report_fail "CLAUDE.md keeps the name-the-path rule" \
        "\"Name the full path you wrote to\"" "missing"
fi

# ── 5. docs/source sweep is clean ───────────────────────────────────────────
# The old convention could be restated anywhere that instructs a session. The
# corrections log itself is excluded: it is an append-only history that quotes
# the superseded wording on purpose, and it is never read as instructions.
section "5. No stale worktree-copy wording anywhere"

stale=$(grep -rn -e "in your own worktree" -e "commit it on your branch" \
    CLAUDE.md docs/ src/ internal/ tests/ 2>/dev/null \
    | grep -v "tests/tasks/e-2051-verify.sh" || true)
if [[ -z "${stale}" ]]; then
    report_pass "no stale worktree-copy / commit-on-branch wording in tracked instructions"
else
    report_fail "no stale worktree-copy wording" "no matches" "${stale}"
fi

# ── 6. guide doc/code sync ──────────────────────────────────────────────────
# Same invariant e-1870-verify.sh pins: every auto-commit glob is documented in
# the guide's "Committing your work" table, or a session reading the guide is
# told a narrower list than the code actually sweeps.
section "6. Guide auto-commit table"

commit_section=$(awk '/^### Committing your work$/,/^### Landing the work$/' "${ORCH}")
[[ -n "${commit_section}" ]] || setup_error "no 'Committing your work' section in ${ORCH}"

while IFS= read -r g; do
    [[ -z "${g}" ]] && continue
    if grep -qF -- "${g}" <<<"${commit_section}"; then
        report_pass "guide documents auto-commit glob: ${g}"
    else
        report_fail "guide documents auto-commit glob: ${g}" "listed in the table" "absent"
    fi
done <<<"${py_globs}"

# ── 7. functional: land's partition, in a throwaway repo ────────────────────
# The point of the glob is behavioral: on the MAIN checkout, a dirty
# LESSONS.md must land in the auto-commit bucket, not the user-work bucket that
# makes `land` refuse. Proven against a scratch git repo — no real DB, ledger,
# or endless command touches anything.
section "7. Functional: main-checkout partition"

TMP_E2051=$(mktemp -d "${TMPDIR:-/tmp}/e2051.XXXXXX") || setup_error "mktemp failed"
repo="${TMP_E2051}/main"
mkdir -p "${repo}/.endless" || setup_error "mkdir failed"
(
    cd "${repo}" || exit 1
    git init -q . && \
    git config user.email e2051@example.test && \
    git config user.name "E-2051 fixture" && \
    printf 'lesson one\n' > .endless/LESSONS.md && \
    printf 'source\n' > app.py && \
    git add -A && git commit -qm init
) >/dev/null 2>&1 || setup_error "could not build the scratch repo"

# Dirty BOTH buckets: a lesson append (endless-managed) and a source edit (user
# work). The partition must separate them.
printf 'lesson two\n' >> "${repo}/.endless/LESSONS.md"
printf 'edit\n' >> "${repo}/app.py"

partition=$(uv run python -c "
from pathlib import Path
from endless.worktree_cmd import _git_status_partition
auto, user = _git_status_partition(Path('${repo}'))
print('AUTO:' + ','.join(sorted(auto)))
print('USER:' + ','.join(sorted(user)))
" 2>/tmp/e2051-part.log) || setup_error "partition probe failed (see /tmp/e2051-part.log)"

auto_line=$(grep '^AUTO:' <<<"${partition}" | cut -d: -f2-)
user_line=$(grep '^USER:' <<<"${partition}" | cut -d: -f2-)

if [[ "${auto_line}" == "${LESSONS_PATH}" ]]; then
    report_pass "a modified LESSONS.md partitions as endless-managed (auto-commit)"
else
    report_fail "LESSONS.md partitions as endless-managed" "${LESSONS_PATH}" "${auto_line}"
fi

if [[ "${user_line}" == "app.py" ]]; then
    report_pass "real source edits still partition as user work (land still refuses them)"
else
    report_fail "source edits partition as user work" "app.py" "${user_line}"
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
