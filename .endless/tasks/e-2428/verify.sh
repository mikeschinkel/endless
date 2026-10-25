#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2428 and records what was true when E-2428
# landed. Edit it only if you ARE E-2428. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2428 — keep a live tmux server's server ID when reading it fails.
#
#   1. The task's own Go tests, fail-fast: the failed-read seams (tmux init and
#      TmuxServerUUID), the alias recording and the one same-server test, and
#      the readers that go through it (liveness, live sessions, pane lookup,
#      events, triage).
#   2. The migration and schema.sql agree on tmux_server_aliases.
#   3. This worktree's endless-go `tmux init` against real tmux, on a private
#      socket: a failed read refuses and stamps nothing; an unset option is
#      stamped; a set one is kept.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go tmux; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done
export GOEXPERIMENT=jsonv2

section "The task's own Go tests"
if out=$(go test ./internal/tmuxcmd -run 'TestReadServerOption' -count=1 2>&1); then
    report_pass "tmux init tells a failed read from an unset option"
else
    report_fail "tmux init tells a failed read from an unset option" "go test passes" "${out}"
    summary
fi
if out=$(go test ./internal/monitor -count=1 \
    -run 'TestLiveTmuxServerUUID|TestRecordTmuxServer|TestLiveness_SessionBoundUnderAlias|TestProcessIDsForPanes_AcceptsAlias|TestLiveness_AliasOnProbedSocket|TestSameTmuxServer|TestLiveness_GoneServer' 2>&1); then
    report_pass "monitor: no stamp on a failed read; aliases recorded and accepted; E-2300 still proves gone"
else
    report_fail "monitor: no stamp on a failed read; aliases recorded and accepted; E-2300 still proves gone" "go test passes" "${out}"
    summary
fi
if out=$(go test ./internal/events ./internal/triagejob -run 'ServerAlias' -count=1 2>&1); then
    report_pass "events and triage resolve a pane bound under an alias"
else
    report_fail "events and triage resolve a pane bound under an alias" "go test passes" "${out}"
    summary
fi

section "Schema"
if out=$(go test ./internal/schema -run 'TestMigrate_MatchesSchemaSQL' -count=1 2>&1); then
    report_pass "migration 00026 matches schema.sql"
else
    report_fail "migration 00026 matches schema.sql" "go test passes" "${out}"
fi

section "tmux init against real tmux"
WORK_TMP=$(mktemp -d /tmp/e2428-XXXXXX) || setup_error "mktemp failed"
SOCK="${WORK_TMP}/s"
trap 'tmux -S "${SOCK}" kill-server >/dev/null 2>&1; rm -rf "${WORK_TMP}"' EXIT
BIN="${WORK_TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "build endless-go"
tmux -f /dev/null -S "${SOCK}" new-session -d sleep 300 || setup_error "start a private tmux server"

init_on() { TMUX="$1,0,0" "${BIN}" tmux init 2>&1; }

out=$(init_on "${WORK_TMP}/no-server"); rc=$?
assert_eq "a failed read refuses" "1" "${rc}"
assert_contains "the refusal names the failed read" "read @server_uuid" "${out}"
assert_not_contains "a failed read stamps nothing" "server initialized" "${out}"

out=$(init_on "${SOCK}")
first=$(tmux -S "${SOCK}" show-options -gqv @server_uuid)
assert_contains "an unset option is stamped" "server initialized (@server_uuid=${first})" "${out}"

out=$(init_on "${SOCK}")
assert_contains "a set option is kept" "already initialized" "${out}"
assert_eq "the id is unchanged" "${first}" "$(tmux -S "${SOCK}" show-options -gqv @server_uuid)"

summary
