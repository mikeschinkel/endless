#!/usr/bin/env bash
#
# E-2055 verification — recording a lesson never requires a land.
#
# Before: a session appended to the MAIN checkout's `.endless/LESSONS.md` by
# hand (E-2051) and the append then sat uncommitted on main until some task's
# `worktree land` swept it up — so recording a correction meant re-landing a
# task that was otherwise finished.
# After: `endless lesson write` appends AND commits that one file on main in
# the same step, exactly as `endless verb add` does for `.endless/verbs.jsonl`
# (E-1208), and land's auto-commit entry for the log is retired.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-2055-verify.sh
#
# What it proves:
#   1. FAIL-FAST unit gate: the Go + Python unit suites this task owns pass.
#   2. The command exists and its help states the contract (main checkout,
#      384-char summary, derived 60-char subject).
#   3. Both auto-commit mirrors have DROPPED ".endless/LESSONS.md", still carry
#      the live entries, and agree entry-for-entry — a one-sided edit would make
#      `worktree check` and `land` disagree about what is user work.
#   4. CLAUDE.md's Memory-is-OFF rule names the command and no longer teaches a
#      raw append or land's sweep; the log's own header agrees with it.
#   5. No stale "land auto-commits the lessons file" wording survives in the
#      tracked instruction sweep.
#   6. The guide's auto-commit table still documents every live glob (the
#      doc/code sync tests/tasks/e-1870-verify.sh pins) and the new command has
#      a guide-map entry.
#   7. FUNCTIONAL, in a throwaway repo with no real DB or ledger writes:
#      a. a modified `.endless/LESSONS.md` now partitions as USER WORK, so a
#         worktree that edits it is no longer silently swept;
#      b. a real `lesson write` into a scratch project creates the log, renders
#         the entry, commits exactly that one path on main, leaves the tree
#         clean, and preserves unrelated dirt;
#      c. run from a WORKTREE of that project, it still writes main's copy and
#         leaves the task branch untouched — the pain this task closes;
#      d. a summary longer than a subject holds is written whole and only the
#         SUBJECT truncates; over its own 384-char cap it is refused outright,
#         before anything is written.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

PY_SRC="${WT}/src/endless/worktree_cmd.py"
GO_SRC="${WT}/internal/monitor/worktree_anomalies.go"
CLAUDE_MD="${WT}/CLAUDE.md"
ORCH="${WT}/docs/guide/orchestration.md"
LOG_HEADER_SRC="${WT}/.endless/LESSONS.md"
GUIDE_MAP="${WT}/docs/guide/help/lesson.md"

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
    return 0
}

setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

TMP_E2055=""
cleanup() { [[ -n "${TMP_E2055}" ]] && rm -rf "${TMP_E2055}"; }
trap cleanup EXIT

for f in "${PY_SRC}" "${GO_SRC}" "${CLAUDE_MD}" "${ORCH}" "${GUIDE_MAP}"; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# If the suites that own this behaviour are red, every assertion below is
# derived noise. Stop here rather than printing a wall of consequences.
section "1. Unit gate (fail-fast)"

if go test ./internal/monitor/ -run 'TestIsAutoManagedPath|TestUserStatusPaths' \
        >/tmp/e2055-go.log 2>&1; then
    report_pass "go test ./internal/monitor (auto-managed path globs)"
else
    report_fail "go test ./internal/monitor" "pass" "failed — see /tmp/e2055-go.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

if uv run pytest -q tests/test_lesson_write.py tests/test_main_commit.py \
        tests/test_verb_gate.py tests/test_worktree_land_dedup.py \
        tests/test_guide_map.py >/tmp/e2055-py.log 2>&1; then
    report_pass "pytest (lesson write, main_commit, verb commit, glob registry, guide map)"
else
    report_fail "pytest" "pass" "failed — see /tmp/e2055-py.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# ── 2. the command exists and states its contract ───────────────────────────
