#!/usr/bin/env bash
#
# E-1975 verification — the minimizer's autoresearch loop.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1975-verify.sh
#
# Single entry point (per E-1596). Fail-fast on the unit contracts, then every
# part driven end-to-end through the real CLI -> worktree endless-go -> sandbox
# DB wherever the behavior is observable there.
#
#   0. Build + the automated suites the parts reduce to.
#   1. The objective changed (ED-1557). The shipped prompt licenses REWRITING
#      and deduplication, forbids fabrication, and no longer carries either of
#      the two sentences the design named for deletion.
#   2. The minimizer, run LIVE. E-1953's six properties still hold, and the new
#      one — material the user already has is removed — is measured against a
#      real task plan rather than asserted about the prompt.
#   3. The corpus row grew into a replayable unit: raw draft byte-for-byte,
#      fetched context recorded, variant and task type attributed.
#   4. The bypass. A draft under the threshold is returned untouched and is
#      still corpus.
#   5. Paired A/B. Two variants, one emitted block, two corpus rows, and a Stop
#      gate that accepts ANY OF N while still catching an embellishment.
#   6. The free-form span-scoped vocabulary (ED-1555) through the REAL
#      UserPromptSubmit hook, including the false positives an open vocabulary
#      makes possible.
#   7. `session turn` — the one surviving human surface.
#   8. The judge writes a scored, blind-flagged judgment and closes it out when
#      the user reacts.
#   9. Promotion is a pointer move, and rollback moves it back.
#  10. The config object, its legacy fallback, and the loop's job registration.
#
# Exit 0 on all-passed, 1 on any failure.
#
# Why the hook runs with an explicit --config-dir: `endless-go hook` calls
# PinMainDB (E-1450/E-1429) so hook-fired writes always hit the REAL DB
# regardless of cwd. HasExplicitDBContext is the documented seam for exactly
# this case — an explicit --config-dir beats the main pin, which is what lets a
# test drive the hook against the sandbox instead of the user's real ledger.
#
# Why the GATE-ON cwd is the repo root and the FIXTURE is gate-off, the reverse
# of E-1953: this repo now ships `"minimizer": {"enabled": true}`. E-1953's
# exemption — a session tuning the prompt must not be governed by the prompt it
# edits — lapsed when tuning became automated, so the checkout that hosts the
# loop is the one place it has to run. The throwaway gate-OFF directory under
# the gitignored .endless/tmp/ proves the switch by exercising the REAL
# nearest-config resolution rather than by editing the repo's config and hoping
# the restore runs.
#
# Model: tests/tasks/e-1953-verify.sh.

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

# A fixed synthetic session UUID so the end-to-end path does not depend on this
# script being run from inside a Claude session (it is usually run from a plain
# shell via `esu`).
TEST_UUID="e1975e1975-0000-4000-8000-000000000975"
SANDBOX_CFG=""
SESSION_EID=""
OFF_DIR=""
TMP_DIR=""
REPO_ROOT=""
FIXTURE="tests/fixtures/report-draft.md"

# The task the dedup test reports against. High enough not to collide with
# anything a developer's sandbox already holds.
DEDUP_TASK=197501
# A phrase that appears ONLY in the task plan and in the draft's restatement of
# it. Distinctive enough that a substring check cannot match anything the
# minimizer would legitimately write.
DEDUP_MARKER="quicksilver reconciliation ledger"

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

note() {
    printf '  %s•%s %s\n' "${DIM}" "${RESET}" "$1"
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

# ─── helpers ────────────────────────────────────────────────────────────────

endless() {
    uv run endless --db sandbox "$@"
}

go_sandbox() {
    ./bin/endless-go --config-dir "${SANDBOX_CFG}" "$@"
}

# Run a Python snippet against the SANDBOX ledger.
#
# `uv run python -c` inherits no --db, and a bare DB read from inside a self-dev
# worktree is refused (E-1429). Without this the snippet would raise, print
# nothing, and every assertion over its output would pass vacuously — which is
# exactly how the first draft of this script reported a green rollback that had
# never run.
py() {
    uv run python -c "
from pathlib import Path
from endless import config
config.set_db_context(Path('${SANDBOX_CFG}'))
$1"
}

sql() {
    endless sql "$@" 2>/dev/null
}

sql_write() {
    endless sql "$1" --write >/dev/null 2>&1
}

# One scalar out of `endless sql`'s table rendering.
sql1() {
    sql "$1" | sed -n '3p' | sed 's/^ *//; s/ *$//'
}

# Drive the hook the way a real harness does.
#
# `hook claude` returns immediately — silently, exit 0, no stdout — on an agent
# harness Endless does not support (E-1962). A bare shell is not one, so anything
# impersonating a session has to export that session's environment or the entire
# hook is a no-op and every assertion below it passes vacuously by expecting
# silence and getting it.
#
# This is not hypothetical: without it the suite passes when run from inside a
# Claude session (which exports the variable) and fails 16 assertions from a
# plain terminal, which is where it is actually run.
hook_env() {
    env CLAUDE_CODE_ENTRYPOINT=cli ./bin/endless-go --config-dir "${SANDBOX_CFG}" "$@"
}

# Feed one Stop payload to the hook; echo its stdout (empty when the turn is
# allowed to end). cwd decides whether the project has the gate on.
stop_hook() {
    local cwd="$1" last_msg_json="$2" agent_id="${3:-}"
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"Stop","transcript_path":"","agent_id":"%s","last_assistant_message":%s}' \
        "${TEST_UUID}" "${cwd}" "${agent_id}" "${last_msg_json}" \
        | hook_env hook claude 2>/dev/null
}

