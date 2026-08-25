#!/usr/bin/env bash
#
# E-1956 verification script — `replaced_by` inline with terminal status,
# `obsolete` guarded on shipped work.
#
# One root cause, three symptoms. `obsolete` reads as "never happened", and no
# status display said otherwise, so the fact that shipped work was SUPERSEDED
# lived only in a `replaced_by` relation nobody saw — which made marking a
# shipped task obsolete the tempting move, and losing that history the result.
#
#   1. The supersession now renders inline with any TERMINAL status wherever
#      status is shown: `task show`, `task list`, `session status`, and their
#      --llm/--json modes. Gated on terminal, so every default listing (which
#      excludes terminal statuses) renders exactly as it did before.
#   2. `obsolete` is REFUSED on work that already shipped (unverified/confirmed/
#      assumed/completed) — via `task update` and via `task replace --status
#      obsolete` alike — and points at `task replace`, which records the relation
#      and keeps the earned status. `task replace` therefore no longer flatly
#      defaults to 'obsolete': shipped work holds its status, everything else
#      keeps the historical default.
#   3. `task update --help` advertised 11 of the 13 statuses. Root cause: four
#      hand-maintained copies of the vocabulary. They are now one list
#      (src/endless/statuses.py) — which also un-rejected `submitted`, a status
#      `update_plan` refused while every other surface accepted it.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1956-verify.sh
#
# Fail-fast: the first failed check exits 1. Exit 0 on all-passed, 2 on a setup
# problem.
#
# Isolation: no real DB, ledger or cache is touched. The Go checks run against
# in-memory SQLite; the Python checks run under pytest's own tmp-dir isolation;
# the end-to-end CLI section drives the freshly-built worktree binaries against a
# throwaway XDG_CONFIG_HOME with its own DB, in a throwaway git repo.

set -u

ROOT=$(git rev-parse --show-toplevel) || { echo "not in a git repo" >&2; exit 2; }
cd "${ROOT}" || exit 2

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

TMP=""
cleanup() { [[ -n "${TMP}" ]] && rm -rf "${TMP}"; }
trap 'cleanup' EXIT

pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; }
fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %s%s%s\n' "${DIM}" "$2" "${RESET}"
    exit 1
}
section() { printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"; }

TMP=$(mktemp -d) || fail "mktemp failed"

TASK_CMD=src/endless/task_cmd.py
STATUSES=src/endless/statuses.py

# ── 1. the task's own unit tests, fail-fast ─────────────────────────────────
section "1. Unit tests (fail-fast)"

if go test ./internal/monitor/ ./internal/sessionstatuscmd/ >"${TMP}/gotest.log" 2>&1; then
    pass "go test: monitor, sessionstatuscmd"
else
    sed 's/^/      /' "${TMP}/gotest.log" >&2
    fail "the task's Go unit tests"
fi

if uv run pytest tests/test_replaced_by_inline.py -q >"${TMP}/pytest.log" 2>&1; then
    pass "pytest: tests/test_replaced_by_inline.py"
else
    sed 's/^/      /' "${TMP}/pytest.log" >&2
    fail "the task's Python unit tests"
fi

# The named tests encode this task's contract. Assert they RAN, so deleting one
# cannot turn this section green by absence.
for t in TestSessionStatusRows_ReplacedBy \
         TestSessionStatusRows_ReplacedByMultiple \
         TestSessionStatusRows_ReplacedByIgnoresRemoved \
         TestSessionStatusRowsForSession_ReplacedBy \
         TestParseReplacedBy; do
    go test ./internal/monitor/ -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}" \
        || fail "${t} did not run and pass"
done
pass "each session-status row-query contract test exists and passes by name"

for t in TestReplacedByNote_TerminalGate \
         TestReplacedByNote_Formatting \
         TestRenderReplacedByNote \
         TestRenderReplacedByNoteRespectsWidth \
         TestJSONReplacedByUngated; do
    go test ./internal/sessionstatuscmd/ -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}" \
        || fail "${t} did not run and pass"
done
pass "each session-status render contract test exists and passes by name"

# ── 2. one status vocabulary (the --help defect's root cause) ───────────────
section "2. One status vocabulary"

[[ -f "${STATUSES}" ]] || fail "${STATUSES} is gone" "the four copies would drift again"

# The two the help had dropped. This is the reported defect, asserted directly.
# E-1891 moved the vocabulary itself into Go and made statuses.py a pass-through
# client, so this asks the registry rather than grepping the Python file — the
# literals it used to grep for are gone on purpose.
for s in submitted completed; do
    ./bin/endless-go task-status has all "${s}" \
        || fail "the vocabulary omits '${s}'"
