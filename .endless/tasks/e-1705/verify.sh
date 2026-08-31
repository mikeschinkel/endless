#!/usr/bin/env bash
#
# E-1705 verification — `endless task spawn` delivers its prompt via a CLI arg
# through the `endless-go spawn-window` / `spawn-launch` launcher, drops forced
# plan-mode, and removes the --no-plan flag.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1705
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# This one suite is the land-time gate. It folds in this task's own tests as a
# fail-fast prefix (the Go launcher unit tests, the SessionStart spawn-bind Go
# tests that prove the @endless_* options the launcher sets bind the session,
# and the Python spawn/attach path tests), then drives the compiled launcher
# end-to-end with recording `tmux`/`claude` shims on PATH — no real tmux window,
# no real claude, no DB pollution. The only irreducibly-interactive residue (a
# real spawn whose window lands in an interactive Claude already answering the
# handoff, and confirming UserPromptSubmit fires for the positional first turn)
# is documented as a manual step at the end.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}

# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
}

# ─── globals wired in main ───────────────────────────────────────────────────

REPO_ROOT=""
EGO=""          # path to the worktree-built endless-go
TMP=""
SHIMBIN=""      # dir holding the recording tmux/claude shims
TMUX_LOG=""
CLAUDE_LOG=""
CLAUDE_SHIM=""

# read_argv FILE  → populate the global `ARGV` array from a NUL-delimited log.
# (bash 3.2-safe; avoids `mapfile`, absent on macOS's system bash.)
ARGV=()
read_argv() {
    ARGV=()
    local a
    while IFS= read -r -d '' a; do ARGV+=("$a"); done < "$1"
}

# make_shims writes recording `tmux` and `claude` shims onto a private PATH dir.
make_shims() {
    SHIMBIN="${TMP}/bin"; mkdir -p "${SHIMBIN}"
    TMUX_LOG="${TMP}/tmux.log"
    CLAUDE_LOG="${TMP}/claude.argv"

    cat > "${SHIMBIN}/tmux" <<SHIM
#!/usr/bin/env bash
# Record each invocation (tab-joined args, one line) and exit success. It does
# NOT exec the window command — spawn-launch is exercised directly below.
{ for a in "\$@"; do printf '%s\t' "\$a"; done; printf '\n'; } >> "${TMUX_LOG}"
exit 0
SHIM

    cat > "${SHIMBIN}/claude" <<SHIM
#!/usr/bin/env bash
# Record the full argv NUL-delimited — \$0 (the program name exec set) plus
# \$@ — preserving newlines/quotes in the prompt, then exit.
printf '%s\0' "\$0" "\$@" > "${CLAUDE_LOG}"
exit 0
SHIM

    chmod +x "${SHIMBIN}/tmux" "${SHIMBIN}/claude"
    CLAUDE_SHIM="${SHIMBIN}/claude"
}

# ─── folded-in unit suites (fail-fast prefix) ───────────────────────────────

test_go_units() {
    section "Go units — launcher argv/options + SessionStart spawn-bind"
    local out rc
    out=$(cd "${REPO_ROOT}" && go test ./internal/spawnlaunchcmd/ 2>&1); rc=$?
    assert_eq "internal/spawnlaunchcmd tests pass" "0" "${rc}"
    [[ "${rc}" -ne 0 ]] && printf '%s\n' "${out}"
    out=$(cd "${REPO_ROOT}" && go test ./internal/hookcmd/ -run 'SpawnBind|SessionStartBind' 2>&1); rc=$?
    assert_eq "internal/hookcmd spawn-bind tests pass" "0" "${rc}"
    [[ "${rc}" -ne 0 ]] && printf '%s\n' "${out}"
}

test_python_units() {
    section "Python units — foreground spawn + attach launch mechanics"
    local out rc
    out=$(cd "${REPO_ROOT}" && uv run pytest -q \
        tests/test_spawn_foreground.py tests/test_task_attach.py 2>&1); rc=$?
    assert_eq "spawn/attach path tests pass" "0" "${rc}"
    [[ "${rc}" -ne 0 ]] && printf '%s\n' "${out}" | tail -30
}

# ─── integration against the compiled launcher ──────────────────────────────

test_spawn_window_no_sendkeys() {
    section "spawn-window — one tmux new-window launching spawn-launch, no send-keys"
    : > "${TMUX_LOG}"
    local hf; hf="${TMP}/handoff.md"; printf 'the handoff body' > "${hf}"

    PATH="${SHIMBIN}:${PATH}" "${EGO}" spawn-window \
        --claude-bin "${CLAUDE_SHIM}" --handoff-file "${hf}" \
        --permission-mode auto --task-id 1705 --project-id 3 \
        --spawned-by sess-x --window-name 'endless_deliver[E-1705]' \
        --cwd "${TMP}" >/dev/null 2>&1

    local log; log=$(cat "${TMUX_LOG}")
    assert_eq         "exactly one tmux new-window" "1" "$(grep -c 'new-window' "${TMUX_LOG}")"
    assert_contains   "window command is spawn-launch --spec" "spawn-launch" "${log}"
    assert_not_contains "no send-keys"    "send-keys"    "${log}"
    assert_not_contains "no load-buffer"  "load-buffer"  "${log}"
    assert_not_contains "no paste-buffer" "paste-buffer" "${log}"
    assert_not_contains "no /plan slash-command" "/plan" "${log}"

    # The real spawn-launch never ran (shim didn't exec it), so remove the spec
    # file it would have deleted, to keep the system temp dir clean.
    local spec; spec=$(grep -o '[^	]*endless-launchspec[^	]*' "${TMUX_LOG}" | head -1)
    [[ -n "${spec}" ]] && rm -f "${spec}"
}

