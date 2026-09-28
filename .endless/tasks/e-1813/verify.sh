#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1813 and records what was true when E-1813
# landed. Edit it only if you ARE E-1813. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1813: tasks.tier is replaced by two rating axes, complexity and risk
# (ED-1538/ED-1539), proposed by the agent at submit and ratified by the user
# at approve. Ratings never move status.
#
# What is verified here:
#   A. Fail-fast: this task's own Go and Python tests.
#   B. The real CLI against a real, isolated database: the propose/ratify
#      gates, the plan-attach nudge, the rendering, the filters, and that a
#      rating moves no status.
#   C. tier is gone, not merely unused: the column, the flag, the tier-1 edge.
#
# See E-1813's plan (endless task show E-1813 --all-fields).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "${REPO_ROOT}" || setup_error "cannot cd to worktree root ${REPO_ROOT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

go_pkg() { # go_pkg <label> <go test args...>
    local label="$1"; shift
    if out=$(go test "$@" 2>&1); then
        report_pass "go test: ${label}"
    else
        report_fail "go test: ${label}" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

py_file() { # py_file <label> <pytest target...>
    local label="$1"; shift
    if out=$(uv run pytest -q -p no:cacheprovider "$@" 2>&1); then
        report_pass "pytest: ${label}"
    else
        report_fail "pytest: ${label}" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

go_pkg "the rating enum and its mirror-table integrity check" ./internal/rating/
go_pkg "migration 00008, and migrated == schema.sql" ./internal/schema/
go_pkg "executor and projector agree on ratings; tier refused live, skipped on replay" \
    ./internal/events/ -run 'Ratings|PayloadRoundTrips|TaskContent'
go_pkg "notices render ratings by slug; triage context carries them" \
    ./internal/monitor/ -run 'Notice|Headline|TriageContext|UntriagedTasks'
go_pkg "the tmux status line without tier" ./internal/tmuxcmd/
go_pkg "the lifecycle table without the tier-1 edges" ./internal/taskstatus/

py_file "ratings at the CLI surface"         tests/test_ratings_cli.py
py_file "submit proposes, approve ratifies"  tests/test_submit_approve.py
py_file "the triager proposes ratings"       tests/test_triage.py
py_file "--keep-status, and a rating infers nothing" tests/test_keep_status.py
py_file "the plan-attach promotion nudges"   tests/test_plan_auto_promote.py
py_file "untriaged default, no tier-1 exemption" tests/test_untriaged_status.py
py_file "lifecycle gate and diagram in sync" \
    tests/test_status_lifecycle_gate.py tests/test_status_lifecycle_sync.py

# ---------------------------------------------------------------------------
section "B. The real CLI, against a real isolated database"
# ---------------------------------------------------------------------------
# Section A drives functions and fixtures. This drives the installed entry
# points — the candidate endless-go on PATH, the worktree's Python CLI — the
# way a person or an agent types them, against a database built from nothing.

GO="${REPO_ROOT}/bin/endless-go"
EN="${REPO_ROOT}/.venv/bin/endless"
[[ -x "${GO}" ]] || setup_error "${GO} missing — run \`just build\`"
[[ -x "${EN}" ]] || ( uv run endless --version >/dev/null 2>&1 ) \
    || setup_error "could not materialize .venv (uv run endless failed)"

WORK="$(cd "$(mktemp -d)" && pwd -P)"
trap 'rm -rf "${WORK}"' EXIT
export XDG_CONFIG_HOME="${WORK}/config"
export XDG_CACHE_HOME="${WORK}/cache"
export ENDLESS_AUTO_MIGRATE=1
export ENDLESS_NO_TRIAGE=1            # no model call from a verify run
export PATH="${REPO_ROOT}/bin:${PATH}"
unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_ENTRYPOINT 2>/dev/null || true
mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

PROJ="${WORK}/proj"
mkdir -p "${PROJ}"
git -C "${PROJ}" init -q -b main
git -C "${PROJ}" config user.email "verify@example.com"
git -C "${PROJ}" config user.name "Verify"
git -C "${PROJ}" config commit.gpgsign false
: > "${PROJ}/README.md"
git -C "${PROJ}" add README.md && git -C "${PROJ}" commit -q -m init

en() { ( cd "${PROJ}" && "${EN}" "$@" 2>&1 ); }
en project register "${PROJ}" --infer --name verify1813 --status active >/dev/null \
    || setup_error "registering the temp project failed"

add() { # add <title> [flags...] → E-N
    local out
    out=$(en task add "$@" --description "A short spec for the verify run.") \
        || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}
status_of() { en task show "$1" --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])'; }
ratings_of() { en task show "$1" --json | python3 -c '
import json, sys
d = json.load(sys.stdin)
print(str(d["complexity"]) + "/" + str(d["risk"]))
'; }

T1=$(add "Add the first verify thing") || setup_error "task add failed: ${T1}"

assert_eq "a new task is unrated" "None/None" "$(ratings_of "${T1}")"
assert_contains "task show always renders the ratings, unrated included" \
    "complexity unrated · risk unrated" "$(en task show "${T1}" --no-color)"
assert_contains "task show --agent carries both on the status line" \
    "complexity=unrated risk=unrated" "$(en task show "${T1}" --agent)"

out=$(en task submit "${T1}"); rc=$?
assert_eq "task submit refuses an unrated task (non-zero exit)" "1" "${rc}"
assert_contains "the refusal names the flags to pass" \
    "--complexity <low|medium|high> --risk <low|medium|high>" "${out}"
assert_eq "the refusal changes nothing" "untriaged" "$(status_of "${T1}")"

en task submit "${T1}" --complexity low --risk medium >/dev/null
assert_eq "task submit with both ratings reaches submitted" "submitted" "$(status_of "${T1}")"
assert_eq "the proposed ratings are recorded" "low/medium" "$(ratings_of "${T1}")"

out=$(en task approve "${T1}" --risk low)
assert_eq "task approve reaches ready" "ready" "$(status_of "${T1}")"
assert_contains "approve reports what it ratified, override included" \
    "Ratified: complexity low, risk low" "${out}"

T2=$(add "Add the second verify thing" --status unplanned) || setup_error "task add failed: ${T2}"
printf '# Plan\n\nDo the thing.\n' > "${WORK}/plan.md"
out=$(en task update "${T2}" --plan-file "${WORK}/plan.md" --complexity high)
assert_eq "attaching a plan still promotes without ratings" "submitted" "$(status_of "${T2}")"
assert_contains "and nudges for the rating still missing" "submitted without risk" "${out}"
assert_not_contains "but not for one set in the same call" "--complexity <" "${out}"

out=$(en task approve "${T2}"); rc=$?
assert_eq "task approve refuses the unrated task (non-zero exit)" "1" "${rc}"
assert_eq "it stays submitted" "submitted" "$(status_of "${T2}")"
out=$(en task update "${T2}" --status ready); rc=$?
assert_eq "task update --status ready is gated the same way" "1" "${rc}"
en task approve "${T2}" --risk high >/dev/null
assert_eq "approve can supply the missing rating as it ratifies" "high/high" "$(ratings_of "${T2}")"

T3=$(add "Add the third verify thing" --status unplanned) || setup_error "task add failed: ${T3}"
en task update "${T3}" --complexity low --risk low >/dev/null
assert_eq "a rating edit moves no status (tier 1 used to reach ready)" "unplanned" "$(status_of "${T3}")"
en task update "${T3}" --risk none >/dev/null
assert_eq "none clears a rating" "low/None" "$(ratings_of "${T3}")"

ids() { python3 -c 'import json,sys; print(" ".join(r["id"] for r in json.load(sys.stdin)["rows"]))'; }
assert_eq "task list --complexity high" "${T2}" "$(en task list --all --json --complexity high | ids)"
assert_eq "task list --risk none selects the unrated" "${T3}" "$(en task list --all --json --risk none | ids)"
assert_contains "task list shows a Rating column once something is rated" \
    "low/low" "$(en task list --all)"

# ---------------------------------------------------------------------------
section "C. tier is gone, not merely unused"
# ---------------------------------------------------------------------------

DB="${XDG_CONFIG_HOME}/endless/endless.db"
cols=$(python3 -c 'import sqlite3,sys; print(",".join(r[1] for r in sqlite3.connect(sys.argv[1]).execute("PRAGMA table_info(tasks)")))' "${DB}")
assert_not_contains "tasks has no tier column" ",tier," ",${cols},"
assert_contains "tasks has complexity_id" "complexity_id" "${cols}"
assert_contains "tasks has risk_id" "risk_id" "${cols}"

out=$(en task add "Add a tiered thing" --tier 1); rc=$?
assert_eq "--tier is refused, not silently ignored" "2" "${rc}"

T4=$(add "Add the fourth verify thing") || setup_error "task add failed: ${T4}"
out=$(en task update "${T4}" --status ready); rc=$?
assert_eq "untriaged → ready is no longer an edge" "1" "${rc}"
assert_contains "the refusal routes through submit" "submitted" "${out}"

assert_not_contains "the lifecycle diagram has no tier-1 edge" "tier-1" \
    "$(cat "${REPO_ROOT}/docs/status-lifecycle.mmd")"

summary
