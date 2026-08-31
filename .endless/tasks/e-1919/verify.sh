#!/usr/bin/env bash
#
# E-1919 verification script — the Endless output style, embedded and delivered.
#
# The style gives E-1785's output-discipline contract a lever at the system-prompt
# layer: it is injected into the system prompt AND re-asserted every turn, which is
# the durability CLAUDE.md's user-turn context lacks. This does NOT replace E-1911 —
# it shapes only the ORGANIC half of the answer, while `task report` keeps producing
# the appended computed block that carries the guarantee.
#
# What is verified here:
#   1. The style ships embedded in the Go binary (no on-disk source needed).
#   2. Placement and activation are SEPARATE — a bare install never activates.
#   3. A bare install WARNS that the style is inert and names the activation route.
#   4. --activate sets settings.json outputStyle, preserving every other key.
#   5. remove deletes the file and deactivates ONLY when this style is selected —
#      a user's own choice of a different style is never clobbered.
#   6. A malformed settings.json is REFUSED, never overwritten.
#   7. `project init` scaffolds the style but leaves it inactive.
#   8. The style's content carries the contract: the inclusion bar, the veracity
#      rule, the disclosure-framing ban, and the E-1911 carve-out.
#   9. Nothing anywhere tells users to run the removed `/output-style` command.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1919
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: throwaway project roots under a temp dir plus a temp XDG_CONFIG_HOME
# and XDG_CACHE_HOME. No real DB, ledger, cache, or ~/.claude file is touched.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

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
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

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
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}
assert_file()     { if [[ -f "$2" ]]; then report_pass "$1"; else report_fail "$1" "file exists: $2" "missing"; fi; }
assert_no_file()  { if [[ ! -f "$2" ]]; then report_pass "$1"; else report_fail "$1" "file absent: $2" "present"; fi; }

# ─── setup ───────────────────────────────────────────────────────────────────

WT=""; TMP=""; GO_BIN=""

setup() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)" || {
        printf 'setup: not inside a git worktree\n' >&2; exit 2; }
    GO_BIN="${WT}/bin/endless-go"
    if [[ ! -x "${GO_BIN}" ]]; then
        printf 'setup: %s missing — run `just build` first\n' "${GO_BIN}" >&2; exit 2
    fi
    TMP="$(mktemp -d)" || { printf 'setup: mktemp failed\n' >&2; exit 2; }
    export XDG_CONFIG_HOME="${TMP}/config"
    export XDG_CACHE_HOME="${TMP}/cache"
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"
}
cleanup() { [[ -n "${TMP}" && -d "${TMP}" ]] && rm -rf "${TMP}"; }
trap cleanup EXIT

# new_project NAME: make a throwaway project root (a .endless/ dir is all the Go
# command needs to resolve a project from cwd). Echoes the path.
new_project() {
    local p="${TMP}/$1"
    mkdir -p "${p}/.endless"
    printf '%s' "${p}"
}
# OS ROOT ARGS...: run the outputstyle command from a project root, merging streams.
OS() { local root="$1"; shift; ( cd "${root}" && "${GO_BIN}" outputstyle "$@" 2>&1 ); }

STYLE_REL=".claude/output-styles/Endless.md"

setup

# ─── 1. embedded delivery ────────────────────────────────────────────────────

section "1. The style ships embedded in the Go binary"

P="$(new_project embed1)"
OUT="$(OS "${P}" install)"
assert_file   "install writes the style file" "${P}/${STYLE_REL}"
assert_contains "install reports the path" "${STYLE_REL}" "${OUT}"

# The embedded copy is the ONLY source — nothing was read from the repo.
BODY="$(cat "${P}/${STYLE_REL}")"
assert_contains "embedded content has the style frontmatter name" "name: Endless" "${BODY}"
assert_contains "frontmatter keeps the coding instructions" "keep-coding-instructions: true" "${BODY}"

OUT="$(OS "${P}" path)"
assert_contains "path prints the install location without writing" "${STYLE_REL}" "${OUT}"

# ─── 2 & 3. placement is separate from activation, and says so ───────────────

section "2/3. A bare install places but never activates — loudly"

P="$(new_project inert)"
OUT="$(OS "${P}" install)"
assert_no_file  "bare install creates no settings.json" "${P}/.claude/settings.json"
assert_contains "bare install warns it is NOT ACTIVE" "NOT ACTIVE" "${OUT}"
assert_contains "warning names the activation command" "/config output-style=Endless" "${OUT}"
assert_contains "warning names the --activate alternative" "--activate" "${OUT}"

OUT="$(OS "${P}" install)"
assert_contains "re-install is idempotent" "already present" "${OUT}"

# ─── 4. activation preserves other keys ──────────────────────────────────────

section "4. --activate selects the style, preserving other settings"

P="$(new_project activate)"
mkdir -p "${P}/.claude"
printf '%s\n' '{"enabledPlugins":{"demo":true}}' > "${P}/.claude/settings.json"
OUT="$(OS "${P}" install --activate)"
assert_contains "activate reports success" "activated" "${OUT}"

SETTINGS="$(cat "${P}/.claude/settings.json")"
assert_contains "outputStyle is set to Endless" '"outputStyle": "Endless"' "${SETTINGS}"
assert_contains "unrelated keys survive activation" '"enabledPlugins"' "${SETTINGS}"
assert_contains "nested unrelated values survive activation" '"demo": true' "${SETTINGS}"

