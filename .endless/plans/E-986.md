# E-986 — Pluggable post-worktree-create hook for project-specific bootstrap

## Context
Worktree creation (`create_task_worktree`, the single path both `task claim` and `task spawn`
use) can't bake in every project's bootstrap needs (Go go.mod replace paths, npm install, venv,
Rust target/, and — for endless itself — copying the binary + repointing the Claude hook). Make
bootstrap pluggable: at creation, run a **project-owned** script if present. Endless ships no
default; each project writes its own. The runner is generic shipped behavior (like git hooks);
the only self_dev-specific part is the *content* of endless's own hook script, absent for
everyone else.

## Mechanism (acceptance a–d)
In `create_task_worktree` (`src/endless/worktree_cmd.py`), after the worktree is materialized
(companion + plan written) and after `_maybe_auto_sandbox_bind`, run the project's hook if it
exists:

- **(a) Discovery:** `<project-root>/.endless/hooks/post-worktree-create.sh` — the main
  checkout's version (project-owned, version-controlled). `.endless/hooks/` must be **tracked**,
  not caught by a broad `.endless/` ignore (verify `.gitignore`).
- **(b) Invocation:** exec the script directly (its own shebang) with `cwd = <worktree-path>` and
  `argv[1] = <worktree-path>`. No shell-string interpolation.
- **(c) Failure handling — non-fatal + idempotent contract + loud:**
  - On non-zero exit, **keep** the worktree and surface a **loud** error naming the script, exit
    code, worktree path, and the re-run command.
  - The hook contract **requires idempotency / re-runnability**: completing a failed bootstrap is
    just re-running the hook, so the runner needs **no teardown logic** and a partial worktree is
    never silently usable-but-broken.
- **(d) Replace E-985's manual symlink:** ship endless's own
  `.endless/hooks/post-worktree-create.sh` doing the Go go-pkgs / `go-work-init` setup that's
  currently a manual step, proving the mechanism end-to-end.

## Files
- `src/endless/worktree_cmd.py` — the runner in `create_task_worktree` (discover/exec/non-fatal,
  after `_maybe_auto_sandbox_bind`).
