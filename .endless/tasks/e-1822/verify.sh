#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1822 and records what was true when E-1822
# landed. Edit it only if you ARE E-1822. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1822 verification script — deliver a task's type handoff on a claim into an
# already-running session, not only on spawn.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1822
#
# Single entry point (per E-1596). It ensures binaries are current, runs the Go
# suites that pin both axes fail-fast, then proves the two claims that matter at
# the binary level:
#
#   Axis 1 (content) — the shared handoff/_mechanics partials render the SAME
#   mechanics lines from a per-type spawn wrapper and from the claim wrapper, so
#   the two renderings cannot drift. Spawn output is checked for meaning, not
#   bytes: E-1822 deliberately makes two invariants explicit in the spawn text
#   that were previously implicit (the `--db main` routing and one-session-one-
#   task) and unwraps three hard-wrapped lines.
#
#   Axis 2 (delivery) — a PostToolUse payload for `endless task claim <id>`, fed
#   to the real `endless-go hook claude` binary, comes back with the rendered
#   claim handoff as hookSpecificOutput.additionalContext. Driven against a
#   throwaway --config-dir + seeded DB + fixture project, so it touches neither
#   the real ledger nor the worktree sandbox.
#
# Honest limit (by design): the tests assert the handoff is DELIVERED with the
# right content and shape. They cannot assert a live model READS and OBEYS it —
# additionalContext is a strong nudge, not a hard gate (the same limit E-1803
# documented for the mechanism this rides on). Nothing here needs checking by
# hand.
#
# Model: .endless/tasks/e-1803/verify.sh.

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

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# Filled in by test_delivery_e2e; cleaned up on exit.
E2E_TMP=""

cleanup() {
    [[ -n "${E2E_TMP}" && -d "${E2E_TMP}" ]] && rm -rf "${E2E_TMP}"
    return 0
}
trap cleanup EXIT

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
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_text_contains DESC PATTERN TEXT
assert_text_contains() {
    local desc="$1" pattern="$2" text="$3"
    if [[ "${text}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pattern}" "${text}"
}

# assert_text_lacks DESC PATTERN TEXT
assert_text_lacks() {
    local desc="$1" pattern="$2" text="$3"
    if [[ "${text}" != *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${text}"
}

# ─── helpers ────────────────────────────────────────────────────────────────

# render TYPE TEMPLATE -> the rendered handoff on stdout
render() {
    local typ="$1" template="$2"
    printf '{"spawned_id":9999,"label_prefix":"E-8888/E-9999","title":"Test task","task_type":"%s","worktree_path":"/tmp/wt/e-9999","branch":"task/9999-test","child_count":0,"children_state":"3 ready (3 total)","bg":false}' "${typ}" \
        | ./bin/endless-go template render "${template}" 2>&1
}

# terminal_rule TYPE -> the per-type terminal-status line handoff_terminal emits
terminal_rule() {
    case "$1" in
        research)
            printf '%s' 'When findings are ready: `endless task update E-9999 --status completed --outcome-file <path> --db main`' ;;
        brainstorm)
            printf '%s' 'When the synthesis is ready: `endless task update E-9999 --status completed --outcome-file <path> --db main`' ;;
        epic)
            printf '%s' 'When all children are `confirmed`/`assumed`: `endless task update E-9999 --status completed --db main`' ;;
        *)
            printf '%s' 'When implementation is done: `endless task update E-9999 --status unverified --db main`' ;;
    esac
}

