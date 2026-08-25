#!/usr/bin/env bash
#
# E-2066 verification — Endless's own ids are gone from user-visible text.
#
# Before: 58 `E-`/`ES-`/`ED-` ids reached the screen across 34 commands'
# --help output, ~80 more across five guide pages, and a handful of runtime
# messages. A user runs Endless against their own project, where every one of
# those ids is a cross-reference into a ledger they cannot open.
# After: citations are deleted (or the fact they stood in for is stated
# plainly), examples use the reserved 1-199 example band, and a test walks the
# whole Click tree, the guide and every runtime string so it cannot come back.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-2066-verify.sh
#
# What it proves:
#   1. FAIL-FAST unit gate: this task's own guard suite passes.
#   2. The reported symptom is gone: `endless session --help` carries no id.
#   3. The worst single offender is gone: `endless session order --help`
#      carries no citation and its ordering example still teaches ordering.
#   4. The full recursive help walk, run against the CLI as installed, yields
#      zero out-of-band ids across every command including hidden ones.
#   5. Every `docs/guide/**/*.md` page yields zero out-of-band ids.
#   6. Runtime output — echo/exception/banner strings, Python and Go — is clean.
#   7. The guard BITES: a re-introduced citation fails, an in-band example does
#      not, in help text and in the guide alike.
#   8. Load-bearing rewrites still read correctly — each place an id stood in
#      for a fact now states the fact.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

GUARD_TEST="${WT}/tests/test_no_self_dev_ids.py"
GUIDE_DIR="${WT}/docs/guide"

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    [[ -n "${3:-}" ]] && printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
    return 0
}

setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

TMP_E2066=""
cleanup() { [[ -n "${TMP_E2066}" ]] && rm -rf "${TMP_E2066}"; }
trap cleanup EXIT

[[ -f "${GUARD_TEST}" ]] || setup_error "missing ${GUARD_TEST}"
[[ -d "${GUIDE_DIR}" ]] || setup_error "missing ${GUIDE_DIR}"

# An id outside the reserved 1-199 documentation-example band. Written to match
# the same class the guard matches, independently of the guard's own regex.
OUT_OF_BAND='\b(E|ES|ED)-([2-9][0-9][0-9]|[0-9]{4,})\b'

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# The guard suite IS the regression protection this task ships. If it is red,
# every assertion below is a derived symptom of the same failure.
section "1. Unit gate (fail-fast)"

if uv run pytest -q tests/test_no_self_dev_ids.py >/tmp/e2066-guard.log 2>&1; then
    report_pass "pytest tests/test_no_self_dev_ids.py (help tree, guide, runtime strings)"
else
    report_fail "pytest tests/test_no_self_dev_ids.py" "pass" \
        "failed — see /tmp/e2066-guard.log"
    printf '\n%sFAIL-FAST: guard suite red; later assertions suppressed.%s\n' \
        "${RED}" "${RESET}"
    tail -20 /tmp/e2066-guard.log
    exit 1
fi

# ── 2-3. the reported symptom and the worst offender ────────────────────────
section "2-3. The reported symptom, and the worst single offender"

sess_help=$(uv run endless session --help 2>/tmp/e2066-help.log) \
    || setup_error "could not run 'endless session --help' (see /tmp/e2066-help.log)"

if grep -qE "${OUT_OF_BAND}" <<<"${sess_help}"; then
    report_fail "\`endless session --help\` carries no id" "no match" \
        "$(grep -oE "${OUT_OF_BAND}" <<<"${sess_help}" | tr '\n' ' ')"
else
    report_pass "\`endless session --help\` carries no id (3 before)"
fi

order_help=$(uv run endless session order --help 2>>/tmp/e2066-help.log) \
    || setup_error "could not run 'endless session order --help'"

if grep -qE "${OUT_OF_BAND}" <<<"${order_help}"; then
    report_fail "\`endless session order --help\` carries no id" "no match" \
        "$(grep -oE "${OUT_OF_BAND}" <<<"${order_help}" | tr '\n' ' ')"
else
    report_pass "\`endless session order --help\` carries no id (13 before)"
fi

# Scrubbing must not have taken the teaching with it: the example is the only
# thing in that help that explains what the compact SPEC syntax means.
order_flat=$(tr '\n' ' ' <<<"${order_help}" | tr -s ' ')
if grep -qF -- 'endless session order "E-100 E-101|E-102 E-103"' <<<"${order_flat}" \
   && grep -qF -- 'parallel with E-101' <<<"${order_flat}"; then
    report_pass "the ordering example survived intact and still shows a parallel group"