done
pass "the shared vocabulary carries 'submitted' and 'completed'"

# Derived, never typed — a status added to the list must reach every --help for
# free. A re-typed literal list is exactly the failure this replaced.
grep -q 'TASK_STATUS_HELP = "Status: " + ", ".join(TASK_STATUSES)' "${STATUSES}" \
    || fail "the help string is no longer derived from the list"
pass "the --status help string is derived from the list"

grep -q 'if status not in TASK_STATUSES:' "${TASK_CMD}" \
    || fail "update_plan validates against a local copy again" \
            "that copy is what rejected 'submitted' while every other surface accepted it"
pass "update_plan validates against the shared vocabulary"

grep -q '_VALID_STATUSES = frozenset(TASK_STATUSES)' src/endless/session_status_cmd.py \
    || fail "session_status_cmd keeps its own status list"
pass "session_status_cmd validates against the shared vocabulary"

# The hand-typed help strings must be gone from cli.py entirely.
grep -q 'help="Status: untriaged' src/endless/cli.py \
    && fail "a hand-typed --status help string survives in cli.py"
pass "no hand-typed --status help string remains"

# End-to-end: what `--help` actually prints.
help_out=$(uv run endless --db sandbox task update --help 2>&1) \
    || fail "task update --help failed" "${help_out}"
# Click hard-wraps the help column, so match on the flat text.
flat=$(tr -s ' \n' ' ' <<<"${help_out}")
for s in untriaged unplanned submitted ready underway unverified confirmed \
         assumed completed blocked revisit declined obsolete; do
    grep -q "${s}" <<<"${flat}" || fail "task update --help omits '${s}'"
done
pass "task update --help advertises all 13 statuses"

# ── 3. the display gate ─────────────────────────────────────────────────────
section "3. The supersession renders with terminal statuses only"

grep -q 'def replaced_by_note' "${TASK_CMD}" || fail "replaced_by_note is gone"
# E-1891 collapsed `_RELATION_TERMINAL_STATUSES` into `_TERMINAL_STATUSES` —
# they were byte-identical five-status lists under two names, both asking "is
# this task finished or abandoned". The gate is unchanged; only the name is.
grep -q 'status not in _TERMINAL_STATUSES' "${TASK_CMD}" \
    || fail "the Python note is no longer gated on a terminal status" \
            "ungating it would change every default listing's Status column"
pass "the Python note is gated on a terminal status"

# E-1185 moved the gate out of replacedByNote/duplicatesNote and back into the
# one relationNote both call — the copies existed only so this line kept
# matching, which is the wrong reason to shape code. The assertion follows the
# gate rather than the other way round. (E-1891 then pointed isTerminal itself
# at the taskstatus registry; the gate is unchanged.)
grep -q 'if len(ids) == 0 || !isTerminal(status) {' internal/sessionstatuscmd/session_status.go \
    || fail "the Go note is no longer gated on a terminal status"
pass "the Go note is gated on a terminal status"

# Batched, not per-row: this feeds table renderers.
grep -q 'def replaced_by_map' "${TASK_CMD}" || fail "replaced_by_map is gone"
# Same E-1185 refactor as the gate above: replaced_by_map and duplicates_map
# collapsed into one parameterized _relation_map, so the join is spelled with
# the column as an argument. The invariant is unchanged — the far end is joined
# to live_tasks, so a removed replacement is never named as a live successor.
grep -q 'JOIN   live_tasks t ON t.id = td.{other_col}' "${TASK_CMD}" \
    || fail "replaced_by_map no longer joins live_tasks" \
            "a removed replacement would be named as a live successor"
pass "the lookup is batched and skips removed replacements"

# Both Go row queries must read the SAME column through the SAME scanner.
grep -q 'replacedByExpr' internal/monitor/session_status.go \
    || fail "the shared replaced_by SQL expression is gone"
n=$(grep -c 'replacedByExpr' internal/monitor/session_status.go)
(( n >= 3 )) || fail "replacedByExpr is used ${n} time(s)" \
                     "want the definition plus both row queries"
grep -q 'func scanSessionStatusRows' internal/monitor/session_status.go \
    || fail "the shared row scanner is gone" \
            "two hand-written scans is how a column silently mis-scans"
pass "both session-status queries share one column expression and one scanner"

# ── 4. the obsolete guard ───────────────────────────────────────────────────
section "4. The obsolete guard on shipped work"

# E-1891 moved this set into internal/taskstatus as the `shipped` group, so the
# membership is asserted by asking the registry rather than by grepping for the
# literal. The claim is unchanged: current-status-only, over exactly these four.
grep -q '_SHIPPED_STATUSES = statuses.get("shipped")' "${TASK_CMD}" \
    || fail "the shipped-status set is no longer read from the registry"