test_spawn_launch_argv_and_options() {
    section "spawn-launch — sets @endless_* before exec, prompt bytes intact"
    : > "${TMUX_LOG}"; : > "${CLAUDE_LOG}"
    local hf spec expected
    hf="${TMP}/handoff-special.md"
    printf '%s' 'first line
`backtick` "double" $DOLLAR '\''single'\''

last line' > "${hf}"
    expected=$(cat "${hf}")

    spec="${TMP}/spec.json"
    cat > "${spec}" <<JSON
{"claude_bin":"${CLAUDE_SHIM}","handoff_file":"${hf}","permission_mode":"auto","model":"","name":"","task_id":"1705","project_id":"3","spawned_by":"sess-x","window_name":"win","cwd":"${TMP}"}
JSON

    TMUX_PANE='%7' PATH="${SHIMBIN}:${PATH}" "${EGO}" spawn-launch --spec "${spec}" >/dev/null 2>&1

    local tlog; tlog=$(cat "${TMUX_LOG}")
    assert_contains "@endless_spawned_by option set (=sess-x)" "@endless_spawned_by	sess-x" "${tlog}"
    assert_contains "@endless_task_id option set (=1705)"      "@endless_task_id	1705"     "${tlog}"
    assert_contains "@endless_project_id option set (=3)"      "@endless_project_id	3"      "${tlog}"
    assert_contains "options target the launch pane %7"        "-t	%7"                     "${tlog}"

    read_argv "${CLAUDE_LOG}"
    assert_eq "argv[1..2] = --permission-mode auto" "--permission-mode|auto" "${ARGV[1]:-}|${ARGV[2]:-}"
    assert_eq "argv length is 4 (bin, mode-flag, mode, prompt)" "4" "${#ARGV[@]}"
    assert_eq "positional prompt bytes intact (quotes/backticks/\$/blanklines)" "${expected}" "${ARGV[3]:-}"

    assert_eq "handoff file deleted by launcher" "gone" "$([[ -e ${hf} ]] && echo present || echo gone)"
    assert_eq "spec file deleted by launcher"    "gone" "$([[ -e ${spec} ]] && echo present || echo gone)"
}

test_spawn_launch_empty_prompt() {
    section "spawn-launch — empty/whitespace handoff ⇒ no positional argument"
    : > "${CLAUDE_LOG}"
    local hf spec
    hf="${TMP}/handoff-empty.md"; printf '   \n\t \n' > "${hf}"
    spec="${TMP}/spec-empty.json"
    cat > "${spec}" <<JSON
{"claude_bin":"${CLAUDE_SHIM}","handoff_file":"${hf}","permission_mode":"auto","task_id":"1","project_id":"1","spawned_by":"s","window_name":"w","cwd":"${TMP}"}
JSON

    TMUX_PANE='' PATH="${SHIMBIN}:${PATH}" "${EGO}" spawn-launch --spec "${spec}" >/dev/null 2>&1

    read_argv "${CLAUDE_LOG}"
    assert_eq "argv length is 3 (no positional prompt)" "3" "${#ARGV[@]}"
    assert_eq "argv still carries --permission-mode auto" "--permission-mode|auto" "${ARGV[1]:-}|${ARGV[2]:-}"
}

test_cli_surface() {
    section "CLI — spawn drops --no-plan, adds --permission-mode"
    local help; help=$(cd "${REPO_ROOT}" && uv run endless task spawn --help 2>&1)
    assert_not_contains "no --no-plan flag" "--no-plan" "${help}"
    assert_contains     "--permission-mode flag present" "--permission-mode" "${help}"
    assert_contains     "help documents the auto default" "auto" "${help}"
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    EGO="${REPO_ROOT}/bin/endless-go"
    [[ -x "${EGO}" ]] || { printf 'ERROR: %s not built — run `just build` first\n' "${EGO}" >&2; exit 2; }
    command -v go >/dev/null 2>&1 || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    TMP=$(mktemp -d)
    trap 'rm -rf "${TMP}"' EXIT
    make_shims

    printf '%sE-1705 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:       %s\n  endless-go: %s\n  scratch:    %s (throwaway)\n' \
        "${REPO_ROOT}" "${EGO}" "${TMP}"

    test_go_units
    test_python_units
    test_spawn_window_no_sendkeys
    test_spawn_launch_argv_and_options
    test_spawn_launch_empty_prompt
    test_cli_surface

    printf '\n%sManual live smoke (not automated — spawns a real interactive Claude):%s\n' "${DIM}" "${RESET}"
    printf '%s  1. From a self-dev worktree in tmux: endless task spawn <ready-task-id>%s\n' "${DIM}" "${RESET}"
    printf '%s  2. The new window lands DIRECTLY in Claude already answering the handoff%s\n' "${DIM}" "${RESET}"
    printf '%s     (no shell prompt, no typed /plan, no paste) and a follow-up message%s\n' "${DIM}" "${RESET}"
    printf '%s     can be typed immediately.%s\n' "${DIM}" "${RESET}"
    printf '%s  3. Confirm UserPromptSubmit fired for the positional first turn — Endless%s\n' "${DIM}" "${RESET}"
    printf '%s     depends on it. If it does NOT fire, stop and report.%s\n' "${DIM}" "${RESET}"

    summary
}

main "$@"
