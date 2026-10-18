#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2373 and records what was true when E-2373
# landed. Edit it only if you ARE E-2373. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2373: minimizer.enabled defaults to false; minimizer.optimizer still
# defaults to true. A project that never set the key gets no report-channel
# SessionStart text and no Stop-hook gate; an explicit opt-in still turns both on.
#
# Drives THIS worktree's `endless-go hook claude` against an isolated database
# (--db-dir under a temp dir) seeded with one registered project, and rewrites
# that project's .endless/config.json between cases. Every "off" case is paired
# with an "on" control, so a hook that fails to run cannot pass as one that
# stayed silent. An "on" Stop is judged by its output naming `task report`, not
# by "decision":"block": every call here lands on one Endless session, whose hold
# budget runs out after two blocks and then releases the turn with a message
# that still names the command.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

root="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "$root" || setup_error "cannot cd to $root"
for tool in go sqlite3 git uv; do
    command -v "$tool" >/dev/null 2>&1 || setup_error "$tool not on PATH"
done

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "$TMP"' EXIT
TMP="$(cd "$TMP" && pwd -P)"

# ── 1. this task's own tests, fail fast ──────────────────────────────────────
section "1. Unit tests (fail fast)"
if out="$(go test ./internal/monitor/ -count=1 -run '^TestMinimizer' 2>&1)"; then
    report_pass "go test ./internal/monitor -run TestMinimizer"
else
    report_fail "go test ./internal/monitor -run TestMinimizer" "ok" "$(printf '%s' "$out" | tail -30)"
    summary
fi
if out="$(uv run pytest -q tests/test_minimizer_gate_cwd.py tests/test_minimizer_loop.py \
        tests/test_report_reminder.py 2>&1)"; then
    report_pass "pytest (minimizer config, loop switches, wind-down reminder)"
else
    report_fail "pytest (minimizer config, loop switches, wind-down reminder)" "ok" \
        "$(printf '%s' "$out" | tail -30)"
    summary
fi
BIN="$TMP/endless-go"
if go build -o "$BIN" ./cmd/endless-go >"$TMP/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (this worktree's hook)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -20 "$TMP/build.log")"
    summary
fi

# ── fixture ──────────────────────────────────────────────────────────────────
# The hook acts only for a supported harness, detected from
# CLAUDE_CODE_ENTRYPOINT; without it the hook exits 0 doing nothing.
hook_bin() { CLAUDE_CODE_ENTRYPOINT=cli "$BIN" --db-dir "$TMP/cfg" hook claude; }
PROJ="$TMP/proj"
mkdir -p "$TMP/cfg" "$TMP/prime" "$PROJ/.endless"
git -C "$TMP" -c init.defaultBranch=main init -q proj || setup_error "git init"
git -C "$PROJ" -c user.email=t@t -c user.name=t commit -q --allow-empty -m init || setup_error "git commit"

printf '{"session_id":"prime","cwd":"%s","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{}}' \
    "$TMP/prime" | hook_bin >/dev/null 2>&1
[[ -f "$TMP/cfg/endless.db" ]] || setup_error "temp database was not created"
sqlite3 "$TMP/cfg/endless.db" "INSERT INTO projects (id, name, path) VALUES (2373, 'e2373', '$PROJ');" \
    || setup_error "seed project"

# set_config BODY — "" removes the file.
set_config() {
    if [[ -z "$1" ]]; then rm -f "$PROJ/.endless/config.json"; else printf '%s\n' "$1" >"$PROJ/.endless/config.json"; fi
}
N=0
session_start() {
    N=$((N + 1))
    printf '{"hook_event_name":"SessionStart","session_id":"e2373-%d","cwd":"%s","source":"startup"}' \
        "$N" "$PROJ" | hook_bin 2>&1
}
stop_hook() {
    printf '{"hook_event_name":"Stop","session_id":"e2373-%d","cwd":"%s","stop_hook_active":false,"last_assistant_message":"Here is the answer."}' \
        "$N" "$PROJ" | hook_bin 2>&1
}

# ── 2. off by default ────────────────────────────────────────────────────────
section "2. No minimizer key: no report channel"
for case in "no config file|" \
            'config without the key|{"name":"e2373"}' \
            'object naming only the optimizer|{"name":"e2373","minimizer":{"optimizer":true}}' \
            'malformed config|{"minimizer": fal'; do
    label="${case%%|*}" body="${case#*|}"
    set_config "$body"
    assert_not_contains "$label: SessionStart gives no report-channel instructions" \
        "endless task report" "$(session_start)"
    assert_eq "$label: the Stop hook says nothing and holds nothing" "" "$(stop_hook)"
done

# ── 3. explicit opt-in still works ───────────────────────────────────────────
section "3. Explicit opt-in turns the channel on"
for case in 'explicit "enabled": true|{"name":"e2373","minimizer":{"enabled":true}}' \
            '"minimizer": true shorthand|{"name":"e2373","minimizer":true}' \
            'legacy "report_gate": true|{"name":"e2373","report_gate":true}'; do
    label="${case%%|*}" body="${case#*|}"
    set_config "$body"
    assert_contains "$label: SessionStart gives the report-channel instructions" \
        "endless task report" "$(session_start)"
    assert_contains "$label: the Stop hook gates the turn on task report" \
        "task report" "$(stop_hook)"
done

# ── 4. what a reader is told ─────────────────────────────────────────────────
section "4. Help and guide describe the new default"
help="$(cd "$TMP/prime" && uv run --project "$root" endless minimizer --help 2>&1)"
assert_contains "minimizer --help: enabled defaults off, optimizer on" \
    "enabled defaults OFF and optimizer defaults ON" "$help"
assert_not_contains "minimizer --help no longer says both default on" "Both switches default ON" "$help"
guide="$(cat docs/guide/tasks.md)"
assert_contains "guide: the channel is off unless a project sets enabled true" \
    'the default unless
`.endless/config.json` sets `{"minimizer": {"enabled": true}}`' "$guide"
assert_contains "guide: it defaults off" "It defaults **off**" "$guide"

summary
