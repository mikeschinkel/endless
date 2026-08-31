#!/usr/bin/env bash
#
# E-2029 verification — the inter-session channel surface is gone, and nothing
# that survived it is broken.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2029
#
# WHAT LANDED
#   The channel surface was dead: the commands no longer worked, and the
#   `channels` table was keyed on the same non-unique sessions.process string
#   behind E-1898's identity incident — the last consumer of that old key.
#   E-2029 removed all of it:
#
#     CLI          `endless channel {beacon,connect,send,inbox,list,close}`
#                  and src/endless/channel_cmd.py
#     MCP server   `endless-go channel` and internal/channelcmd/ (which was
#                  still shipping server instructions into every live session)
#     Setup        `endless setup channel-plugin` / `remove-channel-plugin`
#                  and setup.py's ~/.claude.json mcpServers writer
#     Go           internal/monitor/messaging.go and the
#                  Register/Unregister/LookupChannelPort helpers
#     Hook         the UserPromptSubmit pending-message banner and the
#                  beacon/connect/send action-regex short-circuit
#     Matchers     the three scope:"channel" DEFAULT_MATCHERS
#     Schema       the channels / conversations / messages tables, dropped at
#                  land by internal/schema/changes/e-2029-drop-channel-tables.sql
#     Deps         github.com/modelcontextprotocol/go-sdk and the
#                  mikeschinkel/go-mcp-sdk fork `replace` — channelcmd was the
#                  module's only consumer of either
#
# WHAT THIS SUITE HAS TO PROVE
#   A removal is easy to half-do. Three distinct failure modes:
#
#     1. The surface still answers. A leftover dispatch case or Click group
#        means the thing is not actually gone. Layer A dials each entry point
#        and requires a real refusal, not a grep of the source.
#     2. Something that survived now references what didn't. A dangling
#        import, a verify suite asserting a deleted Go test, an awk range
#        keyed on a deleted heading. Layer D's project-wide regression is what
#        catches this class, so it is not optional.
#     3. The tables outlive the code. schema.sql no longer declares them, but
#        a populated DB keeps them until the change file runs. Layer C applies
#        the real change file to a real DB that HAS the tables and requires
#        them gone afterward — the file being present is not the claim.
#
#   Absence is asserted behaviorally wherever behavior exists (Layer A, C) and
#   by tree search only where it cannot be (Layer B: deleted files, dropped
#   module requirements).
#
# Layers:
#   A. FAIL-FAST — every removed entry point refuses, and a fresh schema.sql
#      DB has none of the three tables. If these break, stop.
#   B. Tree state — deleted files stay deleted, no live reference to a removed
#      symbol survives, and the MCP SDK is out of go.mod.
#   C. The schema change file, applied for real.
#   D. Project-wide regression — build, vet, go test, Python suite, guide gate.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

REPO_ROOT=""
TMP_DIR=""

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }

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

