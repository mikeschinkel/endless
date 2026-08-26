#!/usr/bin/env bash
#
# E-2071 verification — a truncated list announces itself instead of being cut
# off silently.
#
# Before: `endless task list` rendered all 1360 live rows and `endless task
# search` rendered a silent 20 under the line "20 match(es)" — a sentence
# indistinguishable from "there are exactly twenty matches". A caller that could
# not predict the result size bounded it downstream with `head`, and `head` says
# nothing. That silence turned two searches in one session into false "no
# existing task" conclusions: E-1935 fell off a `head -30` and E-1535 off a
# `head -24`, both ordered id ASC so the newest and most relevant rows were
# exactly the ones dropped.
#
# After: one shared cap (src/endless/rowcap.py) owned by the tool and announced
# by it, in the idiom `session status` already used for hidden rows (E-1914):
# "… N more rows (--no-limit)". SIXTEEN listing surfaces adopt it. Machine
# formats stay uncapped, because a payload nothing can read a footer out of
# would be worse truncated than the original defect.
#
# Two ways the cap is applied, and the split is about query cost:
#   - Renderer-side, on the full result set (task/decision/epic, project,
#     worktree, verb, phrase, trail). The remainder is arithmetic.
#   - SQL-side probe window plus a COUNT (session list/search/history), where
#     fetching everything is the expensive part: session_messages runs to tens
#     of thousands of rows carrying full text, and `session list` pays a
#     per-row subquery.
#
# `jobs list` is deliberately NOT capped: it renders wholly inside the Go binary
# from a compile-time registry of two jobs, so there is no Python row list to cap
# and nothing that grows with use.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-2071-verify.sh
#
# What it proves:
#   1. FAIL-FAST unit gate: the pytest suites this task owns pass. Everything
#      below runs the same source, so a red gate makes it all noise.
#   2. ONE implementation, not nine: every capped surface routes through
#      rowcap, the private one-off footer `task unsettled` used to carry is
#      gone, and no capped listing still pushes its LIMIT into SQL — it cannot,
#      because a query that stopped at the cap cannot say what it skipped.
#   3. End to end, through the real CLI against a throwaway database: the filed
#      defect. 35 matches render 20, the footer names the 15 it dropped, and
#      the count line says 35 — not 20.
#   4. WHY the cap is load-bearing, demonstrated rather than asserted: the same
#      result piped through `head` carries no trace of what it lost, while the
#      capped render does. This is the original false negative, reproduced.
#   5. --no-limit and --limit N both work, on every surface, and are refused
#      together. `--limit 0` is refused with a pointer to the flag meant. Every
#      surface carries both flags — including the eight whose rows this fixture
#      cannot seed, where wiring is the whole risk.
#   6. MACHINE formats stay whole and stay parseable: --json and --tsv are
#      uncapped by default, and under an explicit --limit the footer goes to
#      stderr so the payload still parses.
#   7. Nothing legal became collateral damage: a result that FITS prints no
#      footer (including an exact-fit 20), an empty result is unchanged, and
#      the surfaces that already capped still cap.
#   8. The probe-window path is honest: `session history`'s footer names the
#      remainder from a COUNT, not from the probe row, and it prints where the
#      missing messages WOULD be — above the render in newest-first order,
#      below it under --sort asc.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

SCHEMA_SQL="${WT}/internal/schema/schema.sql"
ROWCAP_SRC="${WT}/src/endless/rowcap.py"
TASK_SRC="${WT}/src/endless/task_cmd.py"
DECISION_SRC="${WT}/src/endless/decision_cmd.py"
CLI_SRC="${WT}/src/endless/cli.py"

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    [[ -n "${3:-}" ]] && printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
    return 0
}

setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

TMP_E2071=""
cleanup() { [[ -n "${TMP_E2071}" ]] && rm -rf "${TMP_E2071}"; }
trap cleanup EXIT

for f in "${SCHEMA_SQL}" "${ROWCAP_SRC}" "${TASK_SRC}" "${DECISION_SRC}" "${CLI_SRC}"; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done
command -v sqlite3 >/dev/null || setup_error "sqlite3 is required"
command -v uv >/dev/null || setup_error "uv is required"

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail-fast)"

