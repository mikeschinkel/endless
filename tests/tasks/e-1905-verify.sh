#!/usr/bin/env bash
#
# E-1905 verification — remove the vestigial `sessions.transcript_path` column
# and the `endless session reimport` escape hatch.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1905-verify.sh
#
# WHAT LANDED
#   `transcript_path` was written ONCE, at SessionStart, from Claude's hook
#   payload — and never re-recorded. Claude rotates a session's transcript into
#   `~/.claude/projects/<encoded-cwd>/` , so the moment a session changes cwd
#   the file moves and the stored column goes stale. Nothing live consumed it:
#   `session_messages` is kept current by the Go parser (monitor.ParseTranscript),
#   which is handed the payload path on every event and never reads the column.
#   Its only two readers were `endless session reimport` (a manual-only CLI
#   escape hatch carrying a SECOND, duplicate transcript parser in Python) and
#   one ORDER BY tiebreak in events.inheritedSessionID. All three are gone.
#
#   KEPT: `transcript_offset` — the live parser's resume cursor, unrelated.
#
# THE ONE INTENDED BEHAVIOR CHANGE
#   events.inheritedSessionID (E-1645 reopen-context) ranked prior ended sessions
#   by "left evidence of real work": populated `process` OR populated
#   `transcript_path` OR a >=10s span. Dropping the column removes one disjunct.
#   Layer B pins what that costs — and layer B's last check pins the reason it
#   costs almost nothing: `process IS NOT NULL` was ALREADY unreachable in that
#   query (E-1530's triggers NULL `process` on any row reaching state='ended',
#   and the query filters to state='ended'), so the practical evidence test was
#   already transcript_path-or-span and is now span alone. The rows that lose
#   evidence are exactly the sub-10s E-1640 ghosts this ranking deprioritizes.
#
# LEDGER SAFETY (asked during implementation; asserted, not assumed)
#   The db-ledger journals task / task_dep / decision / decision_relation /
#   session_status / focus / session_tasks entities. There is NO `session`
#   entity and no `transcript_path` FIELD KEY in any structured payload, so
#   `rebuild-db` cannot regress on this drop. Layer D proves that against the
#   real ledger rather than trusting the plan's assertion. (Textual hits for the
#   string DO exist in the ledger — inside task text/description prose, e.g.
#   E-857's plan and E-1905's own description — which replay verbatim into a
#   TEXT column. Layer D distinguishes the two.)
#
# Layers:
#   A. FAIL-FAST unit + build — schema no longer declares the column, the Go
#      accessors are gone, and the packages that touched it still pass.
#   B. Reopen-context ranking — the evidence clause after losing transcript_path.
#   C. Removal surface — `session reimport` is unroutable, the duplicate Python
#      parser and its helpers are gone, and no live source still names the column.
#   D. Ledger rebuild safety — the real db-ledger carries no session entity and
#      no transcript_path field key.
#   E. Migration — apply the change file to a synthetic pre-E-1905 DB: column
#      dropped, rows and transcript_offset preserved, triggers and FKs intact,
#      re-apply is a no-op.
#   F. Project-wide regression — `go vet`, `go test ./...`, `just test`,
#      `just guide-check`.
#
# ISOLATION
#   Every DB this script touches is a throwaway `--config-dir` under $TMP. The
#   real ledger at ~/.config/endless/endless.db is never opened for write, and
#   the worktree sandbox is not used. Layer D only READS .endless/db-ledger.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

REPO_ROOT=""
EGO=""        # the worktree's endless-go (rebuilt in layer A)
TMP=""        # scratch root; all throwaway DBs live under it

CHANGE_FILE="internal/schema/changes/e-1905-drop-sessions-transcript-path.sql"

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
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
}

summary() {
    section "Summary"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s, 0 failed\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
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

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | $(printf '%s' "${output}" | tail -20)"
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"
    else report_fail "$1" "$2" "$3"; fi
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "contains: $2" "$3"; fi
}

