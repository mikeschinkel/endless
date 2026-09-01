#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1859 and records what was true when E-1859
# landed. Edit it only if you ARE E-1859. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1859 verification — the description-sufficiency triager.
#
# Every new task is filed `untriaged`. This feature drains that queue: for each
# task, decide whether its description is already a sufficient spec
# (→ `submitted`) or design work is needed first (→ `unplanned`), and record the
# transition with triager attribution. It runs twice over — detached at file
# time (a latency optimization) and as a periodic background sweep (the
# correctness guarantee).
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1859
#
# Strategy (shape per .endless/tasks/e-1880/verify.sh): a FULLY ISOLATED throwaway
# env (temp XDG_CONFIG_HOME + fresh DB, a temp git project) driving the
# CANDIDATE Python CLI (`.venv/bin/endless`) and the worktree-built
# `bin/endless-go`. Nothing touches the real endless repo or ledger.
#
# The model call is the one thing that cannot be asserted deterministically, so
# a fake `claude` earlier on PATH stands in for it and every check below tests
# the wiring AROUND it. The fake's reply and exit status are driven by
# STUB_REPLY / STUB_EXIT, and one check has it mutate the ledger mid-call to
# reproduce the human-wins race.
#
# What it checks:
#   0. Fail-fast unit front: tests/test_triage.py (which owns the timeout
#      fail-open case — a real 120s timeout is not a shell-test shape), the Go
#      read helpers, the new ActorKind, the job's registration and lease
#      arithmetic, and the canonical status-lifecycle block this task's docs
#      re-synced (asserted HERE, not by chaining another task's suite).
#   1. A stubbed SUBMITTED verdict routes to `submitted`; UNPLANNED to `unplanned`.
#   2. Fail-open: unparseable reply, non-zero exit, and a missing `claude`
#      binary each leave the task `untriaged` and exit 0.
#   3. Idempotency: running the sweep twice transitions each task once.
#   4. The human-wins guard: a task routed by hand DURING the model call is not
#      overwritten.
#   5. Tier-1 tasks (filed straight to `ready`) are never selected.
#   6. `--dry-run` writes nothing.
#   7. The emitted event carries actor.kind=triager and the payload provenance
#      (deciding model + rationale).
#   8. A .endless/templates/triage/sufficiency.md.local.tmpl override wins over
#      the embedded default.
#   9. The E-1486 boundary: triage.py opens no database.
#  10. The job is registered with a lease that exceeds its worst-case sweep.
#  11. The context carries persisted artifacts only — parent, siblings and
#      linked decisions are present; nothing session-scoped is.
#  12. Filing a task triages it without the filing waiting on the model, and
#      ENDLESS_NO_TRIAGE suppresses that automatic path.
#  13. (reopened) The per-task claim serializes the two triage paths, so a
#      second attempt on a task already in flight skips instead of paying for a
#      duplicate model call — and a lapsed claim is reclaimable.
#  14. (reopened) A triage that reaches no verdict records an ERR-0009 fault, so
#      fail-open stops meaning silent.
#  15. (reopened) The sweep interval and the reworded reset message.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

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

# assert_eq DESC GOT WANT
assert_eq() {
    local desc="$1" got="$2" want="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${needle}" "${hay}"
}

# assert_not_contains DESC HAYSTACK NEEDLE
assert_not_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${needle}" "${hay}"
}

REPO_ROOT=""
WORK=""
PROJ=""
EN=""
GO=""
STUB_DIR=""
BASE_PATH=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

# en runs the candidate CLI from inside the temp project, with the fake `claude`
# first on PATH. Automatic file-time triage is off by default (ENDLESS_NO_TRIAGE)
# so each check drives the triager explicitly and nothing races the assertions.
en() { ( cd "${PROJ}" && ENDLESS_NO_TRIAGE=1 "${EN}" "$@" ); }

# en_triaging is `en` with the automatic file-time path ENABLED — used only by
# the check that exercises it.
en_triaging() { ( cd "${PROJ}" && "${EN}" "$@" ); }

