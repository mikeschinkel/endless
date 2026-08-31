#!/usr/bin/env bash
#
# E-2037 verification — the SQLite database is no longer called the ledger.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2037
#
# WHAT LANDED
#   Endless has two durable things and one name was doing both jobs. The
#   db-ledger (`.endless/db-ledger/*.jsonl`) is the permanent record; the
#   SQLite database is a rebuildable projection of it. Ninety-five sites in
#   live code, docs, templates and unit tests called the DATABASE "the
#   ledger" — most damagingly in `internal/monitor/db.go`, the file that
#   decides which database a process opens, and in the two user-facing
#   strings an agent reads the moment it gets `--db` wrong.
#
#   Those sites now say "the main database" / "the sandbox database", the
#   vocabulary `--db main` and `--db sandbox` already established. Every site
#   that genuinely meant the JSONL ledger was left alone — that is the half a
#   blanket sed would have destroyed, and it is checked here as its own layer.
#
#   Out of scope by the plan: the 80 occurrences in .endless/tasks/*-verify.sh.
#   A landed verify script is a spent pre-land gate; nothing runs it again, so
#   its wording cannot mislead anyone. E-2033 was filed and declined for
#   misreading that rule. Layer C proves none of them were touched.
#
# WHAT THIS SUITE HAS TO PROVE
#   A rename has exactly three ways to be wrong, and a source grep alone
#   reaches only one of them:
#
#     1. The words did not reach the surface. The refusal text, the `--help`
#        epilog, the spawn handoff and `endless guide` are rendered — from a
#        Click constant, an embedded Go template and a markdown file
#        respectively. Layer A runs the real candidate build and reads what a
#        user and an agent actually see.
#     2. A site was missed. Layer B greps every in-scope file for the phrases
#        that named the database "ledger".
#     3. The real ledger's vocabulary was collateral damage. A blanket
#        substitution would silently rename the JSONL write-ahead log too, and
#        nothing user-facing would fail. Layer B pins the load-bearing
#        db-ledger sentences that must SURVIVE.
#
# Layers:
#   A. FAIL-FAST — the rendered surfaces, from the candidate build.
#      If an agent still reads "the real ledger", nothing below matters.
#   B. Source sweep — no site missed, no real-ledger wording destroyed.
#   C. Scope — .endless/tasks/*-verify.sh untouched.
#   D. Project-wide regression — build, vet, go test, Python suite, guide map.
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

REPO_ROOT=""
TMP_DIR=""
ENDLESS_BIN=""     # the worktree's editable venv build — candidate Python
GO_BIN=""          # the worktree's Go build — candidate templates

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }

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

# assert_contains DESC HAYSTACK NEEDLE — whitespace-collapsed, so an assertion
# pins the wording and not the column alignment or the terminal's wrapping.
assert_contains() {
    local desc="$1" hay needle
    hay=$(printf '%s' "$2" | tr -s '[:space:]' ' ')
    needle=$(printf '%s' "$3" | tr -s '[:space:]' ' ')
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "output contains '$3'" "$(printf '%s' "$2" | head -4)"
    return 1
}

# assert_lacks DESC HAYSTACK NEEDLE
assert_lacks() {
    local desc="$1" hay needle
    hay=$(printf '%s' "$2" | tr -s '[:space:]' ' ')
    needle=$(printf '%s' "$3" | tr -s '[:space:]' ' ')
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "output does NOT contain '$3'" \
        "$(printf '%s' "$2" | grep -i -- "$3" | head -2)"
    return 1
}

