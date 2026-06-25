#!/usr/bin/env bash
#
# E-1252 verification script — confirms the task-status vocabulary rename:
#   needs_plan  -> unplanned
#   in_progress -> underway
#   verify      -> unverified
#
# E-1252 is a pure reference sweep (the data migration, values table/FK, and
# dropping `blocked` ride E-1532; the workflow-meaning / approve-gate edits and
# the `unapproved` status are E-1648). So verification checks four things:
#
#   1. the old slugs are GONE from every in-scope source/doc surface;
#   2. the new slugs are present at the canonical definition sites;
#   3. nothing was OVER-renamed — the English verb "verify", the
#      internal/verify test-runner package, `git commit --no-verify`,
#      matchers.py's dictionary entry, and historical records are untouched;
#   4. behavior is correct (handoff render emits `--status unverified`) and
#      both full test suites stay green.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1252-verify.sh
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

# Path prefixes that legitimately contain the old strings and are OUT of the
# rename's scope: the ledger/plan mirror, the verify test-runner package, applied
# change-files, point-in-time docs, the audit snapshot, generated CSS, and the
# matchers dictionary. Applied to the `path:` prefix of `git grep -n` output.
SCOPE_EXCLUDE='^\.endless/|^internal/verify/|^internal/schema/changes/|^tests/tasks/|^DB_AUDIT\.md:|^docs/(analysis|design|research|brief|talk|endless-discussion|prompt)|^demo/|output\.css|^src/endless/matchers\.py:'

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
    local detail="$2"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "${detail}"
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

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_cmd DESC CMD [ARGS...]
#   Pass if CMD exits 0. On failure, report the tail of its combined output.
assert_cmd() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_absent DESC PATTERN
#   Pass iff the regex PATTERN has NO in-scope matches (after SCOPE_EXCLUDE).
#   Used to prove an old slug is gone. PATTERN is an extended regex for git grep.
assert_absent() {
    local desc="$1"
    local pattern="$2"
    local hits
    # -e guards patterns that begin with '-' (e.g. --status verify).
    hits=$(git grep -nE -e "${pattern}" 2>/dev/null | grep -Ev "${SCOPE_EXCLUDE}")
    if [[ -z "${hits}" ]]; then
        report_pass "${desc}"
        return
    fi
    local n
    n=$(printf '%s\n' "${hits}" | grep -c .)
    report_fail "${desc}" "${n} stray match(es): $(printf '%s' "${hits}" | head -3 | tr '\n' '⏎')"
}

# assert_present DESC FILE PATTERN
#   Pass iff PATTERN (fixed string) appears in FILE.
assert_present() {
    local desc="$1" file="$2" pattern="$3"
    if grep -qF -- "${pattern}" "${file}" 2>/dev/null; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "expected '${pattern}' in ${file}"
}