OUT="$(OS "${P}" install --activate)"
assert_contains "re-activation is idempotent" "already active" "${OUT}"

# ─── 5. remove deactivates only its own style ────────────────────────────────

section "5. remove deletes and deactivates — but never clobbers another style"

OUT="$(OS "${P}" remove)"
assert_no_file  "remove deletes the style file" "${P}/${STYLE_REL}"
assert_contains "remove reports deactivation" "deactivated" "${OUT}"
SETTINGS="$(cat "${P}/.claude/settings.json")"
assert_not_contains "outputStyle key is gone" "outputStyle" "${SETTINGS}"
assert_contains "unrelated keys survive removal" '"enabledPlugins"' "${SETTINGS}"

OUT="$(OS "${P}" remove)"
assert_contains "remove is idempotent" "not present" "${OUT}"

# A style the USER chose must be left strictly alone.
P="$(new_project foreign)"
mkdir -p "${P}/.claude"
printf '%s\n' '{"outputStyle":"Explanatory"}' > "${P}/.claude/settings.json"
OS "${P}" install >/dev/null 2>&1
OS "${P}" remove  >/dev/null 2>&1
SETTINGS="$(cat "${P}/.claude/settings.json")"
assert_contains "a user-selected different style is NOT deactivated" "Explanatory" "${SETTINGS}"

# ─── 6. malformed settings are refused, not destroyed ────────────────────────

section "6. A malformed settings.json is refused, never overwritten"

P="$(new_project malformed)"
mkdir -p "${P}/.claude"
printf '%s\n' '{not json' > "${P}/.claude/settings.json"
OUT="$(OS "${P}" install --activate)"; RC=$?
assert_contains "activation refuses to parse-and-clobber" "refusing to overwrite" "${OUT}"
assert_eq "activation exits non-zero on malformed settings" "1" "${RC}"
assert_eq "the malformed file is left byte-identical" '{not json' "$(cat "${P}/.claude/settings.json")"

# ─── 7. project init scaffolds without activating ────────────────────────────

section "7. project init scaffolds the style but leaves it inactive"

P="${TMP}/initproj"; mkdir -p "${P}"
INIT_OUT="$( cd "${P}" && PATH="${WT}/bin:${PATH}" \
    uv run --project "${WT}" endless project init "${P}" --infer --name e1919proj 2>&1 )"
assert_file    "project init scaffolds the style file" "${P}/${STYLE_REL}"
assert_no_file "project init does NOT activate it" "${P}/.claude/settings.json"
assert_contains "project init surfaces the not-active warning" "NOT ACTIVE" "${INIT_OUT}"

# ─── 8. the style carries the contract ───────────────────────────────────────

section "8. Style content carries E-1785's contract"

P="$(new_project content)"
OS "${P}" install >/dev/null 2>&1
BODY="$(cat "${P}/${STYLE_REL}")"

# Inclusion bar, not an exclusion test: silence is the default.
assert_contains "default is silence"            "The default is silence"       "${BODY}"
assert_contains "bar: must take an action"      "must take an action"          "${BODY}"
assert_contains "bar: departed from the plan"   "departed from the plan"       "${BODY}"
assert_contains "bar: unfixed bug is the news"  "did NOT fix"                  "${BODY}"
assert_contains "bar: untaken concern"          "took no action on"            "${BODY}"

# Success reporting is suppressed; failures are not.
assert_contains "work that went as expected is discharged" "went as expected"  "${BODY}"
assert_contains "tests reported only on failure"           "only when it"      "${BODY}"
assert_contains "session status output is discharged"      "session status"    "${BODY}"

# The disclosure-framing ban.
assert_contains "bans narrating the disclosure" "Never narrate the disclosure" "${BODY}"
assert_contains "names the framing tic"         "worth noting"                 "${BODY}"

# Veracity.
assert_contains "verify at the moment of surfacing" "at the moment you state it" "${BODY}"
assert_contains "own earlier message is not a source" "not a source"             "${BODY}"

# Lead with IDs.
assert_contains "leads with bare IDs" "Lead with the IDs" "${BODY}"

# The E-1911 carve-out — the style must never justify swallowing the block.
assert_contains "task report block is exempt"   "task report" "${BODY}"
assert_contains "block must be appended unchanged" "unchanged" "${BODY}"

# ─── 9. no reference to the removed slash command ────────────────────────────

section "9. Nothing references the removed /output-style command"

# Claude Code 2.1.x has no `/output-style` command — it is a /config row. Any
# doc or help text saying otherwise sends users to a dead end.
# Match `/output-style` only as a bare command reference: not the
# `output-styles/` directory (trailing s), not the `output-style=` config key,
# and not the line that explicitly tells users the command does NOT exist.
HITS="$( cd "${WT}" && grep -rnE -- '/output-style([^-a-z=]|$)' \
    --include='*.go' --include='*.py' --include='*.md' --include='*.sh' \
    internal src docs tests README.md CLAUDE.md 2>/dev/null \
    | grep -viE 'there is no .?/output-style' \
    | grep -v 'e-1919-verify.sh' || true )"
assert_eq "no doc or help text says to run /output-style" "" "${HITS}"

# The Go help and warning must point at /config instead.
assert_contains "help points at the /config form" "output-style=Endless" "$(OS "$(new_project helptext)" install)"

summary
