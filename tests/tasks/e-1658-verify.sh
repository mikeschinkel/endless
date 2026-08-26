#!/usr/bin/env bash
#
# E-1658 verification — gate task TYPE against verb CATEGORY; replace the boolean
# `completable` verb flag with a `category` set (action | investigation) plus a
# per-task-type `accepts` map.
#
# Model
#   - each verb carries a `category` subset of {action, investigation}; absence
#     defaults to {action} (mirrors the old absence-means-not-completable rule).
#     Genuine duals (design, redesign, document) carry BOTH.
#   - each task type declares accepted categories (task_cmd._TYPE_ACCEPTS):
#       todo, bugfix        -> {action}
#       research, brainstorm -> {investigation}
#       epic                -> exempt (absent from the map; creation gate skipped)
#   - Validity(verb, type) = type.accepts ∩ verb.category ≠ ∅.
#   - creation gate (add_item) blocks a title whose lead-verb category the type
#     does not accept; the completed-status gate is recast onto the same model
#     (`completed` requires the 'investigation' category; epics/brainstorms exempt).
#
# Why the behavior checks run through pytest, not live `endless task add`:
#   the verb registry is resolved as project > machine > DEFAULT_VERBS, and the
#   registered project layer is MAIN's .endless/verbs.jsonl (E-1208). Until this
#   branch lands, that live file still carries the OLD schema, so a live CLI run
#   in the worktree would read stale categories. The pytest suite exercises the
#   real add_item / update_plan / gate code against the migrated DEFAULT_VERBS in
#   a hermetic project (no verbs.jsonl present), which is the faithful pre-land
#   proof. The migrated worktree verbs.jsonl (asserted statically below) becomes
#   MAIN's on land.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1658-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 environment/setup error. Fail-fast on the first failing section boundary is
# NOT enforced (every check runs) so a single run surfaces all regressions.

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

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "$2"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_cmd DESC CMD [ARGS...] — pass iff CMD exits 0.
assert_cmd() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -4 | tr '\n' '⏎')"
    fi
}

# assert_present DESC FILE PATTERN — pass iff PATTERN (fixed string) is in FILE.
assert_present() {
    local desc="$1" file="$2" pattern="$3"
    if grep -qF -- "${pattern}" "${file}" 2>/dev/null; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "expected '${pattern}' in ${file}"
    fi
}