# ─── layer A — build + unit contracts (FAIL-FAST) ───────────────────────────

run_unit_layer() {
    section "A — build + schema/unit contracts (FAIL-FAST)"
    note "if the tree does not build or the column is still declared, nothing"
    note "below is worth running"

    local before="${FAIL_COUNT}"

    assert_succeeds "just go (builds bin/endless-go)" just go
    assert_succeeds "go vet ./... is clean" go vet ./...

    # schema.sql no longer declares the column. Asserted by applying the REAL
    # embedded schema to a fresh DB, not by grepping the file.
    local fresh="${TMP}/fresh"
    mkdir -p "${fresh}"
    sqlite3 "${fresh}/endless.db" < "${REPO_ROOT}/internal/schema/schema.sql" >/dev/null 2>&1
    assert_eq "schema.sql: sessions has NO transcript_path column" \
        "0" "$(sqlite3 "${fresh}/endless.db" \
            "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='transcript_path';")"
    assert_eq "schema.sql: transcript_offset KEPT (the live resume cursor)" \
        "1" "$(sqlite3 "${fresh}/endless.db" \
            "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='transcript_offset';")"

    # The Go accessors are gone, and the packages that referenced the column
    # still pass — including the parser that must keep working off the payload
    # path, which is the whole reason the column was safe to drop.
    assert_succeeds "monitor: schema guard + live transcript parser intact" \
        go test ./internal/monitor/ -count=1 \
            -run 'TestSessionsHasNoTranscriptPathColumn|TestParseTranscript'
    assert_succeeds "schema package tests pass" go test ./internal/schema/ -count=1

    if [[ "${FAIL_COUNT}" -ne "${before}" ]]; then
        printf '\n  %sFail-fast: the build/schema contracts are broken; skipping layers B-E.%s\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── layer B — reopen-context ranking after losing the signal ───────────────

run_reopen_layer() {
    section "B — reopen-context ranking (SUPERSEDED by E-1968)"
    note "E-1968 deleted the resolver this layer ranked; only the premise survives"

    # E-1968 retired `task spawn --reopen`, and with it the whole reopen-context
    # resolver (internal/events/reopen_context.go, its tests, and the
    # `session-query reopen-context` subcommand). Reopening now resumes the
    # prior session's actual transcript via `session goto --resume --revisit`,
    # so there is no "which prior ended session should we inherit" question left
    # to rank.
    #
    # The three TestReopenContext_* assertions that stood here were NOT left in
    # place: `go test -run` exits 0 when no test matches, so they would have
    # gone on reporting green while measuring nothing at all — worse than a
    # failure, which at least tells you something changed.
    report_pass "reopen-context ranking retired with \`spawn --reopen\` (E-1968)"

    # This one survives: it is about ended sessions generally, not the resolver.
    assert_succeeds "premise: E-1530 triggers make process unreachable on ended rows" \
        go test ./internal/events/ -count=1 \
            -run 'TestEndedSessionProcessIsAlwaysNull'
}

# ─── layer C — removal surface ──────────────────────────────────────────────

run_removal_layer() {
    section "C — removal surface: the command, the duplicate parser, the column"
    note "\`session reimport\` must be unroutable and no LIVE source may still"
    note "name the column (historical change files are excluded by design)"

    local out
    out=$(uv run endless session reimport 2>&1)
    assert_contains "\`endless session reimport\` is no longer a command" \
        "No such command 'reimport'" "${out}"

    out=$(uv run endless session --help 2>&1)
    if [[ "${out}" != *reimport* ]]; then
        report_pass "\`session --help\` no longer lists reimport"
    else
        report_fail "\`session --help\` no longer lists reimport" \
            "no 'reimport' in help" "${out}"
    fi

    # The duplicate Python transcript parser and the helpers only it used.
    local sym leftovers=""
    for sym in reimport_sessions _parse_transcript_py _find_jsonl \
               _project_id_from_path _extract_user_text \
               _extract_assistant_content _insert_message _set_summary_if_empty; do
        if grep -q "def ${sym}(" "${REPO_ROOT}/src/endless/session_cmd.py" 2>/dev/null; then
            leftovers="${leftovers} ${sym}"
        fi
    done
    assert_eq "duplicate Python transcript parser + its helpers deleted" \
        "" "${leftovers}"

    # Live-source guard, in two precise halves. Deliberately NOT a bare filename
    # grep: the removal is documented in prose at every site it touched, and
    # those comments must survive — so both halves strip comment lines (//, #,
    # --) before matching. internal/schema/changes/ is excluded wholesale:
    # e-1568's change file is an immutable historical artifact that rebuilds the
    # pre-E-1905 table shape and must keep naming the column.

    # Half 1 — OUR column name must not appear in any executable line. Two
    # exemptions, both things that are not our column:
    #   - pragma_table_info probes, which assert its ABSENCE
    #   - `json:"transcript_path"`, the struct tag naming CLAUDE's hook-payload
    #     wire field. Same spelling, different owner: we do not control it, and
    #     it is what feeds the live parser now that nothing is stored.
    local sql_hits
    sql_hits=$(cd "${REPO_ROOT}" && grep -rn "transcript_path" \
                 --include="*.go" --include="*.py" --include="*.sql" \
                 internal/ src/ 2>/dev/null \
               | grep -v '^internal/schema/changes/' \
               | awk '{
                     line = $0
                     sub(/^[^:]*:[0-9]+:/, "", line)
                     sub(/^[ \t]+/, "", line)
                     if (line ~ /^(\/\/|#|--)/)           next
                     if (line ~ /pragma_table_info/)      next
                     if (line ~ /json:"transcript_path/)  next
                     print
                 }' | tr '\n' ' ')
    assert_eq "no executable line in internal/ or src/ names the SQL column" \
        "" "${sql_hits}"

    # Half 2 — the two Go accessors that existed only to read/write it are gone.
    # (The hook payload's TranscriptPath FIELD is a different thing and must
    # stay: it is the live path that replaced the stored column, asserted below.)
    local accessor_hits
    accessor_hits=$(cd "${REPO_ROOT}" && grep -rn "SetTranscriptPath\|GetTranscriptPath" \
                      --include="*.go" internal/ 2>/dev/null \
                    | grep -v '^internal/schema/changes/' \
                    | awk '{
                          line = $0
                          sub(/^[^:]*:[0-9]+:/, "", line)
                          sub(/^[ \t]+/, "", line)
                          if (line ~ /^\/\//) next
                          print
                      }' | tr '\n' ' ')
    assert_eq "monitor.SetTranscriptPath / GetTranscriptPath deleted" \
        "" "${accessor_hits}"

    # The one deliberate survivor: the hook payload STRUCT field. The live
    # parser is fed payload.TranscriptPath on every event — that is the path
    # that replaced the stored column, so it must still be there.
    assert_eq "hook payload still carries TranscriptPath (feeds the live parser)" \
        "1" "$(cd "${REPO_ROOT}" && grep -c 'TranscriptPath string' internal/hookcmd/claude.go)"
    local parse_calls
    parse_calls=$(cd "${REPO_ROOT}" && grep -c 'monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)' \
                    internal/hookcmd/claude.go)
    assert_eq "all 4 live ParseTranscript calls still use the payload path" \
        "4" "${parse_calls}"
}

# ─── layer D — ledger rebuild safety ────────────────────────────────────────

run_ledger_layer() {
    section "D — ledger rebuild safety (READ-ONLY against the real db-ledger)"
    note "proves \`sessions\` is machine-local: no session entity and no"
    note "transcript_path FIELD KEY in any structured payload"

    local ledger="${REPO_ROOT}/.endless/db-ledger"
    if [[ ! -d "${ledger}" ]]; then
        report_fail "db-ledger present" "a .endless/db-ledger dir" "missing"
        return
    fi

    local probe
    probe=$(cat "${ledger}"/*.jsonl 2>/dev/null | python3 -c '
import sys, json
ents, fields, bad = set(), set(), 0
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        o = json.loads(line)
    except Exception:
        bad += 1
        continue
    ents.add((o.get("entity") or {}).get("type"))
    p = o.get("payload") or {}
    f = p.get("fields")
    if isinstance(f, dict):
        fields.update(f)
print("BAD=%d" % bad)
print("SESSION_ENTITY=%d" % sum(1 for e in ents if e and e.startswith("session")
                                and e != "session_status" and e != "session_tasks"))
print("TP_FIELD=%d" % (1 if "transcript_path" in fields else 0))
')
    assert_contains "every ledger line parses as JSON" "BAD=0" "${probe}"
    assert_contains "no bare \`session\` entity is journaled (machine-local)" \
        "SESSION_ENTITY=0" "${probe}"
    assert_contains "no payload.fields key named transcript_path" \
        "TP_FIELD=0" "${probe}"

    # The projector never had a sessions replay arm to begin with — the drop
    # cannot orphan one.
    assert_eq "projector has no sessions replay arm to orphan" \
        "0" "$(cd "${REPO_ROOT}" && grep -c 'INTO sessions\|UPDATE sessions' internal/events/projector.go)"
}

# ─── layer E — migration against a pre-E-1905 DB ────────────────────────────

# The pre-E-1905 sessions shape, built INLINE (same rationale as e-1568-verify:
# deterministic, no git archaeology). Only the sessions table is needed — the
# apply-change run opens monitor.DB() first, which applies schema.sql and
# creates every other table via CREATE TABLE IF NOT EXISTS, leaving this older
# sessions table untouched for the change file to alter.
#
# needs_recap / summary_seq are kept here on purpose even though E-1906 has
# since dropped them from the real schema: carrying extra columns the change
# file does not mention proves the ALTER is surgical — it must take
# transcript_path and nothing else.
OLD_SESSIONS_DDL="
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
INSERT INTO sessions (session_id, state, kind_id, transcript_offset,
                      transcript_path, summary, started_at)
    VALUES ('uuid-A', 'ended', 1, 4242, '/tmp/stale.jsonl', 'keep me',
            '2026-01-01T00:00:00');
"

run_migration_layer() {
    section "E — migration: apply the change file to a pre-E-1905 DB"
    note "throwaway --config-dir DB; the real ledger is never opened"

    local change="${REPO_ROOT}/${CHANGE_FILE}"
    if [[ ! -f "${change}" ]]; then
        report_fail "change file present" "${CHANGE_FILE}" "missing"
        return
    fi
    report_pass "change file present (${CHANGE_FILE##*/})"

    local mdir="${TMP}/migrate"
    mkdir -p "${mdir}"
    printf '%s' "${OLD_SESSIONS_DDL}" | sqlite3 "${mdir}/endless.db" >/dev/null

    assert_eq "PRE: old DB has transcript_path" \
        "1" "$(sqlite3 "${mdir}/endless.db" \
            "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='transcript_path';")"

    local applied
    applied=$("${EGO}" --config-dir "${mdir}" event apply-change "${change}" 2>&1)
    assert_contains "apply-change succeeds on the old DB" '"status":"applied"' "${applied}"

    assert_eq "POST: transcript_path dropped" \
        "0" "$(sqlite3 "${mdir}/endless.db" \
            "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='transcript_path';")"
    assert_eq "POST: existing row survives with transcript_offset + summary intact" \
        "uuid-A|4242|keep me" \
        "$(sqlite3 "${mdir}/endless.db" \
            "SELECT session_id||'|'||transcript_offset||'|'||summary FROM sessions;")"
    assert_eq "POST: E-1530 end-of-life triggers still present" \
        "2" "$(sqlite3 "${mdir}/endless.db" \
            "SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'sessions_null_process%';")"
    assert_eq "POST: foreign-key integrity intact" \
        "" "$(sqlite3 "${mdir}/endless.db" "PRAGMA foreign_key_check;")"

    local again
    again=$("${EGO}" --config-dir "${mdir}" event apply-change "${change}" 2>&1)
    assert_contains "re-apply is a recorded no-op (idempotent)" '"status":"skipped"' "${again}"

    # The Python-side migration must not resurrect what the change file just
    # dropped. `_migrate_v3` runs on EVERY Python connect and used to
    # `ALTER TABLE sessions ADD COLUMN transcript_path TEXT` whenever the column
    # was absent — which, post-drop, is always. Left in place it would undo the
    # change file on the very next `endless` invocation. Driven directly rather
    # than through the CLI: inside a self-dev worktree the CLI's --db gate
    # refuses an arbitrary DB, and the migration function is the actual thing
    # under test.
    uv run python -c "
import sqlite3, sys
from endless import db
conn = sqlite3.connect('${mdir}/endless.db')
db._migrate_v3(conn)
conn.commit()
conn.close()
" >/dev/null 2>&1
    assert_eq "POST: _migrate_v3 does NOT re-add the column (db.py guard)" \
        "0" "$(sqlite3 "${mdir}/endless.db" \
            "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='transcript_path';")"
    # `summary` and `transcript_offset` are the ALTERs _migrate_v3 still owns:
    # needs_recap / summary_seq went with E-1906, transcript_path with this task.
    assert_eq "POST: _migrate_v3 still maintains its OTHER sessions columns" \
        "1|1" \
        "$(sqlite3 "${mdir}/endless.db" \
            "SELECT (SELECT count(*) FROM pragma_table_info('sessions') WHERE name='transcript_offset')
                    ||'|'||
                    (SELECT count(*) FROM pragma_table_info('sessions') WHERE name='summary');")"
}

# ─── layer F — project-wide regression ──────────────────────────────────────

run_regression_layer() {
    section "F — project-wide regression"
    note "the full Go and Python suites plus the guide-drift gate"

    # EXCLUSION, stated rather than silent: internal/sandboxcmd is skipped.
    # E-1908 — TestDestroyForceOverridesLiveWriterCheck blocks in an exec wait
    # and never returns, so it burns the 10m package timeout and makes a plain
    # `go test ./...` unable to pass. It reproduces on unmodified main and is
    # unrelated to this task (the package never referenced transcript_path).
    # Run it yourself with `go test ./internal/sandboxcmd/ -timeout 1200s` if
    # you want it; it passes in ~450s on an idle machine.
    printf '  %s! excluding internal/sandboxcmd — hangs on main, tracked as E-1908%s\n' \
        "${DIM}" "${RESET}"
    # while-read rather than mapfile: macOS's /bin/bash is 3.2, which has no
    # mapfile, and this script should not silently need homebrew bash.
    local pkgs=() pkg
    while IFS= read -r pkg; do
        [[ -n "${pkg}" ]] && pkgs+=("${pkg}")
    done < <(go list ./... | grep -v '/internal/sandboxcmd$')
    if [[ "${#pkgs[@]}" -eq 0 ]]; then
        report_fail "go list ./... enumerates packages" "a non-empty package list" "empty"
    else
        assert_succeeds "go test ./... (minus the E-1908 package)" \
            go test "${pkgs[@]}"
    fi

    assert_succeeds "just test (full Python suite)" just test
    assert_succeeds "just guide-check (CLI removal left no dangling guide ref)" just guide-check
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    local tool
    for tool in just go uv sqlite3 python3; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2; exit 2; }
    done

    EGO="${REPO_ROOT}/bin/endless-go"
    TMP=$(mktemp -d) || exit 2
    trap 'rm -rf "${TMP}"' EXIT

    printf '%sE-1905 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${REPO_ROOT}"
    printf '  scratch:  %s (throwaway; real ledger untouched)\n' "${TMP}"

    if run_unit_layer; then
        run_reopen_layer
        run_removal_layer
        run_ledger_layer
        run_migration_layer
    fi
    run_regression_layer

    summary
}

main "$@"
