#!/usr/bin/env bash
#
# E-1696 verification script — the session-task relation vocabulary and its two
# correction verbs:
#
#   * `referenced` and `queued` join goal/surfaced/revisited, seeded in the DB
#     and matching the Go enum (the VerifyIntegrity startup check).
#   * relation capture is UPGRADE-ONLY, replacing E-1462's set-once rule: a
#     stronger later capture wins, a weaker one never demotes. This is what lets
#     the read-before-claim happy path classify a session's goal correctly.
#   * `session task add` promotes tasks to `queued`; `session task remove` drops
#     the association outright and clears any hide with it.
#   * `session status` tiers by relation: decided work (goal/queued) leads an
#     equal-action tie, `referenced` sinks below everything and renders dimmed.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1696-verify.sh
#
# It runs the unit layer first as a FAIL-FAST gate (the Go packages E-1696
# touched plus this task's Python suite); if any of that fails the E2E checks
# below are not worth running and the script exits immediately. It then seeds
# tasks via the Python CLI (`endless ... --db sandbox`), seeds a session and its
# session_tasks rows via sqlite3 (a bare shell has no live Claude session), and
# reads the view back through the worktree's candidate Go binary in its headless
# no-goal mode (`./bin/endless-go session-status --session <id>`).
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a missing prerequisite. Each run creates fresh task/session ids;
# the sandbox is not wiped between runs (pollution is bounded and inspectable
# via `uv run endless task list --db sandbox`).

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

# Glyphs the renderer uses for the relation column (internal/sessionstatuscmd).
QUEUED_GLYPH="⊕"
REFERENCED_GLYPH="·"

# relation_id values (internal/schema/schema.sql, mirroring the Go enum).
REL_CLAIMED=1   # renamed from `goal` by E-1967
REL_SURFACED=2
REL_REVISITED=3
REL_REFERENCED=4
REL_QUEUED=5

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
    local expected="$2"
    local actual="$3"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "${expected}"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "${actual}"
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

# ─── helpers ────────────────────────────────────────────────────────────────

# Wrap the Python CLI so every seed/mutation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Render the no-goal session-status view for an explicit session through the
# worktree's candidate Go binary, headless. NO_COLOR + a wide --cols keep the
# output ANSI-free and untruncated so the row/glyph greps are reliable.
go_session_status() {
    NO_COLOR=1 ./bin/endless-go session-status --cols 200 "$@"
}

