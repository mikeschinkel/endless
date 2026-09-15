#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2137 and records what was true when E-2137
# landed. Edit it only if you ARE E-2137. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2137: a task's document mirrors follow the ledger to main.
#
# Endless writes a task's multiline content twice — the ledger entry, enforced
# onto the main checkout, and a human-readable mirror. The mirror went to the
# task BRANCH and waited for a land. Nobody decided that; E-1525 found the file
# untracked and committed it the nearest way to hand.
#
# Measured 2026-09-14 over 133 worktrees, with the content-based probe that
# drives the diamond marker: 56 worktrees held 139 genuinely unlanded commits,
# 123 of them mirrors, and 44 of the 56 were unlanded ONLY because of them.
#
# What is verified here:
#   A. Fail-fast: this task's own tests pass — the path convention, the sweep,
#      the commit path's new retry, the hook gate, and the four Python modules
#      whose contracts changed.
#   B. The claims only visible from inside, named one by one.
#   C. The structural guarantee, through the REAL binary against a REAL
#      repository and a REAL linked worktree: a mirror commit lands on main, and
#      the same call from a worktree is REFUSED. That refusal is what makes
#      "mirrors never reach a branch" a property of the code rather than a
#      convention every caller has to remember.
#   D. The hook gate, through the real binary: the three database-owned .md
#      files in a task's directory are refused, and the task's own verify.sh
#      beside them is not. Before E-2137 the gate lived on a directory nothing
#      else used, so over-matching cost nothing; it now shares a directory with
#      files sessions write constantly.
#   E. The writers that are gone are GONE. Five functions used to put mirrors
#      into worktrees; a grep is the cheapest way to prove none came back.
#
# See E-2137's plan (endless task show E-2137 --all-fields).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# internal/docmirror is the path convention every other piece reads from.
# internal/docsweep is the job that relocates and repairs. internal/events holds
# the index.lock retry this change made necessary by adding writers to that
# path. internal/hookcmd is the gate that now has to discriminate WITHIN a
# directory. The Python modules are the write path, the branch-history cleanup,
# and the two contracts that changed shape underneath them.