# Feed one UserPromptSubmit payload; echo the hook's stdout.
prompt_hook() {
    local prompt_json="$1"
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"UserPromptSubmit","transcript_path":"","prompt":%s}' \
        "${TEST_UUID}" "${REPO_ROOT}" "${prompt_json}" \
        | hook_env hook claude 2>/dev/null
}

# Arm the gate with a checkpoint JSON document.
arm_json() {
    local payload="$1" draft_file="${2:-}"
    sql_write "DELETE FROM session_gates"
    sql_write "UPDATE sessions SET report_bounces=0, report_exempt=0, report_runs=0"
    if [[ -n "${draft_file}" ]]; then
        printf '%s' "${payload}" | go_sandbox session-query relay-checkpoint \
            --json --session-id "${SESSION_EID}" --draft-file "${draft_file}"
    else
        printf '%s' "${payload}" | go_sandbox session-query relay-checkpoint \
            --json --session-id "${SESSION_EID}"
    fi
}

# Pin pairing OFF.
#
# Parts 2, 2b, 4 and 8 assert properties of the MINIMIZER; the paired
# presentation is Part 5's subject and it arms its pairs directly, so it needs
# no sampling. Leaving the rate live made those parts silently sometimes test
# the presentation instead — which is how a preview truncating a fenced code
# block first showed up as "the minimizer stopped preserving code blocks".
pin_no_pairing() {
    sql_write "INSERT INTO minimizer_state (key, value) VALUES ('ab_rate','0')
               ON CONFLICT(key) DO UPDATE SET value='0'"
}

