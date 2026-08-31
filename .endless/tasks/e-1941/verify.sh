#!/usr/bin/env bash
#
# E-1941 verification — no failure of `just land` can leave the database ahead
# of the code.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1941
#
# Background. On 2026-08-10 the `land` recipe applied this branch's schema
# changes BEFORE calling `endless worktree land`. The apply succeeded, the land
# then failed, and the real database was left migrated to a schema no installed
# binary understood: every session query referenced a dropped column, all 64
# sessions went unresolvable, and recovery needed a hand-rolled restore. The
# recipe only ever reasoned about apply FAILING ("the land aborts before main
# advances"); apply succeeding and the merge failing was the unhandled — and
# irreversible — case.
#
# The fix has three parts, and this script checks each:
#   1. The recipe no longer touches the DB at all. `endless worktree land`
#      applies schema changes itself, between the ff-merge and the
#      record-landing, so a failure leaves the DB merely LAGGING landed code.
#   2. A self_dev land is refused up front when the branch is behind base — the
#      binary it points at the real DB is built from that source.
#   3. MAIN ADVANCING obliges a binary rebuild, not the land's exit code. A land
#      that advances main and then fails must still refresh the binaries, or the
#      globals end up older than the DB they now have to read.
#
# Checks 1-3 drive the REAL recipe body (extracted with `just --show land`)
# against a throwaway git repo with stubbed `endless`/`just`, so no real
# database, worktree, or branch is touched. The ordering INSIDE
# `endless worktree land` — apply strictly after the ff-merge and strictly
# before the record — is asserted by the pytest layer in check 5, since the
# harness necessarily stubs that command out.
#
# Fail-fast: each section aborts the run on its first failure, so the first
# broken invariant is the last thing printed.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on environment/setup error.
#
# Model: .endless/tasks/e-1709/verify.sh (the E-1596 reference shape).

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
RECIPE_BODY=""

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

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

report_skip() {
    printf '  %s-%s %s %s(skipped: %s)%s\n' \
        "${DIM}" "${RESET}" "$1" "${DIM}" "$2" "${RESET}"
}

