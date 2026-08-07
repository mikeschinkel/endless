#!/usr/bin/env bash
#
# E-1906 verification — the vestigial session-recap machinery is gone, and
# nothing that merely shared its neighborhood went with it.
#
# `needs_recap` dated from the era when one Claude session was open-ended and
# covered many tasks, so an LLM-generated recap was the only way to reconstruct
# what a session had done. One session now maps to one task and `endless task
# report` covers that handoff directly, so the flag earned nothing and cost a
# `claude -p` call per stale session.
#
# Removed: the writer (`monitor.FlagNeedsRecap`, fired on Stop and SessionEnd),
# the generator (`internal/monitor/recap.go`, `internal/hookcmd/recap.go`,
# `session_cmd.recap_session` / `_generate_recap`), the surfaces (`endless-go
# hook recap`, `endless session recap`, the prompt hook's throttled background
# trigger, the `session list` "N session(s) need recaps" notice), the never-
# emitted `session.recapped` event kind and its payload, and both DB columns.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1906-verify.sh
#
# Requires `just build` (or `just go`) first — checks 4/5 drive the CANDIDATE
# bin/endless-go, not the global install.
#
# What it checks:
#   0. Fail-fast fold-in regression: `go build ./...`, the Go tests for the four
#      packages this touched, `just guide-check`, and the whole of
#      tests/tasks/e-1780-verify.sh (which pinned the appendix command list this
#      change had to amend). A failure here short-circuits the rest.
#   1. No reference survives anywhere in the source tree.
#   2. The two dead Go files are actually deleted, not just unreferenced.
#   3. schema.sql declares neither column, and a DB built from it has neither —
#      while `summary`, which is set independently and rendered by three
#      surfaces, is still there. The overreach guard.
#   4. The change file really migrates: a POPULATED old-shape DB loses both
#      columns and keeps its rows and their summaries.
#   5. Every CLI surface is gone from both the Go and the Python entry points.
#   6. The docs no longer advertise `session recap`.
#   7. The safety premise for retiring the event kind still holds: no ledger
#      line anywhere in the repo references `session.recapped`.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

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

# assert_eq DESC WANT GOT
assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${want}" == "${got}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${needle}" "${hay}"
}

# assert_not_contains DESC HAYSTACK NEEDLE
assert_not_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${needle}" "${hay}"
}

# assert_file_lacks DESC FILE PATTERN
assert_file_lacks() {
    local desc="$1" file="$2" pat="$3"
    if ! grep -qF -- "${pat}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} lacks: ${pat}" "still present"
}

# assert_file_has DESC FILE PATTERN
assert_file_has() {
    local desc="$1" file="$2" pat="$3"
    if grep -qF -- "${pat}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} contains: ${pat}" "absent"
}