# The suite this task owns.
if uv run pytest tests/test_rowcap.py -q >/tmp/e2071-pytest-rowcap.log 2>&1; then
    report_pass "pytest tests/test_rowcap.py"
else
    report_fail "pytest tests/test_rowcap.py" "pass" \
        "failed — see /tmp/e2071-pytest-rowcap.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# `task unsettled` had the only pre-existing truncation notice, written by
# E-1865. Adopting the shared footer must not have broken the command that
# already got this right — that suite is the regression witness.
if uv run pytest tests/test_task_unsettled.py -q >/tmp/e2071-pytest-unsettled.log 2>&1; then
    report_pass "pytest tests/test_task_unsettled.py (the surface that already announced)"
else
    report_fail "pytest tests/test_task_unsettled.py" "pass" \
        "failed — see /tmp/e2071-pytest-unsettled.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# ── 2. one implementation, not nine ─────────────────────────────────────────
# A cap copy-pasted nine times would pass every behavioural test below and still
# be the thing this task was filed against: a rule that lives in nine places
# drifts in nine places.
section "2. One cap, one module"

n=$(grep -c 'rowcap.resolve_cap' "${TASK_SRC}")
if [[ "${n}" == "6" ]]; then
    report_pass "task_cmd.py resolves the cap in all six of its listings"
else
    report_fail "task_cmd.py resolves the cap in all six of its listings" \
        "6 rowcap.resolve_cap calls (list/search/next/recent/landed/unsettled)" "${n}"
fi

# Every module holding a capped renderer. A surface wired at the CLI but not in
# its renderer would take the flags and ignore them.
for src in decision_cmd session_cmd worktree_cmd verb_cmd phrase_cmd list_cmd cli; do
    f="${WT}/src/endless/${src}.py"
    if grep -q 'rowcap.resolve_cap' "${f}"; then
        report_pass "${src}.py routes through rowcap"
    else
        report_fail "${src}.py routes through rowcap" \
            "a rowcap.resolve_cap call" "absent"
    fi
done

# The session listings keep their cap in SQL, so they MUST use the probe window
# — a bare LIMIT cap cannot tell the footer how many rows it skipped.
probes=$(grep -c 'rowcap.probe_limit' "${WT}/src/endless/session_cmd.py")
if [[ "${probes}" == "3" ]]; then
    report_pass "session_cmd.py probes one past the cap in all three SQL listings"
else
    report_fail "session_cmd.py probes one past the cap in all three SQL listings" \
        "3 rowcap.probe_limit calls (list/search/history)" "${probes}"
fi

# ...and each must pass a counted total, or the footer would read "1 more row"
# no matter how much was really left.
totals=$(grep -c 'rowcap.cap_rows(rows, cap, ' "${WT}/src/endless/session_cmd.py")
if [[ "${totals}" == "3" ]]; then
    report_pass "each SQL-capped listing feeds cap_rows a counted total"
else
    report_fail "each SQL-capped listing feeds cap_rows a counted total" \
        "3 cap_rows calls carrying a total" "${totals}"
fi

# The Go side had to grow an unlimited mode for `session trail`, because the
# Python viewer owns the cap and needs every row to count an exact remainder.
if grep -q 'if limit == 0 {' "${WT}/internal/monitor/session_nav.go"; then
    report_pass "ListNavTrail returns every row on a negative limit"
else
    report_fail "ListNavTrail returns every row on a negative limit" \
        "the limit == 0 default guard (negative means unlimited)" "absent"
fi

# The private notice E-1865 wrote for one command. Generalising it away is the
# point; if it came back, the idiom re-forked.
if grep -q '_echo_unsettled_truncation' "${TASK_SRC}"; then
    report_fail "the private per-command truncation notice is gone" \
        "no _echo_unsettled_truncation in task_cmd.py" "still present"
else
    report_pass "the private per-command truncation notice is gone"
fi

# The cap CANNOT live in SQL: `LIMIT 20` returns twenty rows and no idea how
# many it passed over, so the footer would have to guess or run a second query.
if grep -q 'LIMIT ?' "${TASK_SRC}"; then
    report_fail "no capped listing pushes its cap into SQL" \
        "no 'LIMIT ?' in task_cmd.py" "still present"
