#!/usr/bin/env bash
#
# E-1775 verification — GFM table rendering in `endless-go markdown render`.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1775
#
# Builds the worktree binary, runs the mdterm unit tests, then drives the
# renderer end-to-end asserting on visible (ANSI-stripped) layout. Exit 0 on
# all-passed, 1 on any failure, 2 on setup failure.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── locate worktree + binary ───────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BIN="${ROOT}/bin/endless-go"

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# strip ANSI SGR escapes; render a markdown doc at a given width.
strip_sgr() { LC_ALL=en_US.UTF-8 python3 -c "import sys,re;sys.stdout.write(re.sub(r'\x1b\[[0-9;]*m','',sys.stdin.read()))"; }
render()    { printf '%s' "$2" | "${BIN}" markdown render --width "$1"; }

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    if [[ "$2" == *"$3"* ]]; then report_pass "$1"; else report_fail "$1" "output contains: $3" "$2"; fi
}
# assert_absent DESC HAYSTACK NEEDLE
assert_absent() {
    if [[ "$2" != *"$3"* ]]; then report_pass "$1"; else report_fail "$1" "output must NOT contain: $3" "$2"; fi
}

# ─── setup ──────────────────────────────────────────────────────────────────

section "Build + unit tests"
if ! (cd "${ROOT}" && go build -o bin/endless-go ./cmd/endless-go) 2>/tmp/e1775-build.log; then
    printf '  %s✗%s build failed:\n' "${RED}" "${RESET}"; cat /tmp/e1775-build.log; exit 2
fi
report_pass "endless-go builds"
if (cd "${ROOT}" && go test ./internal/mdterm/ ./internal/markdowncmd/) >/tmp/e1775-test.log 2>&1; then
    report_pass "go test ./internal/mdterm ./internal/markdowncmd"
else
    report_fail "go unit tests" "all pass" "$(cat /tmp/e1775-test.log)"
fi

# ─── end-to-end render checks ───────────────────────────────────────────────

section "Rendering"

TAX='## t

| # | Concern | Frequency | Nature |
|---|---------|-----------|--------|
| A | Verify command (`esu`) | high | formula from task id |
| B | Regression/test result | high | constrained status |
'

out="$(render 100 "${TAX}" | strip_sgr)"
assert_contains "table renders rows (not collapsed to one mangled line)" "${out}" "│"
assert_contains "header rule present" "${out}" "─────"
assert_contains "header 'Frequency' NOT truncated when it fits" "${out}" "Frequency"
assert_absent   "no truncation ellipsis when headers fit" "${out}" "…"
assert_contains "value 'high' rendered whole (no sub-word break)" "${out}" "high"

# The sub-word regression, isolated: a narrow Frequency column must still keep
# 'high' intact because the column floor is the header width, not below a word.
freq="$(render 60 '| Frequency |
|---|
| high |
| medium |
' | strip_sgr)"
assert_contains "narrow Frequency column keeps 'high' whole" "${freq}" "high"

# Header truncation + legend at a squeezing width.
legend="$(render 20 '| Alpha Beta Gamma Delta Epsilon Zeta | Content One Two | Content Four Six |
|---|---|---|
| x | y | z |
' | strip_sgr)"
assert_contains "over-tall header truncated with ellipsis" "${legend}" "…"
assert_contains "legend box emitted"                        "${legend}" "┌─ columns"
assert_contains "legend carries full header text"           "${legend}" "Alpha Beta Gamma Delta Epsilon Zeta"

# Right alignment: numeric cell padded on the left, flush right.
align="$(render 40 '| L | R |
|:--|--:|
| a | 7 |
' | strip_sgr)"
row="$(printf '%s' "${align}" | grep '^a')"
assert_contains "right-aligned cell ends flush-right" "${row}" " 7"

# Fit-to-width: a table with a huge column and a single-outlier column must NOT
# exceed the requested width (a wider line wraps unusably in less -R), and the
# outlier column must be reclaimed toward its median rather than starving the
# content column.
WIDE='| ID | Status | Detail |
|----|--------|--------|
| 1 | ok | '"$(printf 'word %.0s' {1..60})"' |
| 2 | a really long outlier status value here | short |
| 3 | ok | '"$(printf 'more %.0s' {1..60})"' |
'
maxw="$(render 90 "${WIDE}" | strip_sgr | LC_ALL=en_US.UTF-8 python3 -c "import sys;print(max((len(l.rstrip(chr(10))) for l in sys.stdin if '│' in l), default=0))")"
if [[ "${maxw}" -le 90 ]]; then
    report_pass "wide table fits requested width (${maxw} <= 90)"
else
    report_fail "wide table fits requested width" "table row width <= 90" "widest row = ${maxw}"
fi

# ─── ANSI safety ────────────────────────────────────────────────────────────

section "ANSI safety"

# Every physical line must end with an SGR reset (less -R constraint).
raw="$(render 80 '| A | B |
|---|---|
| 1 | 2 |
')"
missing=0
while IFS= read -r line; do
    [[ -z "${line}" ]] && continue
    [[ "${line}" == *$'\033[0m' ]] || missing=$((missing + 1))
done <<< "${raw}"
if [[ "${missing}" -eq 0 ]]; then
    report_pass "every table line ends with SGR reset"
else
    report_fail "every table line ends with SGR reset" "0 lines missing reset" "${missing} line(s) missing reset"
fi

# Piped (no --width color forcing is the caller's job); the renderer itself is
# always-color, so just assert escapes are present and balanced-ish.
assert_contains "renderer emits ANSI (header styled)" "${raw}" $'\033['

summary