# assert_refuses DESC EXPECTED_SUBSTRING CMD... — the command must exit
# non-zero AND say why. Exit code alone would pass on an unrelated crash.
assert_refuses() {
    local desc="$1" want="$2"; shift 2
    local output rc
    output=$(cd "${REPO_ROOT}" && "$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 && "${output}" == *"${want}"* ]]; then
        report_pass "${desc}"; return 0
    fi
    report_fail "${desc}" "non-zero exit mentioning '${want}'" \
        "rc=${rc}: $(printf '%s' "${output}" | tail -3)"
    return 1
}

# assert_absent_path DESC RELPATH
assert_absent_path() {
    local desc="$1" rel="$2"
    if [[ ! -e "${REPO_ROOT}/${rel}" ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "${rel} does not exist" "still present"
    return 1
}

# assert_no_match DESC PATTERN PATHSPEC... — no tracked file may match. Uses
# `git grep` so untracked build output and .git/ can never mask or fake a hit.
assert_no_match() {
    local desc="$1" pattern="$2"; shift 2
    local hits
    hits=$(cd "${REPO_ROOT}" && git grep -nE "${pattern}" -- "$@" 2>/dev/null)
    if [[ -z "${hits}" ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "no tracked file matches ${pattern}" \
        "$(printf '%s' "${hits}" | head -3)"
    return 1
}

# assert_cmd DESC CMD... — plain exit-0 check for the regression layer.
assert_cmd() {
    local desc="$1"; shift
    local output rc
    output=$(cd "${REPO_ROOT}" && "$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "$(printf '%s' "${output}" | tail -5)"
    return 1
}

# ─── setup ──────────────────────────────────────────────────────────────────

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
        printf 'setup error: not inside a git repository\n' >&2
        exit 2
    }
    if [[ ! -f "${REPO_ROOT}/internal/schema/changes/e-2029-drop-channel-tables.sql" ]]; then
        printf 'setup error: e-2029-drop-channel-tables.sql not found — is this the E-2029 worktree?\n' >&2
        exit 2
    fi
    for tool in go uv git python3; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'setup error: %s not on PATH\n' "${tool}" >&2
            exit 2
        }
    done
    TMP_DIR=$(mktemp -d) || { printf 'setup error: mktemp failed\n' >&2; exit 2; }
    trap 'rm -rf "${TMP_DIR}"' EXIT
}

# ─── layer A: fail-fast — the surface refuses ───────────────────────────────

layer_a() {
    section "A. The surface is gone"
    note "each entry point dialed for real; a source grep would not prove this"

    # `go run` rather than bin/endless-go: the built binary may be stale, and
    # the claim is about THIS source tree, not whatever was last installed.
    assert_refuses "endless-go channel: unknown subcommand" \
        'unknown subcommand "channel"' \
        go run ./cmd/endless-go channel || return 1

    assert_refuses "endless channel: no such command" \
        "No such command 'channel'" \
        uv run endless channel --help || return 1

    assert_refuses "endless setup channel-plugin: no such command" \
        "No such command 'channel-plugin'" \
        uv run endless setup channel-plugin || return 1

    assert_refuses "endless setup remove-channel-plugin: no such command" \
        "No such command 'remove-channel-plugin'" \
        uv run endless setup remove-channel-plugin || return 1

    # The schema is the other half of "gone": a DB built from schema.sql today
    # must not declare the tables at all. Built in a temp file, so this touches
    # neither the real ledger nor this worktree's sandbox.
    local output rc
    output=$(cd "${REPO_ROOT}" && python3 - "${TMP_DIR}/fresh.db" 2>&1 <<'PY'
import sqlite3, sys, pathlib
db = sqlite3.connect(sys.argv[1])
db.executescript(pathlib.Path("internal/schema/schema.sql").read_text())
have = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
gone = {"channels", "conversations", "messages"}
leftover = sorted(have & gone)
if leftover:
    sys.exit("schema.sql still creates: " + ", ".join(leftover))
# The lookalike must survive — session_messages is transcript history, unrelated.
if "session_messages" not in have:
    sys.exit("session_messages missing — the drop took an unrelated table with it")
PY
)
    rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "a fresh schema.sql DB has no channels/conversations/messages"
    else
        report_fail "a fresh schema.sql DB has no channels/conversations/messages" \
            "none of the three tables created; session_messages intact" \
            "$(printf '%s' "${output}" | tail -3)"
        return 1
    fi

    # The rendered guide is a live surface too: an agent that reads it would
    # otherwise be told to run commands that no longer exist.
    output=$(cd "${REPO_ROOT}" && uv run endless guide orchestration --db main 2>&1)
    if [[ "${output}" != *"endless channel"* ]]; then
        report_pass "the rendered orchestration guide teaches no channel commands"
    else
        report_fail "the rendered orchestration guide teaches no channel commands" \
            "no 'endless channel' in the rendered page" \
            "$(printf '%s' "${output}" | grep -n 'endless channel' | head -3)"
        return 1
    fi

    return 0
}

# ─── layer B: tree state ────────────────────────────────────────────────────

layer_b() {
    section "B. Tree state"
    note "what has no behavior left to dial: deleted files and dropped requires"

    assert_absent_path "internal/channelcmd/ is gone"       internal/channelcmd
    assert_absent_path "monitor/messaging.go is gone"       internal/monitor/messaging.go
    assert_absent_path "monitor/messaging_test.go is gone"  internal/monitor/messaging_test.go
    assert_absent_path "monitor/channels_test.go is gone"   internal/monitor/channels_test.go
    assert_absent_path "src/endless/channel_cmd.py is gone" src/endless/channel_cmd.py
    assert_absent_path "docs/guide/help/channel.md is gone" docs/guide/help/channel.md

    # Every Go symbol the surface exported. A survivor here means some caller
    # was kept alive against a definition that no longer exists — or worse, a
    # definition kept alive with no caller.
    assert_no_match "no reference to a removed Go messaging symbol" \
        'RegisterChannelPort|UnregisterChannelPort|LookupChannelPort|HasPendingMessages|GetPendingMessages|CreateBeacon|ListBeacons|ConnectToConversation|CloseConversation|GetTargetProcess' \
        '*.go'

    assert_no_match "no scope:\"channel\" matcher remains" \
        'scopeChannel|"scope": "channel"' '*.go' '*.py'

    # channelcmd was the module's only consumer of the MCP SDK, so both the
    # requirement and the fork replace go with it.
    assert_no_match "go.mod requires no MCP SDK" \
        'modelcontextprotocol/go-sdk|go-mcp-sdk' go.mod

    # docs/ and demo/ are excluded deliberately: the dated research notes,
    # design brief and talk material are a record of what WAS built, and
    # rewriting them would be falsifying history. Only the guide — the live
    # instructions an agent reads — has to be current, and Layer A checks the
    # rendered page rather than the source.
    assert_no_match "the guide teaches no channel command" \
        'endless channel|channel-plugin' 'docs/guide/*.md' 'docs/guide/help/*.md'
}

# ─── layer C: the schema change, applied ────────────────────────────────────

layer_c() {
    section "C. The schema change file, applied for real"
    note "the tables outlive the code until this runs; presence is not the claim"

    local output rc
    output=$(cd "${REPO_ROOT}" && python3 - "${TMP_DIR}/legacy.db" 2>&1 <<'PY'
import sqlite3, sys, pathlib

db = sqlite3.connect(sys.argv[1])

# A DB as it stood BEFORE this task: the three tables populated, plus the
# pre-E-742 names a DB old enough to predate that rename would still carry.
db.executescript("""
CREATE TABLE channels (process TEXT PRIMARY KEY, port INTEGER, pid INTEGER);
CREATE TABLE conversations (id INTEGER PRIMARY KEY, conversation_id TEXT UNIQUE);
CREATE TABLE messages (id INTEGER PRIMARY KEY, conversation_id TEXT, body TEXT);
CREATE TABLE msg_channels (id INTEGER PRIMARY KEY);
CREATE TABLE msg_queue (id INTEGER PRIMARY KEY);
CREATE TABLE session_messages (id INTEGER PRIMARY KEY, content TEXT);
INSERT INTO channels VALUES ('%5', 9001, 42);
INSERT INTO conversations VALUES (1, 'abc123');
INSERT INTO messages VALUES (1, 'abc123', 'hello');
INSERT INTO session_messages VALUES (1, 'unrelated transcript row');
""")
db.commit()

change = pathlib.Path("internal/schema/changes/e-2029-drop-channel-tables.sql")
db.executescript(change.read_text())
db.commit()

have = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
leftover = sorted(have & {"channels", "conversations", "messages",
                          "msg_channels", "msg_queue"})
if leftover:
    sys.exit("still present after the change: " + ", ".join(leftover))

# The blast radius is the claim, not just the drop: session_messages is a
# different table with a confusable name and must be untouched.
if "session_messages" not in have:
    sys.exit("the change dropped session_messages — wrong table")
if db.execute("SELECT count(*) FROM session_messages").fetchone()[0] != 1:
    sys.exit("session_messages lost rows")

# Idempotent: apply-change is gated by _schema_version, but a re-run must not
# error either, or a partial migration could never be healed by re-running.
db.executescript(change.read_text())
PY
)
    rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "the change drops all five table names and spares session_messages"
    else
        report_fail "the change drops all five table names and spares session_messages" \
            "channels/conversations/messages/msg_channels/msg_queue gone, session_messages intact" \
            "$(printf '%s' "${output}" | tail -3)"
    fi
}

# ─── layer D: project-wide regression ───────────────────────────────────────

layer_d() {
    section "D. Project-wide regression"
    note "a removal breaks its neighbours, not itself — this is where that shows"

    assert_cmd "go build ./..."  go build ./...
    assert_cmd "go vet ./..."    go vet ./...

    # -timeout 20m for the PRE-EXISTING sandboxcmd destroy-test slowness tracked
    # as E-1908, not anything this task introduced. Drop the flag once it lands.
    assert_cmd "go test ./... (-timeout 20m; see E-1908)" go test -timeout 20m ./...

    assert_cmd "just test (Python suite)" just test
    assert_cmd "just guide-check (command -> section map)" just guide-check
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup
    printf '%sE-2029 — the inter-session channel surface is removed%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:  %s\n' "${REPO_ROOT}"

    if ! layer_a; then
        note "fail-fast: the surface is not actually gone; skipping later layers"
        summary
        return 1
    fi
    layer_b
    layer_c
    layer_d

    summary
}

main "$@"