section "2. \`endless lesson write\` surface"

help_out=$(uv run endless lesson write --help 2>/tmp/e2055-help.log) \
    || setup_error "could not run 'endless lesson write --help' (see /tmp/e2055-help.log)"
# Click rewraps help to the terminal width, so a phrase can straddle a newline.
# Collapse whitespace before matching, or the assertions test the wrap point.
help_flat=$(tr '\n' ' ' <<<"${help_out}" | tr -s ' ')

while IFS='|' read -r needle label; do
    if grep -qF -- "${needle}" <<<"${help_flat}"; then
        report_pass "help ${label}"
    else
        report_fail "help ${label}" "'${needle}' in --help" "absent"
    fi
done <<'EOF'
MAIN checkout —|names the main checkout as the target
never a worktree copy|forbids the worktree copy
384 characters|states the summary cap
60 characters|states the derived subject cap
--text|names the flag the detail goes in
EOF

# ── 3. both auto-commit mirrors dropped the log ─────────────────────────────
# The retirement is the half of this task that touches shipped behaviour: with
# the glob gone, land no longer sweeps the log and `worktree check` treats a
# modified worktree copy as the user work it now is.
section "3. Auto-commit mirrors"

py_globs=$(uv run python -c \
    'from endless.worktree_cmd import AUTO_COMMIT_GLOBS; print("\n".join(AUTO_COMMIT_GLOBS))' \
    2>/tmp/e2055-pyimp.log) \
    || setup_error "could not import AUTO_COMMIT_GLOBS (see /tmp/e2055-pyimp.log)"

if grep -qxF -- "${LESSONS_PATH}" <<<"${py_globs}"; then
    report_fail "AUTO_COMMIT_GLOBS dropped '${LESSONS_PATH}'" \
        "absent" "$(tr '\n' ' ' <<<"${py_globs}")"
else
    report_pass "AUTO_COMMIT_GLOBS dropped '${LESSONS_PATH}'"
fi

# Surgical: the removal must not have taken the live entries with it.
for keep in ".endless/verbs.jsonl" ".endless/db-ledger/*.jsonl"; do
    if grep -qxF -- "${keep}" <<<"${py_globs}"; then
        report_pass "AUTO_COMMIT_GLOBS still carries '${keep}'"
    else
        report_fail "AUTO_COMMIT_GLOBS still carries '${keep}'" "present" "missing"
    fi
done

# The matcher, not just the tuple literal.
if uv run python -c \
    'import sys
from endless.worktree_cmd import _is_auto_commit_path
sys.exit(0 if _is_auto_commit_path(".endless/LESSONS.md") else 1)' 2>/dev/null; then
    report_fail "_is_auto_commit_path('${LESSONS_PATH}')" "False" "True"
else
    report_pass "_is_auto_commit_path('${LESSONS_PATH}') is False"
fi

go_globs=$(awk '/^var AutoManagedStatusGlobs = \[\]string\{/,/^\}/' "${GO_SRC}" \
    | grep -oE '"[^"]+"' | tr -d '"')
[[ -n "${go_globs}" ]] || setup_error "could not extract AutoManagedStatusGlobs from ${GO_SRC}"

if grep -qxF -- "${LESSONS_PATH}" <<<"${go_globs}"; then
    report_fail "AutoManagedStatusGlobs dropped '${LESSONS_PATH}'" \
        "absent" "$(tr '\n' ' ' <<<"${go_globs}")"
else
    report_pass "AutoManagedStatusGlobs dropped '${LESSONS_PATH}'"
fi

if [[ "$(sort <<<"${py_globs}")" == "$(sort <<<"${go_globs}")" ]]; then
    report_pass "Python and Go glob lists agree entry-for-entry"
else
    report_fail "Python and Go glob lists agree" \
        "$(sort <<<"${py_globs}" | tr '\n' ' ')" \
        "$(sort <<<"${go_globs}" | tr '\n' ' ')"
fi

