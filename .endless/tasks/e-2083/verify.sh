#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2083 and records what was true when E-2083
# landed. Edit it only if you ARE E-2083. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2083 verification — the fourteen landed verify suites E-2074 rewrote are
# back to what their own tasks landed.
#
# Before: commit bf285b54 (E-2074, "remove background-agent support") deleted
# four landed suites — e-1568, e-1570, e-1572, e-1621 — and amended ten more
# where they asserted on what it was removing, on the rationale that "a suite
# that proves a deleted feature works is worse than no suite". That inverts the
# rule in .endless/tasks/CLAUDE.md: a landed suite is a one-shot land-time gate
# whose post-land result is undefined, and it must not be edited because it
# records what was true when its task landed. Deleting one is the strongest
# possible edit. The violation then propagated — E-2081 read E-2074 as the
# convention and deleted a fifth suite three days later.
#
# After: all fourteen are byte-identical to bf285b54^ once the three PROJECT-
# WIDE sweeps that legitimately crossed every suite since are undone. Those
# sweeps are not E-2074's, and are kept:
#
#   E-2023 (c79857dfc, d3cf23423)  moved tests/tasks/e-NNNN-verify.sh to
#         .endless/tasks/e-NNNN/verify.sh, inserted the harness-source block as
#         each suite's first executable statement, rewrote each header's one
#         run-line to name `endless task verify`, and rewrote prose references
#         to a suite's path.
#   E-2090 (53376963c, 971636d16)  added the four-line DO-NOT-EDIT banner below
#         each shebang, and restored the executable bit.
#
# The four restored suites were already deleted when those sweeps ran, so this
# task applies the sweeps to them — restoring a file means putting it where it
# would be now, not where it was in August.
#
# E-2074's OWN suite is deliberately untouched (Mike's call, this session). Its
# four assert_absent lines name the PRE-E-2023 paths — tests/tasks/e-1568-
# verify.sh — which stay gone, so they still pass; and its reference sweep greps
# only cmd/internal/src/tests/docs/justfile, never .endless/. Nothing in it
# breaks, so editing it would be committing this task's own sin while reverting
# it. Section 6 asserts that it was left alone.
#
#   endless task verify E-2083
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# oneline folds a multi-line detail onto one line before it reaches
# report_fail. The harness emits a failure's `actual` straight into the TAP
# stream it hands the runner, unprefixed, so an embedded newline puts lines the
# TAP parser cannot read in the middle of the results — the runner then stops
# counting there and reports a truncated total while the on-screen summary
# below stays correct. Keeping every diagnostic to one line sidesteps it. The
# harness defect is E-2083's finding, not its fix: `tap()` in _harness.sh
# should prefix continuation lines with `# `, and 30 landed suites pass a
# multi-line `actual` today.
oneline() { tr '\n\t' '  ' | sed 's/  */ /g' | cut -c1-400; }

E2074="bf285b5421e8f291605e4b912df5fa612db23108"   # the commit being undone

RESTORED=(1568 1570 1572 1621)                      # deleted by E-2074
AMENDED=(1573 1624 1645 1648 1659 1905 1906 1914 1967 2067)
ALL=("${RESTORED[@]}" "${AMENDED[@]}")

git cat-file -e "${E2074}^{commit}" 2>/dev/null \
    || setup_error "E-2074's commit ${E2074} is not in this checkout's history"

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
#
# The durable tests that protect the SHAPE of every suite. They parametrize over
# .endless/tasks/e-*/verify.sh, so restoring four suites adds four suites' worth
# of cases to them — banner, harness-first-line, executable bit, no double
# guard. If a restored file is malformed these go red before anything below
# runs, and a red front is the whole point of putting them first.
section "1. Unit gate (fail fast)"

if uv run pytest tests/test_suite_guard.py tests/test_suite_rules.py -q \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest test_suite_guard.py test_suite_rules.py"
else
    report_fail "pytest test_suite_guard.py test_suite_rules.py" "exit 0" \
        "$(grep -E '^(FAILED|ERROR)|failed' "${TMP}/py.log" | head -5 | oneline)"
    summary
fi

# ── 2. the four deleted suites exist again ──────────────────────────────────
section "2. The four suites E-2074 deleted are back"

for id in "${RESTORED[@]}"; do
    f=".endless/tasks/e-${id}/verify.sh"
    if [[ -f "${f}" ]]; then
        report_pass "${f} exists"
    else
        report_fail "${f} exists" "a file" "nothing at that path"
    fi
    if [[ -x "${f}" ]]; then
        report_pass "${f} is executable (the runner exec's it)"
    else
        report_fail "${f} is executable" "the x bit set" "not executable"
    fi
