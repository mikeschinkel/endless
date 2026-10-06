#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2251 and records what was true when E-2251
# landed. Edit it only if you ARE E-2251. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2251 verification — "not a project" lives on projects rows (status
# 'ignored') plus an optional .endless-ignore marker; the nearest registered or
# ignored ancestor wins; no registration path auto-registers an ignored
# directory; the config `ignore` list is imported once by migration 15.
#
#   endless task verify E-2251
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"
TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
TMP="$(cd "${TMP}" && pwd -P)"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test ./internal/monitor/ ./internal/schema/... ./internal/hookcmd/ \
        ./internal/sessionquerycmd/ ./internal/events/ >"${TMP}/go.log" 2>&1; then
    report_pass "go test: resolver, migration 15 import, hook, events"
else
    report_fail "go test monitor / schema / hookcmd / events" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q \
        tests/test_ignored_projects.py \
        tests/test_config.py \
        tests/test_cli.py \
        tests/test_register.py \
        tests/test_reconcile.py \
        tests/test_project_path.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: ignore/unignore/marker, register, unregister, reconcile"
else
    report_fail "pytest ignored projects / register / reconcile" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. end to end against a fresh database ──────────────────────────────────
section "2. Migration 15 imports the config list beside the database"

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${TMP}/build.log" 2>&1 \
    || setup_error "go build failed: $(tail -15 "${TMP}/build.log")"

export HOME="${TMP}/home"
P="${HOME}/Projects"
DB="${TMP}/db"
mkdir -p "${DB}" "${P}/vendor/h2/sub" "${P}/vendor/endless/internal" "${P}/fresh" "${P}/marked/deep"
printf '{"roots":["~/Projects"],"ignore":["~/Projects/vendor"]}\n' >"${DB}/config.json"
GO=("${BIN}" --db-dir "${DB}")
sql() { sqlite3 "${DB}/endless.db" "$1"; }
kind() { "${GO[@]}" project resolve "$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["rows"][0]["kind"])'; }
# The hook no-ops outside a supported harness (agentenv, E-1962), so it is run
# as Claude Code runs it whatever shell this suite was started from. Without
# this, every hook assertion below passes vacuously from a bare terminal.
hook() {
    printf '{"session_id":"%s","hook_event_name":"UserPromptSubmit","cwd":"%s","prompt":"hi"}' "$1" "$2" \
        | env -u TMUX -u TMUX_PANE -u ENDLESS_SESSION_ID CLAUDECODE=1 CLAUDE_CODE_ENTRYPOINT=cli \
            "${GO[@]}" hook claude >/dev/null 2>"${TMP}/hook.err"
}

"${GO[@]}" event migrate >"${TMP}/migrate.log" 2>&1 || setup_error "migrate: $(tail -10 "${TMP}/migrate.log")"
assert_eq "the config entry became an ignored row named by its path" \
    "~/Projects/vendor|ignored" "$(sql "SELECT name || '|' || status FROM projects WHERE path = '~/Projects/vendor'")"
assert_eq "live_projects excludes it" "0" "$(sql "SELECT count(*) FROM live_projects")"

section "3. Nearest row wins; explicit registration under an ignored tree"

assert_eq "a directory under the ignored one is ignored" "ignored" "$(kind "${P}/vendor/h2/sub")"
sql "INSERT INTO projects (name, path) VALUES ('endless', '~/Projects/vendor/endless')"
assert_eq "a registered project inside the ignored tree is a project" "project" "$(kind "${P}/vendor/endless/internal")"
assert_eq "an uncovered directory is neither" "none" "$(kind "${P}/fresh")"

section "4. The hook never auto-registers an ignored directory"

BEFORE="$(sql "SELECT count(*) FROM projects")"
hook s-ignored "${P}/vendor/h2/sub"
assert_eq "hook in an ignored directory exits 0" "0" "$?"
assert_eq "…and writes no projects row" "${BEFORE}" "$(sql "SELECT count(*) FROM projects")"
assert_eq "…and records no session" "0" "$(sql "SELECT count(*) FROM sessions")"

hook s-fresh "${P}/fresh"
assert_eq "hook in an uncovered directory still auto-registers it" \
    "fresh|active" "$(sql "SELECT name || '|' || status FROM projects WHERE path = '~/Projects/fresh'")"

touch "${P}/marked/.endless-ignore"
hook s-marked "${P}/marked/deep"
assert_eq "a marker file alone stops auto-registration" \
    "0" "$(sql "SELECT count(*) FROM projects WHERE path LIKE '~/Projects/marked%'")"

section "5. ignore / activate / clear"

"${GO[@]}" project ignore "${P}/fresh" >/dev/null
assert_eq "ignoring a project keeps its row and name" \
    "fresh|ignored" "$(sql "SELECT name || '|' || status FROM projects WHERE path = '~/Projects/fresh'")"
"${GO[@]}" project activate "${P}/fresh" >/dev/null
assert_eq "activate restores it" "project" "$(kind "${P}/fresh")"
OUT="$("${GO[@]}" project clear "${P}/fresh" 2>&1)"
assert_contains "clear refuses a registered project" "registered project" "${OUT}"
"${GO[@]}" project clear "${P}/vendor" >/dev/null
assert_eq "clear drops a plain ignored row" "0" "$(sql "SELECT count(*) FROM projects WHERE path = '~/Projects/vendor'")"

summary