# assert_absent DESC PATH
assert_absent() {
    local desc="$1" path="$2"
    if [[ ! -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "deleted" "still on disk"
}

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(tail -20 <<<"${out}")"
}

# ─── globals ────────────────────────────────────────────────────────────────

REPO_ROOT=""
GO=""
EN=""
WORK=""
CHANGE="internal/schema/changes/e-1906-drop-sessions-recap-columns.sql"

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    GO="${REPO_ROOT}/bin/endless-go"
    EN="${REPO_ROOT}/.venv/bin/endless"
    [[ -x "${GO}" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${GO}" >&2; exit 2; }
    command -v sqlite3 >/dev/null 2>&1 || {
        printf 'ERROR: sqlite3 not on PATH\n' >&2; exit 2; }
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT
}

# ─── checks ─────────────────────────────────────────────────────────────────

# 0. The fold-in fail-fast front. `go test ./internal/...` as a whole is NOT run
# here: TestDestroyForceOverridesLiveWriterCheck in internal/sandboxcmd hangs on
# main (filed as E-1908) and would swallow this suite in a 10-minute timeout.
# The four packages below are exactly the ones this change touched.
check_regression_front() {
    section "0 — fail-fast fold-in regression"

    assert_succeeds "go build ./... is clean" go build ./...
    assert_succeeds "Go tests pass for the four packages this touched" \
        go test -timeout 120s \
            ./internal/events/... ./internal/monitor/... \
            ./internal/hookcmd/... ./internal/schema/...
    assert_succeeds "just guide-check is green (command→section map intact)" \
        just guide-check
    # E-1780 pinned the appendix's session-command list, which this change had
    # to amend. Running its whole suite proves the amendment is consistent
    # rather than merely making one assertion pass.
    assert_succeeds "tests/tasks/e-1780-verify.sh passes (guide contract)" \
        ./tests/tasks/e-1780-verify.sh
}

# 1. No LIVE CODE reference survives. Two categories of mention are legitimate
# and are filtered out rather than hunted down:
#
#   a. Comments. A `#`/`--`/`//` line that names `needs_recap` to explain why
#      something is absent is the removal documenting itself — db.py says why
#      its ALTERs are gone, event_test.go says why the ValidKinds count dropped
#      by one. Deleting those comments would make the change less legible, not
#      more complete. Only non-comment lines can fail this check.
#   b. Fixture DDL that reproduces a historical `sessions` shape. Any verify
#      script testing a migration builds the pre-change table inline (see
#      e-1568-verify.sh, e-1905-verify.sh, and check 4 below), so a line that
#      DECLARES one of these columns — `needs_recap INTEGER NOT NULL ...` — is
#      history, not a survivor. A line that READS or WRITES the column
#      (`SET needs_recap = 1`, `WHERE needs_recap = 1`) is live code and still
#      fails. Matching on line SHAPE rather than file name is deliberate: an
#      earlier draft excluded fixture files by name and broke the moment
#      e-1905-verify.sh landed carrying a pre-E-1906 sessions table.
#   c. Whole files that must name what they remove or reproduce:
#      - .endless/   the ledger, plans and outcomes are an append-only record;
#      - e-1568-*    that change file rebuilds a pre-E-1568 sessions table which
#                    HAD these columns, in a column list rather than a DDL
#                    declaration, so shape alone does not exempt it;
#      - e-1906-*    this change's own `ALTER TABLE ... DROP COLUMN` statements
#                    and this script's own assertion list.
# live_hits IDENT — every reference to IDENT that is neither a comment nor a
# fixture DDL column declaration. See the two filters documented above.
live_hits() {
    grep -rnI --exclude-dir=vendor --exclude-dir=.git --exclude-dir=.endless \
        --exclude='e-1568-*' --exclude='e-1906-*' \
        -e "$1" cmd internal src tests docs justfile 2>/dev/null \
    | grep -vE ':[0-9]+:[[:space:]]*(#|--|//)' \
    | grep -vE ':[0-9]+:[[:space:]]*(needs_recap|summary_seq)[[:space:]]+(INTEGER|TEXT)[[:space:]]'
}

check_no_references() {
    section "1 — no live-code reference survives in the source tree"

    local ident hits
    for ident in needs_recap NeedsRecap FlagNeedsRecap GetSessionsNeedingRecap \
                 RecapSession RecapOneStale summary_seq \
                 KindSessionRecapped SessionRecappedPayload session.recapped; do
        hits=$(live_hits "${ident}")
        if [[ -z "${hits}" ]]; then
            report_pass "no live \`${ident}\` anywhere in the source tree"
        else
            report_fail "no live \`${ident}\` anywhere in the source tree" \
                "zero hits" "${hits}"
        fi
    done

    # Neither filter may become a blanket amnesty. Two canaries, planted in the
    # tree and removed immediately, prove each one is still narrow:
    #   - a Go usage line (not a comment, not a DDL declaration) must be caught;
    #   - a SQL read of the column must be caught DESPITE living in a .sh
    #     fixture file, which is exactly what the DDL filter must not excuse.
    local go_canary="internal/monitor/e1906_canary_check.go"
    local sh_canary="tests/tasks/e1906-canary-check.sh"
    printf 'package monitor\n\nvar e1906Canary = "needs_recap"\n' > "${go_canary}"
    printf '#!/bin/sh\nsqlite3 db "SELECT 1 FROM sessions WHERE needs_recap = 1;"\n' > "${sh_canary}"
    hits=$(live_hits needs_recap)
    rm -f "${go_canary}" "${sh_canary}"

    assert_contains "a planted Go usage is still caught" \
        "${hits}" "e1906_canary_check.go"
    assert_contains "a planted SQL read is still caught inside a fixture file" \
        "${hits}" "e1906-canary-check.sh"

    # The recap generator was the only caller of the internal `claude -p`
    # helper besides the verb-check; the helper itself must survive.
    assert_file_has "internal_claude.py survives (the verb-check still uses it)" \
        "src/endless/internal_claude.py" "def run_internal_claude"
}

check_files_deleted() {
    section "2 — the dead files are deleted, not merely unreferenced"
    assert_absent "internal/monitor/recap.go is gone" "internal/monitor/recap.go"
    assert_absent "internal/hookcmd/recap.go is gone" "internal/hookcmd/recap.go"
}

# 3. The schema no longer declares the columns — and the overreach guard:
# sessions.summary is NOT recap state. It is set from the first assistant
# response (monitor.setSessionSummary) and rendered by `session list`, the
# live-session query and session navigation. Only the recap generator ever
# wrote it alongside summary_seq.
check_schema_shape() {
    section "3 — schema.sql drops both columns and keeps \`summary\`"

    assert_file_lacks "schema.sql does not declare needs_recap" \
        "internal/schema/schema.sql" "needs_recap"
    assert_file_lacks "schema.sql does not declare summary_seq" \
        "internal/schema/schema.sql" "summary_seq"

    local db cols
    db="${WORK}/fresh.db"
    if ! sqlite3 "${db}" < internal/schema/schema.sql >/dev/null 2>&1; then
        report_fail "schema.sql applies cleanly to an empty DB" "exit 0" "sqlite3 failed"
        return
    fi
    report_pass "schema.sql applies cleanly to an empty DB"

    cols=$(sqlite3 "${db}" "SELECT group_concat(name) FROM pragma_table_info('sessions');")
    assert_not_contains "a fresh DB's sessions has no needs_recap" "${cols}" "needs_recap"
    assert_not_contains "a fresh DB's sessions has no summary_seq" "${cols}" "summary_seq"
    assert_contains "a fresh DB's sessions KEEPS summary" "${cols}" "summary"
    assert_contains "a fresh DB's sessions KEEPS short_id" "${cols}" "short_id"
}

# 4. The change file is what runs against the real populated DB at land time,
# so prove it on a populated old-shape DB rather than trusting the DDL. Shape
# built inline for the same reason tests/tasks/e-1568-verify.sh does it: no git
# archaeology, fully deterministic.
check_change_file_migrates() {
    section "4 — the change file migrates a populated old-shape DB"

    if [[ ! -f "${CHANGE}" ]]; then
        report_fail "the change file exists" "${CHANGE}" "absent"
        return
    fi
    report_pass "the change file exists"

    local mdir="${WORK}/migrate"; mkdir -p "${mdir}"
    sqlite3 "${mdir}/endless.db" <<'SQL'
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude',
    state TEXT NOT NULL DEFAULT 'working',
    active_task_id INTEGER,
    active_epic_id INTEGER,
    kind_id INTEGER NOT NULL DEFAULT 1,
    plan_file_path TEXT,
    process TEXT,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    transcript_path TEXT,
    summary TEXT,
    hidden INTEGER NOT NULL DEFAULT 0,
    needs_recap INTEGER NOT NULL DEFAULT 0,
    summary_seq INTEGER NOT NULL DEFAULT 0,
    short_id TEXT,
    UNIQUE (session_id),
    UNIQUE (short_id)
);
INSERT INTO sessions
    (session_id, state, kind_id, started_at, summary, needs_recap, summary_seq)
    VALUES ('uuid-A', 'working', 1, '2026-01-01T00:00:00',
            'a real summary that must survive', 1, 7);
SQL

    local pre applied post row marker
    pre=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM pragma_table_info('sessions') WHERE name IN ('needs_recap','summary_seq');")
    assert_eq "PRE: the old DB really has both columns" "2" "${pre}"

    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_contains "apply-change succeeds on the populated old DB" \
        "${applied}" '"status":"applied"'

    post=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM pragma_table_info('sessions') WHERE name IN ('needs_recap','summary_seq');")
    assert_eq "POST: both columns are gone" "0" "${post}"

    # The row and its summary must be untouched — a DROP COLUMN that took data
    # with it would be the expensive way to fail this change.
    row=$(sqlite3 "${mdir}/endless.db" "SELECT session_id||'|'||summary FROM sessions;")
    assert_eq "POST: the session row and its summary survive" \
        "uuid-A|a real summary that must survive" "${row}"

    marker=$(sqlite3 "${mdir}/endless.db" \
        "SELECT name FROM _schema_version WHERE name LIKE 'e-1906%';")
    assert_eq "POST: the _schema_version marker is recorded" \
        "e-1906-drop-sessions-recap-columns" "${marker}"

    # Idempotence: `just land` may re-run a change after a partial failure.
    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_not_contains "re-applying is a no-op, not an error" "${applied}" '"status":"applied"'
}

check_cli_surfaces_gone() {
    section "5 — every CLI surface is gone (Go + Python)"

    local out rc

    out=$("${GO}" hook recap 2>&1); rc=$?
    assert_contains "\`endless-go hook recap\` is rejected" "${out}" "Unknown command: recap"
    if [[ "${rc}" -ne 0 ]]; then report_pass "\`endless-go hook recap\` exits non-zero"
    else report_fail "\`endless-go hook recap\` exits non-zero" "non-zero" "exit 0"; fi

    out=$("${GO}" hook 2>&1)
    assert_not_contains "the hook usage line no longer lists recap" "${out}" "recap"
    assert_contains "the hook usage line still lists the real commands" \
        "${out}" "prompt, claude, codex"

    out=$("${GO}" --help 2>&1 | grep -i '^  hook')
    assert_not_contains "the top-level usage no longer lists hook recap" "${out}" "recap"

    out=$(cd "${REPO_ROOT}" && "${EN}" session --help 2>&1)
    assert_not_contains "\`endless session --help\` no longer lists recap" "${out}" "recap"
    assert_contains "\`endless session --help\` still lists its real commands" "${out}" "history"

    out=$(cd "${REPO_ROOT}" && "${EN}" session recap 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "\`endless session recap\` no longer exists"
    else report_fail "\`endless session recap\` no longer exists" "non-zero exit" "exit 0"; fi
}

check_docs() {
    section "6 — the docs no longer advertise the command"
    assert_file_lacks "appendix-a.md drops the \`session recap\` bullet" \
        "docs/guide/appendix-a.md" "session recap"
    assert_file_lacks "sessions.md drops recap from the interactive list" \
        "docs/guide/sessions.md" "search / recap"
    # The pointer and the surviving commands must still be there — this is a
    # deletion, not a demolition.
    assert_file_has "appendix-a.md still documents \`session history\`" \
        "docs/guide/appendix-a.md" "session history"
    assert_file_has "sessions.md still carries the appendix-a pointer" \
        "docs/guide/sessions.md" "endless guide appendix-a"
}

# 7. Retiring KindSessionRecapped rests on one fact: no ledger line anywhere
# references it, so no replay can encounter it. (Replay would skip it silently
# either way — the projector's default branch drops every unrecognized kind,
# which is the separate hazard filed as E-1907 — but this suite pins the
# premise rather than relying on that.)
check_no_ledger_events() {
    section "7 — the retired event kind was never emitted"

    local hits
    hits=$(grep -rl 'session\.recapped' .endless/db-ledger/ 2>/dev/null)
    if [[ -z "${hits}" ]]; then
        report_pass "no ledger segment references session.recapped"
    else
        report_fail "no ledger segment references session.recapped" "zero files" "${hits}"
    fi

    assert_file_lacks "the event vocabulary no longer declares the kind" \
        "internal/events/event.go" "session.recapped"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1906 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"
    printf '  binary: %s\n' "${GO}"

    check_regression_front
    # Fail fast: if the build or the touched packages are broken, the rest of
    # the suite is reporting on rubble.
    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sregression front failed — skipping the E-1906 checks%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        return 1
    fi

    check_no_references
    check_files_deleted
    check_schema_shape
    check_change_file_migrates
    check_cli_surfaces_gone
    check_docs
    check_no_ledger_events

    summary
}

main "$@"