done

# Each was deleted by E-2074 and by no one else — the premise of restoring them.
for id in "${RESTORED[@]}"; do
    status="$(git show --format="" --name-status "${E2074}" \
        -- "tests/tasks/e-${id}-verify.sh" | awk '{print $1}')"
    assert_eq "E-2074 is what deleted e-${id}'s suite" "D" "${status}"
done

# ── 3. all fourteen match what their own tasks landed ───────────────────────
#
# The core proof. Undo the three project-wide sweeps listed in the header — and
# ONLY those three — then the file must equal bf285b54^ byte for byte. Anything
# E-2074 changed, and anything this task got wrong, shows up as a difference;
# the sweeps do not, because they are subtracted explicitly rather than ignored.
section "3. Every suite is byte-identical to its pre-E-2074 text"

cat >"${TMP}/unsweep.py" <<'PY'
"""Print a suite with the E-2023 and E-2090 sweeps undone, and nothing else."""
import re
import sys

path, tid, orig_run = sys.argv[1], sys.argv[2], sys.argv[3]
lines = open(path).read().split("\n")

# E-2090: the four-line DO-NOT-EDIT banner, directly below the shebang.
assert lines[1].startswith("# ── DO NOT EDIT"), "no banner below the shebang"
del lines[1:5]

# E-2023: the six-line harness block ending in the source line.
i = lines.index('source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"')
assert lines[i - 4].startswith("# Refuse a direct run"), "harness block malformed"
assert lines[i - 5] == "", "harness block is not preceded by a blank line"
del lines[i - 5 : i + 1]

# E-2023: the one run-line in the header naming the runner.
j = [k for k, l in enumerate(lines) if l == f"#   endless task verify E-{tid}"]
assert len(j) == 1, f"expected exactly one run-line, found {len(j)}"
lines[j[0]] = orig_run

# E-2023: prose references to a suite's path.
out = "\n".join(lines)
out = re.sub(r"\.endless/tasks/e-(\d+)/verify\.sh", r"tests/tasks/e-\1-verify.sh", out)
sys.stdout.write(out)
PY

for id in "${ALL[@]}"; do
    old="${TMP}/old-${id}.sh"
    git show "${E2074}^:tests/tasks/e-${id}-verify.sh" >"${old}" 2>/dev/null \
        || setup_error "e-${id}'s pre-E-2074 text is not readable from git"

    # The run-line as that file itself spelled it, so section 3 subtracts the
    # sweep rather than assuming one of its two forms.
    run_line="$(grep -E "^#( +| +esu && )\./tests/tasks/e-${id}-verify\.sh$" "${old}" | head -1)"
    [[ -n "${run_line}" ]] || setup_error "e-${id}: no run-line found in the pre-E-2074 text"

    if ! python3 "${TMP}/unsweep.py" ".endless/tasks/e-${id}/verify.sh" "${id}" \
            "${run_line}" >"${TMP}/now-${id}.sh" 2>"${TMP}/err-${id}"; then
        report_fail "e-${id} carries the current suite shape" \
            "banner + harness block + runner run-line" \
            "$(tail -3 "${TMP}/err-${id}" | oneline)"
        continue
    fi

    if diff -q "${TMP}/now-${id}.sh" "${old}" >/dev/null; then
        report_pass "e-${id} is identical to its pre-E-2074 text"
    else
        report_fail "e-${id} is identical to its pre-E-2074 text" \
            "no difference from ${E2074:0:8}^" \
            "$(diff "${old}" "${TMP}/now-${id}.sh" | grep -cE '^[<>]' | tr -d '\n') differing line(s), first: $(diff "${old}" "${TMP}/now-${id}.sh" | grep -E '^[<>]' | head -1 | oneline)"
    fi
done

# ── 4. no E-2074 retrofit text survives ─────────────────────────────────────
#
# Section 3 proves this by construction, but only for the sweeps it models.
# This is the blunt independent check: E-2074 signed every amendment it made by
# naming itself in the comment explaining the retrofit, so a surviving mention
# in one of the fourteen is a surviving amendment.
section "4. No amendment text mentioning E-2074 is left in the fourteen"

for id in "${ALL[@]}"; do
    n="$(grep -c 'E-2074' ".endless/tasks/e-${id}/verify.sh" || true)"
    assert_eq "e-${id}'s suite does not mention E-2074" "0" "${n}"
done

