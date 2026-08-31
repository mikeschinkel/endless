#!/usr/bin/env bash
#
# E-1997 verification — the unsupported-harness banner stays out of other
# commands' output.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1997
#
# Single entry point (per E-1596). Fail-fast on the unit contracts, then
# reproduce the reported symptom with a REAL zsh reading a REAL rc file.
#
#   0. The automated suite: tests/test_agent_env.py (the exemption set, the
#      invariant that constrains it, and the banner that must survive).
#   1. The reported symptom, end to end. `endless setup shell-helpers` appends
#      'eval "$(endless shell-init)"' to the user's rc, so under Claude Code
#      Desktop that ran on every shell launch and wrote the banner to stderr —
#      which `$( )` does not capture. It surfaced ahead of the output of an
#      unrelated `gh repo deploy-key add` the user had approved, and on every
#      terminal opened in Desktop. Here: a shell carrying the rc line must
#      produce output byte-identical to one without it.
#   2. The fix is silence, not amputation — the eval still defines esu/esp/esf.
#   3. The banner itself is untouched: a deliberate `endless` command under
#      Desktop still gets it, including the commands the helpers wrap.
#
# Exit 0 on all-passed, 1 on any failure.
#
# How the harness is simulated: by the environment, because that is the whole
# mechanism. Both dumps are transcribed from real harness processes (2026-08-13,
# see e-1962-verify.sh for why the SOURCE of a dump matters more than it looks).
#
# How the CANDIDATE code gets exercised: the global `endless` on PATH is an
# editable install pointing at the MAIN checkout, so an rc that says `endless`
# would test main and pass no matter what this branch does. A shim directory is
# prepended to PATH whose `endless` execs `uv run --directory <this worktree>
# endless`. The rc file therefore contains the byte-identical line the installer
# writes, and still resolves to this branch's source.
#
# Why compare against a baseline shell rather than assert "stderr is empty":
# zsh reads /etc/zshenv and friends before anything here runs, and any noise
# they make is not this task's. Two runs differing only by the rc line isolate
# exactly the bytes Endless is responsible for.
#
# What this suite does NOT do: run any other task's verify script. Those are
# pre-land gates for their own task in their own worktree, not a regression
# suite. Project-wide regression here is `go build/vet/test ./...` + `just test`.
#
# Model: .endless/tasks/e-1962/verify.sh.

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
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

REPO_ROOT=""
SHIM_DIR=""
RC_WITH=""
RC_WITHOUT=""

# The exact line `endless setup shell-helpers` appends to the user's rc. Kept as
# a literal so a change to SHELL_HELPERS_EVAL that this suite has not been
# taught about shows up as a failure rather than as a silently weaker test.
RC_LINE='eval "$(endless shell-init)"'

# Claude Code Desktop, from the HARNESS process (`ps eww` on the bundled claude
# binary inside Claude.app, Claude Code 2.1.227) — not from Desktop's Bash tool.
DESKTOP_ENV="CLAUDE_CODE_ENTRYPOINT=claude-desktop \
__CFBundleIdentifier=com.anthropic.claudefordesktop \
CLAUDE_AGENT_SDK_VERSION=0.3.227 \
CLAUDE_CODE_HOST_SESSION_ID=local_e44c9f73-0054-4197-ac66-6c6638fc3f95"

# A marker no rc file would print on its own.
MARKER="---E1997-COMMAND-OUTPUT---"

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
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

note() {
    printf '  %s•%s %s\n' "${DIM}" "${RESET}" "$1"
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

assert_text_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains '${needle}'" "${haystack:0:300}"
}

assert_text_lacks() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output WITHOUT '${needle}'" "${haystack:0:300}"
}

# ─── helpers ────────────────────────────────────────────────────────────────

