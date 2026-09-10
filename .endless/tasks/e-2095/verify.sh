#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2095 and records what was true when E-2095
# landed. Edit it only if you ARE E-2095. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2095 verification — a task's record says whether its work landed, and
# whether the record is still authoritative.
#
# THE PROBLEM, in two halves that are one problem:
#
#   A task status answers "what did we decide about this work". It is routinely
#   read as "is this code on the base branch" and as "is this row still the one
#   to quote", and it answers neither. A task sitting at `assumed` with its fix
#   on an unlanded branch looks finished from every listing, so the bug it fixed
#   gets found and fixed again weeks later. A task that is obsolete, declined or
#   replaced gets quoted as current — the status was on screen every time, and
#   an agent reads a status line as provenance, not as a caveat.
#
#   So: state landedness instead of implying it by omission, add the report that
#   surveys it, and lead a non-authoritative record with a caveat.
#
# WHAT MAKES THE ANSWER HARD, and why the checks below are shaped as they are:
#
#   Landedness is a question about CONTENT, not about SHAs. `worktree land`
#   rebases before it fast-forwards, so the base gets a copy of every commit
#   under a new hash while the branch keeps the original; any reachability test
#   calls those unlanded forever. Patch-id is not enough either, because
#   resolving a conflict changes the diff. `git range-diff` matches by
#   similarity and is the only thing that gets this right, which is why check 2
#   drives real repositories rather than a stub — the whole claim is about what
#   git can see.
#
#   And most task branches hold nothing but Endless's own plan and analysis
#   mirrors. Counting those would put one row on the report per task branch in
#   the project: measured on this repository, 61 branches reporting versus 3
#   holding source. So a commit confined to `.endless/` is not the task's work,
#   and check 2 pins both halves of that filter — the cheap pre-filter that
#   dismisses a bookkeeping-only branch without loading a diff, and the
#   per-commit filter that a MIXED branch still needs.
#
# WHAT IS DELIBERATELY NOT HERE:
#
#   The project-status attention claim is SPECIFIED, not built — the board is
#   heading for major revisions and a row designed now is a row designed to be
#   replaced. Check 6 asserts the specification is recorded where that session
#   will find it, which is the deliverable.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2095
#
# Requires `just build` first — check 2 drives the CANDIDATE bin/endless-go, not
# the global install. (Setup builds it if it is missing.)
#
# What it checks:
#   0. FAIL-FAST fold-in regression: go build, go vet, the whole Go suite, the
#      Python suite, `just guide-check`, `just lifecycle-check`. A failure here
#      short-circuits the rest — the remaining checks would report on rubble.
#   1. This task's own unit tests, named, so the suite says what it proves.
#   2. The probe, against REAL repositories: unlanded source, a rebased landing,
#      a bookkeeping-only branch, a mixed branch, a missing branch, a base that
#      cannot be resolved, and a default branch that is not `main`.
#   3. The base branch is resolved and never assumed — no surface writes `main`.
#   4. `task unlanded` and `task unsettled` each name the other, and the shared
#      option set really is shared.
#   5. The authority banner brackets a record, survives head/tail, and stays
#      invisible to a human.
#   6. The project-status claim is specified where the next session will read it.
#   7. The one landing this task's data-repair pass recorded is attributable.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything else runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=""
GO=""
PY=""
WORK=""

# ─── local assertions the harness does not carry ────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(tail -25 <<<"${out}")"
}

