#!/usr/bin/env bash
#
# E-1866 verification — session provenance on `endless task show`.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1866-verify.sh
#
# What E-1866 added:
#   - the `Created:` line names the session that filed the task, as
#     `by ES-NNN (E-NNN)` — the surfacing session and the task it is active on,
#   - a `Touched by:` block, the session-side peer of `This task:`, listing every
#     session that touched the task most-recent-touch-first, and
#   - `session goto` accepts the ES-NNNN form that block prints (E-1261).
#
# Stage 1 (fail-fast): the pytest suite covering rendering, ordering, the
# --json/--llm shapes, and goto's ES- resolution against an isolated DB. If it
# fails the script stops before the slower end-to-end stage.
#
# Stage 2: a real end-to-end run — seed a task, two sessions, three touches and
# one relation into this worktree's self-dev sandbox, render through the
# worktree's own CLI source, and assert the actual bytes. Cleans up its probe
# rows whether or not the assertions passed.
#
# Exit 0 on all-passed, 1 on any failure.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── locate the worktree root ─────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${WT_ROOT}" || { echo "cannot cd to worktree root ${WT_ROOT}"; exit 1; }

# ─── output helpers ───────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
report_skip() { printf '  %s∼%s %s %s(%s)%s\n' "${DIM}" "${RESET}" "$1" "${DIM}" "$2" "${RESET}"; }

# assert_contains <name> <needle> <haystack>
assert_contains() {
    if printf '%s' "$3" | grep -qF -- "$2"; then
        report_pass "$1"
    else
        report_fail "$1" "output contains: $2" "$(printf '%s' "$3" | head -20)"
    fi
}