# ── 4. the instructions name the command ────────────────────────────────────
section "4. CLAUDE.md rule and the log's own header"

if grep -qF -- 'endless lesson write' "${CLAUDE_MD}"; then
    report_pass "CLAUDE.md names \`endless lesson write\`"
else
    report_fail "CLAUDE.md names \`endless lesson write\`" "the command" "not found"
fi

if grep -qF -- "never append to the file by hand" "${CLAUDE_MD}"; then
    report_pass "CLAUDE.md forbids the raw append"
else
    report_fail "CLAUDE.md forbids the raw append" \
        "\"never append to the file by hand\"" "not found"
fi

# The rule this task replaces: land is no longer what records a lesson.
if grep -qF -- 'worktree land\` auto-commits it' "${CLAUDE_MD}"; then
    report_fail "CLAUDE.md drops land's auto-commit as the recorder" \
        "no \"worktree land\` auto-commits it\"" "still present"
else
    report_pass "CLAUDE.md drops land's auto-commit as the recorder"
fi

# The surrounding rules that must survive untouched.
for keep in "Never read that file." "Name the full path"; do
    if grep -qF -- "${keep}" "${CLAUDE_MD}"; then
        report_pass "CLAUDE.md keeps: ${keep}"
    else
        report_fail "CLAUDE.md keeps: ${keep}" "present" "missing"
    fi
done

# The log's header instructs any session that opens the file; a header still
# teaching the old convention is the same defect in a second place.
header=$(sed -n '1,/^## Entries/p' "${LOG_HEADER_SRC}") \
    || setup_error "could not read ${LOG_HEADER_SRC}"

if grep -qF -- 'endless lesson write' <<<"${header}"; then
    report_pass "the log's header names the command"
else
    report_fail "the log's header names the command" "the command" "not found"
fi

if grep -qF -- "Write to your own worktree's copy" <<<"${header}"; then
    report_fail "the log's header drops the worktree-copy convention" \
        "absent" "still present"
else
    report_pass "the log's header drops the worktree-copy convention"
fi

# ── 5. no stale wording anywhere in the tracked instructions ────────────────
# The retired rule could be restated in any file that instructs a session. The
# log's ENTRIES are excluded: they are an append-only history that quotes
# superseded wording on purpose, and are never read as instructions.
section "5. No stale land-sweeps-the-lessons wording"

stale=$(grep -rn -e "land's sweep" -e "swept by land" \
    CLAUDE.md docs/ src/ internal/ 2>/dev/null \
    | grep -v 'e-20[0-9][0-9]-verify.sh' || true)
if [[ -z "${stale}" ]]; then
    report_pass "no 'land sweeps the lessons log' wording in tracked instructions"
else
    report_fail "no stale land-sweep wording" "no matches" "${stale}"
fi

# ── 6. guide doc/code sync ──────────────────────────────────────────────────
section "6. Guide"

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

if grep -qF -- 'endless lesson write' <<<"${commit_section}"; then
    report_pass "guide's commit section names \`endless lesson write\`"
else
    report_fail "guide's commit section names \`endless lesson write\`" \
        "the command" "absent"
fi

if grep -qF -- 'section: orchestration' "${GUIDE_MAP}"; then
    report_pass "guide-map entry exists for the lesson command"
else
    report_fail "guide-map entry for the lesson command" \
        "docs/guide/help/lesson.md with a section" "missing or unsectioned"
fi

# ── 7. functional, in throwaway repos ───────────────────────────────────────
section "7a. Functional: a dirty LESSONS.md is USER WORK again"

TMP_E2055=$(mktemp -d "${TMPDIR:-/tmp}/e2055.XXXXXX") || setup_error "mktemp failed"
part_repo="${TMP_E2055}/partition"
mkdir -p "${part_repo}/.endless" || setup_error "mkdir failed"
(
    cd "${part_repo}" || exit 1
    git init -q . && \
    git config user.email e2055@example.test && \
    git config user.name "E-2055 fixture" && \
    git config commit.gpgsign false && \
    printf 'lesson one\n' > .endless/LESSONS.md && \
    printf '{"x":1}\n' > .endless/verbs.jsonl && \
    git add -A && git commit -qm init
) >/dev/null 2>&1 || setup_error "could not build the partition repo"