[[ "$(./bin/endless-go task-status get shipped | tr '\n' ' ')" \
    == "unverified confirmed assumed completed " ]] \
    || fail "the shipped-status set changed" \
            "the guard's scope is current-status-only over exactly these four"
pass "the shipped-status set is the four current statuses"

grep -q 'def _refuse_obsolete_on_shipped_work' "${TASK_CMD}" || fail "the guard is gone"

# A hard gate: no bypass parameter, matching _require_status_allowed_for_type.
# The signature alone, so the docstring's own "no --force" prose can't match.
sig=$(sed -n '/^def _refuse_obsolete_on_shipped_work(/,/^):/p' "${TASK_CMD}")
[[ -n "${sig}" ]] || fail "could not read the guard's signature"
grep -qi 'force\|bypass\|override' <<<"${sig}" \
    && fail "the guard grew a bypass parameter" \
            "the fix is to record the right fact, not to override the check"
pass "the guard is hard — no bypass parameter"

# Both write paths, one rule.
grep -q '_refuse_obsolete_on_shipped_work(item_id, status, row\[0\]\["status"\])' "${TASK_CMD}" \
    || fail "update_plan no longer runs the guard"
grep -q '_refuse_obsolete_on_shipped_work(old_id, status, old_status, via_replace=True)' "${TASK_CMD}" \
    || fail "replace_task no longer runs the guard"
pass "the guard covers both task update and task replace"

# ── 5. end-to-end through the shipped CLI ───────────────────────────────────
section "5. End-to-end (real CLI, throwaway DB)"

PROJ="${TMP}/proj"
mkdir -p "${PROJ}" || fail "mkdir project"
git -C "${PROJ}" init -q -b main >/dev/null 2>&1 || fail "git init"
git -C "${PROJ}" config user.email "verify@example.com"
git -C "${PROJ}" config user.name "Verify"
git -C "${PROJ}" commit -q --allow-empty -m initial >/dev/null 2>&1 || fail "git commit"

export XDG_CONFIG_HOME="${TMP}/config"
export XDG_CACHE_HOME="${TMP}/cache"
export ENDLESS_AUTO_MIGRATE=1
export ENDLESS_NO_TRIAGE=1
export PATH="${ROOT}/bin:${PATH}"
mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

# `e` runs the CLI against the throwaway config dir, from the throwaway project.
e() { (cd "${PROJ}" && uv run --project "${ROOT}" endless "$@"); }

e project register "${PROJ}" --infer --name verifyproj >"${TMP}/reg.log" 2>&1 \
    || { sed 's/^/      /' "${TMP}/reg.log" >&2; fail "project register"; }

e task add "Add a superseded thing" --status ready >/dev/null 2>&1 || fail "task add (old)"
e task add "Add the replacement thing" --status ready >/dev/null 2>&1 || fail "task add (new)"
e task update E-1 --status assumed >/dev/null 2>&1 || fail "task update E-1 --status assumed"

# The confirmed-absent behavior from the task description: this used to succeed
# silently. It must now refuse, and name the path that keeps the history.
out=$(e task update E-1 --status obsolete 2>&1) && \
    fail "setting an assumed task to obsolete still succeeds" \
         "this is the defect E-1956 exists to close"
grep -q "assumed" <<<"${out}" || fail "the refusal does not name the current status" "${out}"
grep -q "task replace" <<<"${out}" || fail "the refusal does not name the remedy" "${out}"
grep -q "replaced_by" <<<"${out}" || fail "the refusal does not name the fact to record" "${out}"
pass "task update --status obsolete is refused on shipped work, with the remedy"

got=$(e task show E-1 --json 2>/dev/null | tr -d ' \n')
grep -q '"status":"assumed"' <<<"${got}" || fail "the refused update mutated the status"
pass "the refused update left the task untouched"

out=$(e task replace E-1 --by E-2 --status obsolete 2>&1) && \
    fail "task replace --status obsolete still succeeds on shipped work"
grep -q "Omit --status" <<<"${out}" || fail "the replace refusal does not name the fix" "${out}"
pass "task replace --status obsolete is refused on shipped work"

e task relations E-1 2>/dev/null | grep -q "E-2" \
    && fail "the refused replace still wrote the relation" \
            "a rejected call must leave no residue"
pass "the refused replace wrote no relation"

# The sanctioned path: relation recorded, shipped status held.
e task replace E-1 --by E-2 >"${TMP}/replace.log" 2>&1 || \
    { sed 's/^/      /' "${TMP}/replace.log" >&2; fail "task replace E-1 --by E-2"; }
