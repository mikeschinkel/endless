#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1801 and records what was true when E-1801
# landed. Edit it only if you ARE E-1801. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1801 verification script — `session resume --review`/`--reopen`: recover a
# session whose worktree was dropped after landing.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1801
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# WHY this shape: the recovery's LAST step is `os.execvp(claude, ...)`, which
# replaces the process — it can't be asserted from a script. So this script
# asserts everything UP TO the exec, in three layers:
#
#   1. FAIL-FAST GATE — the decision matrix (type gate, base chain, per-status
#      transition) is proven by the Python resolver suite, and the Go
#      resume-target recovery-field surfacing by the Go suite. Both run first;
#      any failure aborts before the slower integration below.
#   2. REAL-GIT INTEGRATION — a run_py snippet drives `recreate_dropped_worktree`
#      against a REAL throwaway git repo, asserting the physical outcome the unit
#      layer stubs: `--review` rebuilds DETACHED at the base, `--reopen` rebuilds
#      on the task BRANCH (fresh off base, or reusing the surviving branch).
#   3. LIVE CLI WIRING — the argument-shape guards that raise before any DB or
#      git access (mutually-exclusive flags; --print-decision requires an intent).
#
# Modeled on .endless/tasks/e-1577/verify.sh.

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

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    printf 'ERROR: not inside a git repository\n' >&2
    exit 2
}
cd "${REPO_ROOT}" || exit 2

# Wrap the CLI so every invocation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# assert_refused DESC PATTERN CMD [ARGS...]
#   Pass if CMD exits non-zero AND its combined output contains PATTERN.
assert_refused() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output contains: ${pattern}" \
        "exit=${rc} | output=${output}"
}

# ─── 1. fail-fast gate: unit suites ─────────────────────────────────────────

test_unit_gate() {
    section "Fail-fast gate — resolver + Go recovery-field unit suites"

    local out desc

    desc="Go: resume-target surfaces recovery fields (type/status/title/.landed)"
    out=$(go test ./internal/monitor/ -run 'TestResumeTarget_' -count=1 2>&1)
    if [[ $? -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "go test exits 0" "${out}"
        summary; exit 1
    fi

    desc="Python: --review/--reopen decision matrix (base chain, status table, gates)"
    out=$(uv run pytest tests/test_session_resume_recover.py -q 2>&1)
    if [[ $? -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "pytest exits 0" "${out}"
        summary; exit 1
    fi
}

# ─── 2. real-git integration ────────────────────────────────────────────────

test_git_integration() {
    section "Real git — recreate_dropped_worktree rebuilds detached / on-branch"

    local out
    # Hermetic XDG so the doc-materialization DB read hits an isolated (empty)
    # config, never the developer's real ledger. endless-go on PATH for it.
    out=$(cd "${REPO_ROOT}" && XDG_CONFIG_HOME="$(mktemp -d)" \
        PATH="${REPO_ROOT}/bin:${PATH}" uv run python - <<'PY' 2>&1
import json, subprocess, tempfile
from pathlib import Path
from endless import worktree_cmd as wc

def git(args, cwd):
    subprocess.run(["git", *args], cwd=str(cwd), check=True,
                   capture_output=True, text=True)

def rev(ref, cwd):
    return subprocess.run(["git", "rev-parse", ref], cwd=str(cwd),
                          capture_output=True, text=True).stdout.strip()

repo = Path(tempfile.mkdtemp())
git(["init", "-q"], repo)
git(["config", "user.email", "v@e1801"], repo)
git(["config", "user.name", "e1801-verify"], repo)
(repo / "f.txt").write_text("one\n")
git(["add", "."], repo); git(["commit", "-q", "-m", "c1"], repo)
sha1 = rev("HEAD", repo)                      # stand-in for an OLDER landing sha
(repo / "f.txt").write_text("two\n")
git(["add", "."], repo); git(["commit", "-q", "-m", "c2"], repo)
sha2 = rev("HEAD", repo)                      # latest landing sha (.landed)

# --review → detached at the base, no working branch.
wt = wc.recreate_dropped_worktree(101, "Review me", repo, sha1, detached=True)
assert wt.is_dir(), "review worktree not created"
assert rev("HEAD", wt) == sha1, f"review HEAD {rev('HEAD', wt)} != base {sha1}"
detached = subprocess.run(["git", "symbolic-ref", "-q", "HEAD"], cwd=str(wt),
                          capture_output=True, text=True).returncode != 0
assert detached, "review worktree is NOT detached"
comp = json.loads((wt / ".endless" / "worktree.json").read_text())
assert comp["branch"] is None, f"review companion branch not null: {comp['branch']!r}"

# --reopen (no existing branch) → fresh branch off the base.
slug = wc._slugify_title("Reopen me")
wt2 = wc.recreate_dropped_worktree(102, "Reopen me", repo, sha2, detached=False)
br = subprocess.run(["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=str(wt2),
                    capture_output=True, text=True).stdout.strip()
assert br == f"task/102-{slug}", f"reopen branch {br!r}"
assert rev("HEAD", wt2) == sha2, f"fresh reopen HEAD {rev('HEAD', wt2)} != {sha2}"

# --reopen (surviving branch) → reuse it (tip wins over the passed base).
git(["branch", "task/103-reuse-me", sha1], repo)
wt3 = wc.recreate_dropped_worktree(103, "Reuse me", repo, sha2, detached=False)
assert rev("HEAD", wt3) == sha1, \
    f"reopen should reuse branch tip {sha1}, got {rev('HEAD', wt3)}"

print("OK")
PY
)

    if [[ "${out}" == *"OK"* ]]; then
        report_pass "review→detached@base; reopen→fresh branch off base; reopen reuses surviving branch"
    else
        report_fail "recreate_dropped_worktree real-git behavior" \
            "snippet prints OK" "${out}"
    fi
}

# ─── 3. live CLI wiring ─────────────────────────────────────────────────────

test_cli_wiring() {
    section "Live CLI — argument-shape guards (raise before DB/git)"

    assert_refused "--review and --reopen are mutually exclusive" \
        "mutually exclusive" \
        endless session resume E-1 --review --reopen

    assert_refused "--print-decision requires an intent flag" \
        "print-decision" \
        endless session resume E-1 --print-decision
}

# ─── run ────────────────────────────────────────────────────────────────────

printf '%sE-1801 — session resume --review/--reopen%s\n' "${BOLD}" "${RESET}"

test_unit_gate
test_git_integration
test_cli_wiring

summary
