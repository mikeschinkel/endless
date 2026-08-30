#!/usr/bin/env bash
#
# E-2081 verification — the session-navigation trail is gone.
#
# WHAT WAS REMOVED, and why it earned it:
#
#   E-1682 added a durable navigation trail: a global tmux focus-change hook
#   appended one row to `session_navigations` for every move between panes,
#   stated purpose "usability analysis of navigation, and recovering a session a
#   user lost track of". Neither materialised. What accumulated instead was
#   5,374 rows at roughly 110 a day, 98% of them via=manual, retention
#   explicitly deferred — feeding exactly one reader, `endless session trail`,
#   whose output is a list of the pane switches the user just performed and
#   therefore already knows about. It was surfaced by a session while designing
#   E-1681, not requested.
#
#   Going with it: the two tables (`session_navigations`, `nav_via_kinds`), the
#   `internal/navvia` enum and its fail-closed startup integrity check, the
#   `monitor.RecordNav` / `ListNavTrail` / `NavEdge` write and read paths, the
#   `endless-go tmux record-nav` recorder and its dispatch case, the two tmux
#   focus-change hooks `apply` installed, the one-shot `@endless_nav_via` marker
#   `session goto` stamped, and the `endless session trail` viewer.
#
#   `endless session back` STAYS. Its back-stack lives in tmux server options,
#   not in this table, and never depended on any of the above. Check 6 is that
#   guarantee, and it has teeth: the whole point of removing a table that sat
#   next to a working feature is not to take the working feature with it.
#
# WHAT IS DELIBERATELY LEFT BROKEN:
#
#   Three landed verify suites assert on what this task removed —
#   e-1682-verify.sh (the trail itself), e-2071-verify.sh (`session trail` as
#   one of its sixteen capped listings, and ListNavTrail's unlimited mode) and
#   e-2037-verify.sh (a comment in session_nav.go, in its surviving-vocabulary
#   list). All three are left untouched. `endless guide orchestration`: a verify
#   suite is a one-shot land-time gate, whether it still passes afterwards is
#   undefined, and retrofitting one rewrites the history it exists to record.
#   The coverage that had to survive moved into the durable suite instead —
#   tests/test_rowcap.py drops `session trail` from its listing table, which is
#   where that flag wiring is really protected.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-2081-verify.sh
#
# Requires `just build` first — checks 4/5 drive the CANDIDATE bin/endless-go,
# not the global install. (Setup builds it if it is missing.)
#
# What it checks:
#   0. FAIL-FAST fold-in regression: `go build`, `go vet`, the whole Go suite,
#      the Python suite, `just guide-check` and `just lifecycle-check`. A
#      failure here short-circuits the rest — the remaining checks would be
#      reporting on rubble.
#   1. No LIVE reference survives anywhere in the source tree.
#   2. The dead files are deleted, not merely unreferenced.
#   3. schema.sql declares neither table, and a DB built from it agrees — while
#      the tables and enum mirrors that merely sat NEXT to them are untouched.
#      The overreach guard.
#   4. The change file really migrates: a POPULATED DB carrying both tables and
#      rows loses exactly those two, keeps everything else, and re-applying is a
#      no-op.
#   5. Every CLI surface is gone, on both sides of the Python/Go seam.
#   6. The overreach guard with teeth: `session goto`/`session back` still work
#      end to end, and goto leaves no orphan marker behind.
#   7. `apply` installs no focus-change hook, and the cleanup that clears a
#      stale one only ever touches a hook that is ours.
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
    report_fail "${desc}" "exit 0" "exit ${rc} | $(tail -25 <<<"${out}")"
}

# assert_refused DESC CMD [ARGS...] — non-zero exit, and NOT a crash trace.
assert_refused() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_fail "${desc}" "non-zero exit" "exit 0 | $(head -5 <<<"${out}")"
        return
    fi
    if [[ "${out}" == *"panic:"* ]]; then
        report_fail "${desc}" "a refusal, not a panic" "$(head -5 <<<"${out}")"
        return
    fi
    report_pass "${desc}"
}