# assert_cmd DESC CMD... — plain exit-0 check for the regression layer.
assert_cmd() {
    local desc="$1"; shift
    local output rc
    output=$(cd "${REPO_ROOT}" && "$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "$(printf '%s' "${output}" | tail -5)"
    return 1
}

# ─── in-scope file set ──────────────────────────────────────────────────────
#
# Everything git tracks, minus the two directories the plan puts out of scope:
#
#   .endless/     — the ledger itself plus the frozen plan/analysis/outcome
#                   record. Historical text; rewriting it would be forgery.
#   .endless/tasks/  — spent pre-land verify scripts (see the header).
#
# Printed as NUL-delimited paths so a space in a filename cannot split one.
in_scope_files() {
    (cd "${REPO_ROOT}" && git ls-files -z \
        | grep -zZv '^\.endless/' \
        | grep -zZv '^tests/tasks/')
}

# scan_for PATTERN — every in-scope file:line matching PATTERN (extended
# regex, case-insensitive), or "" when clean.
scan_for() {
    (cd "${REPO_ROOT}" && in_scope_files | xargs -0 grep -nIiE -- "$1" 2>/dev/null)
}

# ─── setup ──────────────────────────────────────────────────────────────────

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
        printf 'setup error: not inside a git repository\n' >&2
        exit 2
    }
    if [[ ! -f "${REPO_ROOT}/internal/monitor/db.go" ]]; then
        printf 'setup error: internal/monitor/db.go not found — is this the E-2037 worktree?\n' >&2
        exit 2
    fi
    for tool in go uv git just; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'setup error: %s not on PATH\n' "${tool}" >&2
            exit 2
        }
    done

    TMP_DIR=$(mktemp -d) || { printf 'setup error: mktemp failed\n' >&2; exit 2; }
    trap 'rm -rf "${TMP_DIR}"' EXIT

    if ! (cd "${REPO_ROOT}" && uv sync --quiet) 2>"${TMP_DIR}/sync.err"; then
        printf 'setup error: uv sync failed:\n%s\n' "$(cat "${TMP_DIR}/sync.err")" >&2
        exit 2
    fi
    ENDLESS_BIN="${REPO_ROOT}/.venv/bin/endless"
    [[ -x "${ENDLESS_BIN}" ]] || {
        printf 'setup error: %s missing after uv sync\n' "${ENDLESS_BIN}" >&2
        exit 2
    }

    # The templates are EMBEDDED in the Go binary, so the tree's .tmpl edit is
    # invisible until this build exists. Building it here is what makes A3 a
    # test of this branch rather than of whatever is installed globally.
    if ! (cd "${REPO_ROOT}" && go build -o "${TMP_DIR}/endless-go" ./cmd/endless-go) \
            2>"${TMP_DIR}/build.err"; then
        printf 'setup error: go build failed:\n%s\n' "$(cat "${TMP_DIR}/build.err")" >&2
        exit 2
    fi
    GO_BIN="${TMP_DIR}/endless-go"
}

# ─── layer A: what a user and an agent actually read ────────────────────────

layer_a() {
    section "A. The rendered surfaces, from the candidate build"
    note "the defect was wording, so only the real rendered output settles it"

    # --- A1: the --db refusal. The first thing an agent hits when it gets the
    #         flag wrong, and the product's clearest statement of what the two
    #         databases are for. Triggered by omitting --db inside a self-dev
    #         worktree, which is exactly how an agent trips it.
    local out
    out=$(cd "${REPO_ROOT}" && "${ENDLESS_BIN}" task list 2>&1)
    assert_contains "refusal names the main database" \
        "${out}" "--db main     the project's main database — managing the project" || return 1
    assert_contains "refusal names the sandbox database" \
        "${out}" "--db sandbox  this worktree's sandbox database — testing endless itself" || return 1
    assert_lacks "refusal does not call either database a ledger" "${out}" "ledger" || return 1

    # --- A2: the same claim in `--help`, which is a separate string. -------
    out=$(cd "${REPO_ROOT}" && "${ENDLESS_BIN}" --db main --help 2>&1)
    assert_contains "--help epilog names the main database" \
        "${out}" "--db main (the project's main database)" || return 1
    assert_contains "--help epilog names the sandbox database" \
        "${out}" "--db sandbox (this worktree's sandbox database)" || return 1
    assert_lacks "--help epilog does not say ledger" "${out}" "ledger" || return 1

    # --- A3: the spawn handoff — the sentence every spawned session reads
    #         about its own DB routing, rendered from the embedded template.
    out=$(printf '%s' \
        '{"worktree_path":"/wt","branch":"task/x","task_id":"E-1","title":"t"}' \
        | "${GO_BIN}" template render handoff/todo 2>&1)
    assert_contains "handoff tells a session to reach the main database" \
        "${out}" 'must reach the main database takes `--db main`' || return 1
    assert_lacks "handoff does not say ledger" "${out}" "ledger" || return 1

    # --- A4: `endless guide orchestration` — the agent-facing explanation of
    #         --db, served by the CLI from docs/guide/.
    out=$(cd "${REPO_ROOT}" && "${ENDLESS_BIN}" --db main guide orchestration 2>&1)
    assert_contains "guide calls --db main the main database" \
        "${out}" '`--db main` — the main database at `~/.config/endless/endless.db`' || return 1
    assert_lacks "guide never says real ledger" "${out}" "real ledger" || return 1

    # --- A5: the counter-check. The guide must STILL teach the db-ledger,
    #         because that is the thing the word belongs to. A blanket sed
    #         would pass A1-A4 and fail here.
    assert_contains "guide still teaches that ledger entries live on main" \
        "${out}" "Ledger entries are recorded **on the main checkout only**" || return 1
    out=$(cd "${REPO_ROOT}" && "${ENDLESS_BIN}" --db main guide reference 2>&1)
    assert_contains "guide still calls the SQLite DB a projection of the ledgers" \
        "${out}" "rebuildable projection of all project ledgers" || return 1

    return 0
}

# ─── layer B: source sweep, both directions ─────────────────────────────────

