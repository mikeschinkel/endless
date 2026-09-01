#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1754 and records what was true when E-1754
# landed. Edit it only if you ARE E-1754. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1754 verification — the one-shot backfill materializes committed
# .endless/{plans,outcomes,analyses}/E-NNN.md + .endless/decisions/ED-NNN.md
# mirrors for PRE-EXISTING task/decision content, gap-fill (never overwrite),
# in ONE commit on the worktree branch, and is idempotent on re-run.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1754
#
# Strategy: build a FULLY ISOLATED throwaway environment (temp HOME + config dir
# + fresh DB, temp git project, a real linked worktree) and drive the CANDIDATE
# Python CLI (.venv/bin/endless) + the backfill program compiled from THIS
# worktree's module. Nothing touches the real endless repo, the real ledger, or
# this worktree's own branch — teardown is `rm -rf`.
#
# HOME (not just XDG_CONFIG_HOME) is redirected because the backfill routes its
# DB read via monitor.PinMainDB(), which resolves ~/.config/endless from
# os.UserHomeDir(). Setting HOME + XDG_CONFIG_HOME to the same isolated tree
# makes the CLI (writes) and the backfill (reads) target one DB.
#
# The temp worktree is a bare git repo, not the Go module, so we cannot `go run`
# the backfill from inside it. Instead we pre-build the program from the repo
# module once and run the binary with cwd = the temp worktree — the real run
# stays `cd <e-1754-wt>; go run .endless/migrations/e-1754-backfill-doc-mirrors.go`.
#
# What it checks:
#   1. text/outcome/analysis mirrors backfilled to the worktree for seeded tasks
#   2. decision body backfilled to .endless/decisions/ED-NNN.md
#   3. all backfilled files land in ONE commit "Endless: backfill doc mirrors (N files)"
#   4. a task with empty doc fields produces no file
#   5. a pre-existing mirror is left byte-untouched (gap-fill, not overwrite)
#   6. re-running the backfill makes no second commit (idempotent)
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

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

# assert_file_has DESC FILE PATTERN — file exists and contains PATTERN.
assert_file_has() {
    local desc="$1" file="$2" pat="$3"
    if [[ ! -f "${file}" ]]; then
        report_fail "${desc}" "file exists: ${file}" "absent"; return
    fi
    if grep -q -- "${pat}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "contains: ${pat}" "$(cat "${file}")"
}

# assert_file_eq DESC FILE EXPECTED_CONTENT — file exists and equals EXPECTED.
assert_file_eq() {
    local desc="$1" file="$2" want="$3" got
    if [[ ! -f "${file}" ]]; then
        report_fail "${desc}" "file exists: ${file}" "absent"; return
    fi
    got=$(cat "${file}")
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "content == ${want}" "${got}"
}

# assert_head_subject DESC GITDIR SUBJECT — HEAD commit subject equals SUBJECT.
assert_head_subject() {
    local desc="$1" gitdir="$2" want="$3" got
    got=$(git -C "${gitdir}" log --format=%s -n 1 2>&1)
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "HEAD subject == ${want}" "${got}"
}