# assert_absent_in DESC FILE PATTERN — pass iff PATTERN (fixed string) NOT in FILE.
assert_absent_in() {
    local desc="$1" file="$2" pattern="$3"
    if grep -qF -- "${pattern}" "${file}" 2>/dev/null; then
        report_fail "${desc}" "stray '${pattern}': $(grep -nF -- "${pattern}" "${file}" | head -2 | tr '\n' '⏎')"
    else
        report_pass "${desc}"
    fi
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_build() {
    section "Build — packages compile and the worktree binary is current"
    assert_cmd "internal/... compiles" go build ./internal/...
    assert_cmd "endless-go builds" go build -o bin/endless-go ./cmd/endless-go
}

test_schema_migration() {
    section "Migration — verb schema is category, not completable"
    # matchers.py: the new lookup replaces the old boolean.
    assert_present "matchers.verb_categories() defined" \
        src/endless/matchers.py "def verb_categories(verb: str) -> frozenset[str]:"
    assert_absent_in "matchers.is_completable_verb() removed (no shim)" \
        src/endless/matchers.py "def is_completable_verb"
    # DEFAULT_VERBS carry categories, not completable.
    assert_present "DEFAULT_VERBS mark investigation verbs" \
        src/endless/matchers.py '"category": ["investigation"]'
    assert_present "DEFAULT_VERBS mark dual verbs (action+investigation)" \
        src/endless/matchers.py '"category": ["action", "investigation"]'
    assert_absent_in "DEFAULT_VERBS drop the completable field" \
        src/endless/matchers.py '"completable": True'
    # The live registry file (this worktree's copy becomes MAIN's on land).
    assert_present "verbs.jsonl carries category" \
        .endless/verbs.jsonl '"category"'
    assert_absent_in "verbs.jsonl drops completable" \
        .endless/verbs.jsonl 'completable'
    # design is the canonical audited dual — present in both code + registry.
    assert_present "design is dual in DEFAULT_VERBS" \
        src/endless/matchers.py '{"value": "design", "definition": "to plan structure or behavior", "category": ["action", "investigation"]}'
    assert_present "design is dual in verbs.jsonl" \
        .endless/verbs.jsonl '{"value": "design", "definition": "to plan structure or behavior", "category": ["action", "investigation"]}'
}

test_gate_wiring() {
    section "Gate — accepts map + creation gate + recast completed gate"
    assert_present "per-type accepts map defined" \
        src/endless/task_cmd.py "_TYPE_ACCEPTS: dict[str, frozenset[str]] = {"
    assert_present "todo accepts action" \
        src/endless/task_cmd.py '"todo":       frozenset({"action"}),'
    assert_present "research accepts investigation" \
        src/endless/task_cmd.py '"research":   frozenset({"investigation"}),'
    assert_present "creation gate defined" \
        src/endless/task_cmd.py "def _require_verb_category_for_type(title: str | None, task_type: str | None):"
    assert_present "creation gate wired into add_item" \
        src/endless/task_cmd.py "_require_verb_category_for_type(title, task_type)"
    assert_present "gate also wired into update_plan (closes the flip bypass)" \
        src/endless/task_cmd.py "_require_verb_category_for_type(effective_title, effective_type)"
    assert_present "completed gate recast onto category" \
        src/endless/task_cmd.py "def _require_investigation_verb_for_completed("
    assert_absent_in "old completable completed-gate removed" \
        src/endless/task_cmd.py "_require_completable_verb_for_completed"
    # epic must NOT be an accepts key (exempt = absent).
    assert_absent_in "epic is exempt (absent from accepts map)" \
        src/endless/task_cmd.py '"epic":       frozenset('
    # verb CLI surfaces category.
    assert_present "verb add exposes --category" \
        src/endless/cli.py 'type=click.Choice(["action", "investigation"]),'
    assert_present "verb list shows a Category column" \
        src/endless/verb_cmd.py "'Category'"
}

test_resolver_fix() {
    section "Resolver — layered field-wise verb resolution (E-2079, absorbed)"
    # _resolved_verbs() must LAYER project+machine+DEFAULT_VERBS with field-wise
    # fall-through, not return the first non-empty source (which let a fresh
    # project's single auto-registered verb shadow all defaults → a front-door
    # lockout once the gate moved to creation time).
    assert_present "resolver appends DEFAULT_VERBS as a bottom layer" \
        src/endless/matchers.py "layers.append(DEFAULT_VERBS)"
    assert_present "resolver merges field-wise (lower layer fills gaps)" \
        src/endless/matchers.py "merged[key].setdefault(field, val)"
    assert_absent_in "resolver no longer returns the first non-empty source" \
        src/endless/matchers.py "    machine_verbs = _load_verbs_list(machine_verbs_path())"
    # Both former routes now share one resolver (unified).
    assert_present "load_all_verbs delegates to the single resolver" \
        src/endless/matchers.py "    return _resolved_verbs()"
    # verbs.jsonl fully migrated — no completable field survives (incl. the
    # 'draft' verb main registered after the branch diverged).
    assert_absent_in "verbs.jsonl has no completable remnant (draft migrated)" \
        .endless/verbs.jsonl 'completable'
}

test_gate_behavior() {
    section "Behavior — gate + recast completed status (hermetic, DEFAULT_VERBS)"
    # These pytest modules exercise the REAL add_item / update_plan / gate code:
    #   - action verb under research refused; investigation accepted
    #   - dual verb (design) accepted under BOTH an action and investigation type
    #   - epic accepted with any verb (gate skipped)
    #   - error names the verb, its category, and the type's accepted categories
    #   - the rewritten completable→category completed-gate tests pass
    #   - E-2079: a fresh project with one auto-registered verb still resolves
    #     investigation defaults, so `Research … --type research` is accepted;
    #     field-wise fall-through keeps a re-registered default's category
    assert_cmd "creation-gate tests pass (refuse/accept/dual/epic/error naming/E-2079)" \
        env PATH="${PWD}/bin:${PATH}" uv run pytest tests/test_verb_category_gate.py -q
    assert_cmd "completed-status tests pass (recast onto category)" \
        env PATH="${PWD}/bin:${PATH}" uv run pytest tests/test_completed_status.py -q
}

test_rebuild_db() {
    section "rebuild-db — the verbs.jsonl rewrite is a cache change, not a WAL upcast"
    # The eventcmd package rebuilds the tasks table from the event ledger. The
    # verbs.jsonl schema rewrite touches neither events nor schema, so a clean
    # reconstruction here proves it is not a WAL upcast.
    assert_cmd "eventcmd rebuild-db tests pass" \
        go test -count=1 ./internal/eventcmd/...
    # Live read-only smoke: replay the worktree ledger (no --confirm = dry run).
    assert_cmd "rebuild-db dry run reconstructs cleanly" \
        ./bin/endless-go event rebuild-db --project-root "${PWD}"
}

test_suites() {
    section "Regression — full Go and Python suites stay green"
    assert_cmd "go test ./... (all packages)" \
        go test -count=1 ./...
    assert_cmd "pytest tests/ (worktree binary on PATH)" \
        env PATH="${PWD}/bin:${PATH}" uv run pytest tests/ -q
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${repo_root}" || exit 2

    command -v go >/dev/null 2>&1 || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    if [[ ! -f "${repo_root}/go.work" ]]; then
        command -v just >/dev/null 2>&1 && just go-work-init >/dev/null 2>&1
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1658 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_build
    test_schema_migration
    test_gate_wiring
    test_resolver_fix
    test_gate_behavior
    test_rebuild_db
    test_suites

    summary
}

main "$@"