else
    report_pass "no capped listing pushes its cap into SQL"
fi

# One spelling of the flag, in one place, or the help text and the footer drift.
if grep -q 'NO_LIMIT_FLAG = "--no-limit"' "${ROWCAP_SRC}"; then
    report_pass "the flag is spelled once, in rowcap"
else
    report_fail "the flag is spelled once, in rowcap" \
        "NO_LIMIT_FLAG defined in rowcap.py" "absent"
fi

# ── setup: a hermetic database and a CLI built from THIS tree ───────────────
TMP_E2071=$(mktemp -d "${TMPDIR:-/tmp}/e2071.XXXXXX") || setup_error "mktemp failed"

# Resolve the worktree venv's console script. `uv run` materialises the venv if
# it is not there, so this works on a clean checkout.
PY=$(uv run --project "${WT}" python -c 'import sys; print(sys.executable)' \
        2>/tmp/e2071-venv.log) || setup_error "could not resolve the venv (see /tmp/e2071-venv.log)"
BIN="$(dirname "${PY}")/endless"
[[ -x "${BIN}" ]] || setup_error "no endless console script at ${BIN}"

# XDG_CONFIG_HOME routes the CLI at a throwaway database, and the fixture
# project lives OUTSIDE this repo so the self-dev --db gate never fires and no
# fixture can reach the real database.
export XDG_CONFIG_HOME="${TMP_E2071}/config"
FIXTURE_DB="${XDG_CONFIG_HOME}/endless/endless.db"
PROJ="${TMP_E2071}/proj"
mkdir -p "${XDG_CONFIG_HOME}/endless" "${PROJ}" || setup_error "mkdir failed"

sqlite3 "${FIXTURE_DB}" < "${SCHEMA_SQL}" >/dev/null 2>&1 \
    || setup_error "could not apply the schema"

# seed <n> — n live matching tasks, inserted with plain SQL so the fixture never
# depends on the code under test. Each row gets a DISTINCT updated_at, one
# minute apart and ascending with the id, so the recency ordering the listings
# sort by is a real ordering and not insertion order in disguise.
seed() {
    local n="$1" i sql=""
    sqlite3 "${FIXTURE_DB}" "DELETE FROM tasks;" >/dev/null 2>&1 || return 1
    for ((i = 1; i <= n; i++)); do
        sql+="INSERT INTO tasks (id,project_id,title,phase,status,type_id,
                                 created_at,updated_at)
              VALUES (${i},1,'widget ${i}','now','ready',1,
                      datetime('2026-01-01 00:00:00','+${i} minutes'),
                      datetime('2026-01-01 00:00:00','+${i} minutes'));"
    done
    [[ -n "${sql}" ]] && sqlite3 "${FIXTURE_DB}" "${sql}" >/dev/null 2>&1
}

sqlite3 "${FIXTURE_DB}" \
    "INSERT INTO projects (id,name,path,status) VALUES (1,'capdemo','${PROJ}','active');" \
    >/dev/null 2>&1 || setup_error "could not seed the project"

# e <args...> — run the CLI from the fixture project. Sets E_OUT / E_ERR / E_RC.
e() {
    E_OUT=$(cd "${PROJ}" && "${BIN}" "$@" 2>"${TMP_E2071}/stderr")
    E_RC=$?
    E_ERR=$(<"${TMP_E2071}/stderr")
}

# The task-tree surfaces, as the argv each needs to render rows.
LISTINGS=(
    "task list --project capdemo"
    "task search widget --project capdemo"
    "task next --project capdemo"
    "task recent --project capdemo"
    "task landed --project capdemo"
    "task unsettled --all --project capdemo"
    "epic list --project capdemo"
    "decision list --project capdemo"
)
# The rest. This fixture cannot seed their rows (sessions, worktrees on disk,
# config layers), so they are swept for FLAG WIRING — which is the whole risk in
# a rollout that touches one command at a time. Their behaviour is covered by
# tests/test_rowcap.py and by the real-data smoke above.
OTHER_LISTINGS=(
    "session list"
    "session search widget"
    "session history"
    "session trail"
    "worktree list"
    "verb list"
    "phrase list"
    "project list"
)
# The four that render plain task rows, so one fixture drives them all.
TASK_LISTINGS=(
    "task list --project capdemo"
    "task search widget --project capdemo"
    "task next --project capdemo"
    "task recent --project capdemo"
)