# Abort the run at the first failure within a section (fail-fast).
bail_if_failed() {
    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sfail-fast: stopping at the first broken invariant%s\n' \
            "${DIM}" "${RESET}"
        summary
        exit 1
    fi
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

assert_eq() {  # DESC EXPECTED ACTUAL
    if [[ "$2" == "$3" ]]; then report_pass "$1"; return; fi
    report_fail "$1" "$2" "$3"
}

assert_contains() {  # DESC HAYSTACK NEEDLE
    if [[ "$2" == *"$3"* ]]; then report_pass "$1"; return; fi
    report_fail "$1" "output containing '$3'" "${2:-<empty>}"
}

assert_not_contains() {  # DESC HAYSTACK NEEDLE
    if [[ "$2" != *"$3"* ]]; then report_pass "$1"; return; fi
    report_fail "$1" "output WITHOUT '$3'" "${2}"
}

assert_succeeds() {  # DESC CMD [ARGS...]
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# ─── harness ────────────────────────────────────────────────────────────────

# Build a throwaway repo: main + an e-9999 worktree on task/9999-x.
#   $1 = sandbox dir
#   $2 = "behind" to leave the branch 3 commits behind main, else "current"
#   $3 = "change" to add a schema-change file on the branch, else "nochange"
make_sandbox() {
    local tmp="$1" behind="$2" change="$3"
    local main="${tmp}/proj" wt
    wt="${main}/.endless/worktrees/e-9999"

    mkdir -p "${main}"
    git init -q -b main "${main}" >/dev/null 2>&1 || return 1
    git -C "${main}" config user.email t@t.t
    git -C "${main}" config user.name t
    git -C "${main}" config commit.gpgsign false
    printf '.endless/worktrees/\n' > "${main}/.gitignore"
    printf 'base\n' > "${main}/README.md"
    git -C "${main}" add -A
    git -C "${main}" commit -q -m base

    git -C "${main}" branch task/9999-x
    git -C "${main}" worktree add -q "${wt}" task/9999-x >/dev/null 2>&1
    mkdir -p "${wt}/bin"
    if [[ "${change}" == change ]]; then
        mkdir -p "${wt}/internal/schema/changes"
        printf 'ALTER TABLE t DROP COLUMN c;\n' \
            > "${wt}/internal/schema/changes/0099-drop.sql"
        git -C "${wt}" add -A
        git -C "${wt}" commit -q -m "E-9999: schema change"
    else
        printf 'feature\n' > "${wt}/feature.txt"
        git -C "${wt}" add -A
        git -C "${wt}" commit -q -m "E-9999: work"
    fi

    local i
    if [[ "${behind}" == behind ]]; then
        for i in 1 2 3; do
            printf '%s\n' "${i}" >> "${main}/README.md"
            git -C "${main}" add -A
            git -C "${main}" commit -q -m "main ${i}"
        done
    elif [[ "${behind}" == ledger-behind ]]; then
        # What actually accumulates on main during any active session: commits
        # touching ONLY the db-ledger. These cannot affect a binary, so they
        # must not make the land refuse.
        mkdir -p "${main}/.endless/db-ledger"
        for i in 1 2 3; do
            printf '{"e":%s}\n' "${i}" \
                >> "${main}/.endless/db-ledger/db-entries-aaaa-000001.jsonl"
            git -C "${main}" add -A
            git -C "${main}" commit -q -m "Endless: record ledger entry"
        done
    fi
    return 0
}

# Write stub endless / endless-go / just onto a PATH dir.
#   $1 = stubs dir
#   $2 = land behavior: "fail" | "advance-then-fail" | "ok"
make_stubs() {
    local dir="$1" land_mode="$2"
    mkdir -p "${dir}"

    cat > "${dir}/endless" <<STUB
#!/usr/bin/env bash
printf 'endless %s\n' "\$*" >> "\${CALLLOG}"
if [ "\${1:-}" = db ] && [ "\${2:-}" = apply-change ]; then
    printf 'MIGRATED\n' > "\${DBSTATE}"
fi
if [ "\${1:-}" = worktree ] && [ "\${2:-}" = land ]; then
    case "${land_mode}" in
        advance-then-fail)
            git -C "\${SANDBOX_MAIN}" merge --ff-only task/9999-x >/dev/null 2>&1
            printf 'MIGRATED\n' > "\${DBSTATE}"
            echo "  (stub) land advanced main, then failed recording" >&2
            exit 1
            ;;
        fail)
            echo "  (stub) land failed: rebase conflict on the db ledger" >&2
            exit 1
            ;;
        ok)
            git -C "\${SANDBOX_MAIN}" merge --ff-only task/9999-x >/dev/null 2>&1
            exit 0
            ;;
    esac
fi
exit 0
STUB

    cat > "${dir}/endless-go" <<'STUB'
#!/usr/bin/env bash
printf 'endless-go %s\n' "$*" >> "${CALLLOG}"
exit 1
STUB

    cat > "${dir}/just" <<'STUB'
#!/usr/bin/env bash
printf 'just %s (cwd=%s)\n' "$*" "$(basename "${PWD}")" >> "${CALLLOG}"
exit 0
STUB

    chmod +x "${dir}/endless" "${dir}/endless-go" "${dir}/just"
}

# Run the real recipe body in a sandbox.
# Echoes "rc|dbstate|main_moved|calllog_path"; caller splits on '|'.
run_recipe() {  # $1=behind $2=change $3=land_mode
    local tmp; tmp="$(mktemp -d)"
    if ! make_sandbox "${tmp}" "$1" "$2"; then
        printf '2||no|\n'; return
    fi
    make_stubs "${tmp}/stubs" "$3"

    local main="${tmp}/proj" body="${tmp}/land.sh"
    printf '%s\n' "${RECIPE_BODY}" > "${body}"

    local before after rc moved
    before="$(git -C "${main}" rev-parse main)"
    (
        cd "${main}" || exit 2
        CALLLOG="${tmp}/calls.log" DBSTATE="${tmp}/dbstate" \
        SANDBOX_MAIN="${main}" PATH="${tmp}/stubs:${PATH}" \
            bash "${body}"
    ) > "${tmp}/out.txt" 2>&1
    rc=$?
    after="$(git -C "${main}" rev-parse main)"
    moved=no; [[ "${before}" != "${after}" ]] && moved=yes

    local dbstate="clean"
    [[ -f "${tmp}/dbstate" ]] && dbstate="$(cat "${tmp}/dbstate")"
    printf '%s|%s|%s|%s\n' "${rc}" "${dbstate}" "${moved}" "${tmp}"
}

