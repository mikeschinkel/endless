#!/usr/bin/env bash
#
# E-2108 post-land: rename this repo's existing task/<id>-<slug> branches to
# task/<id>.
#
# Endless runs this once, right after E-2108's merge advances main, with:
#   - cwd     = the main checkout
#   - argv[1] = the main checkout's absolute path
#
# Why a script and not a schema change: the branches are git state, not database
# state, so `endless db apply-change` cannot reach them. This is the sanctioned
# way to perform a one-time action on main after a land.
#
# Why rename at all. ED-1587 makes a task branch `task/<id>` — a pure function of
# the id, which is what let this task retire task_landings.branch. New worktrees
# get the new name from `endless task claim`; the ~146 branches created before it
# would otherwise keep a slug forever, leaving one repo with two conventions. They
# are local-only — no remote, no PRs, no CI — which is exactly the fallout
# ED-1167 cited when it accepted the drift instead, and none of it applies here.
#
# Renaming under a live session is safe: `git branch -m` moves the ref and updates
# HEAD in whichever worktree has it checked out, so a session sitting in one keeps
# committing to the same branch under its new name. Nothing endless does looks a
# task branch up by name — `worktree land` reads the branch out of `git worktree
# list`, and the reaper asks git what a directory has checked out — so the rename
# is invisible to work in flight. The one thing that WOULD notice is the worktree
# companion, which records the expected branch and drives `worktree check`'s
# branch-mismatch anomaly; this script rewrites it in the same pass, so no
# worktree is left reporting an anomaly it did not have before.
#
# Idempotent: it selects branches by the `task/<id>-` shape, and a renamed branch
# no longer has it. A second run finds nothing and reports so. Re-running after a
# partial failure finishes the job.
#
# A downstream project on an older endless keeps its slug branches and keeps
# working — nothing derives a name it has to match. This script is Endless's own
# migration, not a general one, which is why it lives here rather than in a
# command.

set -u

MAIN_ROOT=""
RENAMED=0
SKIPPED=0
FAILED=0

# slug_branches lists every local branch still carrying a title slug, oldest
# first. Anything not matching `task/<digits>-` is left alone: a branch already
# renamed, and any hand-made `task/...` name this repo does not own.
slug_branches() {
    git for-each-ref --format='%(refname:short)' refs/heads/task \
    | grep -E '^task/[0-9]+-'
}

# update_companion rewrites the recorded branch in a worktree's companion file so
# `worktree check` keeps agreeing with git. The file is gitignored per-worktree
# state, so this touches nothing the land committed. A worktree that is gone, or
# whose companion names some other branch, is left exactly as it is.
update_companion() {
    local id="$1" old="$2" new="$3"
    local file tmp
    file="${MAIN_ROOT}/.endless/worktrees/e-${id}/.endless/worktree.json"
    if [[ ! -f "${file}" ]]; then
        return 0
    fi
    if ! grep -q "\"branch\": \"${old}\"" "${file}"; then
        return 0
    fi
    tmp="${file}.e2108"
    if ! sed "s#\"branch\": \"${old}\"#\"branch\": \"${new}\"#" \
        "${file}" > "${tmp}"; then
        rm -f "${tmp}"
        return 1
    fi
    mv "${tmp}" "${file}"
    return 0
}

# rename_one moves a single branch and its companion. A target name that already
# exists is reported rather than forced: two branches claiming one task id is a
# state a human should look at, and -M would silently destroy one of them.
rename_one() {
    local old="$1"
    local id new out
    id="${old#task/}"
    id="${id%%-*}"
    new="task/${id}"

    if git show-ref --verify --quiet "refs/heads/${new}"; then
        echo "  skip    ${old} — ${new} already exists" >&2
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if ! out=$(git branch -m "${old}" "${new}" 2>&1); then
        echo "  FAILED  ${old} -> ${new}: ${out}" >&2
        FAILED=$((FAILED + 1))
        return 0
    fi

    if ! update_companion "${id}" "${old}" "${new}"; then
        echo "  FAILED  ${old} -> ${new}: renamed, but its worktree companion" \
            "could not be updated" >&2
        FAILED=$((FAILED + 1))
        return 0
    fi

    RENAMED=$((RENAMED + 1))
    return 0
}

main() {
    local branches branch
    MAIN_ROOT="${1:?usage: e-2108.sh <main-checkout-path>}"
    cd "${MAIN_ROOT}" || return 1

    branches=$(slug_branches)
    if [[ -z "${branches}" ]]; then
        echo "e-2108 post-land: no task/<id>-<slug> branches left; nothing to do"
        return 0
    fi

    echo "e-2108 post-land: renaming task/<id>-<slug> branches to task/<id>"
    while IFS= read -r branch; do
        rename_one "${branch}"
    done <<< "${branches}"

    echo "e-2108 post-land: ${RENAMED} renamed, ${SKIPPED} skipped," \
        "${FAILED} failed"
    if [[ "${FAILED}" -gt 0 ]]; then
        echo "e-2108 post-land: re-run to finish:" \
            "${MAIN_ROOT}/.endless/hooks/post-land/e-2108.sh ${MAIN_ROOT}" >&2
        return 1
    fi
    return 0
}

main "$@"