go_pkg() { # go_pkg <package> <label>
    if out=$(go test "$1" 2>&1); then
        report_pass "go test: $2"
    else
        report_fail "go test: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

go_pkg ./internal/docmirror/ "internal/docmirror"
go_pkg ./internal/docsweep/ "internal/docsweep"
go_pkg ./internal/events/ "internal/events"
go_pkg ./internal/hookcmd/ "internal/hookcmd"

py_mod() { # py_mod <path> <label>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "pytest: $2"
    else
        report_fail "pytest: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

py_mod tests/test_doc_mirror_to_main.py "the write path lands on main"
py_mod tests/test_doc_strip.py "the branch-history cleanup"
py_mod tests/test_worktree_orphan_branch.py "orphan-branch recovery, widened"
py_mod tests/test_worktree_db_context_threading.py "the --db context still threads"

# ---------------------------------------------------------------------------
section "B. The claims only visible from inside, named"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing all of them into four green packages.

go_claim() { # go_claim <package> <test-name> <claim>
    if out=$(go test "$1" -run "^$2\$" -count=1 2>&1); then
        report_pass "$3"
    else
        report_fail "$3" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

py_claim() { # py_claim <file::test> <claim>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

# The convention, and the round trip that keeps writer and reader in step.
go_claim ./internal/docmirror/ TestPathsRoundTripThroughResolve \
    "every path the convention BUILDS is one it RECOGNIZES, back to the same column"
go_claim ./internal/docmirror/ TestTaskDirCasing \
    "the directory is lowercase e-NNNN — the split a case-insensitive disk hides"
go_claim ./internal/docmirror/ TestSubjectsAreIDSpecific \
    "each mirror's subject carries its id, so two tasks never amend over each other"

# The sweep: what converges, and the two things it must never do.
go_claim ./internal/docsweep/ TestLegacyMirrorsMoveIntoTheTaskDirectory \
    "a legacy .endless/plans|outcomes|analyses file moves into the task's directory"
go_claim ./internal/docsweep/ TestRelocationRunsBeforeReconciliation \
    "relocation precedes repair, so git records a rename and not an add+delete"
go_claim ./internal/docsweep/ TestMissingMirrorsAreCreated \
    "a mirror that only ever existed on a branch is backfilled onto main"
go_claim ./internal/docsweep/ TestDriftedMirrorsAreRewritten \
    "a mirror whose bytes drifted from its column is rewritten from the column"
go_claim ./internal/docsweep/ TestAnEmptyColumnNeverOverwritesAFile \
    "an EMPTY column never overwrites a file — the one case regenerating could lose"
go_claim ./internal/docsweep/ TestARemovedTaskIsNotMirrored \
    "a removed task gets no mirror; the sweep reads live_tasks, not tasks"
go_claim ./internal/docsweep/ TestAStrangerInTheLegacyDirectoryIsLeftAlone \
    "a non-mirror file in a legacy directory is not moved"
go_claim ./internal/docsweep/ TestAMatchingMirrorIsNotRewritten \
    "a converged repository is quiet — no commit on main every fifteen minutes"
go_claim ./internal/docsweep/ TestSweepIsIdempotent \
    "a second pass finds nothing to do, which is the lease contract's requirement"
go_claim ./internal/docsweep/ TestRegistered \
    "the job is registered, so something actually runs the sweep"

# The retry this change made necessary.
go_claim ./internal/events/ TestCommitPathsSucceedsOnceTheLockClears \
    "a contended index.lock is waited out rather than failing the command"
go_claim ./internal/events/ TestCommitPathsGivesUpOnAStuckLock \
    "and a stuck lock surfaces as an error rather than wedging the CLI"
go_claim ./internal/events/ TestCommitPathsDoesNotRetryAnUnrelatedFailure \
    "the retry is narrow: a misrouted commit fails on its first attempt"
go_claim ./internal/events/ TestCommitDocPathsMakesOneCommit \
    "the sweep's many files are ONE commit, not four hundred"

# The write path.
py_claim tests/test_doc_mirror_to_main.py::test_nothing_is_written_into_the_worktree \
    "a task update writes nothing into the task's worktree"
py_claim tests/test_doc_mirror_to_main.py::test_later_updates_add_no_commit_to_the_branch \
    "and adds no commit to its branch — the 123-of-139 regression, pinned"
py_claim tests/test_doc_mirror_to_main.py::test_sandbox_context_writes_nothing \
    "under a sandbox database nothing is written to disk at all"
py_claim tests/test_doc_mirror_to_main.py::test_a_failed_commit_warns_and_keeps_the_file \
    "a failed commit warns and keeps going — the DB write already succeeded"

# The branch-history cleanup, in order of what it would cost to get wrong.
py_claim tests/test_doc_strip.py::test_a_commit_bundling_a_mirror_with_source_keeps_the_source \
    "a commit holding BOTH a mirror and source keeps the source"
py_claim tests/test_doc_strip.py::test_nothing_but_the_mirror_paths_changes \
    "the rewritten branch differs from the original in mirror paths and nothing else"
py_claim tests/test_doc_strip.py::test_a_mirror_the_database_lacks_leaves_the_branch_alone \
    "a mirror the database does not have is never dropped — no side is guessed"
py_claim tests/test_doc_strip.py::test_an_unreadable_database_leaves_the_branch_alone \
    "'I could not ask' never reads as 'the database agrees'"
py_claim tests/test_doc_strip.py::test_a_mirror_the_base_already_had_is_restored_not_deleted \
    "a mirror main already had is restored, not proposed for deletion"
py_claim tests/test_doc_strip.py::test_author_and_dates_survive_the_rewrite \
    "retained commits keep their author, dates and message"
py_claim tests/test_doc_strip.py::test_a_worktree_on_the_branch_is_left_clean \
    "a worktree standing on the branch is left clean, not permanently dirty"
py_claim tests/test_doc_strip.py::test_a_worktree_mid_rebase_is_skipped \
    "a worktree mid-rebase is skipped — that ref is not ours to move"

# Orphan-branch recovery, widened from plans to every mirror kind.
py_claim tests/test_worktree_orphan_branch.py::test_every_mirror_kind_counts_as_a_mirror \
    "an orphan branch holding any mirror kind is reclaimable, not 'real work'"
py_claim tests/test_worktree_orphan_branch.py::test_verify_script_in_the_task_dir_is_real_work \
    "but a verify.sh in that same directory IS real work and blocks the reclaim"
py_claim tests/test_worktree_orphan_branch.py::test_empty_db_column_is_a_mismatch_not_an_adoption \
    "an empty column no longer silently adopts a branch's plan"

# ---------------------------------------------------------------------------
section "C. The structural guarantee, through the real binary"
# ---------------------------------------------------------------------------
# Built from this tree rather than taken from bin/, so the suite proves what is
# committed here. GIT_CONFIG_NOSYSTEM keeps a machine-wide setting out of the
# measurement; the runner has already given this suite a temp HOME.
export GIT_CONFIG_NOSYSTEM=1

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

REPO="${TMP}/repo"
git init -q --initial-branch=main "${REPO}" || setup_error "git init failed"
git -C "${REPO}" config user.name t
git -C "${REPO}" config user.email t@example.com
git -C "${REPO}" config commit.gpgsign false
printf 'x\n' >"${REPO}/f.txt"
git -C "${REPO}" add f.txt >/dev/null 2>&1 || setup_error "git add failed"
git -C "${REPO}" commit -qm initial || setup_error "git commit failed"

# A mirror written where task_cmd writes it, committed the way it commits it.
mkdir -p "${REPO}/.endless/tasks/e-4242"
printf '# the plan\n' >"${REPO}/.endless/tasks/e-4242/plan.md"
MAIN_OUT=$("${BIN}" event commit-doc \
    --project-root "${REPO}" \
    --path ".endless/tasks/e-4242/plan.md" \
    --subject "Endless: add plan for E-4242" 2>&1)
MAIN_RC=$?

assert_eq "a mirror commit on the main checkout succeeds" "0" "${MAIN_RC}"
assert_eq "and the subject is the one the writer asked for" \
    "Endless: add plan for E-4242" \
    "$(git -C "${REPO}" log -1 --format=%s)"
assert_eq "and it carries exactly that one file" \
    ".endless/tasks/e-4242/plan.md" \
    "$(git -C "${REPO}" show --name-only --format= HEAD | sed '/^$/d')"
assert_eq "and the working tree is left clean" "" \
    "$(git -C "${REPO}" status --porcelain)"

# The same call from a LINKED worktree. This is the guarantee: the branch is not
# somewhere a mirror CAN go, rather than somewhere writers are asked not to put
# one. Its .git is a file pointing into the main checkout's administrative area,
# which is exactly what ensureMainCheckout distinguishes.
WTDIR="${REPO}/.endless/worktrees/e-4242"
git -C "${REPO}" worktree add -q -b task/4242 "${WTDIR}" main \
    || setup_error "git worktree add failed"
mkdir -p "${WTDIR}/.endless/tasks/e-4242"
printf '# a branch-side plan\n' >"${WTDIR}/.endless/tasks/e-4242/plan.md"
BRANCH_OUT=$("${BIN}" event commit-doc \
    --project-root "${WTDIR}" \
    --path ".endless/tasks/e-4242/plan.md" \
    --subject "Endless: add plan for E-4242" 2>&1)
BRANCH_RC=$?

if [[ "${BRANCH_RC}" -ne 0 ]]; then
    report_pass "the same commit from a linked worktree is REFUSED"
else
    report_fail "the same commit from a linked worktree is REFUSED" \
        "a non-zero exit" "rc=0 — a mirror just landed on a task branch"
fi
assert_contains "and the refusal says why, naming the worktree" \
    "linked worktree" "${BRANCH_OUT}"
assert_eq "so the branch holds no commit of its own" "0" \
    "$(git -C "${WTDIR}" rev-list --count main..HEAD)"

# ---------------------------------------------------------------------------
section "D. The hook gate discriminates WITHIN the task's directory"
# ---------------------------------------------------------------------------
# A second scratch repository, REGISTERED, because the gate only runs for a
# registered project. The runner's isolation gives this suite a temp HOME; the
# Go side resolves its database under $HOME/.config/endless, so the Python CLI
# is pointed at exactly that directory and both halves agree about which
# database "registered" means.
#
# Every command below runs with cwd INSIDE the scratch repository rather than
# this worktree. That is not tidiness: the hook compares its own executable
# against the one a self_dev worktree expects, and running it from here would
# make a scratch build look like a provisioning fault.
HOOKHOME="${TMP}/home"
HOOKCFG="${HOOKHOME}/.config"
mkdir -p "${HOOKCFG}/endless" || setup_error "cannot create a scratch config dir"

REPO2="${TMP}/hooked"
git init -q --initial-branch=main "${REPO2}" || setup_error "git init failed"
git -C "${REPO2}" config user.name t
git -C "${REPO2}" config user.email t@example.com
printf 'x\n' >"${REPO2}/f.txt"
git -C "${REPO2}" add f.txt >/dev/null 2>&1
git -C "${REPO2}" commit -qm initial || setup_error "git commit failed"

if ! ( cd "${REPO2}" && HOME="${HOOKHOME}" XDG_CONFIG_HOME="${HOOKCFG}" \
        uv run --project "${WT}" endless project register "${REPO2}" \
        --name e2137hookprobe --label t --desc t --lang go --status active \
        >/dev/null 2>&1 ); then
    setup_error "cannot register the scratch project the gate needs"
fi

# The phrase the doc-mirror gate — and only it — prints. Matching on it means a
# refusal by the WORKTREE gate (which also fires in some of these cases) is not
# mistaken for this one.
GATE_PHRASE="task document mirror"

run_hook() { # run_hook TOOL KEY PATH
    local tool="$1" key="$2" path="$3" payload
    payload=$(printf '{"session_id":"e2137-verify","cwd":"%s","hook_event_name":"PreToolUse","tool_name":"%s","tool_input":{"%s":"%s","content":"x"}}' \
        "${REPO2}" "${tool}" "${key}" "${path}")
    HOOK_OUT=$(cd "${REPO2}" && printf '%s' "${payload}" \
        | HOME="${HOOKHOME}" XDG_CONFIG_HOME="${HOOKCFG}" "${BIN}" hook claude 2>&1)
    HOOK_RC=$?
}

check_refused() { # check_refused DESC
    if [[ "${HOOK_RC}" -eq 2 && "${HOOK_OUT}" == *"${GATE_PHRASE}"* ]]; then
        report_pass "$1"
        return
    fi
    report_fail "$1" "exit 2 + the doc-mirror refusal" \
        "rc=${HOOK_RC} | out=$(printf '%s' "${HOOK_OUT}" | head -3)"
}

check_silent() { # check_silent DESC
    if [[ "${HOOK_OUT}" != *"${GATE_PHRASE}"* ]]; then
        report_pass "$1"
        return
    fi
    report_fail "$1" "the doc-mirror gate NOT triggered" \
        "rc=${HOOK_RC} | out=$(printf '%s' "${HOOK_OUT}" | head -3)"
}

for stem in plan outcome analysis; do
    run_hook "Write" "file_path" "${REPO2}/.endless/tasks/e-999/${stem}.md"
    check_refused "Write .endless/tasks/e-999/${stem}.md → refused"
done

run_hook "Edit" "file_path" "${REPO2}/.endless/tasks/e-999/plan.md"
check_refused "Edit the same file → refused"

run_hook "NotebookEdit" "notebook_path" "${REPO2}/.endless/tasks/e-999/plan.md"
check_refused "NotebookEdit the same file → refused"

run_hook "Write" "file_path" "${REPO2}/.endless/plans/E-999.md"
check_refused "a legacy .endless/plans/E-999.md → still refused"

assert_contains "and the refusal names the three commands that replace it" \
    "--plan-file" "${HOOK_OUT}"

# The half that would be expensive to get wrong: a session's own files, in the
# very same directory.
run_hook "Write" "file_path" "${REPO2}/.endless/tasks/e-999/verify.sh"
check_silent "Write .endless/tasks/e-999/verify.sh → gate silent (the task's own)"

run_hook "Write" "file_path" "${REPO2}/.endless/tasks/e-999/verify.toml"
check_silent "Write .endless/tasks/e-999/verify.toml → gate silent"

run_hook "Write" "file_path" "${REPO2}/.endless/tasks/CLAUDE.md"
check_silent "Write .endless/tasks/CLAUDE.md → gate silent"

run_hook "Write" "file_path" "${REPO2}/src/endless/task_cmd.py"
check_silent "Write a normal source file → gate silent"

# ---------------------------------------------------------------------------
section "E. The writers that are gone are gone"
# ---------------------------------------------------------------------------
# Five functions put mirrors into worktrees: _materialize_task_docs,
# _materialize_task_doc, _materialize_plan_file, _commit_doc_in_worktree and
# _commit_plan_file_in_worktree. Deleting them is most of this change; a grep is
# the cheapest way to prove none came back under the same name.

absent() { # absent <symbol>
    local hits
    hits=$(grep -rn --include='*.py' -- "def $1" src/ 2>/dev/null)
    if [[ -z "${hits}" ]]; then
        report_pass "$1 is gone"
    else
        report_fail "$1 is gone" "no definition in src/" "${hits}"
    fi
}

absent "_materialize_task_docs"
absent "_materialize_task_doc"
absent "_materialize_plan_file"
absent "_commit_doc_in_worktree"
absent "_commit_plan_file_in_worktree"
absent "_mirror_doc_to_worktree"
absent "_mirror_plan_to_worktree"

# And no module CONSTRUCTS a legacy mirror path any more. The pattern is
# deliberately anchored on a quote, so a path spelled as a string literal is a
# hit and the same path discussed in a docstring — where several of these
# modules explain the history — is not.
MIRROR_LITERALS=$(grep -rn --include='*.py' -E "[\"']\.endless/(plans|outcomes|analyses)/" src/ || true)
assert_eq "no module builds a legacy mirror path as a literal" "" "${MIRROR_LITERALS}"

# The gate is WIRED, not merely written. A correct matcher whose call site went
# missing would pass every unit test in section B and refuse nothing.
assert_contains "the doc-mirror gate is called from the PreToolUse path" \
    "blockDocMirrorWriteIfApplicable(payload)" \
    "$(sed -n '/func handlePreToolUse/,/^}/p' internal/hookcmd/claude.go)"

summary