# Create a task and emit just its E-NNN id on stdout (other output → stderr).
# Titles here are VERB-FIRST on purpose: `task add` runs the verb gate, which
# refuses a title that does not open with a registered action — a fixture named
# "E-1696 verify: ..." fails the gate, not the feature.
add_task_get_id() {
    local title="$1"
    shift
    local output
    output=$(endless task add "${title}" "$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# numeric id from an "E-NNN" token.
num_id() { printf '%s' "${1#E-}"; }

sq() { sqlite3 "${SANDBOX_DB}" "$@"; }

# Create a goal-less session in the sandbox's project and echo its integer id.
# Columns are the CURRENT sessions shape: E-1898 replaced `process` with a
# process_id FK, so the older verify scripts' INSERT no longer prepares.
seed_session() {
    sq "INSERT INTO sessions (session_id, project_id, state, kind_id, last_activity)
        VALUES ('e1696-verify-' || hex(randomblob(4)),
                (SELECT id FROM projects ORDER BY id LIMIT 1),
                'working', 1, '2026-08-21T00:00:00');
        SELECT last_insert_rowid();"
}

# Insert a session_tasks row with an explicit relation_id.
seed_session_task() {
    local session="$1" task="$2" relation="$3"
    sq "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
        VALUES (${session}, ${task}, ${relation}, '2026-08-21T00:00:00', '2026-08-21T00:00:00');"
}

# The relation slug stored for one (session, task) pair, or "" if no row.
relation_of() {
    local session="$1" task="$2"
    sq "SELECT COALESCE(r.slug, '')
          FROM session_tasks st
          LEFT JOIN session_task_relations r ON r.id = st.relation_id
         WHERE st.session_id = ${session} AND st.task_id = ${task};"
}

# The row line for task E-ID within captured session-status OUTPUT, or "".
row_for() {
    local id="$1" output="$2"
    printf '%s\n' "${output}" | grep -E "E-${id} " | head -1
}

# The 1-based position of task E-ID among the rendered task rows, or "" if absent.
row_position() {
    local id="$1" output="$2"
    printf '%s\n' "${output}" \
        | grep -nE 'E-[0-9]+ ' \
        | grep -E ":.*E-${id} " \
        | head -1 \
        | cut -d: -f1
}

# ─── assertions ──────────────────────────────────────────────────────────────

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "${want}" "${got:-<empty>}"
}

assert_row_present() {
    local desc="$1" id="$2" output="$3"
    if [[ -n "$(row_for "${id}" "${output}")" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "a row for E-${id}" "no E-${id} row in:"$'\n'"${output}"
}

assert_row_absent() {
    local desc="$1" id="$2" output="$3"
    if [[ -z "$(row_for "${id}" "${output}")" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "no row for E-${id}" "$(row_for "${id}" "${output}")"
}

assert_row_has_glyph() {
    local desc="$1" id="$2" glyph="$3" output="$4"
    local row
    row=$(row_for "${id}" "${output}")
    if [[ -n "${row}" ]] && [[ "${row}" == *"${glyph}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "E-${id} row contains '${glyph}'" "${row:-<row absent>}"
}

assert_contains() {
    local desc="$1" needle="$2" output="$3"
    if [[ "${output}" == *"${needle}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains '${needle}'" "${output}"
}

assert_cmd_fails() {
    local desc="$1"
    shift
    local output
    if output=$("$@" 2>&1); then
        report_fail "${desc}" "a non-zero exit" "exit 0, output: ${output}"
        return
    fi
    report_pass "${desc}"
}

# ─── scenario 1: the enum reaches the database ──────────────────────────────

test_relation_vocabulary() {
    section "Relation vocabulary: referenced + queued seeded and enum-aligned"

    assert_eq "session_task_relations holds 5 relations" \
        "5" "$(sq 'SELECT count(*) FROM session_task_relations;')"
    assert_eq "id 4 is referenced" \
        "referenced|Referenced" \
        "$(sq 'SELECT slug, label FROM session_task_relations WHERE id = 4;')"
    assert_eq "id 5 is queued" \
        "queued|Queued" \
        "$(sq 'SELECT slug, label FROM session_task_relations WHERE id = 5;')"

    # The binary refuses to open a DB whose relation table disagrees with its
    # enum (sessiontaskrelation.VerifyIntegrity, run from monitor.DB()). That any
    # Go read above succeeded is that check passing; assert it explicitly so a
    # drift failure reads as drift rather than as an unrelated broken command.
    local out
    if out=$(go_session_status --session 0 2>&1); then
        report_pass "Go binary opens the sandbox DB (enum/table integrity check passes)"
    else
        report_fail "Go binary opens the sandbox DB (enum/table integrity check passes)" \
            "a clean open" "${out}"
    fi
}

# ─── scenario 2: the upgrade-only ladder ────────────────────────────────────

test_upgrade_ladder() {
    section "Capture is upgrade-only (replaces E-1462's set-once rule)"

    local sess weak strong
    sess=$(seed_session)
    weak=$(num_id "$(add_task_get_id 'Verify e1696 read then queued')")
    strong=$(num_id "$(add_task_get_id 'Verify e1696 goal stays goal')")

    # referenced → queued: the read-before-act path, strengthened.
    seed_session_task "${sess}" "${weak}" "${REL_REFERENCED}"
    endless session task add "E-${weak}" --session-id "${sess}" >/dev/null 2>&1
    assert_eq "referenced upgrades to queued" \
        "queued" "$(relation_of "${sess}" "${weak}")"

    # claimed → queued is a DOWNGRADE and must be refused. Under E-1462's
    # set-once rule this direction was safe by accident; under the ladder it is
    # safe by rule, and this is the check that tells the two apart.
    seed_session_task "${sess}" "${strong}" "${REL_CLAIMED}"
    local out
    out=$(endless session task add "E-${strong}" --session-id "${sess}" 2>&1)
    assert_eq "a claim is never demoted by an add" \
        "claimed" "$(relation_of "${sess}" "${strong}")"
    assert_contains "the no-op names the claim" "already claimed" "${out}"

    # A pre-E-1462 NULL row must be healed by the next capture, not stranded.
    local historical
    historical=$(num_id "$(add_task_get_id 'Verify e1696 null historical row')")
    sq "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
        VALUES (${sess}, ${historical}, NULL, '2026-01-01T00:00:00', '2026-01-01T00:00:00');"
    endless session task add "E-${historical}" --session-id "${sess}" >/dev/null 2>&1
    assert_eq "a NULL historical row is filled in, not stranded" \
        "queued" "$(relation_of "${sess}" "${historical}")"
}

# ─── scenario 3: the two verbs ──────────────────────────────────────────────

test_add_and_remove() {
    section "session task add / remove"

    local sess untouched doomed
    sess=$(seed_session)
    untouched=$(num_id "$(add_task_get_id 'Verify e1696 queued into the session')")
    doomed=$(num_id "$(add_task_get_id 'Verify e1696 removed from the session')")

    # add enrolls a task nothing has happened to — the case no automatic capture
    # can reach, and the whole reason the verb exists.
    endless session task add "E-${untouched}" --session-id "${sess}" >/dev/null 2>&1
    assert_eq "add enrolls an untouched task as queued" \
        "queued" "$(relation_of "${sess}" "${untouched}")"
    assert_row_present "the queued task appears in session status" \
        "${untouched}" "$(go_session_status --session "${sess}")"

    # remove drops the association AND the hide on the same pair. The two tables
    # are unrelated, so a hide left behind would silently re-suppress the task if
    # it were ever re-captured.
    seed_session_task "${sess}" "${doomed}" "${REL_REVISITED}"
    sq "INSERT INTO session_hidden_tasks (session_id, task_id, hidden_at)
        VALUES (${sess}, ${doomed}, '2026-08-21T00:00:00');"
    endless session task remove "E-${doomed}" --session-id "${sess}" >/dev/null 2>&1
    assert_eq "remove drops the session_tasks row" \
        "" "$(relation_of "${sess}" "${doomed}")"
    assert_eq "remove clears the hide on the same pair" \
        "0" "$(sq "SELECT count(*) FROM session_hidden_tasks
                    WHERE session_id = ${sess} AND task_id = ${doomed};")"
    assert_row_absent "the removed task is gone from session status" \
        "${doomed}" "$(go_session_status --session "${sess}")"

    # Refusals and reported no-ops.
    local claimed_task
    claimed_task=$(num_id "$(add_task_get_id 'Verify e1696 the session claim')")
    seed_session_task "${sess}" "${claimed_task}" "${REL_CLAIMED}"
    assert_cmd_fails "remove refuses the session's own claimed task" \
        endless session task remove "E-${claimed_task}" --session-id "${sess}"
    assert_eq "the refused claimed row survives" \
        "claimed" "$(relation_of "${sess}" "${claimed_task}")"

    assert_cmd_fails "add rejects an id naming no task" \
        endless session task add "E-99999999" --session-id "${sess}"

    assert_contains "removing an untouched task is a REPORTED no-op" \
        "not in this session" \
        "$(endless session task remove 'E-99999999' --session-id "${sess}" 2>&1)"
}

# ─── scenario 4: display tiering ────────────────────────────────────────────

test_display_tiering() {
    section "session status tiers by relation"

    local sess queued referenced revisited out
    sess=$(seed_session)
    queued=$(num_id "$(add_task_get_id 'Verify e1696 tier queued')")
    referenced=$(num_id "$(add_task_get_id 'Verify e1696 tier referenced')")
    revisited=$(num_id "$(add_task_get_id 'Verify e1696 tier revisited')")

    # Seeded in the WORST order for the assertion: referenced first, so a missing
    # sort key shows up as the referenced row leading rather than trailing.
    seed_session_task "${sess}" "${referenced}" "${REL_REFERENCED}"
    seed_session_task "${sess}" "${revisited}" "${REL_REVISITED}"
    seed_session_task "${sess}" "${queued}" "${REL_QUEUED}"

    out=$(go_session_status --session "${sess}")

    assert_row_present "a referenced row is shown (not dropped)" "${referenced}" "${out}"
    assert_row_has_glyph "the queued row wears ${QUEUED_GLYPH}" \
        "${queued}" "${QUEUED_GLYPH}" "${out}"
    assert_row_has_glyph "the referenced row wears ${REFERENCED_GLYPH}" \
        "${referenced}" "${REFERENCED_GLYPH}" "${out}"
    assert_contains "the legend names ${QUEUED_GLYPH} queued" \
        "${QUEUED_GLYPH} queued" "${out}"
    assert_contains "the legend names ${REFERENCED_GLYPH} referenced" \
        "${REFERENCED_GLYPH} referenced" "${out}"

    # Flood control: referenced sinks below everything, and decided work leads.
    local pos_q pos_v pos_r
    pos_q=$(row_position "${queued}" "${out}")
    pos_v=$(row_position "${revisited}" "${out}")
    pos_r=$(row_position "${referenced}" "${out}")
    if [[ -n "${pos_q}" && -n "${pos_v}" && -n "${pos_r}" ]] \
       && (( pos_q < pos_v )) && (( pos_v < pos_r )); then
        report_pass "order is queued < revisited < referenced"
    else
        report_fail "order is queued < revisited < referenced" \
            "positions queued<revisited<referenced" \
            "queued=${pos_q:-?} revisited=${pos_v:-?} referenced=${pos_r:-?}"$'\n'"${out}"
    fi

    # --json is data, not a rendering, but it must agree on ORDER (it shares
    # sortRows) and carry the relation the table encodes as a glyph.
    local json
    json=$(go_session_status --session "${sess}" --json)
    assert_contains "--json carries the queued relation" '"relation": "queued"' "${json}"
    assert_contains "--json carries the referenced relation" \
        '"relation": "referenced"' "${json}"
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

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi
    if ! command -v sqlite3 >/dev/null 2>&1; then
        printf 'ERROR: sqlite3 not on PATH\n' >&2
        exit 2
    fi
    if [[ ! -x ./bin/endless-go ]]; then
        printf 'ERROR: ./bin/endless-go not built — run `just build` first\n' >&2
        exit 2
    fi

    # Deterministic sandbox DB path for this worktree (basename matches the
    # sandbox dir basename, E-1281). The Python seed and the Go read both resolve
    # this same DB (self-detect from cwd, E-1368); we also read it via sqlite3.
    SANDBOX_DB="${HOME}/.cache/endless/sandboxes/$(basename "${repo_root}")/endless/endless.db"

    # A `task list` first materializes the sandbox DB before any sqlite3 seed.
    endless task list >/dev/null 2>&1 || true
    if [[ ! -f "${SANDBOX_DB}" ]]; then
        printf 'ERROR: sandbox DB not found at %s\n' "${SANDBOX_DB}" >&2
        printf '       run `just dev-sandbox-init` from this worktree first\n' >&2
        exit 2
    fi

    # Bring a PRE-EXISTING sandbox up to the current schema before anything reads
    # it with sqlite3. Only a Go connect applies schema.sql (monitor.DB()); the
    # Python CLI reads SQLite directly and adds nothing. Without this warm-up the
    # first `sq` below sees whatever seed set the sandbox was created with — so a
    # sandbox predating this branch reports 3 relations and every check fails
    # against a schema the code never claimed to be running on.
    if ! go_session_status --session 0 >/dev/null 2>&1; then
        printf 'ERROR: the Go binary could not open the sandbox DB\n' >&2
        go_session_status --session 0 >&2
        exit 2
    fi

    printf '%sE-1696 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  go bin:  ./bin/endless-go\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    # Fail fast on the unit layer. Every E2E check below assumes the ladder, the
    # executors and the tier already work; if they do not, the E2E failures would
    # be downstream noise pointing at the wrong place.
    section "Unit tests (fail-fast gate)"
    if go test ./internal/sessiontaskrelation/ ./internal/events/ \
                ./internal/monitor/ ./internal/sessionstatuscmd/ \
                >/tmp/e1696-gotest.log 2>&1; then
        report_pass "go test (sessiontaskrelation, events, monitor, sessionstatuscmd)"
    else
        report_fail "go test (sessiontaskrelation, events, monitor, sessionstatuscmd)" \
            "all four packages pass" "$(cat /tmp/e1696-gotest.log)"
        summary
        exit 1
    fi
    if uv run pytest tests/test_session_task_membership.py -q \
                >/tmp/e1696-pytest.log 2>&1; then
        report_pass "pytest tests/test_session_task_membership.py"
    else
        report_fail "pytest tests/test_session_task_membership.py" \
            "the suite passes" "$(cat /tmp/e1696-pytest.log)"
        summary
        exit 1
    fi

    test_relation_vocabulary
    test_upgrade_ladder
    test_add_and_remove
    test_display_tiering

    summary
}

main "$@"
