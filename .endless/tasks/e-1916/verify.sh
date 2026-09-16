#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1916 and records what was true when E-1916
# landed. Edit it only if you ARE E-1916. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1916: the prohibition on editing and running an already-landed task's
# verification suite moves out of documentation and into the PreToolUse hook.
#
# WHAT LANDED
#   Two arms in internal/hookcmd/verify_suite.go, wired into handlePreToolUse
#   beside the gates they most resemble:
#
#     Arm 1  Write/Edit/NotebookEdit anywhere inside .endless/tasks/e-NNNN/ is
#            refused when E-NNNN has LANDED and is not this session's task.
#     Arm 2  a Bash call that directly EXECUTES a script in such a directory —
#            `./x`, `x`, `bash x`, `sh -e x`, an absolute path — is refused on
#            the same predicate.
#
#   The rule itself is old. It is in .endless/tasks/CLAUDE.md and in `endless
#   guide orchestration`, and it was broken three times in one session (E-1901)
#   by a session whose handoff named the guide, then twice more in E-1889 by a
#   session that had read the same principle in its own task's analysis field on
#   its first tool call. Documentation is the wrong layer for a rule that must
#   be read to be obeyed.
#
#   Two clauses the plan named are deliberately NOT here, and both are
#   reconciliations rather than omissions:
#
#     • `endless task verify <id>` and `just verify <id>`. E-2023 refuses a
#       foreign landed suite inside the runner, where the id is a resolved
#       argument rather than something inferred from a command string. One rule
#       implemented twice against the same landed-ness lookup is a rule that can
#       drift.
#     • the `tests/tasks/e-NNNN-verify.sh` script layout. E-2023 retired it; the
#       plan said whichever task landed second reconciles, and this is that.
#       Both suite forms now sit under the one `.endless/tasks/<id>/` segment
#       the predicate already keys on.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  THE GATE IS WIRED. Both arms are called from handlePreToolUse, each in
#       the block that can reach it: Arm 2 among the Bash gates, Arm 1 after the
#       write-tool early-return. A predicate nobody calls ships inert, and a
#       gate that ships inert is indistinguishable from this task never landing.
#   C2  ONE SPELLING OF THE CONVENTION. Both matchers are built from
#       verify.SuitesDir, so a project that lays its suites out elsewhere moves
#       this gate with them. A second literal would be a second convention.
#   C3  THE TREE IS IN RANGE. Every per-task suite directory in this repository
#       is named e-<digits>, so the gate's id extraction can read all of them.
#       The shared _harness.sh and _guard.sh belong to no task and sit outside
#       any of them, where the gate has no opinion.
#   C4  WHAT A MANIFEST POINTS AT STAYS EDITABLE. Every `paths` selection in the
#       real manifests names project source outside `.endless/`. Blocking the
#       manifest must not leak into blocking the durable tests it selects — that
#       is the whole reason "coverage that must survive belongs in the project's
#       own test suite" is a usable instruction.
#   C5  THE RETIRED LAYOUT IS GONE, so dropping its arm cost nothing: no tracked
#       file lives under tests/tasks/ any more.
#
# ISOLATION
#   Nothing here touches a database. The unit tests in layer A seed their own
#   throwaway SQLite file under a temp HOME; everything else is `go build`,
#   `go test`, and greps over tracked files.
#
# Layers:
#   A. FAIL-FAST — this task's own unit tests, the package they live in, the
#      monitor package whose landed-ness lookup they reuse, and the Python test
#      that governs every file in this directory (including this one). Nothing
#      below is meaningful if they fail.
#   B. The gate is wired — C1.
#   C. One spelling of the convention — C2.
#   D. The tree is in range, and what the manifests point at is not — C3, C4.
#   E. The retired layout is gone — C5.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

GATE=internal/hookcmd/verify_suite.go
HOOK=internal/hookcmd/claude.go

[[ -f "${GATE}" ]] || setup_error "the gate is not where this suite expects it: ${GATE}"
[[ -f "${HOOK}" ]] || setup_error "the hook is not where this suite expects it: ${HOOK}"

# line_of <regex> <file> — the line number of the first match, or empty.
line_of() {
    grep -nE -m1 -e "$1" "$2" 2>/dev/null | cut -d: -f1
}