got=$(e task show E-1 --json 2>/dev/null | tr -d ' \n')
grep -q '"status":"assumed"' <<<"${got}" || fail "task replace did not hold the shipped status" "${got}"
grep -q '"replaced_by":\["E-2"\]' <<<"${got}" || fail "task replace did not record the relation" "${got}"
pass "task replace holds the shipped status and records the relation"

# The historical default survives for everything that did not ship.
e task add "Add a stale idea" --status unplanned >/dev/null 2>&1 || fail "task add (stale)"
e task replace E-3 --by E-2 >/dev/null 2>&1 || fail "task replace E-3"
e task show E-3 --json 2>/dev/null | tr -d ' \n' | grep -q '"status":"obsolete"' \
    || fail "task replace no longer defaults unshipped work to obsolete"
pass "task replace still defaults unshipped work to obsolete"

# ── 6. the three surfaces actually render it ────────────────────────────────
section "6. task show / task list / session status"

e task show E-1 2>/dev/null | grep -q "^Status:.*assumed (replaced by E-2)" \
    || fail "task show's Status line lacks the supersession"
pass "task show: 'Status:  assumed (replaced by E-2)'"

e task show E-1 --llm 2>/dev/null | grep -q "status=assumed replaced_by=E-2" \
    || fail "task show --llm lacks the supersession on the status line"
pass "task show --llm: 'status=assumed replaced_by=E-2'"

e task list --all 2>/dev/null | grep -q "assumed (replaced by E-2)" \
    || fail "task list's Status column lacks the supersession"
pass "task list: the Status column carries it"

e task list --all --llm 2>/dev/null | grep -q "assumed replaced_by=E-2" \
    || fail "task list --llm lacks the supersession"
pass "task list --llm: it follows the status"

# The default listing must be untouched — nothing terminal is in it, so nothing
# can carry a note, and the Status column keeps its original width.
e task list 2>/dev/null | grep -q "replaced by" \
    && fail "the default task list grew a supersession column"
pass "the default task list is unchanged"

# session status is Go and pane-resolved; --task drives it headless against the
# same throwaway DB.
out=$(cd "${PROJ}" && endless-go session-status --task 1 --all --cols 120 2>&1) \
    || fail "endless-go session-status" "${out}"
grep -q "(replaced by E-2)" <<<"${out}" \
    || fail "session status's row lacks the supersession" "${out}"
pass "session status: the row carries it"

out=$(cd "${PROJ}" && endless-go session-status --task 1 --all --json 2>&1) \
    || fail "endless-go session-status --json" "${out}"
tr -d ' \n' <<<"${out}" | grep -q '"replaced_by":\["E-2"\]' \
    || fail "session status --json lacks replaced_by" "${out}"
pass "session status --json: the row carries it"

# ── 7. documentation ────────────────────────────────────────────────────────
section "7. Documentation"

just guide-check >"${TMP}/guide.log" 2>&1 \
    || { sed 's/^/      /' "${TMP}/guide.log" >&2; fail "just guide-check"; }
pass "guide command→section map is complete and in sync"

# The status table must not still describe `obsolete` as unconditional.
grep -q 'Refused on work that already shipped' docs/guide/index.md \
    || fail "the status table does not mention the obsolete guard"
pass "the status table documents the obsolete guard"

out=$(uv run endless --db sandbox guide tasks 2>/dev/null)
grep -q 'Superseded work is `replaced_by`, never `obsolete`' <<<"${out}" \
    || fail "endless guide tasks does not cover the supersession rule"
grep -q 'task replace <old> --by <new>' <<<"${out}" \
    || fail "the guide does not name the sanctioned command"
pass "endless guide tasks explains superseded-vs-obsolete"

# ── 8. project-wide regression ──────────────────────────────────────────────
section "8. Project-wide regression"

if go vet ./... >"${TMP}/vet.log" 2>&1; then
    pass "go vet ./..."
else
    sed 's/^/      /' "${TMP}/vet.log" >&2
    fail "go vet"
fi

if (cd "${ROOT}" && go build ./...) >"${TMP}/build.log" 2>&1; then
    pass "go build ./..."
else
    sed 's/^/      /' "${TMP}/build.log" >&2
    fail "go build"
fi

if go test ./internal/... >"${TMP}/goall.log" 2>&1; then
    pass "go test ./internal/..."
else
    sed 's/^/      /' "${TMP}/goall.log" >&2
    fail "go test ./internal/..."
fi

if (cd "${ROOT}" && uv run pytest tests -q) >"${TMP}/pyall.log" 2>&1; then
    pass "pytest tests (full suite)"
else
    tail -40 "${TMP}/pyall.log" | sed 's/^/      /' >&2
    fail "the full Python suite"
fi

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