else
    report_fail "the ordering example survived" \
        'the SPEC example plus its "parallel with" annotation' \
        "$(grep -o 'Example.*' <<<"${order_flat}" | head -c 140)"
fi

# ── 4. the full recursive help walk ─────────────────────────────────────────
# Against the CLI as installed, not only in-process: this is the surface a user
# actually types, hidden commands included.
section "4. Recursive walk of every command's --help"

walk_out=$(uv run python - <<'PY' 2>/tmp/e2066-walk.log
import re, click
from endless.cli import main

BAD = re.compile(r"\b(?:E|ES|ED)-(\d+)")

def walk(cmd, path, parent=None):
    ctx = click.Context(cmd, info_name=path[-1], parent=parent)
    yield " ".join(path), cmd.get_help(ctx)
    if isinstance(cmd, click.Group):
        for name in sorted(cmd.list_commands(ctx)):
            sub = cmd.get_command(ctx, name)
            if sub is not None:
                yield from walk(sub, path + [name], ctx)

n = 0
for invocation, text in walk(main, ["endless"]):
    n += 1
    for line in text.splitlines():
        for m in BAD.finditer(line):
            if int(m.group(1)) > 199:
                print(f"OFFENDER {invocation}: {m.group(0)} :: {line.strip()}")
print(f"WALKED {n}")
PY
) || setup_error "the help walk failed (see /tmp/e2066-walk.log)"

walked=$(grep -oE 'WALKED [0-9]+' <<<"${walk_out}" | awk '{print $2}')
offenders=$(grep -c '^OFFENDER ' <<<"${walk_out}" || true)

if [[ "${walked:-0}" -gt 100 ]]; then
    report_pass "the walk reached ${walked} commands"
else
    report_fail "the walk reached the whole tree" ">100 commands" "${walked:-0}"
fi

if [[ "${offenders}" == "0" ]]; then
    report_pass "0 out-of-band ids across all ${walked} commands (58 before)"
else
    report_fail "0 out-of-band ids across the help tree" "0" \
        "${offenders}: $(grep '^OFFENDER ' <<<"${walk_out}" | head -3)"
fi

# ── 5. the guide ────────────────────────────────────────────────────────────
section "5. \`endless guide\` pages"

guide_hits=$(grep -rnE "${OUT_OF_BAND}" "${GUIDE_DIR}" || true)
if [[ -z "${guide_hits}" ]]; then
    page_count=$(find "${GUIDE_DIR}" -name '*.md' | wc -l | tr -d ' ')
    report_pass "0 out-of-band ids across ${page_count} guide pages (~80 before)"
else
    report_fail "0 out-of-band ids in the guide" "no matches" \
        "$(head -3 <<<"${guide_hits}")"
fi

# The guide is rendered, not cat'd — prove the rendered form is clean too.
for topic in tasks sessions orchestration decisions reference; do
    rendered=$(uv run endless guide "${topic}" 2>/dev/null) || {
        report_fail "endless guide ${topic} renders" "exit 0" "failed"; continue; }
    if grep -qE "${OUT_OF_BAND}" <<<"${rendered}"; then
        report_fail "rendered \`endless guide ${topic}\` is clean" "no match" \
            "$(grep -oE "${OUT_OF_BAND}" <<<"${rendered}" | sort -u | tr '\n' ' ')"
    else
        report_pass "rendered \`endless guide ${topic}\` is clean"
    fi
done

# ── 6. runtime output ───────────────────────────────────────────────────────
# The plan's pre-work scan of echo/exception strings found zero; scrubbing the
# docstrings could not have introduced any, but the scan is re-run rather than
# assumed, and extended to the Go messages that surface through `endless`.
section "6. Runtime messages"

py_runtime=$(uv run python - <<'PY' 2>/dev/null
import ast, re
from pathlib import Path
BAD = re.compile(r"\b(?:E|ES|ED)-(\d+)")
for path in sorted(Path("src/endless").rglob("*.py")):
    tree = ast.parse(path.read_text())
    docs = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Module, ast.ClassDef, ast.FunctionDef,
                             ast.AsyncFunctionDef)):
            b = node.body
            if (b and isinstance(b[0], ast.Expr)
                    and isinstance(b[0].value, ast.Constant)
                    and isinstance(b[0].value.value, str)):
                docs.add(id(b[0].value))
    for node in ast.walk(tree):
        if (isinstance(node, ast.Constant) and isinstance(node.value, str)
                and id(node) not in docs):
            for m in BAD.finditer(node.value):
                if int(m.group(1)) > 199:
                    print(f"{path}:{node.lineno}: {m.group(0)}")
