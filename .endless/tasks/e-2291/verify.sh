#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2291 and records what was true when E-2291
# landed. Edit it only if you ARE E-2291. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2291 — make `task bind` the planning step before a claim.
#
#   1. The task's own Go and Python tests, fail fast: the bound relation and
#      its ladder, the filed/touched renames, task.bound, the auto-bind's
#      relation, the anchor set in session status, the backfill migration,
#      the main-checkout write gate for a bound session, the claim refusal's
#      bind line, `session touch` and its two aliases, and bind from a shell
#      pane.
#   2. The user-facing text, from this worktree's own `endless`: bind's help,
#      the tasks guide, and `session touch` as the user's command, with
#      `touch` and `session task add` as its aliases.
#   3. The renames left nothing behind: no `Touched by:` header and no
#      `surfaced` / `queued` relation slug in the code, help or guide.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"
for tool in go uv git; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

# ── 1. unit tests, fail fast ─────────────────────────────────────────────────
section "1. The task's own tests (fail fast)"
if out=$(go test -count=1 ./internal/sessiontaskrelation/ ./internal/sessionstatuscmd/ 2>&1); then
    report_pass "go test sessiontaskrelation, sessionstatuscmd (enum, ladder, anchors, touched glyph)"
else
    report_fail "go test sessiontaskrelation, sessionstatuscmd" "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(go test -count=1 ./internal/events/ \
        -run 'TaskBound|SessionTasks|RelationClassification|RelationNeverDowngrades|MovesFocus|ValidKinds|RetiredKinds|Validate_RetiredKinds' 2>&1); then
    report_pass "go test ./internal/events (task.bound, touched, retired queued kind)"
else
    report_fail "go test ./internal/events" "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(go test -count=1 ./internal/monitor/ \
        -run 'BindSessionToTask|SessionStatus_BoundTaskIsAnchorOnly|SessionStatusRowsForSession' 2>&1); then
    report_pass "go test ./internal/monitor (auto-bind relation, bound task is the anchor only)"
else
    report_fail "go test ./internal/monitor" "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(go test -count=1 ./internal/schema/... 2>&1); then
    report_pass "go test ./internal/schema/... (migration 24 backfill, schema parity)"
else
    report_fail "go test ./internal/schema/..." "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(go test -count=1 ./internal/hookcmd/ -run 'WorktreeGate_RefusesBoundSessionWriteInMain|UnboundWorktreeGate' 2>&1); then
    report_pass "go test ./internal/hookcmd (a bound session's Write in main is refused)"
else
    report_fail "go test ./internal/hookcmd" "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(go build -o bin/endless-go ./cmd/endless-go 2>&1); then
    report_pass "go build ./cmd/endless-go (the Python tests drive this worktree's executor)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi
if out=$(uv run --project "${ROOT}" pytest -q --no-header -p no:cacheprovider \
        tests/test_task_bind_planning.py tests/test_session_task_membership.py \
        tests/test_task_show_sessions.py tests/test_task_bind_write_once.py \
        tests/test_claim_output_format.py tests/test_spawn_gate.py 2>&1); then
    report_pass "pytest (claim refusal, task.bound, shell-pane bind, touch alias, Sessions: header)"
else
    report_fail "pytest" "all passed" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ── 2. the user-facing text ──────────────────────────────────────────────────
section "2. What a reader is told"
endless_wt() { uv run --project "${ROOT}" endless "$@" 2>&1; }

bind_help=$(endless_wt task bind --help)
assert_contains "task bind's help names the planning use" \
    "working out" "${bind_help}"
assert_contains "task bind's help says writes stay refused until the claim" \
    "Writes stay refused" "${bind_help}"
assert_contains "task bind's help covers a shell pane" \
    "shell pane" "${bind_help}"

guide=$(endless_wt guide tasks)
assert_contains "the tasks guide has a Binding before a claim section" \
    "### Binding before a claim" "${guide}"
assert_contains "the guide gives the refusal's wording" \
    "if you and your user are working out the plan together now" "${guide}"
assert_contains "the guide documents the Sessions: header" \
    "**\`Sessions:\`** block" "${guide}"

for verb in "session touch" "touch" "session task add"; do
    # shellcheck disable=SC2086
    help=$(endless_wt ${verb} --help)
    assert_contains "${verb} --help: the user's command" \
        "A command for you, the user" "${help}"
    assert_contains "${verb} --help: names the aliases" \
        "are aliases." "${help}"
done

sessions_guide=$(endless_wt guide sessions)
assert_contains "the sessions guide's relation table lists bound" \
    "| \`bound\`" "${sessions_guide}"
assert_contains "the sessions guide calls session touch the user's command" \
    "is the user's command" "${sessions_guide}"

# ── 3. nothing left behind ───────────────────────────────────────────────────
section "3. The renames left nothing behind"
# Migrations and other tasks' history are records of what was, so they are out
# of scope; tests are excluded because some name the old slugs on purpose (to
# prove they are now rejected).
hits=$(grep -rn "Touched by:" src internal docs/guide \
        --include='*.py' --include='*.go' --include='*.md' 2>/dev/null \
        | grep -v '_test\.go' || true)
assert_eq "no code, help or guide text prints a Touched by: header" "" "${hits}"

hits=$(grep -rnE "'(surfaced|queued)'|\"(surfaced|queued)\"" \
        src internal/sessiontaskrelation internal/events internal/monitor \
        internal/sessionstatuscmd internal/schema/schema.sql internal/schema/seeds.sql \
        --include='*.py' --include='*.go' --include='*.sql' 2>/dev/null \
        | grep -v '_test\.go' | grep -v 'session_tasks\.queued' || true)
assert_eq "no reader or seed names surfaced or queued as a relation slug" "" "${hits}"

summary