layer_b() {
    section "B. Source sweep — nothing missed, nothing over-applied"

    # --- B1: no in-scope file still names the DATABASE "ledger". -----------
    #
    # Each pattern is a phrase this task actually removed, so a reintroduction
    # is caught by the same words that were wrong the first time. "ledger"
    # bare is NOT a pattern: it is the correct name for the JSONL log and
    # appears legitimately in ~60 in-scope lines.
    note "phrases that named the SQLite database, in every tracked file outside"
    note ".endless/ and tests/tasks/"
    local patterns=(
        "real ledger"
        "ledger[ -]wide"
        "ledger DB"
        "ledger database"
        "Endless ledger"
        "task ledger"
        "ledger rows"
        "query over the ledger"
        "the ledger is unreachable"
        "no ledger means"
        "(stale|repaired|canonical|legacy) ledger"
        "ledger's (config|cache) dir"
    )
    local p hits
    for p in "${patterns[@]}"; do
        hits=$(scan_for "${p}")
        if [[ -z "${hits}" ]]; then
            report_pass "no site says \"${p}\""
        else
            report_fail "no site says \"${p}\"" "no matches" \
                "$(printf '%s' "${hits}" | head -4)"
        fi
    done

    # --- B2: the real ledger's own vocabulary survived. --------------------
    #
    # These are the sentences that DEFINE the distinction this task exists to
    # protect. If a future blanket rename eats them, the project loses the
    # word for its permanent record and nothing else fails.
    note "and the sentences that must survive, because 'ledger' is right there"
    local -a survivors=(
        "CLAUDE.md|## The ledger is durable state"
        "VISION.md|an append-only JSONL ledger, a write-ahead log that is the actual source of record"
        "docs/guide/reference.md|SQLite DB (rebuildable projection of all project ledgers)"
        "docs/guide/orchestration.md|Ledger entries are recorded **on the main checkout only**"
        "internal/events/writer.go|Path/naming constants for the durable event ledger"
        "internal/monitor/session_nav.go|the committed JSONL ledger"
    )
    local entry file needle
    for entry in "${survivors[@]}"; do
        file="${entry%%|*}"; needle="${entry#*|}"
        if grep -qF -- "${needle}" "${REPO_ROOT}/${file}" 2>/dev/null; then
            report_pass "${file} still explains the db-ledger"
        else
            report_fail "${file} still explains the db-ledger" \
                "contains '${needle}'" "missing — the rename over-applied"
        fi
    done
}

# ─── layer C: the plan's explicit exclusion ─────────────────────────────────

layer_c() {
    section "C. Scope — spent verify scripts left alone"
    note "80 occurrences live in tests/tasks/*-verify.sh; E-2033 was declined for"
    note "wanting to edit them. Only this task's own new script may appear."

    local base changed offenders
    base=$(cd "${REPO_ROOT}" && git merge-base HEAD main 2>/dev/null) || {
        report_fail "branch diff resolves against main" "a merge-base with main" \
            "git merge-base HEAD main failed"
        return
    }
    # Committed work plus anything still in the working tree.
    changed=$( (cd "${REPO_ROOT}" \
        && git diff --name-only "${base}" -- tests/tasks/ \
        && git ls-files --others --exclude-standard -- tests/tasks/) | sort -u)
    offenders=$(printf '%s\n' "${changed}" \
        | grep -v '^$' | grep -v '^tests/tasks/e-2037-verify\.sh$')

    if [[ -z "${offenders}" ]]; then
        report_pass "no landed verify script was rewritten"
    else
        report_fail "no landed verify script was rewritten" \
            "only tests/tasks/e-2037-verify.sh" "$(printf '%s' "${offenders}" | head -5)"
    fi
}

# ─── layer D: project-wide regression ───────────────────────────────────────

layer_d() {
    section "D. Project-wide regression"
    note "the edits touch the DB-routing comments, two Click strings, a Go"
    note "template and the guide — so the blast radius is the whole CLI"

    assert_cmd "go build ./..." go build ./...
    assert_cmd "go vet ./..."   go vet ./...

    # -timeout 20m for the PRE-EXISTING sandboxcmd destroy-test slowness tracked
    # as E-1908, not anything this task introduced. Drop the flag once it lands.
    assert_cmd "go test ./... (-timeout 20m; see E-1908)" go test -timeout 20m ./...

    assert_cmd "just test (Python suite)" just test

    # The db-restore help line this task reworded is mirrored into
    # docs/guide/index.md's generated cross-reference; guide-check fails on drift.
    assert_cmd "just guide-check (guide map in sync)" just guide-check
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup
    printf '%sE-2037 — the SQLite database is no longer called the ledger%s\n' \
        "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  candidate: %s\n             %s\n' "${ENDLESS_BIN}" "${GO_BIN}"

    if ! layer_a; then
        note "fail-fast: an agent still reads the wrong word; skipping later layers"
        summary
        return 1
    fi
    layer_b
    layer_c
    layer_d

    summary
}

main "$@"
