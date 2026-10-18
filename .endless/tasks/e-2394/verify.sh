#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2394 and records what was true when E-2394
# landed. Edit it only if you ARE E-2394. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2394: `endless session end` discards a ghost session.
#
# What is verified here:
#   A. Fail-fast: this task's Go tests by NAME (the end-session write and
#      last_activity on the resume target) and its Python tests (the guards, the
#      prompt, the printed recovery line, and the claim refusal naming it).
#   B. End to end: the real `endless` CLI and an endless-go built from this
#      tree, against a scratch database holding a ghost (its tmux server never
#      recorded, so it reads `unknown`) and a recently active session.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the scratch database"
command -v python3 >/dev/null 2>&1 || setup_error "python3 is required to read --json"

E2E="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${E2E}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

check_named() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}
for t in TestEndSession_EndsTheRowAndReleasesItsLock TestEndSession_UnknownIDRefuses \
         TestResumeTarget_CarriesLastActivity; do
    check_named ./internal/sessionquerycmd "${t}"
done

if out=$(uv run pytest tests/test_session_end.py tests/test_check_task_ownership.py -q 2>&1); then
    report_pass "pytest: session end guards and output, and the claim refusal"
else
    report_fail "pytest: session end guards and output, and the claim refusal" \
        "exit 0" "$(printf '%s' "${out}" | tail -40)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. End to end: the real CLI against a scratch database"
# ---------------------------------------------------------------------------

mkdir -p "${E2E}/home/.config/endless" "${E2E}/proj" "${E2E}/bin"
go build -o "${E2E}/bin/endless-go" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
DBDIR="${E2E}/home/.config/endless"
DB="${DBDIR}/endless.db"

# Run from outside any project so the CLI's database is the scratch one.
e2e() {
    (cd "${E2E}" && env -u TMUX -u TMUX_PANE HOME="${E2E}/home" \
        XDG_CONFIG_HOME="${E2E}/home/.config" PATH="${E2E}/bin:${PATH}" "$@")
}
endless_e2e() { e2e uv run --project "${WT}" endless "$@"; }

e2e endless-go --db-dir "${DBDIR}" session-query list-live --project-root "${E2E}/proj" \
    >/dev/null 2>&1 || setup_error "cannot initialize the scratch database"
PROJECT="$(sqlite3 "${DB}" "SELECT id FROM projects LIMIT 1")"
[[ -n "${PROJECT}" ]] || setup_error "no project row was registered"

OLD="$(python3 -c 'import datetime as d; print((d.datetime.now(d.timezone.utc)-d.timedelta(hours=2)).strftime("%Y-%m-%dT%H:%M:%S"))')"
NEW="$(python3 -c 'import datetime as d; print((d.datetime.now(d.timezone.utc)-d.timedelta(minutes=2)).strftime("%Y-%m-%dT%H:%M:%S"))')"
sqlite3 "${DB}" <<SQL || setup_error "cannot seed the scratch database"
INSERT INTO processes (id, kind_id, server_uuid, address) VALUES (1, 1, 'GONE', '%0'), (2, 1, 'GONE', '%1');
INSERT INTO sessions (id, session_id, project_id, platform, state, process_id, last_activity) VALUES
  (1, 's-ghost',  ${PROJECT}, 'claude', 'working', 1, '${OLD}'),
  (2, 's-recent', ${PROJECT}, 'claude', 'working', 2, '${NEW}');
SQL
state_of() { sqlite3 "${DB}" "SELECT state FROM sessions WHERE id = $1"; }

out="$(endless_e2e session end ES-1 </dev/null 2>&1)"; rc=$?
assert_eq "no terminal and no --yes: refused" "1" "${rc}"
assert_contains "the refusal says how to confirm" "endless session end ES-1 --yes" "${out}"
assert_contains "it shows the liveness it judged" "tmux:          unknown" "${out}"
assert_eq "the refused ghost is untouched" "working" "$(state_of 1)"

out="$(endless_e2e session end ES-2 --yes 2>&1)"; rc=$?
assert_eq "activity 2 minutes ago: refused" "1" "${rc}"
assert_contains "the refusal names --force" "endless session end ES-2 --force" "${out}"
assert_eq "the recent session is untouched" "working" "$(state_of 2)"

out="$(endless_e2e session end ES-1 --yes 2>&1)"; rc=$?
assert_eq "the ghost ends" "0" "${rc}"
assert_eq "its row reads ended" "ended" "$(state_of 1)"
assert_contains "it prints how to get it back" "endless session resume ES-1" "${out}"
assert_eq "no other session was ended" "working" "$(state_of 2)"

out="$(endless_e2e session end ES-1 --yes 2>&1)"; rc=$?
assert_eq "ending it again exits zero" "0" "${rc}"
assert_contains "and says it is already ended" "ES-1 (no task) is already ended" "${out}"

target="$(e2e endless-go --db-dir "${DBDIR}" session-query resume-target --ref ES-1 2>/dev/null \
    | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d.get("rows", d); print(d["endless_id"], d["state"])')"
assert_eq "the resume route still resolves the ended session" "1 ended" "${target}"

out="$(endless_e2e session end ES-2 --force --yes 2>&1)"; rc=$?
assert_eq "--force ends the recently active session" "0 ended" "${rc} $(state_of 2)"

summary