- `.endless/hooks/post-worktree-create.sh` (new, in endless's repo) — initial Go go-pkgs/go-work
  bootstrap (acceptance d). E-1669 later extends this same script with the binary-copy +
  `claude-settings-init` repoint.
- `.gitignore` — confirm `.endless/hooks/` is tracked.
- `tests/tasks/e-986-verify.sh` — create from the appendix below (validated spec-first; currently
  3 pass / 4 fail, the 4 fails being exactly a–d).
- Docs: document the hook path + contract (project-facing shipped feature; no endless E-NNN ids
  in shipped docs).

## Verification
```
esu
./tests/tasks/e-986-verify.sh
```

## Appendix — verify script (create verbatim at `tests/tasks/e-986-verify.sh`)
Drafted and run during planning; goes green when the runner + endless's own hook land. Stored
here because E-986 has no worktree yet — create the file in E-986's worktree at implementation.

```bash
#!/usr/bin/env bash
#
# E-986 verification — pluggable post-worktree-create hook.
#
# Run from inside the worktree:
#   esu
#   ./tests/tasks/e-986-verify.sh
#
# Spec-first: this encodes E-986's acceptance criteria (a-d). It is RED until
# the hook runner lands in create_task_worktree - that is expected. T3 (no hook
# -> creation unaffected) passes against today's code; T1/T2/T4 go green when
# E-986 is implemented.
#
# Isolation: everything runs against a throwaway git project under a temp
# XDG_CONFIG_HOME/XDG_CACHE_HOME, so the real ledger and the worktree's sandbox
# are never touched. The temp tree is removed on exit.
#
# Candidate code: this verifies the INSTALLED `endless` (run from the temp
# project's cwd, so the self-dev gate doesn't apply). Pre-land that's main ->
# T1/T2/T4 are RED until E-986 lands; verify post-land, or install the candidate
# build first. (uv run --directory can't be used to route to the worktree source
# here: it changes cwd to the worktree, which trips the self-dev gate and
# mis-resolves the project - the very cwd-sensitivity this feature lives in.)
#
# Output: pass/fail per check + summary. Exit 0 all-passed, 1 any failure, 2 setup error.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="--------------------------------------------------------------"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s[ok]%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT+1)); }
report_fail() {
    printf '  %s[XX]%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT+1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

assert_file_contains() {
    local desc="$1" file="$2" needle="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${needle}" "${file}"; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "file ${file} contains: ${needle}" "$( [[ -f ${file} ]] && cat "${file}" || echo MISSING )"
}
assert_path_exists() { [[ -e "$2" ]] && { report_pass "$1"; return; }; report_fail "$1" "path exists: $2" "absent"; }
assert_contains()    { [[ "$2" == *"$3"* ]] && { report_pass "$1"; return; }; report_fail "$1" "output contains: $3" "$2"; }

WORKTREE_ROOT=""
TMP=""
cleanup() { [[ -n "${TMP}" && -d "${TMP}" ]] && rm -rf "${TMP}"; }
trap cleanup EXIT

endless() { command endless "$@"; }

LAST_ID=""; LAST_WT=""; LAST_OUT=""
claim_new_worktree() {
    LAST_OUT="$(endless task add "$1" 2>&1)"
    LAST_ID="$(printf '%s\n' "${LAST_OUT}" | grep -oE 'E-[0-9]+' | head -1)"
    [[ -z "${LAST_ID}" ]] && { LAST_WT=""; return 1; }
    LAST_OUT="$(endless task claim "${LAST_ID}" --no-session 2>&1)"
    LAST_WT="$(endless worktree for-task "${LAST_ID}" 2>/dev/null | tr -d '[:space:]')"
    return 0
}

write_hook() {
    mkdir -p "${PROJ}/.endless/hooks"
    printf '%s\n' "$1" > "${PROJ}/.endless/hooks/post-worktree-create.sh"
    chmod +x "${PROJ}/.endless/hooks/post-worktree-create.sh"
}
rm_hook() { rm -f "${PROJ}/.endless/hooks/post-worktree-create.sh"; }

test_hook_runs_with_arg_and_cwd() {
    section "T1 - hook runs with worktree path as arg and cwd"
    local sentinel="${TMP}/sentinel-t1"
    write_hook "#!/usr/bin/env bash
printf 'ARG=%s\n' \"\$1\" > '${sentinel}'
printf 'CWD=%s\n' \"\$(pwd)\" >> '${sentinel}'"
    claim_new_worktree "Verify E-986 hook arg/cwd" || { report_fail "claim created a worktree" "a worktree" "claim failed: ${LAST_OUT}"; return; }
    assert_path_exists "claim created the worktree" "${LAST_WT}"
    assert_file_contains "hook ran with worktree path as \$1" "${sentinel}" "ARG=${LAST_WT}"
    assert_file_contains "hook ran with cwd = worktree" "${sentinel}" "CWD=${LAST_WT}"
}

test_failure_non_fatal_and_loud() {
    section "T2 - hook failure is non-fatal and loud (worktree kept)"
    write_hook "#!/usr/bin/env bash
echo 'boom from post-worktree-create' >&2
exit 1"
    claim_new_worktree "Verify E-986 hook failure non-fatal" || { report_fail "claim still created a worktree" "a worktree" "claim failed: ${LAST_OUT}"; return; }
    assert_path_exists "worktree still created despite hook failure" "${LAST_WT}"
    assert_contains "claim surfaced a loud hook-failure warning" "${LAST_OUT}" "post-worktree-create"
}

test_no_hook_unaffected() {
    section "T3 - no hook present -> creation proceeds unchanged"
    rm_hook
    claim_new_worktree "Verify E-986 no hook" || { report_fail "claim created a worktree with no hook" "a worktree" "claim failed: ${LAST_OUT}"; return; }
    assert_path_exists "worktree created with no hook present" "${LAST_WT}"
}

test_endless_ships_its_hook() {
    section "T4 - endless ships its own post-worktree-create.sh"
    assert_path_exists "endless repo has .endless/hooks/post-worktree-create.sh" \
        "${WORKTREE_ROOT}/.endless/hooks/post-worktree-create.sh"
}

main() {
    WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -z "${WORKTREE_ROOT}" ]] && { echo "ERROR: run from inside the worktree" >&2; exit 2; }
    command -v endless >/dev/null 2>&1 || { echo "ERROR: endless not on PATH" >&2; exit 2; }

    TMP="$(mktemp -d)"
    export XDG_CONFIG_HOME="${TMP}/config" XDG_CACHE_HOME="${TMP}/cache"
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    PROJ="${TMP}/proj"
    mkdir -p "${PROJ}/.endless"
    printf '{"name": "e986-verify"}\n' > "${PROJ}/.endless/config.json"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email t@e.x; git -C "${PROJ}" config user.name t
    git -C "${PROJ}" config commit.gpgsign false
    printf 'x\n' > "${PROJ}/README"; git -C "${PROJ}" add -A; git -C "${PROJ}" commit -q -m init
    cd "${PROJ}" || exit 2
    endless register "${PROJ}" --infer --name e986-verify --status active >/dev/null 2>&1 || true

    printf '%sE-986 verification%s  (spec-first; RED until the hook runner lands)\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  endless:      %s\n  temp project: %s\n' "$(command -v endless)" "${PROJ}"

    test_hook_runs_with_arg_and_cwd
    test_failure_non_fatal_and_loud
    test_no_hook_unaffected
    test_endless_ships_its_hook
    summary
}

main "$@"
```