# ─── check 1: a failed land never migrates the DB ───────────────────────────

test_failed_land_leaves_db_clean() {
    section "1. A failed land never leaves the DB ahead of the code"

    local res rc dbstate moved tmp calls
    res="$(run_recipe current change fail)"
    IFS='|' read -r rc dbstate moved tmp <<< "${res}"
    calls="$(cat "${tmp}/calls.log" 2>/dev/null)"

    assert_eq "recipe reports the land's failure (exit non-zero)" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    assert_eq "DB was NEVER migrated (the 2026-08-10 regression)" \
        "clean" "${dbstate}"
    assert_eq "main never advanced" "no" "${moved}"
    assert_not_contains "recipe never calls 'endless db apply-change'" \
        "${calls}" "db apply-change"
    assert_not_contains "recipe never calls 'endless db backup'" \
        "${calls}" "db backup"

    rm -rf "${tmp}"
    bail_if_failed
}

# ─── check 2: behind-main refusal ───────────────────────────────────────────

test_behind_main_is_refused() {
    section "2. No behind-base refusal anywhere — staleness is removed, not gated"

    # History: the first fix REFUSED any land whose branch was behind base. That
    # gated on a proxy for "is the binary stale?", got the proxy wrong three
    # times (ledger auto-commits, then Python/justfile/test drift, then
    # hardcoded directories), and even once tuned blocked every worktree in the
    # repo continuously — main takes a Go commit every few hours — while
    # printing a hand-rebase as the remedy, which is the one operation that
    # risks the E-1943 ledger conflict. Step 4.2 now rebuilds the binary from
    # the freshly-rebased source instead, so nothing needs gating.
    local code
    code="$(printf '%s\n' "${RECIPE_BODY}" | grep -vE '^[[:space:]]*#')"
    assert_not_contains "recipe has no behind-check" "${code}" "rev-list --count"

    if uv run python -c "
import sys
from endless import worktree_cmd as w
sys.exit(0 if not hasattr(w, '_refuse_if_behind_base')
         and not hasattr(w, 'BINARY_SOURCE_PATHS') else 1)
" >/dev/null 2>&1; then
        report_pass "no behind-base refusal survives in worktree_cmd"
    else
        report_fail "no behind-base refusal survives in worktree_cmd" \
            "_refuse_if_behind_base / BINARY_SOURCE_PATHS both gone" "still present"
    fi

    # End-to-end: a branch behind base must run straight through the recipe.
    # Ledger-only drift was the first false-positive shape and is the cheapest
    # to re-check here.
    local res rc dbstate moved tmp calls
    res="$(run_recipe ledger-behind nochange ok)"
    IFS='|' read -r rc dbstate moved tmp <<< "${res}"
    calls="$(cat "${tmp}/calls.log" 2>/dev/null)"

    assert_eq "a behind branch is not blocked (exit 0)" "0" "${rc}"
    assert_contains "reaches the worktree rebuild" "${calls}" "just go"
    assert_contains "reaches the land" "${calls}" "worktree land"
    assert_eq "DB untouched by the recipe" "clean" "${dbstate}"

    rm -rf "${tmp}"
    bail_if_failed
}

# ─── check 3: rebuild follows main, not the exit code ───────────────────────

test_rebuild_follows_main() {
    section "3. Binaries are refreshed whenever main advanced"

    local res rc dbstate moved tmp calls

    # Land advances main, then fails. The globals would otherwise be left older
    # than the DB they now have to read — the incident's shape.
    res="$(run_recipe current change advance-then-fail)"
    IFS='|' read -r rc dbstate moved tmp <<< "${res}"
    calls="$(cat "${tmp}/calls.log" 2>/dev/null)"
    assert_eq "main advanced in this scenario (harness sanity)" "yes" "${moved}"
    assert_contains "'just build' still ran despite the land failing" \
        "${calls}" "just build"
    assert_eq "the land's failure is still propagated" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    rm -rf "${tmp}"
    bail_if_failed

    # Land fails without advancing main: nothing to match, so no rebuild.
    res="$(run_recipe current change fail)"
    IFS='|' read -r rc dbstate moved tmp <<< "${res}"
    calls="$(cat "${tmp}/calls.log" 2>/dev/null)"
    assert_eq "main did not advance in this scenario (harness sanity)" \
        "no" "${moved}"
    assert_not_contains "no pointless rebuild when main did not move" \
        "${calls}" "just build"
    rm -rf "${tmp}"
    bail_if_failed
}