# assert_absent DESC PATH
assert_absent() {
    local desc="$1" path="$2"
    if [[ ! -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "path does not exist: ${path}" "present"
}

# assert_eq DESC WANT GOT
assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${want}" == "${got}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=""
WORK=""
PROJ=""
WT=""
EN=""
BIN=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

# en — the CANDIDATE Python CLI, run with cwd = the temp project so
# project-from-cwd resolution lands on our isolated project.
en() { ( cd "${PROJ}" && "${EN}" "$@" ); }

# backfill — run the pre-built program with cwd = the temp worktree.
backfill() { ( cd "${WT}" && "${BIN}" "$@" ); }

add_task() {
    local title="$1" out
    out=$(en task add "${title}" 2>&1) || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v go >/dev/null 2>&1 || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    [[ -x "${REPO_ROOT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${REPO_ROOT}" >&2; exit 2; }
    # Materialize the editable venv if needed, then use its console script.
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    # Physical path (pwd -P): on macOS mktemp returns /var/… which is a symlink
    # to /private/var/…. `endless register` stores the resolved path, but the
    # backfill's ProjectIDForPath walks up the unresolved cwd; a physical WORK
    # keeps both sides identical so the project lookup matches.
    WORK=$(cd "$(mktemp -d)" && pwd -P)
    trap cleanup EXIT

    # Pre-build the backfill program from the repo module (explicit-file build
    # works on the //go:build ignore file). The temp worktree isn't a module, so
    # the binary — not `go run` — is what runs there.
    BIN="${WORK}/e-1754-backfill"
    ( cd "${REPO_ROOT}" && go build -o "${BIN}" \
        .endless/migrations/e-1754-backfill-doc-mirrors.go ) || {
        printf 'ERROR: building the backfill program failed\n' >&2; exit 2; }

    # Fully isolated endless environment. HOME is redirected too: the backfill's
    # PinMainDB() resolves ~/.config/endless from os.UserHomeDir(), so HOME and
    # XDG_CONFIG_HOME must point at the SAME isolated config tree as the CLI uses.
    export HOME="${WORK}/home"
    export XDG_CONFIG_HOME="${HOME}/.config"
    export XDG_CACHE_HOME="${WORK}/cache"
    export ENDLESS_AUTO_MIGRATE=1
    export PATH="${REPO_ROOT}/bin:${PATH}"
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    # Temp project = a real git repo. A committed .endless/config.json gives the
    # worktree checkout the project marker; the project is registered at PROJ so
    # ProjectIDForPath walks up from the worktree cwd and finds it.
    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}/.endless"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    printf '{"name":"verify1754","self_dev":false}\n' > "${PROJ}/.endless/config.json"
    # Seed a deterministic verb list so `task add` title validation does not fall
    # through to the non-deterministic haiku auto-register path (E-1264).
    # get_verbs() reads the REGISTERED project's .endless/verbs.jsonl.
    {
        printf '{"value":"document","definition":"put into a written record"}\n'
        printf '{"value":"record","definition":"set down for reference"}\n'
        printf '{"value":"analyze","definition":"examine in detail"}\n'
        printf '{"value":"verify","definition":"confirm the truth of"}\n'
        printf '{"value":"backfill","definition":"fill in missing prior data"}\n'
        printf '{"value":"prefer","definition":"choose over an alternative"}\n'
    } > "${PROJ}/.endless/verbs.jsonl"
    : > "${PROJ}/README.md"
    git -C "${PROJ}" add README.md .endless/config.json .endless/verbs.jsonl
    git -C "${PROJ}" commit -q -m "init"

    en register "${PROJ}" --infer --name verify1754 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }
}

# make_worktree TASK_ID — the worktree the backfill runs from and writes into.
make_worktree() {
    local id="$1" wt="${PROJ}/.endless/worktrees/e-$1"
    git -C "${PROJ}" worktree add -q -b "task/${id}" "${wt}" main 2>/dev/null || return 1
    printf '{"kind":"task","base_branch":"main","branch":"task/%s"}\n' "${id}" \
        > "${wt}/.endless/worktree.json"
    printf '%s' "${wt}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

T_TEXT=""      # task carrying text
T_OUT=""       # task carrying outcome
T_ANA=""       # task carrying analysis
T_EMPTY=""     # task with no doc fields
T_SEED=""      # task whose outcome mirror is pre-committed (gap-fill fixture)
DID=""         # decision with a body
WT_ID=""       # numeric id of the run-from worktree task

run_backfill_and_check() {
    section "1–4 — backfill materializes absent mirrors in one commit"

    local before after out
    before=$(git -C "${WT}" rev-parse HEAD)
    out=$(backfill 2>&1)
    printf '%s\n' "${out}" | sed 's/^/      /'
    after=$(git -C "${WT}" rev-parse HEAD)

    if [[ "${before}" == "${after}" ]]; then
        report_fail "backfill made a commit" "HEAD advances" "HEAD unchanged"
        return
    fi
    report_pass "backfill made a commit"

    assert_head_subject "single commit subject names the file count" \
        "${WT}" "Endless: backfill doc mirrors (4 files)"

    assert_file_has "text mirrored to .endless/plans/${T_TEXT}.md" \
        "${WT}/.endless/plans/${T_TEXT}.md" "TEXT-BODY-MARKER"
    assert_file_has "outcome mirrored to .endless/outcomes/${T_OUT}.md" \
        "${WT}/.endless/outcomes/${T_OUT}.md" "OUTCOME-BODY-MARKER"
    assert_file_has "analysis mirrored to .endless/analyses/${T_ANA}.md" \
        "${WT}/.endless/analyses/${T_ANA}.md" "ANALYSIS-BODY-MARKER"
    assert_file_has "decision body mirrored to .endless/decisions/${DID}.md" \
        "${WT}/.endless/decisions/${DID}.md" "DECISION-BODY-MARKER"

    assert_absent "task with empty doc fields writes no plan file" \
        "${WT}/.endless/plans/${T_EMPTY}.md"
    assert_absent "task with empty doc fields writes no outcome file" \
        "${WT}/.endless/outcomes/${T_EMPTY}.md"
}

check_gap_fill_preserved() {
    section "5 — a pre-existing mirror is left untouched (gap-fill, not overwrite)"
    # The file was pre-committed with PRESEED-DISTINCT while the DB row holds
    # SEED-DB-VALUE; a survived PRESEED-DISTINCT proves the backfill skipped
    # (did not overwrite) the present file.
    assert_file_eq "pre-seeded outcome mirror kept its original content" \
        "${WT}/.endless/outcomes/${T_SEED}.md" "PRESEED-DISTINCT"
}

check_idempotent() {
    section "6 — re-running the backfill is a no-op (no second commit)"
    local before after out
    before=$(git -C "${WT}" rev-parse HEAD)
    out=$(backfill 2>&1)
    after=$(git -C "${WT}" rev-parse HEAD)
    assert_eq "HEAD unchanged on second run" "${before}" "${after}"
    if printf '%s' "${out}" | grep -q "Nothing to backfill"; then
        report_pass "second run reports nothing to backfill"
    else
        report_fail "second run reports nothing to backfill" \
            "'Nothing to backfill' in output" "${out}"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1754 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  backfill:%s\n' "${BIN}"
    printf '  env:     isolated (%s)\n' "${WORK}"

    # Seed tasks WITHOUT per-task worktrees (so no write-time mirror fires), each
    # carrying one doc field, plus an empty-field task and a gap-fill fixture
    # task. Decision add is deferred until AFTER the worktree exists (below).
    T_TEXT=$(add_task "Document the text body") || { printf 'ERROR: %s\n' "${T_TEXT}" >&2; exit 2; }
    en task update "${T_TEXT}" --text "TEXT-BODY-MARKER" >/dev/null 2>&1
    T_OUT=$(add_task "Record the outcome body") || { printf 'ERROR: %s\n' "${T_OUT}" >&2; exit 2; }
    en task update "${T_OUT}" --outcome "OUTCOME-BODY-MARKER" >/dev/null 2>&1
    T_ANA=$(add_task "Analyze the sample input") || { printf 'ERROR: %s\n' "${T_ANA}" >&2; exit 2; }
    en task update "${T_ANA}" --analysis "ANALYSIS-BODY-MARKER" >/dev/null 2>&1
    T_EMPTY=$(add_task "Verify the empty case") || { printf 'ERROR: %s\n' "${T_EMPTY}" >&2; exit 2; }
    T_SEED=$(add_task "Backfill the seeded outcome") || { printf 'ERROR: %s\n' "${T_SEED}" >&2; exit 2; }
    en task update "${T_SEED}" --outcome "SEED-DB-VALUE" >/dev/null 2>&1

    # The worktree the backfill runs from. Reuse T_EMPTY's id as its label so the
    # worktree path is e-<id>; any id works since the backfill scopes by project.
    # Created from main BEFORE the decision add so the decision's write-time
    # mirror (which lands on PROJ main) never reaches the worktree branch.
    WT_ID="${T_EMPTY#E-}"
    WT=$(make_worktree "${WT_ID}") || {
        printf 'ERROR: creating worktree e-%s failed\n' "${WT_ID}" >&2; exit 2; }

    # Pre-seed the gap-fill fixture: commit outcomes/E-SEED.md with content that
    # DIFFERS from the DB row (PRESEED-DISTINCT vs SEED-DB-VALUE). If the backfill
    # overwrote it, its content would flip to the DB value; gap-fill leaves it.
    mkdir -p "${WT}/.endless/outcomes"
    printf 'PRESEED-DISTINCT' > "${WT}/.endless/outcomes/${T_SEED}.md"
    git -C "${WT}" add ".endless/outcomes/${T_SEED}.md"
    git -C "${WT}" commit -q -o ".endless/outcomes/${T_SEED}.md" \
        -m "pre-seed outcome mirror"

    # Decision add from PROJ (main) cwd → its mirror commits on PROJ main, not on
    # the already-created worktree branch, so the backfill is what creates the
    # decision mirror in the worktree.
    local out
    out=$(en decision add "Prefer explicit flags" \
        --description "DECISION-BODY-MARKER" --project verify1754 2>&1)
    DID=$(printf '%s\n' "${out}" | grep -oE 'ED-[0-9]+' | head -1)
    [[ -z "${DID}" ]] && { printf 'ERROR: decision add failed: %s\n' "${out}" >&2; exit 2; }

    printf '  tasks:   %s(text) %s(outcome) %s(analysis) %s(empty) %s(pre-mirrored)\n' \
        "${T_TEXT}" "${T_OUT}" "${T_ANA}" "${T_EMPTY}" "${T_SEED}"
    printf '  decision:%s\n' "${DID}"
    printf '  worktree:%s\n' "${WT}"

    run_backfill_and_check
    check_gap_fill_preserved
    check_idempotent

    summary
}

main "$@"