# shell RCFILE HARNESS COMMAND -> combined stdout+stderr of an interactive zsh
# that read RCFILE and then ran COMMAND.
#
# Interactive (-i) on purpose: that is when a shell reads .zshrc, and "as soon
# as I open a terminal" is half the reported symptom. The other half is the Bash
# tool, whose shell reads the same file.
#
# ENDLESS_SESSION_ID is scrubbed from every run. The documented way to invoke
# this suite is `endless task verify E-1997`, and `esu` EXPORTS that
# variable — so a check written in a shell that had not run `esu` can pass for
# its author and fail for everyone following the instructions. The helpers
# branch on it (see _endless_run and esf), so leaving it inherited makes this
# suite's subject the caller's session state instead of the harness.
shell() {
    local rcfile="$1" harness="$2" cmd="$3"
    local zdotdir; zdotdir=$(dirname "${rcfile}")
    case "${harness}" in
        desktop)
            env -u ENDLESS_SESSION_ID ${DESKTOP_ENV} \
                ZDOTDIR="${zdotdir}" PATH="${SHIM_DIR}:${PATH}" \
                zsh -i -c "${cmd}" 2>&1 ;;
        terminal)
            env -u ENDLESS_SESSION_ID CLAUDE_CODE_ENTRYPOINT=cli \
                ZDOTDIR="${zdotdir}" PATH="${SHIM_DIR}:${PATH}" \
                zsh -i -c "${cmd}" 2>&1 ;;
        human)
            env -u ENDLESS_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT \
                -u CLAUDE_AGENT_SDK_VERSION -u __CFBundleIdentifier -u CLAUDECODE \
                ZDOTDIR="${zdotdir}" PATH="${SHIM_DIR}:${PATH}" \
                zsh -i -c "${cmd}" 2>&1 ;;
        *) printf 'BAD HARNESS %s' "${harness}" ;;
    esac
}

# cli HARNESS ARGS... -> `endless ARGS...` from THIS worktree's source.
cli() {
    local harness="$1"; shift
    case "${harness}" in
        desktop)  env -u ENDLESS_SESSION_ID ${DESKTOP_ENV} \
                      uv run --directory "${REPO_ROOT}" endless "$@" 2>&1 ;;
        terminal) env -u ENDLESS_SESSION_ID CLAUDE_CODE_ENTRYPOINT=cli \
                      uv run --directory "${REPO_ROOT}" endless "$@" 2>&1 ;;
        human)    env -u ENDLESS_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT \
                      -u CLAUDE_AGENT_SDK_VERSION -u __CFBundleIdentifier -u CLAUDECODE \
                      uv run --directory "${REPO_ROOT}" endless "$@" 2>&1 ;;
        *)        printf 'BAD HARNESS %s' "${harness}" ;;
    esac
}

# ─── Part 0: the automated suite ────────────────────────────────────────────

test_suite() {
    section "Part 0 — automated suite (fail-fast)"

    assert_succeeds "pytest tests/test_agent_env.py (exemption + banner)" \
        uv run --directory "${REPO_ROOT}" pytest tests/test_agent_env.py -q

    # The rc line this whole task is about. If the installer ever writes a
    # different command, that command needs the exemption too — and the parts
    # below would silently start testing the wrong one.
    local installed
    installed=$(uv run --directory "${REPO_ROOT}" python -c \
        'from endless import setup; print(setup.SHELL_HELPERS_EVAL)' 2>&1)
    assert_eq "the installer still writes the line this suite simulates" \
        "${RC_LINE}" "${installed}"

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sFail-fast: the unit contracts are broken; skipping the live parts.%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi
}

# ─── Part 1: the reported symptom ───────────────────────────────────────────

# The load-bearing part. Not "the banner is gone" — "a shell carrying Endless's
# own rc line produces exactly the bytes it would produce without it". That is
# the property the `gh` output needed and did not have.
test_no_leak_into_other_output() {
    section "Part 1 — Endless's rc line adds nothing to a shell's output"

    local harness with without
    for harness in desktop terminal human; do
        with=$(shell "${RC_WITH}" "${harness}" "echo ${MARKER}")
        without=$(shell "${RC_WITHOUT}" "${harness}" "echo ${MARKER}")
        assert_eq "${harness}: rc line changes nothing on the shell's output" \
            "${without}" "${with}"
        assert_text_lacks "${harness}: no banner" \
            'does not support' "${with}"
    done

    # The user's actual shape: an unrelated tool's output, unpolluted and first.
    # `git --version` stands in for `gh repo deploy-key add` — a real third-party
    # CLI, no side effects, no network, no auth.
    with=$(shell "${RC_WITH}" desktop "git --version")
    assert_text_lacks "desktop: an unrelated CLI's output carries no banner" \
        'Endless' "${with}"
    assert_text_lacks "desktop: and no false claim about that command" \
        'This command did not run' "${with}"
    note "reported as: the banner printed ahead of an approved 'gh repo deploy-key add'"
}

# ─── Part 2: silence, not amputation ────────────────────────────────────────