# ─── build + automated suites (fail-fast on the E-1822 unit contract) ────────

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]] \
       || [[ -n "$(find internal/templatecmd/templates -name '*.tmpl' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Fail-fast: the E-1822 contract — the shared partials + claim wrapper
    # (Axis 1) and the PostToolUse delivery (Axis 2).
    assert_succeeds "go test templatecmd (E-1822 claim wrapper + shared mechanics)" \
        go test ./internal/templatecmd/... -run 'TestRender_Claim|TestRender_MechanicsPartial'
    assert_succeeds "go test hookcmd (E-1822 claim handoff delivery)" \
        go test ./internal/hookcmd/... \
        -run 'TestClaimHandoff|TestHandlePostToolUseSession|TestChildrenBreakdown|TestHierarchicalLabelPrefix'

    # Full packages — no regression in the surrounding template/hook logic.
    assert_succeeds "go test ./internal/templatecmd/... (full package)" \
        go test ./internal/templatecmd/...
    assert_succeeds "go test ./internal/hookcmd/... (full package)" \
        go test ./internal/hookcmd/...
}

# ─── Axis 1: the shared partials cannot drift ───────────────────────────────

test_shared_mechanics() {
    section "Axis 1 — spawn and claim render the same mechanics"

    local typ spawn_out claim_out rule
    for typ in todo bugfix research epic brainstorm; do
        spawn_out=$(render "${typ}" "handoff/${typ}")
        claim_out=$(render "${typ}" "handoff/claim")
        rule=$(terminal_rule "${typ}")

        assert_text_contains "${typ}: spawn carries the worktree/--db main mechanics" \
            'Your worktree is: /tmp/wt/e-9999 (branch task/9999-test)' "${spawn_out}"
        assert_text_contains "${typ}: claim carries the worktree/--db main mechanics" \
            'Your worktree is: /tmp/wt/e-9999 (branch task/9999-test)' "${claim_out}"

        assert_text_contains "${typ}: spawn carries one-session-one-task" \
            'Stay focused on E-9999 — one session, one task.' "${spawn_out}"
        assert_text_contains "${typ}: claim carries one-session-one-task" \
            'Stay focused on E-9999 — one session, one task.' "${claim_out}"

        assert_text_contains "${typ}: spawn carries its terminal rule" \
            "${rule}" "${spawn_out}"
        assert_text_contains "${typ}: claim carries the SAME terminal rule" \
            "${rule}" "${claim_out}"

        # A partial that failed to resolve, or a var the caller stopped
        # supplying, shows up as Go's placeholder.
        assert_text_lacks "${typ}: spawn render has no unresolved vars" \
            '<no value>' "${spawn_out}"
        assert_text_lacks "${typ}: claim render has no unresolved vars" \
            '<no value>' "${claim_out}"
    done
}

# ─── Axis 1: the claim wrapper's arrival framing ────────────────────────────

test_claim_arrival_framing() {
    section "Axis 1 — the claim-only arrival framing"

    local out
    out=$(render todo handoff/claim)

    assert_text_contains "names the retrofit (not a fresh spawn)" \
        'already-running' "${out}"
    assert_text_contains "tells the session to relocate into the worktree" \
        '/cd /tmp/wt/e-9999' "${out}"
    assert_text_contains "names the cwd gate that backstops relocation" \
        'cwd gate' "${out}"
    assert_text_contains "tells it to fold existing planning into the task" \
        'endless task update E-9999 --text <path> --db main' "${out}"
    assert_text_contains "ends with the shared close tail" \
        'endless task report E-9999 --db main' "${out}"

    # The spawn-only framing must NOT leak into the claim wrapper — a claimed-in
    # session was not spawned and is not already in the worktree.
    assert_text_lacks "does not claim the session is already in the worktree" \
        "You're already claimed and in the worktree" "${out}"
}

# ─── Axis 2: PostToolUse delivery through the real binary ───────────────────

# seed_e2e_fixture builds a throwaway config dir (DB + fixture project + fixture
# worktree) and echoes its path. Isolated from both the real ledger and the
# worktree sandbox: the hook is invoked with an explicit --config-dir, which
# wins over the main-DB pin.
seed_e2e_fixture() {
    local tmp; tmp=$(mktemp -d "${TMPDIR:-/tmp}/e1822-XXXXXX") || return 1
    local root="${tmp}/project"
    local wt="${root}/.endless/worktrees/e-4242"
    mkdir -p "${wt}" || return 1

    # The project matchers matchers.Load reads — only start/task is needed.
    cat > "${root}/.endless/config.json" <<'JSON'
{"matchers":[{"type":"start","scope":"task","method":"regex",
  "match":"endless\\s+task\\s+claim\\s+(?:[Ee]-)?(\\d+)"}]}
JSON

    git -C "${wt}" init -q --initial-branch=task/4242-fixture >/dev/null 2>&1 || return 1

    python3 - "${tmp}/endless.db" "${root}" internal/schema/schema.sql <<'PY' || return 1
import sqlite3, sys
db_path, root, schema_path = sys.argv[1], sys.argv[2], sys.argv[3]
con = sqlite3.connect(db_path)
con.executescript(open(schema_path).read())
con.execute("INSERT INTO projects (id, name, path) VALUES (1, 'e1822-fixture', ?)", (root,))
# type_id 3 = research, so the per-type branch is observable in the output.
con.execute(
    "INSERT INTO tasks (id, project_id, title, status, type_id) "
    "VALUES (4242, 1, 'Investigate the fixture thing', 'ready', 3)"
)
con.execute(
    "INSERT INTO sessions (id, session_id, project_id, platform, state, started_at, last_activity) "
    "VALUES (1, 'e1822-sess', 1, 'claude', 'working', "
    "'2026-08-01T00:00:00', '2026-08-01T00:00:00')"
)
con.commit()
con.close()
PY
    printf '%s\n' "${tmp}"
}

# fire_hook CONFIG_DIR PROJECT_ROOT COMMAND -> the hook's stdout
fire_hook() {
    local cfg="$1" root="$2" cmd="$3"
    python3 - "${root}" "${cmd}" <<'PY' | ./bin/endless-go --config-dir "${cfg}" hook claude 2>/dev/null
import json, sys
print(json.dumps({
    "session_id": "e1822-sess",
    "cwd": sys.argv[1],
    "hook_event_name": "PostToolUse",
    "tool_name": "Bash",
    "tool_input": {"command": sys.argv[2]},
}))
PY
}

test_delivery_e2e() {
    section "Axis 2 — PostToolUse delivers the handoff (real binary, isolated DB)"

    if ! command -v python3 >/dev/null 2>&1; then
        report_fail "seed e2e fixture" "python3 on PATH" "python3 not found"
        return
    fi

    if ! E2E_TMP=$(seed_e2e_fixture); then
        report_fail "seed e2e fixture" "fixture DB + project + worktree" "seeding failed"
        return
    fi
    local root="${E2E_TMP}/project"
    report_pass "seeded isolated fixture (DB + project + worktree)"

    local out
    out=$(fire_hook "${E2E_TMP}" "${root}" "endless task claim E-4242")

    assert_text_contains "hook responds with a PostToolUse hookSpecificOutput" \
        '"hookEventName":"PostToolUse"' "${out}"
    assert_text_contains "response carries additionalContext" \
        '"additionalContext"' "${out}"
    assert_text_contains "additionalContext is the claim handoff (arrival framing)" \
        'already-running' "${out}"
    assert_text_contains "handoff points at the task's real worktree" \
        ".endless/worktrees/e-4242" "${out}"
    assert_text_contains "handoff carries the resolved branch" \
        'task/4242-fixture' "${out}"
    assert_text_contains "handoff carries the per-type (research) artifact rule" \
        'Findings are the deliverable.' "${out}"
    assert_text_lacks "research claim handoff does not carry the todo terminal rule" \
        '--status unverified' "${out}"

    # The claim itself still happened — delivery is additive, not a replacement.
    local status
    status=$(python3 -c "
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
print(con.execute('SELECT status FROM tasks WHERE id = 4242').fetchone()[0])
" "${E2E_TMP}/endless.db" 2>&1)
    assert_text_contains "the claim still flipped the task to underway" \
        'underway' "${status}"

    # A non-claim Bash call must not deliver a handoff.
    out=$(fire_hook "${E2E_TMP}" "${root}" "endless task show E-4242 --db main")
    assert_text_lacks "a non-claim command delivers no claim handoff" \
        'already-running' "${out}"
}

# ─── docs ───────────────────────────────────────────────────────────────────

test_docs() {
    section "Guide documents the claim-into-a-running-session handoff"
    local guide="docs/guide/orchestration.md"
    if [[ ! -f "${guide}" ]]; then
        report_fail "guide file present" "${guide} exists" "missing"
        return
    fi
    local text; text=$(cat "${guide}")
    assert_text_contains "guide names the claim-into-a-running-session case" \
        "Claiming into a session that's already running" "${text}"
    assert_text_contains "guide names the shared mechanics partial" \
        "_mechanics.tmpl" "${text}"
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

    printf '%sE-1822 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      throwaway (--config-dir), neither main nor sandbox\n'

    test_build_and_suites
    test_shared_mechanics
    test_claim_arrival_framing
    test_delivery_e2e
    test_docs

    summary
}

main "$@"
