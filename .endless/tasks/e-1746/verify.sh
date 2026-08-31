#!/usr/bin/env bash
#
# E-1746 verification — colorized markdown rendering for `endless task show`.
#
# Run from anywhere inside the worktree (after `esu`):
#   endless task verify E-1746
#
# Exercises the candidate end-to-end against the worktree's sandbox DB so both
# the Python CLI (`uv run endless`) and the Go binary (`./bin/endless-go`,
# reached via --db sandbox resolution) are the branch's builds, not main's.
#
# Output: pass/fail per check, then a summary.
#   exit 0  all checks passed
#   exit 1  any check failed
#   exit 2  setup error (not in a worktree, uv/binary missing, fixture failed)
#
# A real terminal is simulated headlessly with a Python pty so the color-on-TTY
# path is tested without a human. The only checks NOT automated here are the two
# interactive ones (see the handoff): paging with `-p` through less, and
# mouse-wheel scroll inside it.

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

# A long token that must never be broken mid-word by the renderer.
LONG_TOKEN="this-is-an-extremely-long-inline-code-identifier-that-must-not-break-midword"

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ──────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "$2"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── helpers ──────────────────────────────────────────────────────────────────

# Sandbox-routed Python CLI (candidate source via `uv run`, candidate Go binary
# via --db sandbox resolution).
endless() { uv run endless "$@" --db sandbox; }

# Run `endless task show <args> --db sandbox` under a Python pty so the child
# sees a TTY (isatty() == true) — the color-on-terminal path — and echo its
# output to stdout for capture. stdin is closed so the pty copy loop can't block.
pty_show() {
    uv run python -c '
import pty, sys
pty.spawn(["uv", "run", "endless", "task", "show"] + sys.argv[1:] + ["--db", "sandbox"])
' "$@" </dev/null
}

# True when stdin contains at least one ANSI escape (ESC[).
has_ansi() { grep -q $'\033\[' ; }

REPO_ROOT=""
GO_BIN=""
FIXTURE_ID=""

# ─── checks ───────────────────────────────────────────────────────────────────

check_tty_color_and_no_midword_break() {
    section "task show on a TTY: colorized, no mid-word break"
    local out
    out=$(pty_show "${FIXTURE_ID}" --text)

    if printf '%s' "${out}" | has_ansi; then
        report_pass "emits ANSI color on a TTY"
    else
        report_fail "emits ANSI color on a TTY" "no ESC[ sequences found in TTY output"
    fi

    if printf '%s' "${out}" | grep -qF "${LONG_TOKEN}"; then
        report_pass "long inline-code token is intact (not broken mid-word)"
    else
        report_fail "long inline-code token is intact (not broken mid-word)" \
            "the ${#LONG_TOKEN}-char token was not found contiguously in the output"
    fi
}

check_piped_no_ansi() {
    section "task show piped (| cat): zero ANSI"
    local out
    out=$(endless task show "${FIXTURE_ID}" --text | cat)
    if printf '%s' "${out}" | has_ansi; then
        report_fail "piped output contains zero ANSI escapes" \
            "found ESC[ sequences when stdout is a pipe"
    else
        report_pass "piped output contains zero ANSI escapes"
    fi
}

check_no_color_flag() {
    section "task show --no-color on a TTY: zero ANSI"
    local out
    out=$(pty_show "${FIXTURE_ID}" --text --no-color)
    if printf '%s' "${out}" | has_ansi; then
        report_fail "--no-color emits zero ANSI even on a TTY" \
            "found ESC[ sequences despite --no-color"
    else
        report_pass "--no-color emits zero ANSI even on a TTY"
    fi
}

check_markdown_render_subcommand() {
    section "endless-go markdown render: ANSI for heading / inline code / fenced code"

    local heading_out inline_out fenced_out
    heading_out=$(printf '## A Heading\n' | "${GO_BIN}" markdown render)
    inline_out=$(printf 'prose with `inline_code` here\n' | "${GO_BIN}" markdown render)
    fenced_out=$(printf '```go\nfenced_code_line\n```\n' | "${GO_BIN}" markdown render)

    if printf '%s' "${heading_out}" | has_ansi && printf '%s' "${heading_out}" | grep -qF "A Heading"; then
        report_pass "heading rendered with ANSI"
    else
        report_fail "heading rendered with ANSI" "no ESC[ or heading text missing"
    fi

    if printf '%s' "${inline_out}" | has_ansi && printf '%s' "${inline_out}" | grep -qF "inline_code"; then
        report_pass "inline code rendered with ANSI"
    else
        report_fail "inline code rendered with ANSI" "no ESC[ or inline text missing"
    fi

    if printf '%s' "${fenced_out}" | has_ansi && printf '%s' "${fenced_out}" | grep -qF "fenced_code_line"; then
        report_pass "fenced code rendered with ANSI (verbatim)"
    else
        report_fail "fenced code rendered with ANSI (verbatim)" "no ESC[ or code line missing"
    fi
}

# ─── setup ────────────────────────────────────────────────────────────────────

setup() {
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

    GO_BIN="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${GO_BIN}" ]]; then
        printf 'ERROR: %s missing or not executable — run `just build` first.\n' "${GO_BIN}" >&2
        exit 2
    fi

    # Fixture markdown: a heading, a paragraph carrying the long inline-code
    # token, and a fenced code block. Written to the sandbox DB's text field.
    local tmp
    tmp=$(mktemp)
    cat > "${tmp}" <<EOF
## Heading Two

A prose paragraph with a very long inline code span
\`${LONG_TOKEN}\` embedded in the middle of the sentence.

\`\`\`go
func main() { fmt.Println("verbatim code line that should overflow, not wrap") }
\`\`\`

- bullet with \`code\`
- second bullet
EOF

    local add_out
    add_out=$(endless task add "Render markdown fixture for e-1746 verify" --text-file "${tmp}" 2>&1)
    local rc=$?
    rm -f "${tmp}"
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: fixture task add failed:\n%s\n' "${add_out}" >&2
        exit 2
    fi
    FIXTURE_ID=$(printf '%s\n' "${add_out}" | grep -oE 'E-[0-9]+' | head -1)
    if [[ -z "${FIXTURE_ID}" ]]; then
        printf 'ERROR: could not parse fixture task id from:\n%s\n' "${add_out}" >&2
        exit 2
    fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1746 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:      %s\n' "${REPO_ROOT}"
    printf '  db:       sandbox\n'
    printf '  go bin:   %s\n' "${GO_BIN}"
    printf '  fixture:  %s\n' "${FIXTURE_ID}"

    check_tty_color_and_no_midword_break
    check_piped_no_ansi
    check_no_color_flag
    check_markdown_render_subcommand

    summary
}

main "$@"
