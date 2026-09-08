#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1957 and records what was true when E-1957
# landed. Edit it only if you ARE E-1957. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1957 verification — `endless worktree land` no longer guesses at a rebase
# conflict, and the command that classifies one prescribes only what it proves.
#
# Background. Landing E-1943 on 2026-08-12 hit a rebase conflict whose branch
# side referenced `BINARY_SOURCE_PATHS` and `_refuse_if_behind_base` — two names
# E-1941 had just deleted from main. `land` answered with two numbered recovery
# options for the operator to choose between, and BOTH restore the branch's side
# of the hunk. Either one would have produced a land that succeeded while
# shipping an `endless worktree sync` that raised `NameError` the first time it
# ran. Mike caught it because he knows the codebase and does not trust the
# message; a product user has neither advantage and reads two numbered steps as
# instructions from the tool.
#
# The fix is a split, and this suite checks both halves:
#
#   1. `land` stops prescribing for a source conflict. It reports the facts —
#      phase, the commit that failed to replay, the conflicting paths —
#      RECORDS the conflict state before `git rebase --abort` destroys it, and
#      hands off to `endless worktree diagnose`. The one confident recovery
#      survives, because it was never a guess: a conflict confined to
#      endless-managed auto-files has a mechanical restore that is lossless by
#      construction.
#
#   2. `diagnose` classifies the recorded conflict into one of five kinds and
#      prints a recovery only for the three it can prove. For the two it cannot
#      — the branch using names the base branch deleted, and a genuine overlap
#      between two intentional edits — it prints evidence and stops.
#
#   3. `land --dry-run` rehearses the rebase on a throwaway branch in a
#      throwaway checkout, so it can finally preview the failure it exists to
#      preview, and reports the same class the post-mortem would.
#
# Sections 2-6 build real throwaway git repositories and drive the SHIPPED
# functions against them — `_rebase_conflict_message` is what both of land's
# conflict handlers call, `_rehearse_land_rebase` is what `--dry-run` calls,
# `endless-go worktree ledger-orphans` is the real binary. Nothing is stubbed
# and no expected string is copied out of the code being tested.
#
# Fail-fast: section 1 gates the rest. A broken unit layer makes every drive
# below meaningless, so the suite stops rather than printing a cascade.
#
#   endless task verify E-1957
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup/environment error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# Captures are written under the sandbox root, which composes from
# XDG_CACHE_HOME. The runner isolates HOME and XDG_CONFIG_HOME; pointing the
# cache at the temp dir too keeps every fixture's capture in here and gone
# afterwards.
export XDG_CACHE_HOME="${TMP}/cache"
WORK="${TMP}/repos"
DRIVER="${ENDLESS_VERIFY_DIR}/driver.py"
[[ -f "${DRIVER}" ]] || setup_error "driver.py is missing from ${ENDLESS_VERIFY_DIR}"

# The Go detector is reached by name, as it is in production. Put THIS
# worktree's binary first so the classifier under test talks to the candidate
# and not to whatever is globally installed.
[[ -x "${WT}/bin/endless-go" ]] || setup_error \
    "bin/endless-go is not built — run 'just build' first"
export PATH="${WT}/bin:${PATH}"

# drive runs one driver verb against one scenario and caches the output, so a
# section can assert several properties of one run without rebuilding the repo.
DRIVE_OUT=""
drive() {
    local verb="$1" scenario="$2"
    DRIVE_OUT="$(uv run python "${DRIVER}" "${verb}" "${scenario}" "${WORK}" 2>&1)" \
        || setup_error "driver ${verb} ${scenario} failed:"$'\n'"${DRIVE_OUT}"
}

# field reads one `key=value` line out of the cached driver output.
field() {
    printf '%s\n' "${DRIVE_OUT}" | sed -n "s/^$1=//p" | head -1
}

# after prints everything following a `---marker---` line.
after() {
    printf '%s\n' "${DRIVE_OUT}" | sed -n "/^---$1---\$/,\$p" | tail -n +2
}

# ── 1. Unit gate (fail fast) ────────────────────────────────────────────────
#
# The durable coverage lives in the project's own suites, per
# .endless/tasks/CLAUDE.md: tests/test_land_conflict.py owns capture, storage,
# conflict-marker parsing, every classifier and the rehearsal;
# tests/test_worktree_land_conflict_msg.py owns what land's message says; and
# internal/events owns the content-based ledger-orphan detector.
section "1. Unit gate (fail fast)"

if uv run pytest -q tests/test_land_conflict.py \
        tests/test_worktree_land_conflict_msg.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — capture, the five classifiers, and land's message"
else
    report_fail "pytest tests/test_land_conflict.py tests/test_worktree_land_conflict_msg.py" \
        "exit 0" "$(tail -30 "${TMP}/py.log")"
    summary
fi