# assert_absent <name> <needle> <haystack>
assert_absent() {
    if printf '%s' "$3" | grep -qF -- "$2"; then
        report_fail "$1" "output does NOT contain: $2" "$(printf '%s' "$3" | head -20)"
    else
        report_pass "$1"
    fi
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# ─── Stage 1: pytest (fail-fast) ──────────────────────────────────────────────

section "Stage 1 — pytest (fail-fast)"

if uv run pytest tests/test_task_show_sessions.py -q >/tmp/e1866-pytest.log 2>&1; then
    report_pass "tests/test_task_show_sessions.py ($(grep -oE '[0-9]+ passed' /tmp/e1866-pytest.log | head -1))"
else
    report_fail "tests/test_task_show_sessions.py" "pytest PASS" "$(tail -25 /tmp/e1866-pytest.log)"
fi

# The Created:/Touched by: blocks render inside the shared task-detail path, so
# a regression there would surface as a break in the neighboring show tests.
if uv run pytest tests/test_relations.py tests/test_analysis_show.py \
        tests/test_session_goto_back.py -q >/tmp/e1866-neighbors.log 2>&1; then
    report_pass "neighboring show/relations/goto suites still pass"
else
    report_fail "neighboring show/relations/goto suites" "pytest PASS" \
        "$(tail -25 /tmp/e1866-neighbors.log)"
fi

if [[ "${FAIL_COUNT}" -ne 0 ]]; then
    printf '\n%sfail-fast: unit tests failed; skipping end-to-end stage%s\n' "${RED}" "${RESET}"
    summary; exit 1
fi

# ─── Stage 2: real end-to-end through the worktree's CLI ──────────────────────

section "Stage 2 — end-to-end through the worktree CLI + self-dev sandbox"

SB="$(uv run endless db path --db sandbox 2>/dev/null | tail -1)"
if [[ -z "${SB}" || ! -f "${SB}" ]]; then
    report_skip "end-to-end probe" "no self-dev sandbox DB (run: just dev-sandbox-init)"
    summary; exit $?
fi

# Probe ids sit in a high, reserved band so they can never collide with real
# sandbox rows a concurrent self-dev session is writing, and so cleanup can
# delete by range without touching anything else.
LO=900001; HI=900099
TARGET=900001; CREATOR_TASK=900002; DOWNSTREAM=900003
S_CREATOR=900010; S_REVISITOR=900011; S_GONE=900012

cleanup_probe() {
    sqlite3 "${SB}" <<SQL >/dev/null 2>&1
DELETE FROM task_deps    WHERE source_id BETWEEN ${LO} AND ${HI} OR target_id BETWEEN ${LO} AND ${HI};
DELETE FROM session_tasks WHERE task_id  BETWEEN ${LO} AND ${HI};
DELETE FROM sessions     WHERE id        BETWEEN ${LO} AND ${HI};
DELETE FROM tasks        WHERE id        BETWEEN ${LO} AND ${HI};
SQL
}
trap cleanup_probe EXIT
cleanup_probe  # clear residue from an interrupted earlier run

PROJ="$(sqlite3 "${SB}" "SELECT id FROM projects ORDER BY id LIMIT 1")"
if [[ -z "${PROJ}" ]]; then
    report_skip "end-to-end probe" "sandbox has no project row"
    summary; exit $?
fi

# Three touches, deliberately out of id order so the recency sort is observable:
# the revisitor touched last, the creator second, the deleted session first.
sqlite3 "${SB}" <<SQL >/dev/null
INSERT INTO tasks (id, project_id, title, status, type_id, phase, created_at, updated_at) VALUES
  (${TARGET},        ${PROJ}, 'E-1866 probe target',    'ready', 1, 'now', '2026-08-04T04:00:00', '2026-08-04T04:00:00'),
  (${CREATOR_TASK},  ${PROJ}, 'E-1866 probe creator',   'ready', 1, 'now', '2026-08-04T04:00:00', '2026-08-04T04:00:00'),
  (${DOWNSTREAM},    ${PROJ}, 'E-1866 probe dependent', 'ready', 1, 'now', '2026-08-04T04:00:00', '2026-08-04T04:00:00');
INSERT INTO sessions (id, session_id, project_id, state, active_task_id, started_at) VALUES
  (${S_CREATOR},   'e1866-probe-${S_CREATOR}',   ${PROJ}, 'idle',    ${CREATOR_TASK}, '2026-08-04T04:00:00'),
  (${S_REVISITOR}, 'e1866-probe-${S_REVISITOR}', ${PROJ}, 'working', NULL,            '2026-08-04T04:00:00');
-- relation ids mirror session_task_relations: 1=claimed 2=surfaced 3=revisited.
-- The ${S_GONE} row has NO sessions row on purpose: session_tasks carries no FK
-- so a touch outlives its session, and NULL relation_id is a pre-E-1462 row.
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (${S_CREATOR},   ${TARGET}, 2,    '2026-08-04T04:00:00', '2026-08-04T04:00:00'),
  (${S_REVISITOR}, ${TARGET}, 3,    '2026-08-04T05:00:00', '2026-08-04T05:00:00'),
  (${S_GONE},      ${TARGET}, NULL, '2026-08-04T03:00:00', '2026-08-04T03:00:00');
INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
  VALUES ('task', ${TARGET}, 'task', ${DOWNSTREAM}, 'blocks');
SQL

SHOW="$(uv run endless task show "E-${TARGET}" --db sandbox --no-color 2>&1)"

assert_contains "Created: names the surfacing session and its active task" \
    "by ES-${S_CREATOR} (E-${CREATOR_TASK})" "${SHOW}"
assert_contains "Touched by: block is present" "Touched by:" "${SHOW}"
assert_contains "surfacing session row carries its active task and state" \
    "- Surfaced:   ES-${S_CREATOR} (E-${CREATOR_TASK}) [idle]" "${SHOW}"
assert_contains "revisiting session with no active task renders bare" \
    "- Revisited:  ES-${S_REVISITOR} [working]" "${SHOW}"
assert_contains "a touch whose session row is gone still renders" \
    "- Touched:    ES-${S_GONE} [gone]" "${SHOW}"
assert_absent "sessions never render with a task's bare E- prefix" \
    " E-${S_CREATOR} " "${SHOW}"

# Ordering: most recent touch first — revisitor (05:00), creator (04:00), gone (03:00).
SEQ="$(printf '%s\n' "${SHOW}" | sed -n '/^Touched by:/,$p' | grep -oE 'ES-[0-9]+' | tr '\n' ' ')"
if [[ "${SEQ}" == "ES-${S_REVISITOR} ES-${S_CREATOR} ES-${S_GONE} " ]]; then
    report_pass "rows are ordered most-recent-touch-first"
else
    report_fail "rows are ordered most-recent-touch-first" \
        "ES-${S_REVISITOR} ES-${S_CREATOR} ES-${S_GONE}" "${SEQ}"
fi

# The two bullet blocks are siblings, so their ids share one column. The
# relation phrase itself is not asserted — a fresh sandbox's one-shot V5
# migration may swap a hand-seeded task_deps row's direction.
LINK_ROW="$(printf '%s\n' "${SHOW}" | sed -n '/^This task:/,/^Touched by:/p' | grep -m1 '^- ')"
TOUCH_ROW="$(printf '%s\n' "${SHOW}" | sed -n '/^Touched by:/,$p' | grep -m1 '^- ')"
LINK_COL="$(awk -v s="${LINK_ROW}" 'BEGIN{print index(s, "E-")}')"
TOUCH_COL="$(awk -v s="${TOUCH_ROW}" 'BEGIN{print index(s, "ES-")}')"
if [[ -n "${LINK_ROW}" && "${LINK_COL}" == "${TOUCH_COL}" && "${LINK_COL}" != "0" ]]; then
    report_pass "This task: and Touched by: share one id column (col ${LINK_COL})"
else
    report_fail "This task: and Touched by: share one id column" \
        "equal, non-zero id columns" "link=${LINK_COL} (${LINK_ROW}) touch=${TOUCH_COL} (${TOUCH_ROW})"
fi

if [[ "$(printf '%s\n' "${SHOW}" | grep -n '^This task:\|^Touched by:' | cut -d: -f1 | tr '\n' ' ')" =~ ^([0-9]+)\ ([0-9]+)\ $ ]]; then
    if (( BASH_REMATCH[1] < BASH_REMATCH[2] )); then
        report_pass "Touched by: follows This task:"
    else
        report_fail "Touched by: follows This task:" "This task: first" "reversed"
    fi
else
    report_fail "Touched by: follows This task:" "both headings present" "${SHOW}"
fi

# --json and --llm must report the same facts as the human block.
JSON="$(uv run endless task show "E-${TARGET}" --db sandbox --json 2>&1)"
JSON_CHECK="$(printf '%s' "${JSON}" | python3 -c '
import json, sys
d = json.load(sys.stdin)
c = d.get("created_by") or {}
t = d.get("touched_by") or []
print("creator=%s active=%s rel=%s" % (c.get("session"), c.get("active_task"), c.get("relation")))
print("order=%s" % ",".join(x["session"] for x in t))
print("gone_state=%s gone_rel=%s" % (t[-1].get("state"), t[-1].get("relation")))
' 2>&1)"
assert_contains "--json created_by names the surfacing session" \
    "creator=ES-${S_CREATOR} active=E-${CREATOR_TASK} rel=surfaced" "${JSON_CHECK}"
assert_contains "--json touched_by keeps the recency order" \
    "order=ES-${S_REVISITOR},ES-${S_CREATOR},ES-${S_GONE}" "${JSON_CHECK}"
assert_contains "--json reports a gone session and a null relation" \
    "gone_state=gone gone_rel=None" "${JSON_CHECK}"

LLM="$(uv run endless task show "E-${TARGET}" --db sandbox --llm 2>&1)"
assert_contains "--llm reports created_by" \
    "created_by=ES-${S_CREATOR} (E-${CREATOR_TASK})" "${LLM}"
assert_contains "--llm reports touched_by in relation-first order" \
    "touched_by=revisited ES-${S_REVISITOR} [working],surfaced ES-${S_CREATOR} (E-${CREATOR_TASK}) [idle],touched ES-${S_GONE} [gone]" \
    "${LLM}"

# An untouched task must render exactly as it did before E-1866.
UNTOUCHED="$(uv run endless task show "E-${DOWNSTREAM}" --db sandbox --no-color 2>&1)"
assert_absent "an untouched task gains no creator on Created:" " by ES-" "${UNTOUCHED}"
assert_absent "an untouched task gains no Touched by: block" "Touched by:" "${UNTOUCHED}"

# `session goto` must accept the ES-NNNN form the block prints. Focusing a pane
# needs a live tmux client, so assert on the refusal path instead — which is
# still discriminating: the ref resolves to the seeded session BY ID, which only
# the ES- branch can do. Without it the ref falls through to the UUID-prefix
# matcher, misses (the probe's uuid is `e1866-probe-NNN`), and the error quotes
# 'ES-NNNN' verbatim instead of naming the session.
GOTO="$(TMUX=fake uv run endless session goto "ES-${S_CREATOR}" --db sandbox 2>&1)"
assert_contains "session goto resolves ES-NNNN to that session by id" \
    "Session ${S_CREATOR} has no reachable" "${GOTO}"
assert_absent "session goto does not fall through to the UUID-prefix matcher" \
    "'ES-${S_CREATOR}'" "${GOTO}"
# A task ref must still resolve as a task, not get swallowed by the new branch.
GOTO_TASK="$(TMUX=fake uv run endless session goto "E-${TARGET}" --db sandbox 2>&1)"
assert_contains "session goto still reads E-NNNN as a task" \
    "E-${TARGET}" "${GOTO_TASK}"

summary