# ─── check 4: recipe hygiene ────────────────────────────────────────────────

test_recipe_hygiene() {
    section "4. Recipe hygiene"

    local code go_ln land_ln
    code="$(printf '%s\n' "${RECIPE_BODY}" | grep -vE '^[[:space:]]*#')"

    assert_not_contains "no 'endless db apply-change' in the recipe" \
        "${code}" "endless db apply-change"
    assert_not_contains "no bash strict mode (house rule)" \
        "${code}" "set -euo pipefail"
    assert_contains "keeps 'set -u'" "${code}" "set -u"

    go_ln="$(printf '%s\n' "${code}" | grep -nF 'cd "$wt" && just go' \
        | head -1 | cut -d: -f1)"
    land_ln="$(printf '%s\n' "${code}" | grep -nF 'endless worktree land' \
        | head -1 | cut -d: -f1)"
    if [[ -n "${go_ln}" && -n "${land_ln}" && "${go_ln}" -lt "${land_ln}" ]]; then
        report_pass "worktree rebuild still precedes the land (E-1709)"
    else
        report_fail "worktree rebuild still precedes the land (E-1709)" \
            "just go before endless worktree land" \
            "go=${go_ln:-<missing>} land=${land_ln:-<missing>}"
    fi

    if printf '%s\n' "${RECIPE_BODY}" | bash -n 2>/dev/null; then
        report_pass "recipe body parses (bash -n)"
    else
        report_fail "recipe body parses (bash -n)" "no syntax errors" \
            "$(printf '%s\n' "${RECIPE_BODY}" | bash -n 2>&1 | head -5)"
    fi

    if command -v shellcheck >/dev/null 2>&1; then
        local sc
        sc="$(printf '%s\n' "${RECIPE_BODY}" \
            | shellcheck -s bash -e SC2034 - 2>&1)"
        if [[ -z "${sc}" ]]; then
            report_pass "shellcheck clean"
        else
            report_fail "shellcheck clean" "no findings" "$(head -12 <<< "${sc}")"
        fi
    else
        report_skip "shellcheck clean" "shellcheck not installed"
    fi

    bail_if_failed
}

# ─── check 5: unit tests ────────────────────────────────────────────────────

test_unit_suites() {
    section "5. Automated suites"

    assert_succeeds "pytest tests/test_worktree_land_schema_apply.py" \
        uv run pytest tests/test_worktree_land_schema_apply.py -q
    bail_if_failed

    assert_succeeds "pytest tests/test_worktree_land_*.py (no regressions)" \
        bash -c 'uv run pytest tests/test_worktree_land_*.py \
            tests/test_worktree_orphan_branch.py -q'
    bail_if_failed
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v just >/dev/null 2>&1; then
        printf 'ERROR: just not on PATH\n' >&2
        exit 2
    fi

    # The real recipe body, with the task-id placeholder bound. `just --show`
    # prints doc comments then the `land task_id="":` header; drop through it.
    RECIPE_BODY="$(just --show land 2>/dev/null \
        | sed -n '/^land task_id/,$p' \
        | tail -n +2 \
        | sed -e 's/{{ *task_id *}}/E-9999/')"
    if [[ -z "${RECIPE_BODY}" ]]; then
        printf 'ERROR: could not extract the land recipe body\n' >&2
        exit 2
    fi

    printf '%sE-1941 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${REPO_ROOT}"
    printf '  db:      none — checks 1-3 use throwaway repos and stub binaries\n'

    test_failed_land_leaves_db_clean
    test_behind_main_is_refused
    test_rebuild_follows_main
    test_recipe_hygiene
    test_unit_suites

    summary
}

main "$@"