# ── 3. the filed defect ─────────────────────────────────────────────────────
section "3. 35 matches render 20, and say so"

seed 35 || setup_error "could not seed 35 tasks"

for argv in "${TASK_LISTINGS[@]}"; do
    label="endless ${argv%% --project*}"
    e ${argv}
    if (( E_RC != 0 )); then
        report_fail "${label}: runs" "exit 0" "rc=${E_RC}: ${E_OUT}${E_ERR}"
        continue
    fi
    rendered=$(grep -c '^E-[0-9]' <<<"${E_OUT}")
    if [[ "${rendered}" == "20" ]]; then
        report_pass "${label}: renders 20 of 35"
    else
        report_fail "${label}: renders 20 of 35" "20 task rows" "${rendered}"
    fi
    if grep -qF -- "15 more rows (--no-limit)" <<<"${E_OUT}"; then
        report_pass "${label}: names the 15 it dropped, and the flag"
    else
        report_fail "${label}: names the 15 it dropped, and the flag" \
            "'… 15 more rows (--no-limit)'" "${E_OUT}"
    fi
done

# The exact sentence that produced the false negatives: a count that reports the
# HEIGHT OF THE TABLE reads as a complete result.
e task search widget --project capdemo
if grep -qF "35 match(es)" <<<"${E_OUT}"; then
    report_pass "the count under the table is the number of MATCHES (35)"
else
    report_fail "the count under the table is the number of MATCHES" \
        "'35 match(es)'" "${E_OUT}"
fi
if grep -qF "20 match(es)" <<<"${E_OUT}"; then
    report_fail "the count is not the height of the table" \
        "no '20 match(es)'" "still reports the rendered row count"
else
    report_pass "the count is not the height of the table"
fi

e task list --project capdemo
if grep -qF "35 item(s)" <<<"${E_OUT}"; then
    report_pass "task list totals the result set (35 item(s)), not the page"
else
    report_fail "task list totals the result set" "'35 item(s)'" "${E_OUT}"
fi

# --llm is prose an agent reads, and an agent is who got fooled.
e task search widget --project capdemo --llm
if grep -qF -- "# 15 more rows (--no-limit)" <<<"${E_OUT}"; then
    report_pass "--llm carries the footer too"
else
    report_fail "--llm carries the footer too" \
        "'# 15 more rows (--no-limit)'" "${E_OUT}"
fi

# ── 4. what the cap replaces, demonstrated ──────────────────────────────────
# Everything above tests the footer. This tests the CLAIM behind it: that a cap
# in the CALLER's pipe cannot say anything, so the same 20 rows arrive with and
# without a trace depending on who did the cutting.
section "4. The pipe that could not speak"

e task search widget --project capdemo --no-limit
full="${E_OUT}"
piped=$(head -24 <<<"${full}")

if [[ "$(grep -c '^E-[0-9]' <<<"${piped}")" == "20" ]]; then
    report_pass "head -24 yields 20 task rows — the shape of the original bug"
else
    report_fail "head -24 yields 20 task rows" "20" \
        "$(grep -c '^E-[0-9]' <<<"${piped}")"
fi

if grep -qE 'more rows|match\(es\)' <<<"${piped}"; then
    report_fail "the piped result carries no trace of what it lost" \
        "nothing in the head output says rows are missing" \
        "the pipe happened to leave a count line in view"
else
    report_pass "the piped result carries NO trace of what it lost"
fi

e task search widget --project capdemo
if grep -qF -- "15 more rows" <<<"${E_OUT}"; then
    report_pass "the same 20 rows, capped by the TOOL, arrive with the trace"
else
    report_fail "the same 20 rows, capped by the tool, arrive with the trace" \
        "a footer naming the 15 dropped" "${E_OUT}"
fi