printf 'lesson two\n' >> "${part_repo}/.endless/LESSONS.md"
printf '{"x":2}\n' >> "${part_repo}/.endless/verbs.jsonl"

partition=$(uv run python -c "
from pathlib import Path
from endless.worktree_cmd import _git_status_partition
auto, user = _git_status_partition(Path('${part_repo}'))
print('AUTO:' + ','.join(sorted(auto)))
print('USER:' + ','.join(sorted(user)))
" 2>/tmp/e2055-part.log) || setup_error "partition probe failed (see /tmp/e2055-part.log)"

auto_line=$(grep '^AUTO:' <<<"${partition}" | cut -d: -f2-)
user_line=$(grep '^USER:' <<<"${partition}" | cut -d: -f2-)

if [[ "${user_line}" == "${LESSONS_PATH}" ]]; then
    report_pass "a modified LESSONS.md partitions as user work (land refuses it)"
else
    report_fail "LESSONS.md partitions as user work" "${LESSONS_PATH}" "${user_line}"
fi

if [[ "${auto_line}" == ".endless/verbs.jsonl" ]]; then
    report_pass "verbs.jsonl still partitions as endless-managed"
else
    report_fail "verbs.jsonl still partitions as endless-managed" \
        ".endless/verbs.jsonl" "${auto_line}"
fi

section "7b-d. Functional: \`lesson write\` end to end"

# A scratch endless home + scratch project, so nothing here touches the real
# database, the real config, or the real corrections log. XDG_CONFIG_HOME is the
# isolation knob endless's own conftest uses; the rest suppress the background
# work (schema migration on first touch, triage model calls, session attribution)
# that a throwaway home has no business doing.
export XDG_CONFIG_HOME="${TMP_E2055}/xdg"
export ENDLESS_AUTO_MIGRATE=1
export ENDLESS_NO_TRIAGE=1
mkdir -p "${XDG_CONFIG_HOME}" || setup_error "mkdir failed"
proj="${TMP_E2055}/scratch-project"
mkdir -p "${proj}/.endless" || setup_error "mkdir failed"
printf '{"name": "scratch"}\n' > "${proj}/.endless/config.json"
(
    cd "${proj}" || exit 1
    git init -q -b main . && \
    git config user.email e2055@example.test && \
    git config user.name "E-2055 fixture" && \
    git config commit.gpgsign false && \
    printf 'source\n' > app.py && \
    git add -A && git commit -qm init
) >/dev/null 2>&1 || setup_error "could not build the scratch project"

if ! (cd "${proj}" && uv run --project "${WT}" endless --no-session \
        project register . --infer --name scratch ) \
        >/tmp/e2055-register.log 2>&1; then
    setup_error "could not register the scratch project (see /tmp/e2055-register.log)"
fi

# `register` scaffolds a .gitignore and may rewrite the config; commit whatever
# it left so the assertions below judge the lesson commit, not its leftovers.
git -C "${proj}" add -A >/dev/null 2>&1
git -C "${proj}" commit -qm "register" >/dev/null 2>&1 || true

el() { (cd "$1" && shift && uv run --project "${WT}" endless --no-session "$@"); }

# 7b — the write itself.
head_before=$(git -C "${proj}" rev-parse HEAD)
printf 'work in flight\n' > "${proj}/stray.txt"
git -C "${proj}" add stray.txt

if el "${proj}" lesson write "verify scripts are task-scoped" \
        --text "- **Rule**: one verify script per task" \
        >/tmp/e2055-write.log 2>&1; then
    report_pass "lesson write succeeded in a fresh project"
else
    report_fail "lesson write succeeded" "exit 0" "$(tail -3 /tmp/e2055-write.log)"
fi

log_file="${proj}/${LESSONS_PATH}"
if [[ -f "${log_file}" ]]; then
    report_pass "the log was created on the first lesson"
else
    report_fail "the log was created" "${log_file}" "missing"
fi

if grep -q '^### \[[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}\] verify scripts are task-scoped$' \
        "${log_file}" 2>/dev/null; then
    report_pass "the entry renders as '### [Date] <summary>'"
else
    report_fail "the entry renders as '### [Date] <summary>'" \
        "a dated heading" "$(grep -c '^### ' "${log_file}" 2>/dev/null || echo 0) headings"
fi

if grep -qF -- "- **Project**: scratch" "${log_file}" 2>/dev/null; then
    report_pass "the project is derived into the entry, not retyped"
else
    report_fail "the project is derived into the entry" \
        "- **Project**: scratch" "absent"
fi

head_after=$(git -C "${proj}" rev-parse HEAD)
if [[ "${head_after}" != "${head_before}" ]]; then
    report_pass "main advanced — the write committed itself, no land needed"
else
    report_fail "main advanced" "a new commit" "HEAD unchanged"
fi

subject=$(git -C "${proj}" log -1 --format=%s)
if [[ "${subject}" == "lesson: verify scripts are task-scoped" ]]; then
    report_pass "commit subject: ${subject}"
else
    report_fail "commit subject" \
        "lesson: verify scripts are task-scoped" "${subject}"
fi

if (( ${#subject} <= 60 )); then
    report_pass "commit subject is within 60 characters (${#subject})"
else
    report_fail "commit subject within 60 characters" "<= 60" "${#subject}"
fi

# What actually separates endless's commits from session work: no task id. A
# vendor prefix was tried and dropped — nothing matches on one, and it cost 11
# of the subject's 60 characters.
if [[ ! "${subject}" =~ ^E-[0-9]+: ]]; then
    report_pass "the subject carries no task id, so it reads as endless's own"
else
    report_fail "the subject carries no task id" "no 'E-<id>:' prefix" "${subject}"
fi

if git -C "${proj}" log -1 --format=%b | grep -qF -- "one verify script per task"; then
    report_pass "the detail is the commit body"
else
    report_fail "the detail is the commit body" \
        "the --text content in %b" "$(git -C "${proj}" log -1 --format=%b | head -1)"
fi

committed=$(git -C "${proj}" show --name-only --format= HEAD)
if [[ "${committed}" == "${LESSONS_PATH}" ]]; then
    report_pass "exactly one path committed: ${LESSONS_PATH}"
else
    report_fail "exactly one path committed" "${LESSONS_PATH}" "${committed}"
fi

if [[ -z "$(git -C "${proj}" status --porcelain -- "${LESSONS_PATH}")" ]]; then
    report_pass "the log is clean afterwards — nothing left for land to sweep"
else
    report_fail "the log is clean afterwards" "no status" \
        "$(git -C "${proj}" status --porcelain -- "${LESSONS_PATH}")"
fi

if git -C "${proj}" status --porcelain -- stray.txt | grep -q '^A '; then
    report_pass "unrelated staged work on main is preserved"
else
    report_fail "unrelated staged work preserved" "stray.txt still staged" \
        "$(git -C "${proj}" status --porcelain -- stray.txt)"
fi

# 7c — the pain this task closes: recording from inside a worktree.
wt_dir="${proj}/.endless/worktrees/e-1"
if ! git -C "${proj}" worktree add -q "${wt_dir}" -b task/1 main >/tmp/e2055-wt.log 2>&1; then
    setup_error "could not create the scratch worktree (see /tmp/e2055-wt.log)"
fi
branch_before=$(git -C "${proj}" rev-parse task/1)
main_before=$(git -C "${proj}" rev-parse main)

if el "${wt_dir}" lesson write "recorded from a worktree" \
        --text "- **Rule**: the append goes to main, always" \
        >/tmp/e2055-wt-write.log 2>&1; then
    report_pass "lesson write succeeded from inside a worktree"
else
    report_fail "lesson write from a worktree" "exit 0" \
        "$(tail -3 /tmp/e2055-wt-write.log)"
fi

if grep -qF -- "recorded from a worktree" "${log_file}"; then
    report_pass "the worktree's lesson landed in MAIN's copy of the log"
else
    report_fail "the worktree's lesson landed in MAIN's copy" "the entry" "absent"
fi

if [[ "$(git -C "${proj}" rev-parse main)" != "${main_before}" ]]; then
    report_pass "main advanced from a worktree-issued write"
else
    report_fail "main advanced from a worktree-issued write" "a new commit" "unchanged"
fi

if [[ "$(git -C "${proj}" rev-parse task/1)" == "${branch_before}" ]]; then
    report_pass "the task branch is untouched — no re-land to record a lesson"
else
    report_fail "the task branch is untouched" "${branch_before}" \
        "$(git -C "${proj}" rev-parse task/1)"
fi

if [[ -z "$(git -C "${wt_dir}" status --porcelain)" ]]; then
    report_pass "the worktree is clean — the write never dirtied it"
else
    report_fail "the worktree is clean" "no status" \
        "$(git -C "${wt_dir}" status --porcelain)"
fi

# 7d — a summary far longer than a subject holds is NOT refused: it is written
# whole and the subject truncates. This is the behaviour the 384/60 split exists
# for, so it is worth proving end to end and not only in the unit suite.
entries_before=$(grep -c '^### ' "${log_file}")
mid_summary="do not assert provenance without checking git log first; a plausible-sounding attribution that turns out to be wrong costs more than the thirty seconds the check takes"
if el "${proj}" lesson write "${mid_summary}" --text "- **Rule**: check first" \
        >/tmp/e2055-mid.log 2>&1; then
    report_pass "a summary longer than the subject is accepted, not refused"
else
    report_fail "a long summary is accepted" "exit 0" "$(tail -3 /tmp/e2055-mid.log)"
fi

if grep -qF -- "${mid_summary}" "${log_file}"; then
    report_pass "the long summary is written to the log verbatim"
else
    report_fail "the long summary is written verbatim" "the full text" "absent or cut"
fi

mid_subject=$(git -C "${proj}" log -1 --format=%s)
if (( ${#mid_subject} <= 60 )) && [[ "${mid_subject}" == *"…" ]]; then
    report_pass "the subject truncated to fit: ${mid_subject}"
else
    report_fail "the subject truncated to fit" "<= 60 chars ending in an ellipsis" \
        "${#mid_subject} chars: ${mid_subject}"
fi

# 7e — over its OWN cap, the refusal fires before anything is written.
entries_now=$(grep -c '^### ' "${log_file}")
over_cap=$(printf 'x%.0s' $(seq 1 385))
if el "${proj}" lesson write "${over_cap}" --text "detail" \
        >/tmp/e2055-long.log 2>&1; then
    report_fail "a summary over 384 characters is refused" "non-zero exit" "exit 0"
else
    report_pass "a summary over 384 characters is refused"
fi

if grep -qF -- "384" /tmp/e2055-long.log && grep -qF -- "--text" /tmp/e2055-long.log; then
    report_pass "the refusal names the cap and where the overflow belongs"
else
    report_fail "the refusal names the cap and where the overflow belongs" \
        "'384' and '--text' in the message" "$(head -2 /tmp/e2055-long.log)"
fi

if [[ "$(grep -c '^### ' "${log_file}")" == "${entries_now}" ]]; then
    report_pass "the refused write left the log untouched"
else
    report_fail "the refused write left the log untouched" \
        "${entries_now} entries" "$(grep -c '^### ' "${log_file}")"
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