# ─── globals ────────────────────────────────────────────────────────────────

REPO_ROOT=""
GO=""
EN=""
WORK=""
CHANGE="internal/schema/changes/e-2081-drop-session-navigations.sql"

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    GO="${REPO_ROOT}/bin/endless-go"
    EN="${REPO_ROOT}/.venv/bin/endless"
    command -v sqlite3 >/dev/null 2>&1 || {
        printf 'ERROR: sqlite3 not on PATH\n' >&2; exit 2; }
    if [[ ! -x "${GO}" ]]; then
        printf 'building bin/endless-go …\n'
        ( cd "${REPO_ROOT}" && just build >/dev/null 2>&1 ) || {
            printf 'ERROR: just build failed\n' >&2; exit 2; }
    fi
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT
}

# ─── 0 — the fail-fast fold-in regression front ─────────────────────────────
#
# The project-wide regression, folded in rather than handed to the user as a
# separate checklist item.
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

# ─── 1 — no live reference survives ─────────────────────────────────────────
#
# Two categories of mention are legitimate and are filtered out rather than
# hunted down, following the pattern e-2074-verify.sh established:
#
#   a. Comments. A `#`/`--`/`//` line naming the trail to explain why something
#      is absent is the removal documenting itself. Deleting those comments
#      would make the change less legible, not more complete.
#   b. Whole files that must name what they remove or reproduce:
#      - .endless/     the ledger, plans and outcomes are an append-only record;
#      - e-1682-*      BOTH of E-1682's artifacts are kept deliberately, and
#                      both must name what they built:
#                        e-1682-session-navigations.sql — the change file that
#                          CREATED these tables. Change files are the record of
#                          how a populated database got its shape, and a DB old
#                          enough to still need it applies create-then-drop.
#                        e-1682-verify.sh — its landed verify suite. `endless
#                          guide orchestration` is explicit: do not edit a
#                          landed task's verify suite, and whether it still
#                          passes after that task lands is undefined. Check 2
#                          asserts both are still there;
#      - e-2074-*      a landed change file whose prose lists session_navigations
#                      among the tables referencing `sessions`. True at the
#                      moment it runs; editing it would be rewriting history;
#      - e-2081-*      this change's own DDL and this script's assertion list;
#      - tests/tasks/  every OTHER landed verify suite, for the reason in
#                      "WHAT IS DELIBERATELY LEFT BROKEN" above: e-2071-* and
#                      e-2037-* assert on what this task removed and are frozen
#                      records, not live code. Excluding the directory rather
#                      than naming two files is deliberate — the alternative is
#                      a sweep that pressures a later session into editing a
#                      landed suite to make it pass, which is the rule this
#                      exemption exists to protect. Nothing under tests/tasks/
#                      is imported, executed at build time, or read by the
#                      product; a stale mention there cannot reach a user.
#
# Two identifiers additionally have a PER-IDENTIFIER exemption, passed as extra
# arguments so the amnesty covers only the one word each file has to name — the
# shape e-2074-verify.sh used for sessions_removed_columns_test.go:
#   - `record-nav`, in nav_hook_retire.go and its test. That pair is the
#     transitional cleanup that clears a stale hook from a tmux server still
#     running from before this change; it cannot match a hook command without
#     quoting it. They live in files of their own so this stays narrow.
#   - `@endless_nav_via`, in tests/test_session_goto_back.py, whose whole job
#     here is asserting that goto no longer stamps the marker. Check 6 proves
#     that guard is real rather than a name in a comment.
#
# live_hits PATTERN [EXTRA-EXCLUDED-FILE ...]
live_hits() {
    local pat="$1"; shift
    local extra=() f
    for f in "$@"; do extra+=(--exclude="${f}"); done
    grep -rnI --exclude-dir=vendor --exclude-dir=.git --exclude-dir=.endless \
        --exclude='e-1682-*' --exclude='e-2074-*' --exclude='e-2081-*' \
        "${extra[@]}" \
        --exclude-dir=tasks \
        -e "${pat}" cmd internal src tests docs justfile 2>/dev/null \
    | grep -vE ':[0-9]+:[[:space:]]*(#|--|//)'
}