# A shell-init that printed nothing would also pass Part 1, and would break the
# helpers for everyone. The banner is what had to go; the snippet is what the rc
# line is FOR.
test_helpers_still_defined() {
    section "Part 2 — the eval still defines the helpers"

    local harness out
    for harness in desktop terminal human; do
        out=$(shell "${RC_WITH}" "${harness}" 'command -v esu esp esf')
        assert_text_contains "${harness}: esu defined" "esu" "${out}"
        assert_text_contains "${harness}: esp defined" "esp" "${out}"
        assert_text_contains "${harness}: esf defined" "esf" "${out}"
    done

    # Byte-identical across harnesses: no rc grows a harness-conditional branch,
    # and nobody has to reason about which snippet they got.
    local d t
    d=$(cli desktop shell-init)
    t=$(cli terminal shell-init)
    assert_eq "the snippet is the same text on every harness" "${t}" "${d}"
}

# ─── Part 3: the banner survives where it belongs ───────────────────────────

# The exemption is for commands an agent never types. Everything an agent DOES
# type must still be met with the banner — otherwise this fix has quietly
# undone E-1962, whose whole reason for existing is that CLAUDE.md files say
# "First: run `endless guide`".
test_banner_survives() {
    section "Part 3 — a deliberate command still refuses"

    local out
    out=$(cli desktop guide)
    assert_text_contains "desktop: \`endless guide\` still names the harness" \
        'does not support Claude Code Desktop' "${out}"
    assert_text_contains "desktop: still says the command did not run" \
        'This command did not run' "${out}"
    assert_text_lacks "desktop: still withholds the guide body" \
        'The happy path' "${out}"

    # The commands the helpers wrap. Defining esu is exempt; USING it is not.
    local cmd
    for cmd in "session use" "session forget" "task list"; do
        out=$(cli desktop ${cmd})
        assert_text_contains "desktop: \`endless ${cmd}\` still refuses" \
            'does not support Claude Code Desktop' "${out}"
    done

    # Calling a helper through a real shell, which is the path a Desktop user
    # takes: the function exists, runs endless, and meets the banner there.
    #
    # `esp`, not `esf`. esf short-circuits on an unset ENDLESS_SESSION_ID and
    # prints its own "no active session" from the SNIPPET, never reaching
    # endless at all — so asserting on that text proved nothing about the banner
    # and inverted with the caller's session state. esp always calls through.
    out=$(shell "${RC_WITH}" desktop 'esp')
    assert_text_contains "desktop: invoking a helper reaches the banner" \
        'does not support Claude Code Desktop' "${out}"

    out=$(cli terminal guide)
    assert_text_contains "terminal: the guide prints normally" \
        'Using Endless in a Claude Code Session' "${out}"

    out=$(cli human guide)
    assert_text_contains "bare shell: a human still gets the guide" \
        'Using Endless in a Claude Code Session' "${out}"
}

# ─── main ───────────────────────────────────────────────────────────────────

TMP_ROOT=""

cleanup() {
    [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]] && rm -rf "${TMP_ROOT}"
    return 0
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi
    if ! command -v zsh >/dev/null 2>&1; then
        printf 'ERROR: zsh not on PATH (the installer targets a zsh rc file)\n' >&2
        exit 2
    fi

    # Deliberately unset here: this suite's subject is the difference between
    # environments, so every call sets its own.
    unset CLAUDE_CODE_ENTRYPOINT

    TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/e-1997.XXXXXX") || exit 2
    trap cleanup EXIT

    SHIM_DIR="${TMP_ROOT}/bin"
    mkdir -p "${SHIM_DIR}"
    # `endless` must mean THIS worktree, or the rc line would test the main
    # checkout's editable install and pass regardless of this branch.
    {
        printf '#!/bin/sh\n'
        printf 'exec uv run --directory %s endless "$@"\n' "${REPO_ROOT}"
    } > "${SHIM_DIR}/endless"
    chmod +x "${SHIM_DIR}/endless"

    # Two ZDOTDIRs differing by exactly one line.
    mkdir -p "${TMP_ROOT}/with" "${TMP_ROOT}/without"
    RC_WITH="${TMP_ROOT}/with/.zshrc"
    RC_WITHOUT="${TMP_ROOT}/without/.zshrc"
    printf '# Endless: session shell helpers (esu/esp/esf)\n%s\n' "${RC_LINE}" > "${RC_WITH}"
    : > "${RC_WITHOUT}"

    printf '%sE-1997 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:      %s\n' "${REPO_ROOT}"
    printf '  shim:     %s/endless -> uv run --directory %s\n' "${SHIM_DIR}" "${REPO_ROOT}"
    printf '  rc line:  %s\n' "${RC_LINE}"

    test_suite
    test_no_leak_into_other_output
    test_helpers_still_defined
    test_banner_survives

    summary
}

main "$@"