# ---------------------------------------------------------------------------
section "A. What this change could have broken (fail-fast)"
# ---------------------------------------------------------------------------
# The gate's own tests are this task's durable deliverable — the predicates, the
# landed/foreign conjunction against a real SQLite database, the two refusals'
# shape, and each arm driven from the payload the harness delivers. monitor is
# here because task_report.go now reads landed-ness through taskHasLanded rather
# than its own copy of the query, and test_suite_guard.py because this task adds
# a file to the directory it governs — including the DO-NOT-EDIT banner that is
# Arm 1's human-readable half.

if out=$(go build ./... 2>&1); then
    report_pass "go build ./... — the tree compiles"
else
    report_fail "go build ./... — the tree compiles" "exit 0" \
        "$(printf '%s' "${out}" | tail -20)"
    summary
fi

if out=$(go test -count=1 ./internal/hookcmd/ ./internal/monitor/ 2>&1); then
    report_pass "go test: the gate's own tests, and monitor's landed-ness lookup"
else
    report_fail "go test: the gate's own tests, and monitor's landed-ness lookup" \
        "exit 0" "$(printf '%s' "${out}" | tail -40)"
    summary
fi

if out=$(uv run pytest -q tests/test_suite_guard.py 2>&1); then
    report_pass "pytest: this suite obeys the rules of the directory it joins"
else
    report_fail "pytest: this suite obeys the rules of the directory it joins" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. The gate is wired into handlePreToolUse (C1)"
# ---------------------------------------------------------------------------
# The failure this guards against is the quiet one: a predicate that is written,
# tested and never called. Both arms are checked by POSITION, not merely by
# presence, because each is only reachable from one place — Arm 2 from a Bash
# branch, Arm 1 from after the write-tool early-return. A call in the wrong
# block compiles, passes every unit test, and never fires.

handler="$(line_of '^func handlePreToolUse\(' "${HOOK}")"
[[ -n "${handler}" ]] || setup_error "handlePreToolUse is gone from ${HOOK}"

bash_gate="$(line_of '^\tif payload\.ToolName == "Bash" \{' "${HOOK}")"
write_gate="$(line_of '^\tif !writeTools\[payload\.ToolName\] \{' "${HOOK}")"
run_arm="$(line_of '^\t+blockLandedSuiteRunIfApplicable\(payload\)' "${HOOK}")"
edit_arm="$(line_of '^\t+blockLandedSuiteEditIfApplicable\(payload\)' "${HOOK}")"

assert_eq "Arm 2 is called from handlePreToolUse" \
    "called" "$([[ -n "${run_arm}" ]] && echo called || echo "missing — the run arm ships inert")"
assert_eq "Arm 1 is called from handlePreToolUse" \
    "called" "$([[ -n "${edit_arm}" ]] && echo called || echo "missing — the edit arm ships inert")"

if [[ -n "${run_arm}" && -n "${bash_gate}" && -n "${write_gate}" ]]; then
    assert_eq "Arm 2 sits in a Bash branch, where a command string exists to match" \
        "yes" "$([[ ${run_arm} -gt ${bash_gate} && ${run_arm} -lt ${write_gate} ]] && echo yes || echo no)"
fi
if [[ -n "${edit_arm}" && -n "${write_gate}" ]]; then
    assert_eq "Arm 1 sits after the write-tool early-return, where a target path exists" \
        "yes" "$([[ ${edit_arm} -gt ${write_gate} ]] && echo yes || echo no)"
fi

# The plan-file gate is the precedent Arm 1 follows, and the order between them
# is deliberate: a plan-file write gets the plan-specific redirect, not this one.
plan_arm="$(line_of '^\tblockPlanFileWriteIfApplicable\(payload\)' "${HOOK}")"
if [[ -n "${edit_arm}" && -n "${plan_arm}" ]]; then
    assert_eq "Arm 1 follows the plan-file gate it is modelled on" \
        "yes" "$([[ ${edit_arm} -gt ${plan_arm} ]] && echo yes || echo no)"
fi

# The worktree gate's generic "edits in main" refusal must not pre-empt the
# suite-specific one, for the same reason the plan-file gate precedes it.
wt_gate="$(line_of '^\tenforceWorktreeGate\(projectID, payload\)' "${HOOK}")"
if [[ -n "${edit_arm}" && -n "${wt_gate}" ]]; then
    assert_eq "Arm 1 precedes the worktree gate, so its refusal is the one seen" \
        "yes" "$([[ ${edit_arm} -lt ${wt_gate} ]] && echo yes || echo no)"