# ── 5. E-2074's three "pre-existing leftover" claims are false ──────────────
#
# E-2074's commit message defends three of its ten amendments as fixes to real
# leftovers "the new suite's reference sweep caught" — e-1573 asserting the
# guide documents `bg_throttle_warn`, e-1914 probing sessions.summary, and
# e-2067 probing session.go for nearestEpicAncestor. The plan for this task
# asked for a per-file judgment on exactly that: honest historical record, or
# already wrong when it landed?
#
# All three were live in the tree at bf285b54^ — the commit's own parent. Each
# was made stale BY E-2074, in the same commit that then "fixed" the suite for
# it. So none was pre-existing, and all ten amendments revert uniformly.
section "5. Nothing E-2074 called a pre-existing leftover was one"

# live_at REV PATH PATTERN — "yes" when PATTERN appears in PATH at REV. The
# question is whether the thing existed, never how many times it was written.
live_at() {
    if git show "$1:$2" 2>/dev/null | grep -q "$3"; then printf 'yes'; else printf 'no'; fi
}

assert_eq "bg_throttle_warn was live in task_cmd.py at E-2074^ (e-1573's claim)" \
    "yes" "$(live_at "${E2074}^" src/endless/task_cmd.py 'bg_throttle_warn')"
assert_eq "...and the guide still documented it there" \
    "yes" "$(live_at "${E2074}^" docs/guide/orchestration.md 'bg_throttle_warn')"
assert_eq "sessions.summary was declared in schema.sql at E-2074^ (e-1914's claim)" \
    "yes" "$(git show "${E2074}^:internal/schema/schema.sql" \
        | sed -n '/CREATE TABLE IF NOT EXISTS sessions/,/^);/p' \
        | grep -q '^ *summary TEXT,' && printf 'yes' || printf 'no')"
assert_eq "nearestEpicAncestor was in monitor/session.go at E-2074^ (e-2067's claim)" \
    "yes" "$(live_at "${E2074}^" internal/monitor/session.go 'nearestEpicAncestor')"

# Each of the three left in E-2074 itself, not before it.
for spec in "src/endless/task_cmd.py:bg_throttle_warn" \
            "internal/monitor/session.go:nearestEpicAncestor"; do
    src="${spec%%:*}"; needle="${spec##*:}"
    assert_eq "E-2074 is the commit that removed ${needle}" "no" \
        "$(live_at "${E2074}" "${src}" "${needle}")"
done

# ── 6. the blast radius is exactly the fourteen ─────────────────────────────
#
# A revert that reaches product code is not a revert. And E-2074's own suite is
# a landed suite like any other: leaving it alone is the rule this task exists
# to restore, so its untouchedness is an assertion, not an omission.
section "6. Nothing outside the fourteen suites changed"

BASE="$(git merge-base HEAD main)" || setup_error "could not find the merge base with main"

changed="$(git diff --name-only "${BASE}" -- . \
    | grep -vE '^\.endless/(db-ledger|plans|analyses|outcomes|LESSONS\.md|verbs\.jsonl)' || true)"
outside="$(printf '%s\n' "${changed}" | grep -v '^$' \
    | grep -vE "^\.endless/tasks/e-(1568|1570|1572|1621|1573|1624|1645|1648|1659|1905|1906|1914|1967|2067|2083)/verify\.sh$" || true)"
if [[ -z "${outside}" ]]; then
    report_pass "no file outside the fourteen suites and this one is touched"
else
    report_fail "no file outside the fourteen suites and this one is touched" \
        "nothing" "$(printf '%s' "${outside}" | oneline)"
fi

assert_eq "E-2074's own suite is untouched" "" \
    "$(git diff --name-only "${BASE}" -- .endless/tasks/e-2074/verify.sh)"
assert_eq "...and still carries its four assert_absent lines, on the old paths" \
    "4" "$(grep -cE '^ *assert_absent "tests/tasks/e-(1568|1570|1572|1621)-verify\.sh is gone"' \
        .endless/tasks/e-2074/verify.sh)"

# Those lines pass BECAUSE the old paths stay gone — the fact that makes leaving
# E-2074's suite alone correct rather than merely convenient.
for id in "${RESTORED[@]}"; do
    if [[ -e "tests/tasks/e-${id}-verify.sh" ]]; then
        report_fail "tests/tasks/e-${id}-verify.sh is still gone" \
            "absent" "restored to the retired path"
    else
        report_pass "tests/tasks/e-${id}-verify.sh is still gone (restored to the new path)"
    fi
done

summary