# assert_render DESC TEMPLATE MODE NEEDLE
#   Render a handoff template via the freshly-built worktree binary and assert
#   NEEDLE is present (MODE=has) or absent (MODE=lacks). Proves the embedded
#   template carries the rename.
HANDOFF_VARS='{"spawned_id":"1252","spawned_title":"Rename statuses","spawner_task_id":"1621","worktree":"wt","branch":"br","return_window":"win","parent_title":"","parent_id":""}'
assert_render() {
    local desc="$1" tmpl="$2" mode="$3" needle="$4"
    local out
    out=$(printf '%s' "${HANDOFF_VARS}" | ./bin/endless-go template render "${tmpl}" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        report_fail "${desc}" "render exit=${rc} | $(printf '%s' "${out}" | tail -2 | tr '\n' '⏎')"
        return
    fi
    if [[ "${mode}" == "has" ]]; then
        if printf '%s' "${out}" | grep -qF -- "${needle}"; then
            report_pass "${desc}"
        else
            report_fail "${desc}" "expected '${needle}' in rendered ${tmpl}"
        fi
    else
        if printf '%s' "${out}" | grep -qF -- "${needle}"; then
            report_fail "${desc}" "unexpected '${needle}' in rendered ${tmpl}"
        else
            report_pass "${desc}"
        fi
    fi
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_build() {
    section "Build — packages compile and the worktree binary embeds new templates"
    assert_cmd "internal/... compiles" go build ./internal/...
    assert_cmd "endless-go builds (re-embeds handoff templates)" \
        go build -o bin/endless-go ./cmd/endless-go
}

test_old_slugs_gone() {
    section "Rename — old slugs removed from every in-scope surface"
    assert_absent "no 'needs_plan' anywhere in scope" 'needs_plan'
    assert_absent "no 'in_progress' anywhere in scope" 'in_progress'
    # Status-`verify` in its code/doc forms: quoted literal, --status flag,
    # backtick reference. (English "verify"/"verification" has none of these.)
    assert_absent "no quoted 'verify' status literal in scope" "'verify'"
    assert_absent "no quoted \"verify\" status literal in scope" '"verify"'
    assert_absent "no '--status verify' in scope" '--status verify'
    assert_absent "no backtick \`verify\` status reference in scope" '`verify`'
    # Disposition / display labels that mirror the status must not stay capital-V.
    assert_absent "session-rollup bucket renamed off \"Verify\"" '"Verify":'
}

test_new_slugs_present() {
    section "Rename — new slugs at the canonical definition sites"
    assert_present "cli.py TASK_STATUSES has 'unplanned'" src/endless/cli.py '"unplanned"'
    assert_present "cli.py TASK_STATUSES has 'underway'"  src/endless/cli.py '"underway"'
    assert_present "cli.py TASK_STATUSES has 'unverified'" src/endless/cli.py '"unverified"'
    assert_present "schema default status is 'unplanned'" \
        internal/schema/schema.sql "DEFAULT 'unplanned'"
    assert_present "session_status.go matches 'unverified'" \
        internal/events/session_status.go 'case "unverified":'
    assert_present "session rollup bucket is 'Unverified'" \
        internal/events/session_status.go '"Unverified"'
    assert_present "web status legend lists Unverified" \
        internal/web/pages/status_detail.templ '{"unverified", "Unverified",'
    assert_present "web status legend lists Underway" \
        internal/web/pages/status_detail.templ '{"underway", "Underway",'
    assert_present "web status legend lists Unplanned" \
        internal/web/pages/status_detail.templ '{"unplanned", "Unplanned",'
}

test_not_over_renamed() {
    section "Scope — the English verb and excluded surfaces are untouched"
    assert_cmd "internal/verify test-runner package still compiles" \
        go build ./internal/verify/
    assert_present "git 'commit --no-verify' preserved" \
        internal/hookcmd/claude.go 'commit --no-verify'
    assert_present "matchers.py 'verify' dictionary entry preserved" \
        src/endless/matchers.py '"value": "verify"'
    assert_present "guide English verb 'verify before recording' preserved" \
        docs/guide/index.md 'verify before recording'
}

test_handoff_render() {
    section "Behavior — handoff templates render the new status token"
    assert_render "task handoff emits '--status unverified'" \
        handoff/task.md has '--status unverified'
    assert_render "bug handoff emits '--status unverified'" \
        handoff/bug.md has '--status unverified'
    assert_render "task handoff no longer emits '--status verify'" \
        handoff/task.md lacks '--status verify'
    # Epics terminate via `--status completed`, never verify/unverified.
    assert_render "epic handoff does not emit '--status unverified'" \
        handoff/epic.md lacks '--status unverified'
    assert_render "epic handoff does not emit '--status verify'" \
        handoff/epic.md lacks '--status verify'
}

test_suites() {
    section "Regression — full Go and Python suites stay green"
    assert_cmd "go test ./internal/... (all packages)" \
        go test -count=1 ./internal/...
    # The handoff render tests shell out to `endless-go` via PATH; put the
    # freshly-built worktree binary first so they exercise the new templates
    # (a bare `just test` would hit the stale global endless-go symlink).
    assert_cmd "pytest tests/ (worktree binary on PATH)" \
        env PATH="${PWD}/bin:${PATH}" uv run pytest tests/ -q
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi
    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    # Worktrees need a go.work pointing at the local go-pkgs/ modules; without it
    # the replace directives resolve at the wrong depth and the build fails.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        if command -v just >/dev/null 2>&1; then
            just go-work-init >/dev/null 2>&1
        fi
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1252 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_build
    test_old_slugs_gone
    test_new_slugs_present
    test_not_over_renamed
    test_handoff_render
    test_suites

    summary
}

main "$@"