if go test ./internal/events/ -run TestLedgerOrphans >"${TMP}/go.log" 2>&1; then
    report_pass "go test — the content-based ledger-orphan detector"
else
    report_fail "go test ./internal/events/ -run TestLedgerOrphans" \
        "exit 0" "$(tail -30 "${TMP}/go.log")"
    summary
fi

# ── 2. The hazard is gone ───────────────────────────────────────────────────
#
# The E-1943 shape, rebuilt and driven through land's real conflict handler.
# Neither of the two recoveries that shipped the NameError may appear, in any
# form — not the commands, not a numbered menu that could hold them.
section "2. The hazard is gone (the E-1943 shape)"

drive message supersession
assert_eq "the branch conflicts against main's deletions" "yes" "$(field conflict)"
MSG="$(after message)"

assert_not_contains "no candidate framing" \
    "the wrong recovery can duplicate or lose work" "${MSG}"
assert_not_contains "no 'reset and re-apply' recovery" \
    "reset --hard" "${MSG}"
assert_not_contains "no delta-capture patch file" "land-delta.patch" "${MSG}"
if printf '%s\n' "${MSG}" | grep -qE '^[[:space:]]+[0-9]+\.[[:space:]]'; then
    report_fail "no numbered menu of candidates" "no '  1.' / '  2.' lines" "${MSG}"
else
    report_pass "no numbered menu of candidates"
fi

# `git rebase --continue` DOES still appear — inside the hints git printed,
# which land quotes verbatim. What must never appear is endless recommending
# it. So the test is where the string sits: only above the "git said:" block's
# end, and answered by our own prose underneath.
OURS="$(printf '%s\n' "${MSG}" | sed -n '/^A source file conflicts/,$p')"
assert_not_contains "endless never recommends resolving in place" \
    "git rebase --continue" "${OURS}"
assert_contains "and tells the reader not to follow git's generic hint" \
    "Do not follow it yet" "${MSG}"
assert_contains "while still quoting git verbatim" "git said:" "${MSG}"

assert_contains "hands off to the command that can classify it" \
    "endless worktree diagnose" "${MSG}"
assert_contains "still names the commit that failed to replay" \
    "E-1943: print each binary source path" "${MSG}"
assert_contains "still names the conflicting file" "sync.py" "${MSG}"
assert_contains "still names the step it failed at" \
    "rebasing your branch onto main" "${MSG}"
assert_not_contains "does not misreport a source conflict as auto-file-only" \
    "endless-managed auto-file" "${MSG}"

# ── 3. The evidence survives the abort ──────────────────────────────────────
#
# The whole reason capture happens mid-rebase. After `git rebase --abort`, git
# knows none of this; the capture has to.
section "3. The evidence survives the abort"

drive classify supersession
assert_eq "the capture reads back" "yes" "$(field loaded)"
if [[ -n "$(field rebase_head)" ]]; then
    report_pass "REBASE_HEAD is recoverable although the rebase was aborted"
else
    report_fail "REBASE_HEAD is recoverable although the rebase was aborted" \
        "a commit sha" "(empty)"
fi
assert_eq "both sides of the conflicting hunk were kept" "1" "$(field hunks)"

# ── 4. Each classifier, on a repository built for it ────────────────────────
#
# One throwaway repo per class. A prescription must appear for the three that
# are provable and for neither of the two that are not.
section "4. Each classifier"

check_class() {
    local scenario="$1" want_class="$2" want_prescribes="$3"
    drive classify "${scenario}"
    assert_eq "${scenario} → ${want_class}" "${want_class}" "$(field klass)"
    assert_eq "${scenario} → proven" "true" "$(field proven)"
    assert_eq "${scenario} → prescribes: ${want_prescribes}" \
        "${want_prescribes}" "$(field prescribes)"
}

check_class already-landed "already-landed"       "true"
check_class ledger-orphan  "orphaned-ledger-base" "true"
check_class auto-file      "auto-file-only"       "true"
check_class supersession   "symbol-supersession"  "false"

# The Go detector, driven as the binary the classifier actually shells out to.
ORPHAN_REPO="${WORK}/ledger-orphan"
ORPHAN_JSON="$(endless-go worktree ledger-orphans --repo "${ORPHAN_REPO}" \
    --base main --branch task/5000 2>&1)" \
    || setup_error "endless-go worktree ledger-orphans failed:"$'\n'"${ORPHAN_JSON}"
assert_contains "the Go detector reports the run as contiguous at the base" \
    '"contiguous_at_base": true' "${ORPHAN_JSON}"
assert_contains "and not as mid-branch" '"mid_branch": false' "${ORPHAN_JSON}"

# ── 5. The two unprovable classes prescribe nothing ─────────────────────────
#
# The contract, checked on the two cases it exists for: the E-1943 shape, and
# the explicit negative case of two intentional edits to the same line.
section "5. The unprovable classes prescribe nothing"