check_no_references() {
    section "1 — no live reference survives in the source tree"

    local ident hits
    for ident in session_navigations nav_via_kinds navvia NavVia \
                 RecordNav ListNavTrail NavEdge \
                 runRecordNav readAndClearNavVia navViaOption \
                 session_trail _nav_endpoint_label \
                 _set_nav_via_goto _clear_nav_via _NAV_VIA_OPTION; do
        hits=$(live_hits "${ident}")
        if [[ -z "${hits}" ]]; then
            report_pass "no live \`${ident}\` anywhere in the source tree"
        else
            report_fail "no live \`${ident}\` anywhere in the source tree" \
                "zero hits" "${hits}"
        fi
    done

    # The two with a named exemption. Swept separately so the exemption is
    # visible at the point it is granted rather than buried in a list.
    hits=$(live_hits record-nav nav_hook_retire.go nav_hook_retire_test.go)
    if [[ -z "${hits}" ]]; then
        report_pass "no live \`record-nav\` outside the transitional cleanup"
    else
        report_fail "no live \`record-nav\` outside the transitional cleanup" \
            "zero hits" "${hits}"
    fi
    hits=$(live_hits @endless_nav_via test_session_goto_back.py)
    if [[ -z "${hits}" ]]; then
        report_pass "no live \`@endless_nav_via\` outside the guard asserting its absence"
    else
        report_fail "no live \`@endless_nav_via\` outside the guard asserting its absence" \
            "zero hits" "${hits}"
    fi

    # And the exempted files must be what they claim. A file granted amnesty
    # that stopped being a guard would be the quiet way this sweep goes hollow.
    if grep -q 'assert "@endless_nav_via" not in ft.options' \
            tests/test_session_goto_back.py; then
        report_pass "the exempted goto test really asserts the marker's ABSENCE"
    else
        report_fail "the exempted goto test really asserts the marker's ABSENCE" \
            "a not-in assertion on @endless_nav_via" "absent"
    fi

    # The two hook names are tmux's, not ours — a user may bind them for their
    # own purposes and the removal must not claim the names. What must be gone
    # is any `set-hook` INSTALL of them, which is a narrower thing to sweep for.
    hits=$(live_hits 'set-hook", "-g", "client-session-changed')$(
           live_hits 'set-hook", "-g", "session-window-changed')
    if [[ -z "${hits}" ]]; then
        report_pass "nothing installs a focus-change hook any more"
    else
        report_fail "nothing installs a focus-change hook any more" "zero hits" "${hits}"
    fi

    # The comment filter must not become a blanket amnesty. A canary planted in
    # the tree and removed immediately proves it is still narrow: a Go usage
    # line — not a comment — must be caught.
    local canary="internal/monitor/e2081_canary_check.go"
    printf 'package monitor\n\nvar e2081Canary = "session_navigations"\n' > "${canary}"
    hits=$(live_hits session_navigations)
    rm -f "${canary}"
    assert_contains "a planted Go usage is still caught (the filter is narrow)" \
        "${hits}" "e2081_canary_check.go"
}

# ─── 2 — the dead files are deleted, not merely unreferenced ────────────────

check_files_deleted() {
    section "2 — the dead files are deleted"

    assert_absent "internal/navvia/ is gone" "internal/navvia"
    assert_absent "internal/monitor/session_nav.go is gone" \
        "internal/monitor/session_nav.go"
    assert_absent "internal/monitor/session_nav_test.go is gone" \
        "internal/monitor/session_nav_test.go"
    assert_absent "internal/tmuxcmd/record_nav.go is gone" \
        "internal/tmuxcmd/record_nav.go"
    assert_absent "tests/test_session_trail.py is gone" "tests/test_session_trail.py"

    # The two E-1682 artifacts that are deliberately NOT deleted. These are the
    # inverse assertions: a later reader must not "finish the job".
    #
    # The change file, because change files are the record of how a populated
    # database got its shape. The verify suite, because `endless guide
    # orchestration` says a landed suite records what was true when its task
    # landed and must not be edited — and deleting one is the strongest possible
    # edit. It will fail if anyone runs it now; the same guide says that is
    # undefined and meaningless after land.
    local keeper
    for keeper in "internal/schema/changes/e-1682-session-navigations.sql" \
                  "tests/tasks/e-1682-verify.sh"; do
        if [[ -f "${keeper}" ]]; then
            report_pass "${keeper} is KEPT (E-1682's landed record)"
        else
            report_fail "${keeper} is KEPT (E-1682's landed record)" \
                "on disk" "deleted"
        fi
    done
}