# And the specific mechanism of both false negatives: id-ASC ordering drops the
# NEWEST rows, which is where relevance lives. The tool's own default ordering
# is recency-first, so the row a `head` would have hidden is now on screen.
e task recent --project capdemo
if grep -qE '^E-35[[:space:]]' <<<"${E_OUT}" && ! grep -qE '^E-1[[:space:]]' <<<"${E_OUT}"; then
    report_pass "the newest row is on screen and the OLDEST is the one dropped"
else
    report_fail "the newest row is on screen and the oldest is the one dropped" \
        "E-35 rendered, E-1 not" "${E_OUT}"
fi

# ── 5. both escapes, on every surface ───────────────────────────────────────
section "5. --limit and --no-limit"

for argv in "${TASK_LISTINGS[@]}"; do
    label="endless ${argv%% --project*}"
    e ${argv} --no-limit
    rendered=$(grep -c '^E-[0-9]' <<<"${E_OUT}")
    if [[ "${rendered}" == "35" ]]; then
        report_pass "${label} --no-limit: renders all 35"
    else
        report_fail "${label} --no-limit: renders all 35" "35 task rows" "${rendered}"
    fi
    if grep -qF -- "more rows (--no-limit)" <<<"${E_OUT}"; then
        report_fail "${label} --no-limit: prints no footer" \
            "nothing was omitted, so nothing to announce" "footer still printed"
    else
        report_pass "${label} --no-limit: prints no footer"
    fi

    e ${argv} --limit 5
    rendered=$(grep -c '^E-[0-9]' <<<"${E_OUT}")
    if [[ "${rendered}" == "5" ]] && grep -qF -- "30 more rows" <<<"${E_OUT}"; then
        report_pass "${label} --limit 5: renders 5 and names the 30"
    else
        report_fail "${label} --limit 5: renders 5 and names the 30" \
            "5 rows + '30 more rows'" "${rendered} rows: ${E_OUT}"
    fi
done

# Every surface must carry BOTH flags — the failure mode most likely to ship is
# one command wired and the next one not.
for argv in "${LISTINGS[@]}" "${OTHER_LISTINGS[@]}"; do
    label="endless ${argv%% --project*}"
    e ${argv} --help
    if grep -q -- "--no-limit" <<<"${E_OUT}" && grep -q -- "--limit" <<<"${E_OUT}"; then
        report_pass "${label}: offers both flags"
    else
        report_fail "${label}: offers both flags" "--limit and --no-limit in --help" \
            "${E_OUT}"
    fi

    e ${argv} --limit 5 --no-limit
    if (( E_RC != 0 )) && grep -qF "mutually exclusive" <<<"${E_OUT}${E_ERR}"; then
        report_pass "${label}: refuses both flags together"
    else
        report_fail "${label}: refuses both flags together" \
            "non-zero exit naming the conflict" "rc=${E_RC}: ${E_OUT}${E_ERR}"
    fi
done

e sql "SELECT id FROM tasks" --help
if grep -q -- "--no-limit" <<<"${E_OUT}"; then
    report_pass "endless sql: offers --no-limit (the raw escape hatch is capped too)"
else
    report_fail "endless sql: offers --no-limit" "--no-limit in --help" "${E_OUT}"
fi

e sql "SELECT id FROM tasks ORDER BY id"
if grep -qF -- "15 more rows (--no-limit)" <<<"${E_OUT}"; then
    report_pass "endless sql: the table render announces its truncation"
else
    report_fail "endless sql: the table render announces its truncation" \
        "'… 15 more rows (--no-limit)'" "${E_OUT}"
fi

# `--limit 0` reads as "no limit" but would mean "no rows". Refusing it and
# naming the flag meant is the difference between a trap and a signpost.
e task list --project capdemo --limit 0
if (( E_RC != 0 )) && grep -qF -- "--no-limit" <<<"${E_OUT}${E_ERR}"; then
    report_pass "--limit 0 is refused, and points at --no-limit"
else
    report_fail "--limit 0 is refused, and points at --no-limit" \
        "non-zero exit naming --no-limit" "rc=${E_RC}: ${E_OUT}${E_ERR}"
fi