PY
)
if [[ -z "${py_runtime}" ]]; then
    report_pass "no ids in any Python runtime string (echo, exception, banner)"
else
    report_fail "no ids in Python runtime strings" "no matches" \
        "$(head -3 <<<"${py_runtime}")"
fi

# The two Go messages a user can actually reach through an `endless` command.
for probe in \
    'internal/events/commit.go|Auto-commits must land on' \
    'internal/monitor/resume.go|ES- takes an integer session id'; do
    gofile="${probe%%|*}"; needle="${probe##*|}"
    [[ -f "${gofile}" ]] || { report_fail "${gofile} exists" "present" "missing"; continue; }
    msg=$(grep -A2 -F -- "${needle}" "${gofile}")
    if grep -qE "${OUT_OF_BAND}" <<<"${msg}"; then
        report_fail "${gofile} message carries no id" "no match" \
            "$(grep -oE "${OUT_OF_BAND}" <<<"${msg}" | tr '\n' ' ')"
    else
        report_pass "${gofile} message carries no id"
    fi
done

# The shell helpers `endless shell-init` writes into the user's rc are output
# too — their comments are read by whoever installs them.
shell_init=$(uv run endless shell-init 2>/dev/null)
if [[ -n "${shell_init}" ]] && ! grep -qE "${OUT_OF_BAND}" <<<"${shell_init}"; then
    report_pass "\`endless shell-init\` output carries no id (3 before)"
else
    report_fail "\`endless shell-init\` output carries no id" "no match" \
        "$(grep -oE "${OUT_OF_BAND}" <<<"${shell_init}" | tr '\n' ' ')"
fi

# ── 7. the guard bites ──────────────────────────────────────────────────────
# A clean tree passing proves nothing on its own — a guard that can never fail
# reads identically. Re-introduce the exact defect, in both surfaces, in a
# throwaway copy, and require a failure; then require an in-band example NOT to
# fail, or the guard would just ban the examples the docs need.
section "7. The guard bites (regression injection)"

TMP_E2066=$(mktemp -d "${TMPDIR:-/tmp}/e2066.XXXXXX") || setup_error "mktemp failed"

# `mode` is "bites" when the injection must make the guard fail, or "tolerates"
# when it must not — the second is what keeps the guard from banning the
# in-band example ids the docs are written with.
inject_and_check() {
    local mode="$1" label="$2" file="$3" find_s="$4" replace_s="$5" testname="$6"
    if ! grep -qF -- "${find_s}" "${file}"; then
        report_fail "${label}" "an injectable site in ${file}" "'${find_s}' not found"
        return 0
    fi
    cp "${file}" "${TMP_E2066}/backup" || setup_error "could not back up ${file}"
    uv run python "${TMP_E2066}/inject.py" "${file}" "${find_s}" "${replace_s}"

    local guard_passed=0
    uv run pytest -q "tests/test_no_self_dev_ids.py::${testname}" \
        >/tmp/e2066-inject.log 2>&1 && guard_passed=1
    cp "${TMP_E2066}/backup" "${file}" || setup_error "could not restore ${file}"

    if [[ "${mode}" == "bites" && "${guard_passed}" == "0" ]] \
       || [[ "${mode}" == "tolerates" && "${guard_passed}" == "1" ]]; then
        report_pass "${label}"
    else
        report_fail "${label}" \
            "the guard to $([[ ${mode} == bites ]] && echo fail || echo pass)" \
            "it $([[ ${guard_passed} == 1 ]] && echo passed || echo failed)"
    fi
}

cat > "${TMP_E2066}/inject.py" <<'INJECT'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
p.write_text(p.read_text().replace(sys.argv[2], sys.argv[3], 1))
INJECT

inject_and_check bites \
    "a re-introduced citation in a command docstring fails the guard" \
    "src/endless/cli.py" \
    '"""Inspect and clear recorded errors."""' \
    '"""Inspect and clear recorded errors (E-698)."""' \
    test_help_tree_carries_no_self_dev_ids

inject_and_check bites \
    "a re-introduced citation in the guide fails the guard" \
    "docs/guide/tasks.md" \
    '## Adding tasks' \
    '## Adding tasks (E-2066)' \
    test_guide_carries_no_self_dev_ids

inject_and_check bites \
    "a re-introduced citation in a runtime message fails the guard" \
    "src/endless/phrase_cmd.py" \
    "\"verbs are managed via 'endless verb add', not 'phrase add verb'\"" \
    "\"verbs are managed via 'endless verb add' (E-1117), not 'phrase add verb'\"" \
    test_runtime_strings_carry_no_self_dev_ids