# ─── 3 — the schema shape, and the overreach guard ──────────────────────────

check_schema_shape() {
    section "3 — schema.sql drops two tables, and keeps the neighbors"

    assert_file_lacks "schema.sql declares no nav_via_kinds table" \
        "internal/schema/schema.sql" "CREATE TABLE IF NOT EXISTS nav_via_kinds"
    assert_file_lacks "schema.sql declares no session_navigations table" \
        "internal/schema/schema.sql" "CREATE TABLE IF NOT EXISTS session_navigations"
    assert_file_lacks "schema.sql declares no session_navigations index" \
        "internal/schema/schema.sql" "session_navigations_client"

    local db tables
    db="${WORK}/fresh.db"
    if ! sqlite3 "${db}" < internal/schema/schema.sql >/dev/null 2>&1; then
        report_fail "schema.sql applies cleanly to an empty DB" "exit 0" "sqlite3 failed"
        return
    fi
    report_pass "schema.sql applies cleanly to an empty DB"

    tables=$(sqlite3 "${db}" "SELECT ','||group_concat(name)||',' FROM sqlite_master;")
    assert_not_contains "a fresh DB has no session_navigations table" \
        "${tables}" ",session_navigations,"
    assert_not_contains "a fresh DB has no nav_via_kinds table" \
        "${tables}" ",nav_via_kinds,"
    assert_not_contains "a fresh DB has no session_navigations_client index" \
        "${tables}" ",session_navigations_client,"

    # The overreach guard. nav_via_kinds was one of several ED-1506 enum mirrors
    # and sat between two unrelated `session_*` tables. None of its neighbors,
    # and none of the other mirrors, may have been swept up.
    local keep
    for keep in sessions session_gates session_messages session_statuses \
                session_tasks session_hidden_tasks session_notices \
                session_task_relations gate_kinds process_kinds task_types \
                processes tasks task_deps; do
        assert_contains "a fresh DB KEEPS ${keep}" "${tables}" ",${keep},"
    done

    # The mirrors that stay must still be SEEDED — removing one enum's seed
    # block is the plausible way to break the others.
    assert_eq "gate_kinds is still seeded" "1" \
        "$(sqlite3 "${db}" "SELECT count(*) > 0 FROM gate_kinds;")"
    assert_eq "process_kinds is still seeded" "1" \
        "$(sqlite3 "${db}" "SELECT count(*) > 0 FROM process_kinds;")"
    assert_eq "session_task_relations is still seeded" "1" \
        "$(sqlite3 "${db}" "SELECT count(*) > 0 FROM session_task_relations;")"
}

# ─── 4 — the change file migrates a populated DB ────────────────────────────
#
# The change file is what runs against the real populated DB at land time, so
# prove it on a populated DB carrying the old tables rather than trusting the
# DDL. The nav shape is built inline, because schema.sql no longer declares it:
# no git archaeology, fully deterministic.
check_change_file_migrates() {
    section "4 — the change file drops both tables from a populated DB"

    if [[ ! -f "${CHANGE}" ]]; then
        report_fail "the change file exists" "${CHANGE}" "absent"
        return
    fi
    report_pass "the change file exists"

    local mdir="${WORK}/migrate"; mkdir -p "${mdir}"
    sqlite3 "${mdir}/endless.db" <<'SQL'
CREATE TABLE nav_via_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);
INSERT INTO nav_via_kinds (id, slug, label) VALUES (1,'manual','Manual'), (2,'goto','Goto');
CREATE TABLE session_navigations (
    id              INTEGER PRIMARY KEY,
    client          TEXT NOT NULL,
    project_id      INTEGER,
    from_session_id INTEGER,
    from_pane       TEXT,
    to_session_id   INTEGER,
    to_pane         TEXT NOT NULL,
    via_id          INTEGER NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (via_id) REFERENCES nav_via_kinds(id)
);
CREATE INDEX session_navigations_client ON session_navigations(client, id);
INSERT INTO session_navigations (id, client, to_pane, via_id, created_at)
    VALUES (1, '/dev/ttys001', '%1', 1, '2026-08-01T00:00:00'),
           (2, '/dev/ttys001', '%2', 2, '2026-08-01T00:01:00');