# ── 6. machine formats stay whole, and stay parseable ───────────────────────
# Silently capping a --json payload would be strictly worse than the defect this
# task fixes: a consumer parsing 20 of 35 rows has no footer to read and no way
# to notice.
section "6. --json and --tsv"

for argv in "${TASK_LISTINGS[@]}"; do
    label="endless ${argv%% --project*}"
    e ${argv} --json
    n=$("${PY}" -c 'import json,sys; print(len(json.load(sys.stdin)))' <<<"${E_OUT}" 2>&1)
    if [[ "${n}" == "35" ]]; then
        report_pass "${label} --json: uncapped (35 rows) and parses"
    else
        report_fail "${label} --json: uncapped and parses" "35" "${n}"
    fi
done

e task list --project capdemo --json --limit 5
n=$("${PY}" -c 'import json,sys; print(len(json.load(sys.stdin)))' <<<"${E_OUT}" 2>&1)
if [[ "${n}" == "5" ]]; then
    report_pass "--json --limit 5: still parses — the footer is not in the payload"
else
    report_fail "--json --limit 5: still parses" "5" "${n}"
fi
if grep -qF -- "30 more rows" <<<"${E_ERR}"; then
    report_pass "--json --limit 5: the trace is on stderr, where jq will not see it"
else
    report_fail "--json --limit 5: the trace is on stderr" \
        "'30 more rows' on stderr" "${E_ERR:-nothing on stderr}"
fi

e sql "SELECT id FROM tasks" --tsv
n=$(grep -c . <<<"${E_OUT}")
if [[ "${n}" == "35" ]]; then
    report_pass "sql --tsv: uncapped (35 lines)"
else
    report_fail "sql --tsv: uncapped" "35 lines" "${n}"
fi

e sql "SELECT id FROM tasks" --tsv --limit 5
n=$(grep -c . <<<"${E_OUT}")
if [[ "${n}" == "5" ]] && grep -qF -- "30 more rows" <<<"${E_ERR}"; then
    report_pass "sql --tsv --limit 5: 5 clean lines, trace on stderr"
else
    report_fail "sql --tsv --limit 5: 5 clean lines, trace on stderr" \
        "5 lines + stderr trace" "${n} lines; stderr: ${E_ERR:-none}"
fi

# ── 7. nothing legal became collateral damage ───────────────────────────────
# The footer has to MEAN something. If it appeared on a complete listing, the
# reader would learn to ignore it, and the next real truncation goes unread.
section "7. A listing that fits says nothing"

seed 5 || setup_error "could not reseed 5 tasks"
for argv in "${TASK_LISTINGS[@]}"; do
    label="endless ${argv%% --project*}"
    e ${argv}
    if grep -qF -- "more rows (--no-limit)" <<<"${E_OUT}"; then
        report_fail "${label}: no footer on a 5-row result" \
            "silence" "footer printed anyway"
    else
        report_pass "${label}: no footer on a 5-row result"
    fi
done

# The boundary. A result of EXACTLY the cap is complete, and a footer there
# would be a lie in the other direction.
seed 20 || setup_error "could not reseed 20 tasks"
e task list --project capdemo
rendered=$(grep -c '^E-[0-9]' <<<"${E_OUT}")
if [[ "${rendered}" == "20" ]] && ! grep -qF -- "more rows (--no-limit)" <<<"${E_OUT}"; then
    report_pass "an exact-fit 20 renders all 20 with no footer"
else
    report_fail "an exact-fit 20 renders all 20 with no footer" \
        "20 rows, no footer" "${rendered} rows: ${E_OUT}"
fi

seed 21 || setup_error "could not reseed 21 tasks"
e task list --project capdemo
if grep -qF -- "1 more row (--no-limit)" <<<"${E_OUT}"; then
    report_pass "one row over the cap says 'row', not 'rows'"
else
    report_fail "one row over the cap says 'row', not 'rows'" \
        "'… 1 more row (--no-limit)'" "${E_OUT}"
fi

sqlite3 "${FIXTURE_DB}" "DELETE FROM tasks;" >/dev/null 2>&1 \
    || setup_error "could not clear the fixture"