drive classify supersession
SUPER="$(after rendered)"
assert_eq "symbol supersession names both deleted symbols" \
    "BINARY_SOURCE_PATHS,_refuse_if_behind_base" "$(field symbols)"
assert_contains "and says so out loud" "No recovery is prescribed." "${SUPER}"
assert_not_contains "and offers no 'resolve in place'" \
    "git rebase --continue" "${SUPER}"
assert_not_contains "and offers no 'reset and re-apply'" \
    "reset --hard" "${SUPER}"
assert_contains "and states what both recoveries would do" \
    "reintroduces these references" "${SUPER}"

drive classify overlap
OVERLAP="$(after rendered)"
assert_eq "semantic overlap is the class" "semantic-overlap" "$(field klass)"
assert_eq "and is reported as NOT proven" "false" "$(field proven)"
assert_eq "and prescribes nothing" "false" "$(field prescribes)"
assert_contains "and shows both sides in full" "Both sides, in full:" "${OVERLAP}"
assert_contains "including the base branch's edit" \
    "round(sum(rows), 2)" "${OVERLAP}"
assert_contains "and the branch's edit" "sum(r for r in rows if r)" "${OVERLAP}"
assert_contains "and says a human has to choose" \
    "has to choose" "${OVERLAP}"

# ── 6. --dry-run rehearses, and leaves nothing behind ───────────────────────
#
# The preview that could not preview the one failure worth previewing. It runs
# the real rebase on a copy, so what it reports is git's answer.
section "6. land --dry-run rehearses the rebase"

drive rehearse supersession
assert_eq "a branch that will conflict is predicted to conflict" \
    "yes" "$(field conflict)"
assert_eq "the evidence is marked as a rehearsal" "true" "$(field rehearsal_flag)"
assert_eq "and carries the same class the post-mortem gives" \
    "symbol-supersession" "$(field klass)"
assert_eq "base, the task branch and HEAD are untouched" \
    "true" "$(field unchanged)"
assert_eq "the throwaway checkout is gone" "1" "$(field checkouts)"
assert_eq "the throwaway branch is gone" "0" "$(field throwaway_branches)"
assert_eq "a rehearsal does not overwrite the post-mortem slot" \
    "false" "$(field capture_written)"

drive rehearse clean
assert_eq "a branch that will rebase cleanly is reported clean" \
    "no" "$(field conflict)"
assert_eq "and still leaves no throwaway checkout" "1" "$(field checkouts)"
assert_eq "and no throwaway branch" "0" "$(field throwaway_branches)"

# ── 7. The command is wired ─────────────────────────────────────────────────
#
# Driven through the real CLI, which is as far as the runner's isolation allows:
# resolving a task id needs the project database, and the runner deliberately
# gives a suite a temp HOME with no database in it. The refusal that resolution
# leads to — "no land conflict is recorded", exit non-zero, nothing
# reproduced — is covered in tests/test_land_conflict.py, which drives
# `diagnose_land_conflict` with only the resolver stood in for.
section "7. The command is wired"

HELP="$(uv run endless --db sandbox worktree diagnose --help 2>&1)"
HELP_RC=$?
assert_eq "endless worktree diagnose exists" "0" "${HELP_RC}"
assert_contains "and takes an optional task id" "[TASK_ID]" "${HELP}"
assert_contains "and offers --json for an agent" "--json" "${HELP}"
assert_contains "and states the contract at the CLI surface" \
    "prove" "${HELP}"

# ── 8. Both of land's conflict handlers still route through the capture ─────
#
# Capture lives inside the message builder precisely so a third conflict handler
# cannot report a conflict without recording it. That only holds while both
# existing handlers reach it.
#
# The route has two hops since E-2122 landed: each call site calls
# `_rebase_failure_message`, which decides WHICH KIND of failure this was, and
# delegates a real content conflict — and only that — to
# `_rebase_conflict_message`, which captures. Asserting the hops separately is
# what keeps a future edit from quietly bypassing one.
section "8. Both conflict handlers route through the capture"

assert_eq "land calls the failure dispatcher at both call sites" "2" \
    "$(grep -c '^            msg = _rebase_failure_message(' src/endless/worktree_cmd.py)"
assert_contains "the dispatcher delegates a real conflict to the capturing builder" \
    "return _rebase_conflict_message(" "$(cat src/endless/worktree_cmd.py)"
assert_contains "and the builder is what writes the capture" \
    "land_conflict.store_evidence(worktree_path, ev)" \
    "$(cat src/endless/worktree_cmd.py)"
assert_contains "REBASE_HEAD stays gated on a rebase actually running" \
    "rebase_in_progress=_rebase_in_progress(worktree_path)" \
    "$(cat src/endless/worktree_cmd.py)"
assert_contains "storage composes from the sandbox root, so it moves with it" \
    'config.sandbox_root(worktree_path.name) / "land-conflict"' \
    "$(cat src/endless/land_conflict.py)"

summary