SQL

    local pre applied tables rows

    pre=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM sqlite_master WHERE name IN ('session_navigations','nav_via_kinds');")
    assert_eq "PRE: the old DB really carries both tables" "2" "${pre}"
    assert_eq "PRE: and the trail really has rows in it" "2" \
        "$(sqlite3 "${mdir}/endless.db" "SELECT count(*) FROM session_navigations;")"

    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_contains "apply-change succeeds on the populated old DB" \
        "${applied}" '"status":"applied"'

    tables=$(sqlite3 "${mdir}/endless.db" \
        "SELECT ','||group_concat(name)||',' FROM sqlite_master;")
    assert_not_contains "POST: session_navigations is dropped" \
        "${tables}" ",session_navigations,"
    assert_not_contains "POST: nav_via_kinds is dropped" "${tables}" ",nav_via_kinds,"
    assert_not_contains "POST: the index went with its table" \
        "${tables}" ",session_navigations_client,"

    # apply-change opens the DB through monitor.DB(), which applies schema.sql
    # first — so the run is also an end-to-end check that the CURRENT schema and
    # this change agree, and that the drop takes nothing else with it.
    assert_contains "POST: the rest of the schema is intact (sessions)" \
        "${tables}" ",sessions,"
    assert_contains "POST: the rest of the schema is intact (session_gates)" \
        "${tables}" ",session_gates,"
    assert_contains "POST: the other enum mirrors survive (gate_kinds)" \
        "${tables}" ",gate_kinds,"

    # Idempotence: `just land` may re-run a change after a partial failure.
    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_not_contains "re-applying is a no-op, not an error" \
        "${applied}" '"status":"applied"'

    # A DB that never carried the tables must also survive the change — that is
    # every fresh install, and every sandbox.
    local ndir="${WORK}/never"; mkdir -p "${ndir}"
    applied=$("${GO}" --config-dir "${ndir}" event apply-change "${CHANGE}" 2>&1)
    assert_contains "a DB that never had the tables applies it cleanly" \
        "${applied}" '"status":"applied"'
    rows=$(sqlite3 "${ndir}/endless.db" \
        "SELECT count(*) FROM sqlite_master WHERE name IN ('session_navigations','nav_via_kinds');")
    assert_eq "…and still has neither table afterwards" "0" "${rows}"
}

# ─── 5 — every CLI surface is gone ──────────────────────────────────────────

check_cli_surfaces_gone() {
    section "5 — every CLI surface is gone (Python + Go)"

    local out

    out=$(cd "${REPO_ROOT}" && "${EN}" session --help 2>&1)
    assert_not_contains "\`endless session --help\` no longer lists \`trail\`" \
        "${out}" "trail"
    # The neighbors it was listed between must still be there — a --help sweep
    # that lost `back` would pass the assertion above for the wrong reason.
    assert_contains "\`endless session --help\` still lists \`back\`" "${out}" "back"
    assert_contains "\`endless session --help\` still lists \`goto\`" "${out}" "goto"

    assert_refused "\`endless session trail\` no longer exists" \
        env -C "${REPO_ROOT}" "${EN}" session trail

    out=$("${GO}" session-query 2>&1)
    assert_not_contains "session-query usage drops \`trail\`" "${out}" "trail"
    assert_contains "session-query still lists its real subcommands" \
        "${out}" "list-live"
    assert_refused "\`endless-go session-query trail\` is refused" \
        "${GO}" session-query trail

    out=$("${GO}" tmux --help 2>&1)
    assert_not_contains "\`endless-go tmux --help\` drops \`record-nav\`" \
        "${out}" "record-nav"
    assert_contains "\`endless-go tmux --help\` still lists \`apply\`" "${out}" "apply"
    assert_refused "\`endless-go tmux record-nav\` is refused" \
        "${GO}" tmux record-nav --client=x --pane=%1

    # The guide must not advertise what the CLI no longer has.
    assert_file_lacks "appendix A drops \`endless session trail\`" \
        "docs/guide/appendix-a.md" "endless session trail"
    assert_contains "appendix A still documents \`endless session back\`" \
        "$(cat docs/guide/appendix-a.md)" "endless session back"
}