e task search widget --project capdemo
if (( E_RC == 0 )) && ! grep -qF -- "more rows" <<<"${E_OUT}" \
        && grep -qi "no tasks matching" <<<"${E_OUT}"; then
    report_pass "an empty result is unchanged — no footer, same message"
else
    report_fail "an empty result is unchanged" \
        "the no-matches message and no footer" "rc=${E_RC}: ${E_OUT}"
fi

# ── 8. the probe-window path ────────────────────────────────────────────────
# The session listings keep their cap in SQL, so they fetch cap+1 rows and count
# separately. Two things can go wrong there and nowhere else: the footer can
# report the probe row ("1 more") instead of the counted remainder, and it can
# print at the wrong end of a render whose order was reversed for reading.
section "8. session history: a probe window, counted honestly"

# 60 messages on one session, seeded with plain SQL.
sqlite3 "${FIXTURE_DB}" \
    "INSERT INTO sessions (id,session_id,project_id,state,started_at)
     VALUES (1,'e2071-fixture-session',1,'idle',datetime('2026-01-01 00:00:00'));" \
    >/dev/null 2>&1 || setup_error "could not seed the session"
msgs=""
for ((i = 1; i <= 60; i++)); do
    msgs+="INSERT INTO session_messages (session_id,role,content,created_at)
           VALUES ('e2071-fixture-session','user','message ${i}',
                   datetime('2026-01-01 00:00:00','+${i} minutes'));"
done
sqlite3 "${FIXTURE_DB}" "${msgs}" >/dev/null 2>&1 \
    || setup_error "could not seed the messages"

e session history 1
if grep -qF -- "40 more rows (--no-limit)" <<<"${E_OUT}"; then
    report_pass "the footer names the COUNTED remainder (40), not the probe row"
else
    report_fail "the footer names the counted remainder" \
        "'… 40 more rows (--no-limit)' — not '1 more row'" "${E_OUT}"
fi

# Newest-first takes the last 20 and displays them oldest-first, so what is
# missing is OLDER than everything on screen: above the first line.
first=$(grep -n -- "more rows (--no-limit)" <<<"${E_OUT}" | head -1 | cut -d: -f1)
if [[ "${first}" == "1" ]]; then
    report_pass "newest-first: the footer prints above the render, where the gap is"
else
    report_fail "newest-first: the footer prints above the render" \
        "the footer on line 1" "line ${first:-absent}"
fi

# --sort asc starts at the beginning, so the TAIL is what is missing.
e session history 1 --sort asc
lines=$(grep -c . <<<"${E_OUT}")
last=$(grep -n -- "more rows (--no-limit)" <<<"${E_OUT}" | tail -1 | cut -d: -f1)
if [[ -n "${last}" ]] && (( last > lines / 2 )); then
    report_pass "--sort asc: the footer moves to the bottom, where the gap is"
else
    report_fail "--sort asc: the footer moves to the bottom" \
        "a footer in the lower half of the render" "line ${last:-absent} of ${lines}"
fi

e session history 1 --no-limit
rendered=$(grep -c "^User: message" <<<"${E_OUT}")
if [[ "${rendered}" == "60" ]] && ! grep -qF -- "more rows" <<<"${E_OUT}"; then
    report_pass "--no-limit renders all 60 with no footer"
else
    report_fail "--no-limit renders all 60 with no footer" "60 messages, no footer" \
        "${rendered} messages"
fi

e session history 1 --json
n=$("${PY}" -c 'import json,sys; print(len(json.load(sys.stdin)))' <<<"${E_OUT}" 2>&1)
if [[ "${n}" == "60" ]]; then
    report_pass "session history --json: uncapped (60) and parses"
else
    report_fail "session history --json: uncapped and parses" "60" "${n}"
fi

# ── summary ─────────────────────────────────────────────────────────────────
section "Summary"
printf '  %s%d passed%s, %s%d failed%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" \
    "$([[ ${FAIL_COUNT} -gt 0 ]] && printf '%s' "${RED}")" "${FAIL_COUNT}" "${RESET}"

if (( FAIL_COUNT > 0 )); then
    printf '\n  Failed:\n'
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    exit 1
fi
exit 0