# add_task seeds one task and echoes its id. A seeding failure is reported as a
# FAILED check, never swallowed: a silent `return` would skip an entire check and
# render as "nothing to see here" in the summary.
add_task() {
    local out id
    out=$(en task add "$@" 2>&1)
    id=$(printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1)
    if [[ -z "${id}" ]]; then
        report_fail "seed task: endless task add $*" "a task id" "${out}"
        return 1
    fi
    printf '%s\n' "${id}"
}

status_of() {
    en task show "$1" 2>&1 | grep -E '^Status:' | awk '{print $2}'
}

# go_q runs a session-query verb against the throwaway DB. The DB context must
# be threaded explicitly: the script's cwd is the self-dev worktree, so without
# --config-dir the Go binary refuses (E-1429) rather than reading the temp env.
go_q() { "${GO}" --config-dir "${XDG_CONFIG_HOME}/endless" "$@"; }

# The ledger the Go executor appends every event to. Read directly rather than
# through a query surface: the point of check 7 is the WIRE shape.
ledger() { cat "${PROJ}"/.endless/db-ledger/*.jsonl 2>/dev/null; }

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    GO="${REPO_ROOT}/bin/endless-go"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${GO}" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${GO}" >&2; exit 2; }
    # A WRONG binary is worse than a missing one: every Go-side assertion below
    # silently tests some other build, producing a wall of unrelated failures.
    # Both traps below were hit for real while finishing this task.
    #
    # Trap 1 — stale: you edited Go and did not rebuild.
    local newest_go
    newest_go=$(find "${REPO_ROOT}/cmd" "${REPO_ROOT}/internal" -name '*.go' -newer "${GO}" -print -quit 2>/dev/null)
    if [[ -n "${newest_go}" ]]; then
        printf 'ERROR: %s is older than %s — run `just build`\n' "${GO}" "${newest_go}" >&2
        exit 2
    fi
    #
    # Trap 2 — foreign: the worktree bootstrap COPIES main's prebuilt binary
    # into bin/ (see CLAUDE.md), which gives a fresh mtime with stale content,
    # so the timestamp check above sails straight past it. Ask the binary what
    # it can actually do instead — the only check a copy cannot fool.
    local verbs missing_verb v
    verbs=$("${GO}" session-query 2>&1)
    for v in untriaged-tasks triage-context triage-claim triage-release; do
        if ! grep -q -- "${v}" <<<"${verbs}"; then missing_verb="${v}"; break; fi
    done
    if [[ -n "${missing_verb:-}" ]]; then
        printf 'ERROR: %s does not implement `session-query %s`.\n' "${GO}" "${missing_verb}" >&2
        printf '       It is probably main'"'"'s binary copied in by the worktree bootstrap.\n' >&2
        printf '       Rebuild this worktree: just build\n' >&2
        exit 2
    fi
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT

    export XDG_CONFIG_HOME="${WORK}/config"
    export XDG_CACHE_HOME="${WORK}/cache"
    export ENDLESS_AUTO_MIGRATE=1
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    # The fake `claude`. Earlier on PATH than any real one, so no check here can
    # spend a token or depend on a model's judgement. It also carries the seam
    # for the human-wins race (STUB_RACE_STATUS).
    STUB_DIR="${WORK}/stub"
    mkdir -p "${STUB_DIR}"
    cat > "${STUB_DIR}/claude" <<'STUB'
#!/usr/bin/env bash
# Stand-in for `claude -p`. The prompt is the last argument.
prompt="${!#}"
if [[ -n "${STUB_RACE_CMD:-}" ]]; then
    # Reproduce the human-wins race: route the task by hand WHILE the model
    # call is in flight, between triage's context read and its write.
    task_id=$(printf '%s' "${prompt}" | grep -oE '^- ID: E-[0-9]+' | grep -oE 'E-[0-9]+' | head -1)
    [[ -n "${task_id}" ]] && ${STUB_RACE_CMD} "${task_id}" >/dev/null 2>&1
fi
printf '%s\n' "${STUB_REPLY:-SUBMITTED: the description names the exact change.}"
exit "${STUB_EXIT:-0}"
STUB
    chmod +x "${STUB_DIR}/claude"

    BASE_PATH="${STUB_DIR}:${REPO_ROOT}/bin:${REPO_ROOT}/.venv/bin:${PATH}"
    export PATH="${BASE_PATH}"

    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    : > "${PROJ}/README.md"
    git -C "${PROJ}" add README.md
    git -C "${PROJ}" commit -q -m "init"

    en project register "${PROJ}" --infer --name verify1859 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_unit_front() {
    section "0 — fail-fast unit front (Python + Go + doc sync)"
    local out rc

    out=$(cd "${REPO_ROOT}" && uv run pytest tests/test_triage.py -q 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "Python tests/test_triage.py pass (incl. the timeout fail-open case)"
    else
        report_fail "Python tests/test_triage.py pass" "pytest exit 0" "exit ${rc}
${out}"
    fi

    out=$(cd "${REPO_ROOT}" && go test ./internal/monitor/ -run 'Triage|Untriaged' 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Go triage read helpers pass"
    else report_fail "Go triage read helpers pass" "go test exit 0" "exit ${rc}
${out}"; fi

    out=$(cd "${REPO_ROOT}" && go test ./internal/events/ -run 'Validate' 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Go event validation accepts the new triager ActorKind"
    else report_fail "Go event validation accepts the new triager ActorKind" "go test exit 0" "exit ${rc}
${out}"; fi

    out=$(cd "${REPO_ROOT}" && go test ./internal/triagejob/ ./internal/jobs/ ./internal/templatecmd/ 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Go job registration, runner and template render pass"
    else report_fail "Go job registration, runner and template render pass" "go test exit 0" "exit ${rc}
${out}"; fi

    # The status-lifecycle prose this task rewrote lives in three files that must
    # carry the canonical block byte-identically.
    #
    # Asserted HERE rather than by invoking .endless/tasks/e-1648/verify.sh. A
    # landed verify suite is a point-in-time proof, frozen at its own land and
    # UNDEFINED afterward — chaining one makes this suite's result depend on
    # another task's expired assertions, which is exactly how E-1859's suite
    # once reported red for a reason with nothing to do with triage.
    local canonical extracted ok=1 f
    canonical="${REPO_ROOT}/docs/status-lifecycle.mmd"
    if [[ ! -f "${canonical}" ]]; then
        report_fail "canonical status-lifecycle block exists" "${canonical}" "missing"
        return
    fi
    for f in README.md CLAUDE.md docs/guide/index.md; do
        # The embedded copies wrap the canonical text in a ```mermaid fence;
        # strip it so the comparison is against the canonical file's contents.
        # Match the HTML-comment markers specifically. The canonical text ITSELF
        # mentions the marker names in a `%%` comment, so a bare substring match
        # would treat that line as a marker and silently drop it.
        extracted=$(awk '
            /^<!-- BEGIN canonical:docs\/status-lifecycle.mmd/ {grab=1; next}
            /^<!-- END canonical:docs\/status-lifecycle.mmd/    {grab=0}
            grab' "${REPO_ROOT}/${f}" | sed '1{/^```mermaid$/d;}; ${/^```$/d;}')
        if [[ -z "${extracted}" ]]; then
            report_fail "${f} carries the canonical block" "the marked block" "not found"
            ok=0
            continue
        fi
        if [[ "${extracted}" != "$(cat "${canonical}")" ]]; then
            report_fail "${f} matches docs/status-lifecycle.mmd" \
                "byte-identical to the canonical file" "differs"
            ok=0
        fi
    done
    [[ "${ok}" -eq 1 ]] && report_pass "the canonical status-lifecycle block is in sync across all three copies"
}

check_routes_both_ways() {
    section "1 — a verdict routes the task"
    local sufficient insufficient out

    sufficient=$(add_task "Rename the config key" \
        --description "Rename foo to bar in cli.py.") || {
        report_fail "create task" "task id" "${sufficient}"; return; }
    out=$(STUB_REPLY="SUBMITTED: the description names the exact rename." en triage run 2>&1)
    assert_eq "SUBMITTED verdict -> submitted" "$(status_of "${sufficient}")" "submitted"
    assert_contains "the rationale is echoed to the operator" "${out}" "names the exact rename"

    insufficient=$(add_task "Improve the export performance" \
        --description "It is slow.") || {
        report_fail "create second task" "task id" "${insufficient}"; return; }
    STUB_REPLY="UNPLANNED: no approach is named." en triage run >/dev/null 2>&1
    assert_eq "UNPLANNED verdict -> unplanned" "$(status_of "${insufficient}")" "unplanned"
}

check_fail_open() {
    section "2 — fail-open: the task stays untriaged and the exit is 0"
    local t rc out

    t=$(add_task "Fix the crash on empty input" --description "Crashes on empty.") || {
        report_fail "create task" "task id" "${t}"; return; }

    out=$(STUB_REPLY="I think this one is probably fine?" en triage run 2>&1); rc=$?
    assert_eq "unparseable reply: exit 0" "${rc}" "0"
    assert_eq "unparseable reply: still untriaged" "$(status_of "${t}")" "untriaged"

    out=$(STUB_EXIT=1 en triage run 2>&1); rc=$?
    assert_eq "non-zero exit: exit 0" "${rc}" "0"
    assert_eq "non-zero exit: still untriaged" "$(status_of "${t}")" "untriaged"

    # A PATH with the candidate binaries but no `claude` at all.
    out=$( cd "${PROJ}" && env PATH="${REPO_ROOT}/bin:${REPO_ROOT}/.venv/bin:/usr/bin:/bin" \
        ENDLESS_NO_TRIAGE=1 "${EN}" triage run 2>&1 ); rc=$?
    assert_eq "missing claude binary: exit 0" "${rc}" "0"
    assert_eq "missing claude binary: still untriaged" "$(status_of "${t}")" "untriaged"

    # Fail-open is what makes a re-claimed sweep safe, so the task must still
    # be routable once the model comes back.
    STUB_REPLY="SUBMITTED: reproducible with an obvious fix shape." en triage run >/dev/null 2>&1
    assert_eq "recovers once the model is reachable again" "$(status_of "${t}")" "submitted"
}

check_idempotency() {
    section "3 — idempotency: a second sweep transitions nothing again"
    local a b before after

    a=$(add_task "Update the README wording" --description "Fix the typo in line 3.") || return
    b=$(add_task "Remove the dead helper" --description "Delete unused _foo() in db.py.") || return

    STUB_REPLY="SUBMITTED: mechanical and fully specified." en triage run >/dev/null 2>&1
    assert_eq "first sweep routed ${a}" "$(status_of "${a}")" "submitted"
    assert_eq "first sweep routed ${b}" "$(status_of "${b}")" "submitted"

    before=$(ledger | grep -c '"kind":"triager"')
    STUB_REPLY="UNPLANNED: this must never be applied." en triage run >/dev/null 2>&1
    after=$(ledger | grep -c '"kind":"triager"')

    assert_eq "second sweep emitted no new triager event" "${after}" "${before}"
    assert_eq "${a} unchanged by the second sweep" "$(status_of "${a}")" "submitted"
    assert_eq "${b} unchanged by the second sweep" "$(status_of "${b}")" "submitted"
}

check_human_wins() {
    section "4 — the human wins a race against an in-flight call"
    local t

    t=$(add_task "Add the retry backoff" --description "Retry with backoff.") || return
    # The stub routes the task by hand mid-call, then returns the OPPOSITE
    # verdict. The write must be refused, leaving the human's call standing.
    STUB_RACE_CMD="${EN} task submit" \
    STUB_REPLY="UNPLANNED: the triager must lose this race." \
        en triage run >/dev/null 2>&1

    assert_eq "the hand-routed status stands" "$(status_of "${t}")" "submitted"
}

check_tier1_never_selected() {
    section "5 — tier-1 tasks are exempt from triage"
    local t queue

    t=$(add_task "Update the version string" --tier 1) || {
        report_fail "create tier-1 task" "task id" "${t}"; return; }
    assert_eq "tier-1 is filed ready, not untriaged" "$(status_of "${t}")" "ready"

    queue=$("${GO}" session-query untriaged-tasks --limit 50 2>&1)
    assert_not_contains "tier-1 task absent from the triage queue" \
        "${queue}" "\"id\":${t#E-},"
}

check_dry_run() {
    section "6 — --dry-run writes nothing"
    local t out

    t=$(add_task "Remove the stale fixture" --description "Delete tests/fixtures/old.json.") || return
    before=$(ledger | wc -l | tr -d ' ')
    out=$(STUB_REPLY="SUBMITTED: a single file deletion." en triage run --dry-run 2>&1)
    after=$(ledger | wc -l | tr -d ' ')

    assert_contains "the decision is printed" "${out}" "would route to"
    assert_eq "the task is untouched" "$(status_of "${t}")" "untriaged"
    assert_eq "no event was appended" "${after}" "${before}"
}

check_event_attribution() {
    section "7 — the transition is attributed to the triager, with provenance"
    local t event

    t=$(add_task "Add the missing index" --description "Add an index on tasks.status.") || return
    STUB_REPLY="SUBMITTED: names the exact index to add." en triage run >/dev/null 2>&1

    event=$(ledger | grep '"id":"'"${t#E-}"'"' | grep 'task.status_changed' | tail -1)
    assert_contains "actor.kind is triager, not system" "${event}" '"kind":"triager"'
    assert_contains "the transition is untriaged -> submitted" \
        "${event}" '"old_status":"untriaged","new_status":"submitted"'
    assert_contains "the payload names the deciding model" "${event}" '"model":"sonnet"'
    assert_contains "the payload carries the rationale" \
        "${event}" "names the exact index to add"
    assert_contains "the payload names the template used" \
        "${event}" '"template":"triage/sufficiency"'
    # A triager event has no session to attribute to — recording one would be a
    # lie about who decided.
    assert_not_contains "no session is attributed" "${event}" '"session_id"'
}

check_template_override() {
    section "8 — a project template override wins over the embedded default"
    local t rendered

    t=$(add_task "Update the cached token on 401" --description "Refresh the token when a call 401s.") || return
    mkdir -p "${PROJ}/.endless/templates/triage"
    printf 'OVERRIDE-MARKER for E-{{.task_id}}\n' \
        > "${PROJ}/.endless/templates/triage/sufficiency.md.local.tmpl"

    rendered=$( cd "${PROJ}" && "${GO}" session-query triage-context --id "${t#E-}" \
        | "${GO}" template render triage/sufficiency 2>&1 )
    assert_contains "the .local.tmpl override is rendered" "${rendered}" "OVERRIDE-MARKER"
    assert_not_contains "the embedded default is not" "${rendered}" "You are triaging one task"

    rm -rf "${PROJ}/.endless/templates"
}

check_no_python_sqlite() {
    section "9 — the E-1486 boundary: triage.py opens no database"
    local src rc out
    src="${REPO_ROOT}/src/endless/triage.py"
    [[ -f "${src}" ]] || { report_fail "triage.py exists" "${src}" "missing"; return; }

    # Parsed, not grepped: the module docstring NAMES sqlite3 to explain the
    # prohibition, so a substring check would flag its own documentation.
    out=$(cd "${REPO_ROOT}" && uv run python - "${src}" <<'PY' 2>&1
import ast, sys
tree = ast.parse(open(sys.argv[1]).read())
bad = []
for node in ast.walk(tree):
    if isinstance(node, ast.Import):
        bad += [a.name for a in node.names if a.name.split(".")[0] == "sqlite3"]
    elif isinstance(node, ast.ImportFrom):
        if (node.module or "") in ("sqlite3", "endless.db"):
            bad.append(node.module)
        if (node.module or "") == "endless" and any(a.name == "db" for a in node.names):
            bad.append("endless.db")
    elif isinstance(node, ast.Call):
        f = node.func
        if isinstance(f, ast.Attribute) and isinstance(f.value, ast.Name) and f.value.id == "db":
            bad.append(f"db.{f.attr}() at line {node.lineno}")
if bad:
    print("FORBIDDEN:", ", ".join(bad))
    sys.exit(1)
print("clean")
PY
); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "triage.py imports no sqlite3 and calls no db.* helper"
    else
        report_fail "triage.py imports no sqlite3 and calls no db.* helper" \
            "reads via endless-go session-query, writes via emit_event" "${out}"
    fi

    # The reads it uses instead must actually exist on the Go surface.
    out=$("${GO}" session-query 2>&1)
    assert_contains "untriaged-tasks is on the Go read surface" "${out}" "untriaged-tasks"
    assert_contains "triage-context is on the Go read surface" "${out}" "triage-context"
}

check_job_registered() {
    section "10 — the background sweep is registered and leased sanely"
    local out
    out=$(en jobs list 2>&1)
    assert_contains "triage-sufficiency is in the registry" "${out}" "triage-sufficiency"
    # MaxBackoff is not decoration: internal/jobs names model-calling jobs as
    # exactly the case it exists for, so a broken triager decays instead of
    # burning spend every interval.
    assert_contains "the schedule declares a backoff cap" "${out}" "max "
    assert_not_contains "the runner is not suppressed here" "${out}" "jobs suppressed"
}

check_context_is_persisted_artifacts_only() {
    section "11 — the context carries persisted artifacts, and only those"
    local parent child sibling ctx

    parent=$(add_task "Implement the umbrella" --description "The parent spec.") || return
    child=$(add_task "Add the first piece" --parent "${parent}" \
        --description "Piece one.") || return
    sibling=$(add_task "Add the second piece" --parent "${parent}" \
        --description "Piece two.") || return
    en decision add "Fail open on every model failure" --about "${child}" >/dev/null 2>&1

    ctx=$( cd "${PROJ}" && "${GO}" session-query triage-context --id "${child#E-}" 2>&1 )
    assert_contains "the description is present" "${ctx}" '"description":"Piece one."'
    assert_contains "the parent is present" "${ctx}" '"title":"Implement the umbrella"'
    assert_contains "the parent description is present" "${ctx}" '"The parent spec."'
    assert_contains "sibling titles are present" "${ctx}" "Add the second piece"
    assert_not_contains "the task is not its own sibling" \
        "${ctx}" '"siblings":["Add the first piece"'
    assert_contains "linked decisions are present" "${ctx}" "Fail open on every model failure"
    # The exclusion IS the feature: triage must judge what is written down.
    assert_not_contains "no session/transcript field on the wire" "${ctx}" "session"
    assert_not_contains "no transcript field on the wire" "${ctx}" "transcript"
}

check_inline_file_time_path() {
    section "12 — filing a task triages it without waiting on the model"
    local t i st

    # ENDLESS_NO_TRIAGE deliberately NOT set here: this is the one check that
    # exercises the detached file-time path `task add` spawns.
    t=$(en_triaging task add "Rename the internal flag" \
        --description "Rename --foo to --bar." 2>&1 | grep -oE 'E-[0-9]+' | head -1)
    if [[ -z "${t}" ]]; then
        report_fail "file a task with triage enabled" "task id" "no id printed"
        return
    fi

    # Generous: the child is a cold Python start plus two endless-go calls plus
    # the model call, and this check is the one place a slow machine could turn
    # a working feature into a red line.
    for i in $(seq 1 60); do
        st=$(status_of "${t}")
        [[ "${st}" == "submitted" || "${st}" == "unplanned" ]] && break
        sleep 0.5
    done
    st=$(status_of "${t}")
    if [[ "${st}" != "submitted" ]]; then
        # Say WHY, so a failure here is diagnosable instead of just "not yet".
        # A detached child writes nowhere by design, so re-run it in the
        # foreground and show what it says.
        printf '      %sdiagnostic:%s still %s after 30s; foreground run says:\n' \
            "${DIM}" "${RESET}" "${st}"
        printf '      %s\n' "$(en triage run --task "${t}" 2>&1)"
    fi
    assert_eq "the detached triage routed it" "$(status_of "${t}")" "submitted"

    # …and the suppression switch a test suite or bulk import relies on works.
    t=$(add_task "Rename the other flag" --description "Rename --baz to --qux.") || return
    sleep 1
    assert_eq "ENDLESS_NO_TRIAGE suppresses the automatic path" \
        "$(status_of "${t}")" "untriaged"
}

check_claim_serializes_the_paths() {
    section "13 — the per-task claim stops two paths paying for one task"
    local t out held

    t=$(add_task "Add the claim-guarded thing" --description "Add a flag.") || return

    # Hold the claim as some other process, then ask triage to run. It must
    # decline WITHOUT calling the model.
    held=$(go_q session-query triage-claim --id "${t#E-}" --ttl-seconds 300 --owner other-proc 2>&1)
    assert_eq "another process can take the claim" "${held}" "1"

    out=$(STUB_REPLY="SUBMITTED: must never be applied." en triage run --task "${t}" 2>&1)
    assert_contains "triage declines a claimed task" "${out}" "claim"
    assert_eq "the task is untouched" "$(status_of "${t}")" "untriaged"

    # Release it, and the same run now succeeds — proving the skip was the
    # claim and not some unrelated refusal.
    go_q session-query triage-release --id "${t#E-}" --owner other-proc >/dev/null 2>&1
    STUB_REPLY="SUBMITTED: now it may run." en triage run --task "${t}" >/dev/null 2>&1
    assert_eq "releasing the claim unblocks triage" "$(status_of "${t}")" "submitted"

    # A claimant that dies must not wedge the task: a zero-TTL claim is already
    # lapsed, so the next claimant takes it over.
    local t2 lapsed
    t2=$(add_task "Add the lapsed-claim thing" --description "Add another flag.") || return
    go_q session-query triage-claim --id "${t2#E-}" --ttl-seconds 1 --owner dead-proc >/dev/null 2>&1
    sleep 2
    lapsed=$(go_q session-query triage-claim --id "${t2#E-}" --ttl-seconds 300 --owner live-proc 2>&1)
    assert_eq "a lapsed claim is reclaimable" "${lapsed}" "1"
    go_q session-query triage-release --id "${t2#E-}" --owner live-proc >/dev/null 2>&1
}

check_failure_is_recorded() {
    section "14 — a no-verdict triage records a fault instead of vanishing"
    local t out before after

    t=$(add_task "Add the observable-failure thing" --description "Add a thing.") || return

    STUB_REPLY="this is not a verdict at all" en triage run --task "${t}" >/dev/null 2>&1
    assert_eq "the task stays untriaged (still fail-open)" "$(status_of "${t}")" "untriaged"

    # The store deliberately collapses repeats into one incident with an
    # occurrence count, so assert on presence and attribution rather than on a
    # row-count delta — a second failure raises the count, not the row count.
    out=$(en errors show --all 2>&1)
    assert_contains "the failure is recorded as ERR-0009" "${out}" "ERR-0009"
    assert_contains "the incident names the task" "${out}" "${t}"
    assert_contains "the incident names which path failed" "${out}" "triage:"

    # ERR-0009 must be a documented catalog code, not an invented string.
    assert_contains "ERR-0009 is in the catalog" "$(en errors codes 2>&1)" "ERR-0009"
}

check_reopened_constants() {
    section "15 — sweep cadence and the reworded reset message"
    local out t

    out=$(en jobs list 2>&1)
    assert_contains "the sweep runs every 5m" "${out}" "5m0s"

    # Fix 5: the note must name the COST of re-triage, not offer an undo.
    t=$(add_task "Add the reset-message thing" --description "Original description.") || return
    en task approve "${t}" >/dev/null 2>&1
    out=$(en task update "${t}" --description "A materially different description now." 2>&1)
    assert_contains "the note names the model-call cost" "${out}" "one model call"
    assert_not_contains "it no longer reads as an undo offer" "${out}" "to suppress"
}

main() {
    setup

    printf '%sE-1859 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s\n' "${GO}"
    printf '  claude:  %s (stub)\n' "${STUB_DIR}/claude"
    printf '  env:     isolated (%s)\n' "${WORK}"

    check_unit_front
    check_routes_both_ways
    check_fail_open
    check_idempotency
    check_human_wins
    check_tier1_never_selected
    check_dry_run
    check_event_attribution
    check_template_override
    check_no_python_sqlite
    check_job_registered
    check_context_is_persisted_artifacts_only
    check_inline_file_time_path
    check_claim_serializes_the_paths
    check_failure_is_recorded
    check_reopened_constants

    summary
}

main "$@"