# ─── 6 — the overreach guard with teeth ─────────────────────────────────────
#
# `session goto` and `session back` sat directly on top of the removed trail:
# goto stamped the @endless_nav_via marker the recorder read, and the module
# they live in described the trail as its sibling. The back-stack itself is a
# tmux server option and never touched the table — so the whole navigation
# surface must still work, minus the marker.
check_navigation_survives() {
    section "6 — session goto / session back survive the table they sat next to"

    assert_succeeds "the goto + back suite passes end to end" \
        uv run pytest tests/test_session_goto_back.py -q

    # Named explicitly rather than left inside the suite above, because THIS is
    # the assertion that says goto stopped stamping a marker nothing reads.
    assert_succeeds "goto leaves no orphan @endless_nav_via marker" \
        uv run pytest tests/test_session_goto_back.py -q \
            -k test_goto_sets_no_nav_marker

    # The back-stack's storage is the reason back is unaffected. If it ever
    # moved into the DB, this removal would have taken it out.
    if grep -q 'def _backstack_key' src/endless/session_cmd.py; then
        report_pass "the back-stack still lives in a tmux option, not the DB"
    else
        report_fail "the back-stack still lives in a tmux option, not the DB" \
            "_backstack_key in session_cmd.py" "absent"
    fi
}

# ─── 7 — the tmux hooks, and the cleanup for servers already running ────────
#
# `tmux init` gates on @server_uuid, so it runs once per server LIFETIME: a
# server started before this change keeps firing the old hook — invoking a
# subcommand that no longer exists — until it restarts. `apply` clears it, and
# must only ever clear a hook that is ours: these are global tmux hook names a
# user may have bound for their own purposes.
check_hooks() {
    section "7 — apply installs no focus hook, and retires a stale one safely"

    assert_succeeds "apply installs neither focus-change hook" \
        go test ./internal/tmuxcmd/ -count=1 \
            -run 'TestBuildApplySteps_InstallsNoFocusHooks'
    assert_succeeds "the stale-hook cleanup only matches OUR recorder" \
        go test ./internal/tmuxcmd/ -count=1 -run 'TestIsNavRecorderHook'

    if grep -q 'func retireNavHooks' internal/tmuxcmd/nav_hook_retire.go; then
        report_pass "the transitional cleanup exists"
    else
        report_fail "the transitional cleanup exists" \
            "retireNavHooks in nav_hook_retire.go" "absent"
    fi
    # It has to be CALLED, not merely defined.
    if grep -q '^	retireNavHooks()' internal/tmuxcmd/apply.go; then
        report_pass "runApply actually calls it"
    else
        report_fail "runApply actually calls it" \
            "a retireNavHooks() call in runApply" "absent"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-2081 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"
    printf '  binary: %s\n' "${GO}"

    check_regression_front
    # Fail fast: if the build or the suites are broken, the rest of this script
    # is reporting on rubble.
    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sregression front failed — skipping the E-2081 checks%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        return 1
    fi

    check_no_references
    check_files_deleted
    check_schema_shape
    check_change_file_migrates
    check_cli_surfaces_gone
    check_navigation_survives
    check_hooks

    summary
}

main "$@"