fi

# ---------------------------------------------------------------------------
section "C. One spelling of the suites-directory convention (C2)"
# ---------------------------------------------------------------------------
# PRODUCT: .endless/tasks is where Endless puts suites in every project it
# tracks, and verify.SuitesDir is the one place that says so. A literal here
# would be a second convention that happens to agree today — and the day a
# project's layout differs, the gate would guard a directory nothing uses.

assert_eq "both matchers are built from verify.SuitesDir" \
    "2" "$(grep -c 'regexp.QuoteMeta(verify.SuitesDir)' "${GATE}")"

# Code lines only: the file's comments name the path constantly, and must.
assert_eq "the gate hard-codes the path nowhere in its code" \
    "" "$(grep -nE '^[^/]*"[^"]*\.endless/tasks' "${GATE}" | grep -v '^\s*//' || true)"

# ---------------------------------------------------------------------------
section "D. The tree is in range, and what the manifests point at is not (C3, C4)"
# ---------------------------------------------------------------------------
# The predicate reads a task number out of an `e-<digits>` directory name. A
# suite directory named any other way is one the gate cannot see — so the claim
# "every landed suite is covered the moment this ships" rests on the tree
# actually being shaped that way, which is a fact about the tree and not about
# the regex.

strays=""
for entry in .endless/tasks/*; do
    [[ -d "${entry}" ]] || continue
    name="$(basename "${entry}")"
    [[ "${name}" =~ ^[eE]-[0-9]+$ ]] || strays+=" ${name}"
done
assert_eq "every per-task suite directory is named e-<digits>, so the gate can read it" \
    "" "${strays}"

# A glob that matched nothing would make the assertion above vacuous.
suite_count="$(find .endless/tasks -maxdepth 1 -type d -name 'e-*' | wc -l | tr -d ' ')"
assert_eq "and there are suites to be in range of" \
    "many" "$([[ ${suite_count} -gt 100 ]] && echo many || echo "only ${suite_count}")"

# The two shared files belong to no task. They sit directly in the suites
# directory rather than inside an e-NNNN/ one, which is exactly what keeps them
# outside the predicate — the gate has no landed-ness to look up for them.
for shared in _harness.sh _guard.sh CLAUDE.md; do
    if [[ -e ".endless/tasks/${shared}" ]]; then
        report_pass "${shared} sits outside any task's directory, where the gate has no opinion"
    else
        report_fail "${shared} sits outside any task's directory" \
            "present at .endless/tasks/${shared}" "missing"
    fi
done

# A manifest is a POINTER: its checks select tests that live where the project's
# tests live. Blocking the manifest must not leak into blocking them, and the
# evidence is that none of them is under .endless/ at all.
inside=""
for m in .endless/tasks/*/verify.toml; do
    [[ -f "${m}" ]] || continue
    while IFS= read -r sel; do
        [[ "${sel}" == .endless/* || "${sel}" == ./.endless/* ]] && inside+=" ${m}:${sel}"
    done < <(grep -E '^paths[[:space:]]*=' "${m}" \
        | tr ',' '\n' | grep -oE '"[^"]+"' | tr -d '"')
done
assert_eq "every manifest check selects project source outside .endless/" "" "${inside}"

# Vacuous if no manifest was read, or none declares paths.
assert_eq "manifests were actually read" \
    "yes" "$([[ -n "$(grep -lE '^paths[[:space:]]*=' .endless/tasks/*/verify.toml 2>/dev/null)" ]] && echo yes || echo no)"

# ---------------------------------------------------------------------------
section "E. The retired script layout is gone (C5)"
# ---------------------------------------------------------------------------
# The plan named two suite forms and a second path, tests/tasks/e-NNNN-verify.sh.
# E-2023 retired it before this landed. Dropping that arm is only free if the
# path is genuinely empty — an arm removed while files still live there would be
# the exact blindness this task exists to prevent.

assert_eq "no tracked file lives under tests/tasks/" \
    "" "$(git ls-files -- 'tests/tasks' | head -5)"
assert_eq "and the directory itself is gone" \
    "absent" "$([[ -d tests/tasks ]] && echo present || echo absent)"

summary
