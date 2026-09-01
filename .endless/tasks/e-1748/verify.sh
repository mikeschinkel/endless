#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1748 and records what was true when E-1748
# landed. Edit it only if you ARE E-1748. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1748 verification script — project-local .endless/tmp scratch dir.
#
# Guides, hook messages, and CLI guidance used to tell agents to author scratch
# content (plans, outcomes) at a system /tmp path, then load it via
#   endless task update <id> --text-file <path>
# /tmp is global + ephemeral: content only *referenced* from the ledger, or left
# when a worktree is dropped, is lost — and /tmp is wiped on reboot. This task
# sanctions a project-local, gitignored `.endless/tmp` scratch dir and points
# every /tmp recommendation at it. This is the "suspenders" half of
# belt-and-suspenders: a forgotten-but-not-yet-loaded scratch file stays
# co-located and recoverable instead of vaporized.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1748
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a precondition failure. The register check writes to the
# worktree's sandbox DB (via `uv run endless ... --db sandbox`) and cleans up
# after itself (unregister + rm the throwaway repo); nothing touches the real DB.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

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
    local desc="$1"
    local expected="$2"
    local actual="$3"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "${expected}"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "${actual}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("${desc}")
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── helpers ────────────────────────────────────────────────────────────────

# Wrap the CLI so every invocation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# ─── 1. .endless/tmp is live-gitignored in this repo ────────────────────────

test_gitignore_active() {
    section "1 — .endless/tmp/ is gitignored in this repo"

    if git check-ignore .endless/tmp/probe.md >/dev/null 2>&1; then
        report_pass "git check-ignore matches .endless/tmp/probe.md"
    else
        report_fail "git check-ignore matches .endless/tmp/probe.md" \
            "exit 0 (path ignored)" "exit != 0 (path NOT ignored)"
    fi

    # A real file written under .endless/tmp/ must not surface in git status.
    mkdir -p .endless/tmp
    local probe=".endless/tmp/e1748-verify-probe.$$"
    printf 'scratch\n' > "${probe}"
    local status
    status=$(git status --porcelain "${probe}" 2>&1)
    if [[ -z "${status}" ]]; then
        report_pass "a file under .endless/tmp/ does not appear in git status"
    else
        report_fail "a file under .endless/tmp/ does not appear in git status" \
            "empty git status" "${status}"
    fi
    rm -f "${probe}"
}

# ─── 2. `endless project register` scaffolds .gitignore + the scratch dir ───────────

test_register_scaffolds() {
    section "2 — endless project register scaffolds .gitignore + .endless/tmp/"

    local base repo name
    base=$(mktemp -d)
    repo="${base}/e1748probe"
    name="e1748probe$$"
    mkdir -p "${repo}"
    git -C "${repo}" init -q
    touch "${repo}/main.py"

    endless project register "${repo}" --infer --name "${name}" >/dev/null 2>&1

    local gi="${repo}/.gitignore"
    local entry
    for entry in ".endless/worktrees/" ".endless/tmp/"; do
        if [[ -f "${gi}" ]] && grep -qF -- "${entry}" "${gi}"; then
            report_pass "register wrote canonical entry ${entry}"
        else
            report_fail "register wrote canonical entry ${entry}" \
                "${entry} present in ${gi}" "absent"
        fi
    done

    if [[ -d "${repo}/.endless/tmp" ]]; then
        report_pass "register created the .endless/tmp/ directory"
    else
        report_fail "register created the .endless/tmp/ directory" \
            "directory exists" "missing"
    fi

    # Idempotent: a second register must not duplicate any canonical line.
    endless project register "${repo}" --infer --name "${name}" >/dev/null 2>&1
    local dupes=0
    for entry in ".endless/worktrees/" ".endless/tmp/"; do
        local n
        n=$(grep -cF -- "${entry}" "${gi}")
        if [[ "${n}" -ne 1 ]]; then
            dupes=1
            report_fail "re-register left exactly one ${entry}" \
                "count == 1" "count == ${n}"
        fi
    done
    if [[ "${dupes}" -eq 0 ]]; then
        report_pass "re-register is idempotent (no duplicate .gitignore lines)"
    fi

    # Cleanup: remove the sandbox row and the throwaway repo.
    endless project unregister "${name}" >/dev/null 2>&1
    rm -rf "${base}"
}

# ─── 3. worktree bootstrap creates .endless/tmp ─────────────────────────────

test_worktree_bootstrap() {
    section "3 — worktree bootstrap creates .endless/tmp/"

    # Behavioral proof of the mkdir path is covered by test 2 (register creates
    # the same dir). Spinning a real endless worktree here would add a git
    # worktree + full bootstrap side effects to a verification run, so instead
    # assert the creation is wired into create_task_worktree at the source.
    local src="src/endless/worktree_cmd.py"
    if grep -qE 'companion_dir / "tmp"\)\.mkdir' "${src}"; then
        report_pass "create_task_worktree mkdirs .endless/tmp (${src})"
    else
        report_fail "create_task_worktree mkdirs .endless/tmp (${src})" \
            'companion_dir / "tmp").mkdir present' "absent"
    fi
}

# ─── 4. no /tmp scratch-for-content recommendation remains ──────────────────

test_no_tmp_scratch_recs() {
    section "4 — no /tmp scratch-for-content recommendation remains"

    # Flags the anti-pattern shapes only: the phrase "temp path", or a system
    # `/tmp/<file>.md|.txt|.rst` scratch path (preceded by whitespace/quote — a
    # redirect target or flag arg). Intentional OS-temp mentions that are NOT
    # scratch recommendations stay clear: cli.py's "a /tmp path is lost" warning
    # (no .md), the SessionStart `cwd":"/tmp"` example, and docs describing
    # Claude's own `~/.claude/jobs/<id>/tmp/` feature.
    local pattern='[Tt]emp path|(^|[[:space:]"'"'"'`])/tmp/[A-Za-z0-9_.-]*\.(md|txt|rst)'
    local hits
    hits=$(grep -rnE \
        --include="*.md" --include="*.py" --include="*.go" --include="*.tmpl" \
        -- "${pattern}" docs/ src/ internal/ 2>/dev/null \
        | grep -v "_test.go" || true)
    if [[ -z "${hits}" ]]; then
        report_pass "no /tmp scratch recommendation in docs/, src/, or internal/"
    else
        report_fail "no /tmp scratch recommendation in docs/, src/, or internal/" \
            "zero matches" "${hits}"
    fi

    # The sanctioned replacement IS present in the guidance surfaces.
    if grep -qF -- ".endless/tmp/" internal/hookcmd/claude.go; then
        report_pass "hook message points at .endless/tmp/ (internal/hookcmd/claude.go)"
    else
        report_fail "hook message points at .endless/tmp/ (internal/hookcmd/claude.go)" \
            ".endless/tmp/ present" "absent"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1748 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_gitignore_active
    test_register_scaffolds
    test_worktree_bootstrap
    test_no_tmp_scratch_recs

    summary
}

main "$@"