inject_and_check tolerates \
    "an in-band example id is accepted, so the docs keep their examples" \
    "docs/guide/tasks.md" \
    '## Adding tasks' \
    '## Adding tasks, e.g. E-142' \
    test_guide_carries_no_self_dev_ids

# Every injection is reverted inline; prove the tree really is back.
if uv run pytest -q tests/test_no_self_dev_ids.py >/dev/null 2>&1; then
    report_pass "every injection was reverted — the guard is green again"
else
    report_fail "every injection was reverted" "guard green again" "still failing"
fi

# ── 8. load-bearing rewrites still read ─────────────────────────────────────
# A trailing "(E-1234)" can just be deleted. Where the id stood IN for a fact,
# deleting alone leaves a dangling sentence — those had to be restated, and a
# restatement that lost the fact is a worse defect than the id was.
section "8. Load-bearing rewrites state the fact"

check_help_phrase() {
    local cmd="$1" needle="$2" label="$3"
    local out
    out=$(uv run endless ${cmd} --help 2>/dev/null | tr '\n' ' ' | tr -s ' ')
    if grep -qF -- "${needle}" <<<"${out}"; then
        report_pass "${label}"
    else
        report_fail "${label}" "'${needle}' in \`endless ${cmd} --help\`" "absent"
    fi
}

check_help_phrase "task unsettled" \
    "Unsettled means modified (uncommitted changes) OR unlanded" \
    "task unsettled states what unsettled means (was: 'Per ED-1540, ...')"
check_help_phrase "task continue" \
    "There is no \`task pause\`" \
    "task continue explains the absent verb (was: 'E-1968 removed task pause')"
check_help_phrase "task start" \
    "Deprecated stub for \`task claim\`, the verb that replaced it" \
    "task start names its successor (was: 'E-1232 rename')"
check_help_phrase "errors record" \
    "cannot write the fault store directly. Shipped rather than Go-only" \
    "errors record reads as one sentence pair (two citations removed mid-sentence)"
check_help_phrase "session task remove" \
    "a claim cannot be dropped. Naming a task this session never touched" \
    "session task remove reads as one sentence pair (was: '(ED-1560).')"
check_help_phrase "db path" \
    "Uses the single global --db: run" \
    "db path keeps its colon-led instruction (was: '(E-1476):')"

check_guide_phrase() {
    local file="$1" needle="$2" label="$3"
    if grep -qF -- "${needle}" "${GUIDE_DIR}/${file}"; then
        report_pass "${label}"
    else
        report_fail "${label}" "'${needle}' in ${file}" "absent"
    fi
}

check_guide_phrase "reference.md" \
    '**Query `live_tasks`, not `tasks`.** A removed task keeps its row' \
    "reference.md: the live_tasks rule kept its emphasis (was: '** (E-1929).')"
check_guide_phrase "reference.md" \
    'and refuses without one. They are not pinned' \
    "reference.md: the --db refusal states the rule, not the ticket that set it"
check_guide_phrase "sessions.md" \
    'Read commands are not implemented yet. Once shipped:' \
    "sessions.md: unshipped reads say so (was: 'tracked under E-1319')"
check_guide_phrase "sessions.md" \
    'The session-status handler already does this; if you add a new event kind' \
    "sessions.md: the deadlock note states the pattern (was: 'E-1315 fixed this')"
check_guide_phrase "orchestration.md" \
    'That is a union of two sub-states which need **opposite fixes**' \
    "orchestration.md: the unsettled union stands alone (was: 'Per ED-1540 that')"
check_guide_phrase "tasks.md" \
    '**`remove` does not delete the row — it marks it removed.**' \
    "tasks.md: the remove rule kept its emphasis (was: '** (E-1929, implementing ED-1547).')"
check_guide_phrase "tasks.md" \
    'This is the same rule' \
    "tasks.md: the duplicates rule points at replaces, not at a ticket"
check_guide_phrase "tasks.md" \
    'is a **second party**. That is the whole fix.' \
    "tasks.md: the minimizer rationale ends on the fact"

# ── summary ─────────────────────────────────────────────────────────────────
section "Summary"
printf '  %s%d passed%s, %s%d failed%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" \
    "$([[ ${FAIL_COUNT} -gt 0 ]] && printf '%s' "${RED}")" "${FAIL_COUNT}" "${RESET}"

if (( FAIL_COUNT > 0 )); then
    printf '\n  Failed:\n'
    for t in "${FAILED_TESTS[@]}"; do [[ -n "${t}" ]] && printf '    - %s\n' "${t}"; done
    exit 1
fi
exit 0