reset_turn() {
    sql_write "DELETE FROM session_gates"
    sql_write "UPDATE sessions SET report_bounces=0, report_exempt=0, report_runs=0"
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

assert_fails() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit != 0" "exit=0 | output=${output}"
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

assert_str_contains() {
    local desc="$1" pattern="$2" haystack="$3"
    if [[ "${haystack}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${pattern}" "${haystack:0:400}"
}

assert_str_not_contains() {
    local desc="$1" pattern="$2" haystack="$3"
    if [[ "${haystack}" != *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${pattern}" "${haystack:0:400}"
}

assert_file_contains() {
    local desc="$1" pattern="$2" file="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${file} contains: ${pattern}" "absent"
}

assert_file_not_contains() {
    local desc="$1" pattern="$2" file="$3"
    if [[ ! -f "${file}" ]] || ! grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${file} does NOT contain: ${pattern}" "still present"
}

# ─── Part 0: build + automated suites ───────────────────────────────────────

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Fail-fast: everything slower below is the end-to-end proof that these
    # units are wired to the real surfaces.
    assert_succeeds "go test: the gate accepts any of N and still catches an append" \
        go test ./internal/hookcmd/... -count=1 -run 'TestRelayVerdict'
    assert_succeeds "go test: the free-form vocabulary and its drift question" \
        go test ./internal/hookcmd/... -count=1 -run 'TestScanSigils|TestDriftNotice'
    assert_succeeds "go test: paired checkpoints, labels, picks, the sample-rate ladder" \
        go test ./internal/monitor/... -count=1 \
        -run 'TestReportCheckpoint|TestPendingReportCheckpoint|TestRecordReportLabels|TestRecordPick|TestNudgeABRate'
    assert_succeeds "go test: the config object and its legacy key" \
        go test ./internal/monitor/... -count=1 -run 'TestMinimizer'
    assert_succeeds "go test: the loop's job is registered and its lease covers a full tick" \
        go test ./internal/minimizerjob/... -count=1

    assert_succeeds "go test ./internal/... (every package)" go test ./internal/... -count=1

    assert_succeeds "pytest the loop (store, vetoes, replay rule, judge, fetch record)" \
        uv run pytest tests/test_minimizer_loop.py -q
    assert_succeeds "pytest the report command (plumbing, bypass, pairing, fail-closed)" \
        uv run pytest tests/test_task_report.py -q
}

# ─── Preflight: the hook is actually live ───────────────────────────────────

# Half the assertions below expect the hook to be SILENT — a turn allowed to
# end, a gate-off project, a subagent. Every one of them passes against a hook
# that does nothing at all, so a dead hook does not fail this suite, it hollows
# it out. This runs first and fails loudly instead.
test_hook_is_live() {
    section "Preflight — the hook responds"

    reset_turn
    local resp
    resp=$(stop_hook "${REPO_ROOT}" '"a reply that never went through the minimizer"')
    if [[ "${resp}" == *'"decision":"block"'* ]]; then
        report_pass "the hook fires and the harness is recognized"
        return
    fi
    report_fail "the hook fires and the harness is recognized" \
        "a block response from a gate-on cwd with no checkpoint" \
        "silence — `hook claude` no-ops on an unsupported harness (E-1962), so \
every 'expect silence' assertion below would pass vacuously. Drive it with \
CLAUDE_CODE_ENTRYPOINT=cli."
}

# ─── Part 1: the objective changed ──────────────────────────────────────────

test_objective() {
    section "Part 1 — the minimizer's objective (ED-1557)"

    local src="src/endless/report_prompts.py"

    # The two sentences the design named for deletion. Both argued for the OLD
    # objective, and leaving either in would have the prompt contradict itself.
    assert_file_not_contains "the deletion-only instruction is gone" \
        "you are deleting, not rewriting" "${src}"
    assert_file_not_contains "the not-shorter instruction is gone" \
        "NOT TO MAKE IT SHORT" "${src}"

    # What replaces them.
    assert_file_contains "rewriting is licensed" "You may REWRITE, not only delete" "${src}"
    assert_file_contains "deduplication is the second objective" \
        "DO NOT TELL THE USER WHAT THEY ALREADY HAVE" "${src}"

    # Deletion-only was safe by construction — an editor that can only remove
    # cannot assert. Licensing a rewrite removes that guarantee, so the
    # anti-fabrication invariant is what makes the licence survivable.
    assert_file_contains "fabrication is an invariant, not a preference" \
        "NEVER INVENT" "${src}"

    # The protection the deleted sentence used to carry, restated positively.
    assert_file_contains "a requested discussion still survives at length" \
        "a long discussion is correct" "${src}"

    # The two objectives can contradict: deduplication says anything the user
    # already has goes, the invariants say a command always survives. Measured
    # with recent replies in context and no precedence stated, the minimizer
    # dropped the table, the code block and the command TOGETHER in 2 of 8 live
    # runs; with it stated, 0 of 8.
    assert_file_contains "the invariants outrank deduplication" \
        "OBJECTIVE TWO NEVER OUTRANKS THE INVARIANTS" "${src}"
    assert_file_contains "and the delete list scopes itself to prose" \
        "PROSE restated from WHAT THE USER ALREADY HAS" "${src}"

    # The fourth input the dedup objective forced.
    local built
    built=$(py "
from endless import report_prompts
p = report_prompts.load_prompts()
print(report_prompts.build_minimize_prompt(p, 'q?', 'DRAFT', 'CONTEXT'))
" 2>/dev/null)
    assert_str_contains "the fetched context reaches the prompt" "CONTEXT" "${built}"
    assert_str_contains "and so does the draft" "DRAFT" "${built}"
    assert_str_not_contains "no placeholder survives unspliced" "{context}" "${built}"
}

# ─── Part 2: the minimizer, live ────────────────────────────────────────────

test_minimizer_live() {
    section "Part 2 — the minimizer, run live over the fixture"

    if ! command -v claude >/dev/null 2>&1; then
        report_fail "claude is on PATH" "a claude binary to run the minimizer" \
            "not found — the only part that can test judgment cannot run"
        return
    fi

    local out
    out=$(endless task report --draft-file "${FIXTURE}" 2>&1)
    if [[ -z "${out}" ]]; then
        report_fail "the minimizer produced output" "non-empty stdout" "empty"
        return
    fi
    report_pass "the minimizer produced output"

    # 1 + 2. Table and code block survive BYTE FOR BYTE. Both are asserted line
    # by line rather than as a blob: a reflowed table that kept every cell would
    # pass a substring check on any single row.
    local table_ok=1 row
    while IFS= read -r row; do
        [[ -z "${row}" ]] && continue
        if [[ "${out}" != *"${row}"* ]]; then table_ok=0; break; fi
    done < <(grep -E '^\|' "${FIXTURE}")
    if [[ "${table_ok}" -eq 1 ]]; then
        report_pass "the markdown table survives byte for byte"
    else
        report_fail "the markdown table survives byte for byte" \
            "every table row present verbatim" "row altered or dropped: ${row}"
    fi

    local code_ok=1 line
    while IFS= read -r line; do
        [[ -z "${line}" ]] && continue
        if [[ "${out}" != *"${line}"* ]]; then code_ok=0; break; fi
    done < <(sed -n '/^```go$/,/^```$/p' "${FIXTURE}")
    if [[ "${code_ok}" -eq 1 ]]; then
        report_pass "the fenced code block survives byte for byte"
    else
        report_fail "the fenced code block survives byte for byte" \
            "every code line present verbatim" "line altered or dropped: ${line}"
    fi

    # 3. The verify command survives — the most expensive thing to lose, because
    # the user cannot reconstruct it.
    assert_str_contains "the verify command survives" \
        'go test ./internal/parser/ -run TestNestedQuotes' "${out}"

    # 4. The direct answer to the direct question survives.
    local answered=0
    [[ "${out}" == *"yes"* || "${out}" == *"Yes"* ]] && answered=1
    if [[ "${answered}" -eq 1 && "${out}" == *"stack"* ]]; then
        report_pass "the direct answer survives"
    else
        report_fail "the direct answer survives" \
            "the yes/stack answer to the question asked" "${out:0:300}"
    fi

    # 5. The narration goes. These are the phrases the generative rule targets
    # and the denylist anchors name.
    local phrase
    for phrase in "reframes the decision" "load-bearing" \
                  "the thing that survives from your instinct" \
                  "It is worth noting" "this is where it gets interesting" \
                  "no stray files" "happy to keep going"; do
        assert_str_not_contains "narration deleted: \"${phrase}\"" "${phrase}" "${out}"
    done

    # 6. Shorter than the input. Necessary, nowhere near sufficient.
    local in_len out_len
    in_len=$(wc -c < "${FIXTURE}" | tr -d ' ')
    out_len=${#out}
    if [[ "${out_len}" -lt "${in_len}" ]]; then
        report_pass "output is shorter than input (${out_len} < ${in_len} bytes)"
    else
        report_fail "output is shorter than input" "< ${in_len} bytes" "${out_len} bytes"
    fi
}

# ─── Part 2b: the dedup objective, measured ─────────────────────────────────

test_dedup_live() {
    section "Part 2b — material the user already has is removed (ED-1557)"

    if ! command -v claude >/dev/null 2>&1; then
        note "skipped: claude is not on PATH"
        return
    fi

    # A real task with a real plan, so the fetch policy has something to pull.
    # Inserted by SQL rather than through `task add` so the fixture is exact and
    # does not depend on triage, phases or defaults.
    sql_write "DELETE FROM tasks WHERE id=${DEDUP_TASK}"
    sql_write "INSERT INTO tasks (id, project_id, title, status, phase, type_id, text)
               VALUES (${DEDUP_TASK}, 1, 'Dedup fixture', 'underway', 'now', 1,
                       'The ${DEDUP_MARKER} is rebuilt nightly from the event stream, and
                        the rebuild is idempotent because every entry carries its own
                        sequence number.')"

    local draft="${TMP_DIR}/dedup-draft.md"
    cat > "${draft}" <<EOF
Yes — the nightly rebuild is safe to re-run.

Some background before I answer, so we are on the same page about the design.
The ${DEDUP_MARKER} is rebuilt nightly from the event stream, and the rebuild is
idempotent because every entry carries its own sequence number. That is what the
plan for this task says, and it is worth restating here so the context is clear.

The specific thing you asked about: re-running the rebuild twice in one night
produces the same ledger, because the second pass sees the same sequence numbers
and writes the same rows.

Run \`just rebuild-ledger\` to try it.
EOF

    local out
    out=$(endless task report "${DEDUP_TASK}" --draft-file "${draft}" 2>&1)

    # The answer the user actually asked for survives.
    assert_str_contains "the answer survives" "sequence number" "${out}"
    assert_str_contains "the command survives" 'just rebuild-ledger' "${out}"

    # The restated plan does not. This is the property ED-1557 exists for, and
    # the only one that cannot be asserted about the prompt instead of the
    # output: the phrase appears in the task plan, so repeating it back is
    # transcription rather than information.
    assert_str_not_contains "the material restated from the task plan is gone" \
        "${DEDUP_MARKER}" "${out}"

    # And the fetch that made that possible is on the record.
    local ctx
    ctx=$(sql1 "SELECT length(COALESCE(fetched_context,'')) FROM session_gates
                WHERE kind_id=2 ORDER BY id DESC LIMIT 1")
    if [[ "${ctx}" =~ ^[0-9]+$ ]] && [[ "${ctx}" -gt 0 ]]; then
        report_pass "the fetched context was recorded (${ctx} bytes)"
    else
        report_fail "the fetched context was recorded" ">0 bytes" "${ctx}"
    fi
    assert_str_contains "and it names the source it came from" "task_plan" \
        "$(sql "SELECT fetched_context FROM session_gates WHERE kind_id=2 ORDER BY id DESC LIMIT 1")"
}

# ─── Part 3: the corpus row is replayable ───────────────────────────────────

test_corpus_row() {
    section "Part 3 — the corpus row grew into a replayable unit"

    reset_turn
    arm_json '{"emitted":"COMBINED","task_type":"todo","context":"[{\"source\":\"task_plan\",\"ok\":true,\"text\":\"plan text\"}]","variants":[{"sanctioned":"only reply","slot":"","variant_hash":"deadbeef","bypassed":false}]}' \
        "${FIXTURE}" >/dev/null

    local got want
    got=$(go_sandbox session-query report-draft --session-id "${SESSION_EID}")
    want=$(cat "${FIXTURE}")
    assert_eq "the raw draft still round-trips byte for byte" "${want}" "${got}"

    assert_eq "the variant that produced it is attributed" "deadbeef" \
        "$(sql1 "SELECT variant_hash FROM session_gates ORDER BY id DESC LIMIT 1")"
    assert_eq "so is the task-type bucket it was drawn from" "todo" \
        "$(sql1 "SELECT task_type FROM session_gates ORDER BY id DESC LIMIT 1")"

    # Replay must serve context from the record, never re-fetch. A row whose
    # context is rebuilt from live state is not a paired comparison.
    local rebuilt
    rebuilt=$(py "
from endless import minimizer_fetch
record = '[{\"source\":\"task_plan\",\"ok\":true,\"text\":\"plan text\"}]'
print(minimizer_fetch.context_from_record(record))
" 2>/dev/null)
    assert_str_contains "context rebuilds from the record alone" "plan text" "${rebuilt}"

    # No draft at all is still distinguishable from an empty one.
    sql_write "DELETE FROM session_gates"
    assert_fails "no persisted draft exits non-zero" \
        go_sandbox session-query report-draft --session-id "${SESSION_EID}"
}

# ─── Part 4: the bypass ─────────────────────────────────────────────────────

test_bypass() {
    section "Part 4 — a draft under the threshold skips the minimizer"

    local draft="${TMP_DIR}/short.md"
    printf 'Landed. Verify with `just test`.\n' > "${draft}"

    local out
    out=$(endless task report --draft-file "${draft}" 2>&1)
    assert_eq "a short draft comes back untouched" 'Landed. Verify with `just test`.' "${out}"

    # It is still corpus. "We let this one through untouched" is exactly the
    # evidence the threshold axis is tuned on — and at design time nothing had
    # ever been under a threshold, so there is no evidence yet at all.
    assert_eq "and it is still recorded, marked bypassed" "1" \
        "$(sql1 "SELECT bypassed FROM session_gates ORDER BY id DESC LIMIT 1")"
}

# ─── Part 5: paired A/B ─────────────────────────────────────────────────────

test_pairing() {
    section "Part 5 — paired A/B, and a gate that accepts any of N"

    arm_json '{"emitted":"COMBINED BLOCK","variants":[{"sanctioned":"reply A","slot":"A","variant_hash":"aaa"},{"sanctioned":"reply B","slot":"B","variant_hash":"bbb"}]}' \
        "${FIXTURE}" >/dev/null

    # Both variants are corpus — the judge scores a minimization, not a
    # presentation — but only one row may be open, because that is what the
    # gate's lookup rests on.
    assert_eq "both variants are corpus rows" "2" \
        "$(sql1 "SELECT count(*) FROM session_gates WHERE kind_id=2")"
    assert_eq "exactly one of them is the open checkpoint" "1" \
        "$(sql1 "SELECT count(*) FROM session_gates WHERE kind_id=2 AND cleared_at IS NULL")"
    assert_eq "they are grouped as a pair" "2" \
        "$(sql1 "SELECT count(*) FROM session_gates WHERE pair_id IS NOT NULL")"
    # A pair is ONE run against the appeal budget: the doubling is the
    # experiment's cost, not a second bite.
    assert_eq "a pair spends one run, not two" "1" \
        "$(sql1 "SELECT report_runs FROM sessions WHERE id=${SESSION_EID}")"

    # The gate accepts the combined block the agent owes AND either variant on
    # its own. Only the first is used by the shipped shape; the other two are
    # what make an AskUserQuestion preview or a TUI a no-op later instead of a
    # schema change.
    local text
    for text in "COMBINED BLOCK" "reply A" "reply B"; do
        arm_json '{"emitted":"COMBINED BLOCK","variants":[{"sanctioned":"reply A","slot":"A"},{"sanctioned":"reply B","slot":"B"}]}' >/dev/null
        assert_eq "the gate accepts \"${text}\"" "" \
            "$(stop_hook "${REPO_ROOT}" "\"${text}\"")"
    done

    # And still catches the habit it exists to catch.
    arm_json '{"emitted":"COMBINED BLOCK","variants":[{"sanctioned":"reply A","slot":"A"},{"sanctioned":"reply B","slot":"B"}]}' >/dev/null
    local resp
    resp=$(stop_hook "${REPO_ROOT}" '"reply B\n\nI also refactored three unrelated files."')
    assert_str_contains "an embellished variant is still blocked" '"decision":"block"' "${resp}"
    # The bounce must quantify the SMALLEST divergence: telling an agent that
    # appended one line to the short option that it added forty teaches the
    # wrong correction.
    assert_str_contains "and quantifies what was actually added" "1 line" "${resp}"

    # The presentation itself: two labelled options, previews, and the question
    # that is the only control over how often they appear.
    local block
    block=$(py "
from endless import report_cmd
print(report_cmd._pair_block('option one text', 'option two text', ''))
" 2>/dev/null)
    assert_str_contains "the block labels option A" "[Option A of B]" "${block}"
    assert_str_contains "the block labels option B" "[Option B of B]" "${block}"
    assert_str_contains "it tells the user how to pick" '`$A`' "${block}"
    assert_str_contains "it points at the full text" "endless session turn" "${block}"
    assert_str_contains "and asks how often they want these" '$MORE' "${block}"
}

# ─── Part 6: the free-form vocabulary ───────────────────────────────────────

labels_of() {
    sql "SELECT token, COALESCE(span,'-') AS span FROM report_labels ORDER BY id"
}

test_vocabulary() {
    section "Part 6 — free-form span-scoped labels (ED-1555)"

    sql_write "DELETE FROM report_labels"
    arm_json '{"emitted":"a reply","variants":[{"sanctioned":"a reply","slot":""}]}' "${FIXTURE}" >/dev/null

    # An invented token, scoped to two spans, is two facts about two places.
    prompt_hook '"$JARGON \"load-bearing\" and \"at its core\" — stop using these"' >/dev/null
    local labels
    labels=$(labels_of)
    assert_str_contains "an invented token is recorded" "JARGON" "${labels}"
    assert_str_contains "scoped to the first span" "load-bearing" "${labels}"
    assert_str_contains "and to the second" "at its core" "${labels}"
    assert_eq "one row per span" "2" "$(sql1 "SELECT count(*) FROM report_labels")"

    # E-1953's columns keep answering as a projection of the first label.
    assert_eq "the legacy label column is mirrored" "jargon" \
        "$(sql1 "SELECT label FROM session_gates WHERE label IS NOT NULL ORDER BY id DESC LIMIT 1")"

    # A bare token is RECORDED now, not refused. Under a fixed vocabulary it
    # carried nothing the user chose; under a free one the token is the account.
    sql_write "DELETE FROM report_labels"
    arm_json '{"emitted":"a reply","variants":[{"sanctioned":"a reply","slot":""}]}' "${FIXTURE}" >/dev/null
    prompt_hook '"$CUT"' >/dev/null
    assert_eq "a bare token is recorded rather than refused" "1" \
        "$(sql1 "SELECT count(*) FROM report_labels WHERE token='CUT'")"

    # Drift is surfaced by ASKING. Auto-merging would silently rewrite what the
    # user said, and the pair it would get wrong is the closest pair.
    local drift
    drift=$(prompt_hook '"$CUTS \"another span\""')
    assert_str_contains "a near-neighbour token raises a merge question" "\$CUTS" "${drift}"
    assert_str_contains "naming the token it clusters with" "\$CUT" "${drift}"
    assert_str_contains "and asking rather than merging" "same thing" "${drift}"

    # The false positives an OPEN vocabulary makes possible. Each of these is a
    # thing a user types constantly, and any of them firing would teach them the
    # signal is unreliable.
    sql_write "DELETE FROM report_labels"
    arm_json '{"emitted":"a reply","variants":[{"sanctioned":"a reply","slot":""}]}' "${FIXTURE}" >/dev/null
    prompt_hook '"CUT the scope down to the parser"' >/dev/null
    prompt_hook '"WRONG: I meant the other file"' >/dev/null
    prompt_hook '"$PATH=/usr/bin"' >/dev/null
    prompt_hook '"run this:\n```sh\n$PATH=/usr/bin\n$HOME/bin/thing\n```\nthanks"' >/dev/null
    assert_eq "prose, assignments and fenced shell all stay inert" "0" \
        "$(sql1 "SELECT count(*) FROM report_labels")"

    # The pick, and the rate ladder the pair's closing question drives.
    arm_json '{"emitted":"COMBINED","variants":[{"sanctioned":"reply A","slot":"A"},{"sanctioned":"reply B","slot":"B"}]}' >/dev/null
    prompt_hook '"$B \"this sentence\""' >/dev/null
    assert_eq "a pick marks the chosen side" "1" \
        "$(sql1 "SELECT picked FROM session_gates WHERE pair_slot='B' ORDER BY id DESC LIMIT 1")"
    assert_eq "and leaves the other unmarked" "0" \
        "$(sql1 "SELECT picked FROM session_gates WHERE pair_slot='A' ORDER BY id DESC LIMIT 1")"
    assert_eq "the pick also records its span as a label" "this sentence" \
        "$(sql1 "SELECT span FROM report_labels WHERE token='B' ORDER BY id DESC LIMIT 1")"

    sql_write "DELETE FROM minimizer_state"
    prompt_hook '"$MORE"' >/dev/null
    local up
    up=$(sql1 "SELECT value FROM minimizer_state WHERE key='ab_rate'")
    prompt_hook '"$LESS"' >/dev/null
    local down
    down=$(sql1 "SELECT value FROM minimizer_state WHERE key='ab_rate'")
    if [[ -n "${up}" && "${up}" != "${down}" ]]; then
        report_pass "\$MORE / \$LESS move the sample rate (${up} -> ${down})"
    else
        report_fail "\$MORE / \$LESS move the sample rate" "two different rungs" \
            "up=${up} down=${down}"
    fi
    pin_no_pairing
}

# ─── Part 7: session turn ───────────────────────────────────────────────────

test_session_turn() {
    section "Part 7 — \`session turn\`, the one surviving human surface"

    arm_json '{"emitted":"COMBINED","variants":[{"sanctioned":"reply A","slot":"A"},{"sanctioned":"reply B","slot":"B"}]}' \
        "${FIXTURE}" >/dev/null

    # The raw draft, verbatim. No diff, no columns — the reviewer keeps the
    # minimized reply in the adjacent pane and compares by eye.
    local got want
    got=$(endless session turn --session "ES-${SESSION_EID}" 2>&1)
    want=$(cat "${FIXTURE}")
    assert_eq "the default prints the most recent raw draft, verbatim" "${want}" "${got}"

    # A/B addresses the pair in front of the user.
    assert_eq "A prints option A" "reply A" \
        "$(endless session turn A --session "ES-${SESSION_EID}" 2>&1)"
    assert_eq "B prints option B" "reply B" \
        "$(endless session turn B --session "ES-${SESSION_EID}" 2>&1)"

    # A paired turn is ONE turn. Counting back must skip replies, not variants.
    assert_fails "a pair counts as one turn, not two" \
        env COLUMNS=200 uv run endless --db sandbox session turn 1 --session "ES-${SESSION_EID}"

    assert_fails "an offset past the history is refused" \
        uv run endless --db sandbox session turn 99 --session "ES-${SESSION_EID}"
    assert_fails "a non-numeric, non-A/B argument is refused" \
        uv run endless --db sandbox session turn zzz --session "ES-${SESSION_EID}"

    # The help text is what makes 0-based read naturally. "N turns back" is
    # obvious where "the Nth turn" is surprising, and this was very nearly
    # mis-designed as 1-based on the strength of the second phrasing alone.
    local help
    help=$(uv run endless --db sandbox session turn --help 2>&1)
    assert_str_contains "the help says 'turns back', never 'the Nth turn'" \
        "TURNS BACK" "${help}"
    assert_str_not_contains "it never says 'the Nth turn'" "Nth turn" "${help}"
    assert_str_contains "and names the base explicitly" "0-based" "${help}"
}

# ─── Part 8: the judge ──────────────────────────────────────────────────────

test_judge() {
    section "Part 8 — the judge scores blind, then is scored against the user"

    if ! command -v claude >/dev/null 2>&1; then
        note "skipped: claude is not on PATH"
        return
    fi

    sql_write "DELETE FROM report_judgments"
    sql_write "DELETE FROM report_labels"
    reset_turn

    # A real draft and a real minimization of it, so the judge has something it
    # can actually score.
    local minimized
    minimized=$(endless task report --draft-file "${FIXTURE}" 2>&1)
    local gate_id
    gate_id=$(sql1 "SELECT id FROM session_gates ORDER BY id DESC LIMIT 1")

    # The judge is a live model call, and it FAILS OPEN by design: an
    # unreachable judge leaves the row unjudged for the next sweep rather than
    # writing a fabricated score. That is correct behavior and it makes a
    # single-shot assertion here flaky, so the sweep is retried before the
    # absence of a row is called a defect — and if it still does not appear, the
    # message says which of the two it was.
    local attempt=0
    while [[ "${attempt}" -lt 3 ]]; do
        env COLUMNS=200 uv run endless --db sandbox minimizer judge --limit 1 >/dev/null 2>&1
        [[ -n "$(sql1 "SELECT id FROM report_judgments WHERE gate_id=${gate_id}")" ]] && break
        attempt=$((attempt + 1))
    done
    if [[ -z "$(sql1 "SELECT id FROM report_judgments WHERE gate_id=${gate_id}")" ]]; then
        report_fail "the judge sweep scored the turn" \
            "a judgment row after up to 3 sweeps" \
            "none — the model call did not return; the loop failed open as designed"
        return
    fi
    report_pass "the judge sweep scored the turn"

    local fidelity blind
    fidelity=$(sql1 "SELECT COALESCE(fidelity,-1) FROM report_judgments WHERE gate_id=${gate_id}")
    blind=$(sql1 "SELECT blind FROM report_judgments WHERE gate_id=${gate_id}")
    if [[ "${fidelity}" =~ ^[0-9]+$ ]]; then
        report_pass "it recorded a fidelity score (${fidelity}/100)"
    else
        report_fail "it recorded a fidelity score" "0-100" "${fidelity}"
    fi
    assert_eq "and the prediction was blind — the user had not reacted" "1" "${blind}"

    # Nothing to agree with yet: a prediction with no outcome is not a miss.
    assert_eq "no agreement is claimed before the user reacts" "" \
        "$(sql1 "SELECT COALESCE(agreed,'') FROM report_judgments WHERE gate_id=${gate_id}")"

    # The user reacts. Now the prediction is scored.
    prompt_hook '"$BLOAT \"the middle section\""' >/dev/null
    # Closing a prediction out is pure DB work (observe_reactions), so unlike
    # the scoring above it cannot fail on a model call.
    assert_succeeds "the sweep closes the prediction out" \
        env COLUMNS=200 uv run endless --db sandbox minimizer judge --limit 1
    local agreed
    agreed=$(sql1 "SELECT COALESCE(agreed,'') FROM report_judgments WHERE gate_id=${gate_id}")
    if [[ "${agreed}" == "0" || "${agreed}" == "1" ]]; then
        report_pass "the prediction is now scored against what the user did (agreed=${agreed})"
    else
        report_fail "the prediction is scored against what the user did" "0 or 1" "${agreed}"
    fi

    # Calibration says "not yet" rather than a number on a handful of samples.
    # "Not calibrated" and "doing badly" are different statements.
    local status
    status=$(env COLUMNS=200 uv run endless --db sandbox minimizer status 2>&1)
    assert_str_contains "status reports calibration honestly" "not yet calibrated" "${status}"
    assert_str_contains "and prints keep-ratio by draft size" "Keep-ratio by draft size" "${status}"
    assert_str_contains "labelled as an alarm rather than a verdict" "never a verdict" "${status}"
}

# ─── Part 9: promotion is a pointer move ────────────────────────────────────

test_promotion() {
    section "Part 9 — promotion is a pointer move, and rollback moves it back"

    sql_write "DELETE FROM minimizer_champions"
    sql_write "DELETE FROM minimizer_variants"

    # Seed the champion from the shipped defaults, then hand-promote a child so
    # the rollback path is exercised without spending a replay round.
    local seed
    seed=$(py "
from endless import minimizer_store
print(minimizer_store.champion('')['hash'])
" 2>/dev/null)
    if [[ "${seed}" =~ ^[0-9a-f]{16}$ ]]; then
        report_pass "a fresh install seeds its champion from the shipped default (${seed})"
    else
        report_fail "a fresh install seeds its champion from the shipped default" \
            "a 16-hex content hash" "${seed}"
    fi
    assert_eq "the seed is recorded as such" "seed" \
        "$(sql1 "SELECT promoted_by FROM minimizer_champions WHERE task_type=''")"

    local child
    child=$(py "
from endless import minimizer_store
champ = minimizer_store.champion('')
h = minimizer_store.save_variant(
    task_type='', prompt_text='challenger {prompt} {context} {draft} {denylist}',
    fetch_policy=champ['fetch_policy'], bypass_threshold=champ['bypass_threshold'],
    parent_hash=champ['hash'], origin='generated', note='verify fixture')
minimizer_store.promote('', h, by='replay', note='verify fixture')
print(h)
" 2>/dev/null)
    assert_eq "promotion moves the pointer" "${child}" \
        "$(sql1 "SELECT hash FROM minimizer_champions WHERE task_type=''")"

    assert_succeeds "rollback moves it back" \
        env COLUMNS=200 uv run endless --db sandbox minimizer rollback
    assert_eq "the champion is the parent again" "${seed}" \
        "$(sql1 "SELECT hash FROM minimizer_champions WHERE task_type=''")"

    # Nothing was rewritten, so nothing has to be reconstructed. That is the
    # property that makes promoting without asking the user safe.
    assert_eq "both variants survive the rollback" "2" \
        "$(sql1 "SELECT count(*) FROM minimizer_variants")"

    # And there is nowhere further back to go.
    assert_fails "rollback at the seed is refused, not silently no-op" \
        env COLUMNS=200 uv run endless --db sandbox minimizer rollback

    local variants
    variants=$(env COLUMNS=200 uv run endless --db sandbox minimizer variants 2>&1)
    assert_str_contains "variants lists the lineage" "parent=" "${variants}"
    assert_str_contains "and marks which one is in force" "* = champion" "${variants}"
}

# ─── Part 10: the switches and the job ──────────────────────────────────────

test_switches_and_job() {
    section "Part 10 — the config object, its legacy key, and the loop's job"

    # This repo now ships the gate ON. E-1953's exemption lapsed when tuning
    # became automated: the checkout that hosts the loop is the one place it has
    # to run.
    assert_file_contains "this repo ships the minimizer enabled" \
        '"enabled": true' .endless/config.json
    assert_file_not_contains "and no longer carries the retired scalar" \
        "report_gate" .endless/config.json

    # The gate-OFF fixture proves nearest-config resolution in the harder
    # direction: it declares false BELOW a repo that declares true.
    assert_file_contains "the fixture declares it OFF below a repo that says ON" \
        '"enabled": false' "${OFF_DIR}/.endless/config.json"

    reset_turn
    assert_eq "a gate-off cwd does not block an unreported turn" "" \
        "$(stop_hook "${OFF_DIR}" '"A long answer that never went through the minimizer."')"

    # The same payload from the gate-ON repo root IS blocked — same session,
    # same message, only cwd differs, which isolates the switch as the cause.
    reset_turn
    assert_str_contains "the same turn from the gate-on root is blocked" \
        '"decision":"block"' \
        "$(stop_hook "${REPO_ROOT}" '"A long answer that never went through the minimizer."')"

    # The old scalar still answers, or the rename would silently re-enable a
    # gate a project had switched off — the one migration failure the user
    # cannot see.
    local legacy
    legacy=$(py "
import json, pathlib, tempfile
d = pathlib.Path(tempfile.mkdtemp())
(d / '.endless').mkdir()
(d / '.endless' / 'config.json').write_text(json.dumps({'report_gate': False}))
cfg = config.project_minimizer_config(d)
print(cfg['enabled'], cfg['optimizer'])
" 2>/dev/null)
    assert_eq "the retired scalar still turns the gate off" "False True" "${legacy}"

    # The loop rides the existing fire-once runner. A job that compiles but never
    # registers is silently dead, and every other symptom looks fine.
    assert_str_contains "the loop's job is registered with the runner" \
        "minimizer-loop" "$(go_sandbox jobs list 2>&1)"
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${TMP_DIR}" && -d "${TMP_DIR}" ]] && rm -rf "${TMP_DIR}"
    [[ -n "${DEDUP_TASK}" ]] && sql_write "DELETE FROM tasks WHERE id=${DEDUP_TASK}"
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    SANDBOX_CFG="$(uv run endless db path --db sandbox 2>/dev/null | xargs dirname)"
    if [[ -z "${SANDBOX_CFG}" || ! -d "${SANDBOX_CFG}" ]]; then
        printf 'ERROR: cannot resolve the sandbox config dir; run `just dev-sandbox-init`\n' >&2
        exit 2
    fi

    # A sandbox created before this branch predates the E-1975 columns:
    # schema.sql declares them but CREATE TABLE IF NOT EXISTS silently no-ops
    # against an existing table, so an ALTER only reaches a populated DB through
    # its change file. Applying it here is idempotent (the _schema_version marker
    # gates a re-run) and is what the real DB gets at land time.
    if ! sqlite3 "${SANDBOX_CFG}/endless.db" 'PRAGMA table_info(session_gates)' 2>/dev/null \
        | grep -q emitted_text; then
        ./bin/endless-go --config-dir "${SANDBOX_CFG}" \
            event apply-change internal/schema/changes/e-1975-minimizer-loop.sql >/dev/null 2>&1
    fi

    # The gate-OFF cwd. Under .endless/tmp/ because that path is gitignored —
    # the sanctioned home for throwaway agent-authored content — and inside the
    # repo so project resolution still finds this project.
    TMP_DIR="${REPO_ROOT}/.endless/tmp/e-1975"
    OFF_DIR="${TMP_DIR}/gate-off"
    trap cleanup EXIT
    mkdir -p "${OFF_DIR}/.endless"
    printf '{"minimizer": {"enabled": false}}\n' > "${OFF_DIR}/.endless/config.json"

    printf '%sE-1975 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:      %s\n' "${REPO_ROOT}"
    printf '  db:       sandbox (%s)\n' "${SANDBOX_CFG}"
    printf '  gate off: %s\n' "${OFF_DIR}"

    test_build_and_suites

    SESSION_EID=$(go_sandbox session-query ensure-claude-id \
        --session-id "${TEST_UUID}" --project-root "${REPO_ROOT}" 2>/dev/null)
    if [[ ! "${SESSION_EID}" =~ ^[0-9]+$ ]]; then
        printf '\nERROR: could not create a sandbox session row (got %q)\n' "${SESSION_EID}" >&2
        exit 2
    fi
    export ENDLESS_SESSION_ID="${SESSION_EID}"
    pin_no_pairing

    test_hook_is_live
    test_objective
    test_minimizer_live
    test_dedup_live
    test_corpus_row
    test_bypass
    test_pairing
    test_vocabulary
    test_session_turn
    test_judge
    test_promotion
    test_switches_and_job

    summary
}

main "$@"