# assert_no_hits DESC HITS
assert_no_hits() {
    local desc="$1" hits="$2"
    if [[ -z "${hits}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "zero hits" "${hits}"
}

# g runs git in a directory, quietly.
g() { local dir="$1"; shift; git -C "${dir}" "$@" >/dev/null 2>&1; }

# probe RepoDir Branch... prints the task-landedness JSON array.
probe() {
    local dir="$1"; shift
    "${GO}" session-query task-landedness --project-root "${dir}" "$@" 2>&1
}

# field JSON INDEX KEY prints one key of one row, or "<unparsable>".
field() {
    "${PY}" -c '
import json, sys
try:
    rows = json.loads(sys.argv[1])
except ValueError:
    print("<unparsable>"); raise SystemExit
print(rows[int(sys.argv[2])].get(sys.argv[3], "<missing>"))
' "$1" "$2" "$3" 2>/dev/null || printf '<unparsable>'
}

# ─── setup ──────────────────────────────────────────────────────────────────

cleanup() {
    if [[ -n "${WORK}" && -d "${WORK}" ]]; then
        rm -rf "${WORK}"
    fi
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && setup_error "not inside a git worktree"
    cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"

    GO="${REPO_ROOT}/bin/endless-go"
    PY="${REPO_ROOT}/.venv/bin/python"

    if [[ ! -x "${GO}" ]]; then
        printf 'building bin/endless-go …\n'
        ( cd "${REPO_ROOT}" && just build >/dev/null 2>&1 ) \
            || setup_error "just build failed"
    fi
    if [[ ! -x "${PY}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) \
            || setup_error "could not materialize .venv (uv run endless failed)"
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT
}

# commit_on DIR BRANCH SUBJECT PATH... — write and commit each path on a branch,
# leaving the repo back on its base.
commit_on() {
    local dir="$1" branch="$2" base="$3" subject="$4"; shift 4
    g "${dir}" checkout "${branch}"
    local p
    for p in "$@"; do
        mkdir -p "${dir}/$(dirname "${p}")"
        printf '%s\n' "${subject}" >"${dir}/${p}"
        g "${dir}" add "${p}"
    done
    g "${dir}" commit -m "${subject}"
    g "${dir}" checkout "${base}"
}

# build_fixture DIR BASE — a repository shaped like an Endless project, with one
# branch per case the probe has to tell apart.
build_fixture() {
    local dir="$1" base="$2"
    mkdir -p "${dir}"
    g "${dir}" init -q -b "${base}"
    g "${dir}" config user.email verify@example.com
    g "${dir}" config user.name Verify
    g "${dir}" commit --allow-empty -m "initial"

    g "${dir}" branch task/1
    g "${dir}" branch task/2
    g "${dir}" branch task/3
    g "${dir}" branch task/4

    commit_on "${dir}" task/1 "${base}" "E-1: the fix" src/fix.go
    commit_on "${dir}" task/2 "${base}" "Endless: add plan for E-2" .endless/plans/E-2.md
    commit_on "${dir}" task/3 "${base}" "Endless: add plan for E-3" .endless/plans/E-3.md
    commit_on "${dir}" task/3 "${base}" "E-3: the other fix" src/other.go
    commit_on "${dir}" task/4 "${base}" "E-4: the landed fix" src/landed.go

    # The base moves, so range-diff has a right-hand range to match against.
    # mkdir first: git prunes an empty directory on checkout, so `src/` is not
    # guaranteed to survive the branch commits above.
    mkdir -p "${dir}/src"
    printf 'moved\n' >"${dir}/src/moved.go"
    g "${dir}" add src/moved.go
    g "${dir}" commit -m "the base moves"

    # task/4 lands the way `worktree land` lands: rebased onto the base under
    # new SHAs, with the branch left pointing at its originals.
    g "${dir}" checkout -b landing-copy task/4
    g "${dir}" rebase "${base}"
    g "${dir}" checkout "${base}"
    g "${dir}" merge --ff-only landing-copy
    g "${dir}" branch -D landing-copy
}

# ─── 0 — the fail-fast fold-in regression front ─────────────────────────────
#
# The project-wide regression, folded in rather than handed to the user as a
# separate checklist. Everything below assumes a tree that builds.
check_regression_front() {
    section "0 — fail-fast fold-in regression"

    assert_succeeds "go build ./... is clean" go build ./...
    assert_succeeds "go vet ./... is clean (catches orphaned test references)" \
        go vet ./...
    assert_succeeds "the whole Go suite passes" go test -timeout 600s ./...
    assert_succeeds "the Python suite passes" uv run pytest -q
    assert_succeeds "just guide-check is green (command→section map intact)" \
        just guide-check
    assert_succeeds "just lifecycle-check is green" just lifecycle-check
}

# ─── 1 — this task's own tests, named ───────────────────────────────────────
#
# They ran inside check 0's `pytest -q` and `go test ./...`. Naming them here is
# not redundancy: a suite has to say WHAT it proves, and "the Python suite
# passes" does not distinguish a run in which these files existed from one in
# which they were deleted.
check_own_tests() {
    section "1 — this task's unit tests"

    assert_succeeds "the landedness rendering tests pass" \
        uv run pytest -q tests/test_landedness.py
    assert_succeeds "the authority banner tests pass" \
        uv run pytest -q tests/test_authority_banner.py
    assert_succeeds "the Go probe's real-git acceptance tests pass" \
        go test -count=1 -run 'Landedness|Bookkeeping|Unlanded' ./internal/monitor/
    assert_succeeds "the status registry still pins every group's membership" \
        go test -count=1 -run TestGroupMembershipIsPinned ./internal/taskstatus/
}

# ─── 2 — the probe, against real repositories ───────────────────────────────
check_probe() {
    section "2 — landedness measured against real git"

    local repo="${WORK}/proj"
    build_fixture "${repo}" main

    local out
    out=$(probe "${repo}" task/1 task/2 task/3 task/4 task/5)

    assert_eq "a branch holding source the base lacks reports it" \
        "1" "$(field "${out}" 0 unlanded_count)"

    assert_eq "a branch holding only Endless's own records reports nothing" \
        "0" "$(field "${out}" 1 unlanded_count)"

    assert_eq "a MIXED branch reports only its source commit" \
        "1" "$(field "${out}" 2 unlanded_count)"
    assert_contains "and names that commit, not the plan mirror" \
        "E-3: the other fix" "$(field "${out}" 2 unlanded_log)"

    assert_eq "a rebased landing is landed, though its SHA is not on the base" \
        "0" "$(field "${out}" 3 unlanded_count)"

    assert_eq "a branch that is not there is not measured…" \
        "False" "$(field "${out}" 4 branch_exists)"
    assert_eq "…and that is not an error, so it does not read as undetermined" \
        "False" "$(field "${out}" 4 undetermined)"

    # The failure E-1940 removed: an answer nobody could obtain must not render
    # identically to a verified-clean one.
    local nonrepo="${WORK}/not-a-repo"
    mkdir -p "${nonrepo}"
    out=$(probe "${nonrepo}" task/1)
    assert_eq "a base branch that cannot be resolved is UNDETERMINED" \
        "True" "$(field "${out}" 0 undetermined)"
    assert_eq "…and reports zero unlanded commits, which is not a verdict" \
        "0" "$(field "${out}" 0 unlanded_count)"
}

# ─── 3 — the base branch is resolved, never assumed ─────────────────────────
#
# Two hardcoded `main`s once made two probes exit 128 forever on a repository
# that named its default branch anything else — a permanent false all-clear.
# This task's surfaces must not reintroduce it, and the risk lives in the
# RENDERING as much as in the probe.
check_base_is_resolved() {
    section "3 — a default branch that is not 'main'"

    local repo="${WORK}/trunkproj"
    build_fixture "${repo}" trunk
    mkdir -p "${repo}/.endless"
    printf '{"name":"trunkproj","default_branch":"trunk"}\n' \
        >"${repo}/.endless/config.json"

    local out
    out=$(probe "${repo}" task/1 task/4)
    assert_eq "the resolved base is named in the verdict" \
        "trunk" "$(field "${out}" 0 base)"
    assert_eq "unlanded source is still found there" \
        "1" "$(field "${out}" 0 unlanded_count)"
    assert_eq "a landed branch is still clean there — no false positive either" \
        "0" "$(field "${out}" 1 unlanded_count)"

    # The rule is "no surface writes the base branch's name into a message, a
    # query or a rev-range" — so CODE is what is searched, and whole-line
    # comments are stripped first. The neighbouring probe already carries such a
    # comment (session_query.go explains why it does not say "main"), and
    # rewording an explanation of the rule to satisfy a grep for the rule makes
    # the prose worse without making the code safer.
    #
    # The file list is asserted present BEFORE the grep, and that half is not
    # ceremony. This check first ran as a bare `git grep` while both files were
    # still untracked; git grep skips untracked files, so it passed by examining
    # nothing. A guard that reports green without looking is worse than one that
    # fails.
    local guarded=(
        internal/monitor/task_landedness.go
        src/endless/authority.py
    )
    local f missing="" hits=""
    for f in "${guarded[@]}"; do
        if [[ ! -f "${f}" ]]; then
            missing+="${f} "
            continue
        fi
        hits+=$(grep -vE '^[[:space:]]*(//|#)' "${f}" | grep -n '"main"' \
            | sed "s,^,${f}: ," )
    done
    assert_eq "every file this check guards is present to be searched" \
        "" "${missing% }"
    assert_no_hits "no file this task added writes the word \"main\" in code" \
        "${hits}"
}

# ─── 4 — the two adjacent reports name each other ───────────────────────────
#
# `task unlanded` and `task unsettled` sit next to each other and look alike. A
# task can be unlanded with no worktree at all; a worktree can be unsettled on a
# task nobody has finished. Each help text opens by naming the difference.
check_report_help() {
    section "4 — task unlanded and task unsettled, told apart"

    local unlanded unsettled
    unlanded=$(uv run endless task unlanded --help 2>&1)
    unsettled=$(uv run endless task unsettled --help 2>&1)

    assert_contains "task unlanded names its neighbour" "task unsettled" "${unlanded}"
    assert_contains "task unsettled names its neighbour" "task unlanded" "${unsettled}"
    assert_contains "and unlanded says what it asks about" "TASK" "${unlanded}"
    assert_contains "and unsettled says what it asks about" "WORKTREE" "${unsettled}"

    # The shared option set, written once so a flag cannot mean two things
    # across the pair.
    local landed
    landed=$(uv run endless task landed --help 2>&1)
    local flag
    for flag in --project --all --llm --json --limit --no-limit; do
        assert_contains "task unlanded carries ${flag}, as task landed does" \
            "${flag}" "${unlanded}"
        assert_contains "task landed carries ${flag}" "${flag}" "${landed}"
    done
}

# ─── 5 — the authority banner ───────────────────────────────────────────────
#
# Driven through the module rather than through a seeded database: the property
# is the WORDING and the audience gate, and the end-to-end bracketing is pinned
# by tests/test_authority_banner.py, which check 1 runs by name.
check_banner() {
    section "5 — the authority caveat"

    # `env -u` rather than a monkeypatch: the gate asks whether an AGENT is
    # reading, and this suite is itself running inside one, so the negative case
    # can only be posed by actually removing the harness's signal.
    local out
    out=$(env -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier -u CLAUDECODE \
        "${PY}" -c '
from endless import agent_help, authority
agent_help.set_agent_view(True)
def line(c, s):
    return authority.banner(c, s) or "<none>"
print(line(authority.for_task("obsolete", [], []), "E-101"))
print(line(authority.for_task("confirmed", ["E-102"], []), "E-101"))
print(line(authority.for_decision("proposed", []), "ED-103"))
print(line(authority.for_decision("rejected", []), "ED-103"))
print(line(authority.for_task("confirmed", [], []), "E-101"))
print(line(authority.for_decision("accepted", []), "ED-103"))
agent_help.set_agent_view(False)
print(line(authority.for_task("obsolete", [], []), "E-101"))
' 2>&1)

    assert_contains "an obsolete task is flagged" "E-101 is NOT authoritative" \
        "$(sed -n 1p <<<"${out}")"
    assert_contains "a REPLACED task is flagged whatever its status says" \
        "Read E-102 instead." "$(sed -n 2p <<<"${out}")"
    assert_contains "a proposed decision is 'not yet', not 'not ever'" \
        "NOT YET authoritative" "$(sed -n 3p <<<"${out}")"
    assert_contains "a rejected decision is 'not ever'" \
        "ED-103 is NOT authoritative" "$(sed -n 4p <<<"${out}")"
    assert_eq "a confirmed task is left alone" "<none>" "$(sed -n 5p <<<"${out}")"
    assert_eq "an accepted decision is left alone" "<none>" "$(sed -n 6p <<<"${out}")"
    assert_eq "and a human sees nothing at all" "<none>" "$(sed -n 7p <<<"${out}")"
}

# ─── 6 — the project-status claim, specified rather than built ──────────────
check_board_spec() {
    section "6 — the project-status claim is on the record"

    local board="internal/projectstatuscmd/board.go"
    local text
    text=$(cat "${board}" 2>/dev/null)
    assert_contains "the board file carries the claim's specification" \
        "SPECIFIED, NOT BUILT" "${text}"
    assert_contains "…naming the drill-down verb" "task unlanded" "${text}"
    assert_contains "…and saying which section it counts" \
        "first section" "${text}"
}

# ─── 7 — the data repair's one attributable landing ─────────────────────────
#
# The plan expected roughly twenty backfilled landings from grepping commit
# messages, and warned in the same breath that the grep over-credits. Held to
# the stated bar — the commit's own SUBJECT names the task, and the commit is on
# the base branch — exactly one of the candidates qualified. This pins the git
# half of that one; the ledger row itself is in the main database, which this
# suite is deliberately isolated from.
check_data_repair() {
    section "7 — the recorded landing is attributable"

    local sha="e03b74e26c1dcd5c1338b07531b5465c615ebc30"

    if git merge-base --is-ancestor "${sha}" main 2>/dev/null; then
        report_pass "the recorded commit is on the base branch"
    else
        report_fail "the recorded commit is on the base branch" \
            "${sha} reachable from main" "not an ancestor"
    fi

    local subject
    subject=$(git log -1 --format=%s "${sha}" 2>/dev/null)
    assert_contains "and its own subject names the task it was credited to" \
        "(E-1186)" "${subject}"
}

main() {
    setup

    check_regression_front
    if (( FAIL_COUNT > 0 )); then
        printf '\n  %sregression front failed — stopping before the rest%s\n' \
            "${RED}" "${RESET}"
        summary
    fi

    check_own_tests
    check_probe
    check_base_is_resolved
    check_report_help
    check_banner
    check_board_spec
    check_data_repair

    summary
}

main "$@"
