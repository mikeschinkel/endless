#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1939 and records what was true when E-1939
# landed. Edit it only if you ARE E-1939. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1939 verification script — the web dashboard and its build stack are gone.
#
# What was removed: the `internal/web` package tree (components, pages,
# templates, data, static, assets), `internal/servecmd` (its only caller), the
# `serve` subcommand on both the Go dispatcher and the Python CLI, the justfile
# recipes that drove templ / tailwindcss / templUI, the Go module requirements
# that existed only for that stack, and the tracked generated artifacts
# (`*_templ.go`, the built CSS).
#
# Removal is easy to do PARTIALLY, which is why this suite leans on absence
# checks rather than on "it still builds". A leftover import, a `serve` case
# still wired into the dispatcher, a justfile recipe shelling to a binary
# nobody installs any more, or a stale `go.mod` require — each survives a
# green build and only bites the next person to clone the repo.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1939
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Section A is a FAIL-FAST gate and is the load-bearing check of the whole
# suite: it builds the project with SABOTAGED `templ` and `tailwindcss` shims
# first on PATH. Any surviving invocation of either tool aborts the build
# loudly instead of silently succeeding on a developer machine that happens to
# still have them installed. Everything after it asserts a narrower absence
# that is meaningless if the build still needs the toolchain.
#
# Section H exercises the post-land script against a throwaway git repo, not
# the real main checkout — the script deletes the untracked templUI symlink
# residue that the merge cannot remove, and a bug there would delete files on
# main. Nothing in this suite touches the real DB, ledger or main checkout.

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
note()        { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }
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

setup_fail() { printf '\n%sSETUP FAILED:%s %s\n\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output containing: $2" "$(printf '%s' "$3" | head -5)"; fi
}

# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output WITHOUT: $2" "$(printf '%s' "$3" | grep -n -- "$2" | head -5)"; fi
}

# assert_absent_path DESC PATH
assert_absent_path() {
    if [[ ! -e "$2" ]]; then report_pass "$1"
    else report_fail "$1" "$2 absent" "$2 still exists"; fi
}

# assert_no_matches DESC LABEL <grep args...>
# Passes when the grep prints nothing; the matches are shown on failure.
# Note stderr is NOT swallowed: a malformed grep would otherwise print
# nothing on stdout and read as a pass — a vacuous check is worse than a
# loud one, since it silently retires the thing it claims to guard.
assert_no_matches() {
    local desc="$1" label="$2"; shift 2
    local out
    out=$(grep "$@")
    if [[ -z "${out}" ]]; then report_pass "${desc}"
    else report_fail "${desc}" "no ${label}" "$(printf '%s' "${out}" | head -8)"; fi
}

# ─── setup ──────────────────────────────────────────────────────────────────

WT=""
BIN=""
SHIMS=""

setup() {
    WT=$(git rev-parse --show-toplevel 2>/dev/null) \
        || setup_fail "not inside a git checkout; run this from the E-1939 worktree"
    cd "${WT}" || setup_fail "cannot cd to ${WT}"
    [[ -f "${WT}/justfile" ]] || setup_fail "no justfile at ${WT}; wrong checkout?"
    command -v go   >/dev/null 2>&1 || setup_fail "go not on PATH"
    command -v just >/dev/null 2>&1 || setup_fail "just not on PATH"
    command -v uv   >/dev/null 2>&1 || setup_fail "uv not on PATH"
    BIN="${WT}/bin/endless-go"

    # Sabotage shims for section A. A build that still calls templ or
    # tailwindcss gets a non-zero exit and a message naming the tool.
    SHIMS=$(mktemp -d) || setup_fail "cannot create a temp dir for the PATH shims"
    local tool
    for tool in templ tailwindcss; do
        cat > "${SHIMS}/${tool}" <<EOF
#!/usr/bin/env bash
echo "E-1939: the build invoked '${tool}', which should no longer exist" >&2
exit 97
EOF
        chmod +x "${SHIMS}/${tool}"
    done
    trap 'rm -rf "${SHIMS}"' EXIT
}

# ─── A: fail-fast gate — the build no longer needs the web toolchain ────────

test_build_without_toolchain() {
    section "A. Build — \`just build\` with templ/tailwindcss sabotaged (FAIL-FAST)"
    note "shims at ${SHIMS} exit 97 if either tool is invoked"
    local out rc
    out=$(cd "${WT}" && PATH="${SHIMS}:${PATH}" just build 2>&1); rc=$?
    if [[ ${rc} -eq 0 ]]; then
        report_pass "just build succeeds without templ or tailwindcss"
    else
        report_fail "just build succeeds without templ or tailwindcss" "exit 0" \
            "exit=${rc}"$'\n'"$(printf '%s' "${out}" | tail -20)"
        summary; exit 1
    fi
    assert_not_contains "the build never shelled out to templ" \
        "the build invoked 'templ'" "${out}"
    assert_not_contains "the build never shelled out to tailwindcss" \
        "the build invoked 'tailwindcss'" "${out}"
    if [[ -x "${BIN}" ]]; then report_pass "bin/endless-go built"
    else report_fail "bin/endless-go built" "an executable at ${BIN}" "missing"; summary; exit 1; fi

    out=$(cd "${WT}" && go build ./... 2>&1); rc=$?
    if [[ ${rc} -eq 0 ]]; then report_pass "go build ./... — nothing imports the removed packages"
    else report_fail "go build ./..." "exit 0" "exit=${rc}"$'\n'"$(printf '%s' "${out}" | tail -20)"
        summary; exit 1; fi
}

# ─── B: the trees are gone ──────────────────────────────────────────────────

test_trees_removed() {
    section "B. Removal — the package trees and their artifacts are gone"
    assert_absent_path "internal/web/ removed"        "${WT}/internal/web"
    assert_absent_path "internal/servecmd/ removed"   "${WT}/internal/servecmd"
    assert_absent_path "docs/guide/help/serve.md removed" "${WT}/docs/guide/help/serve.md"
    assert_no_matches "no generated *_templ.go survives" "generated templ files" \
        -r --include='*_templ.go' -l '' "${WT}/internal" "${WT}/cmd"
    assert_no_matches "no built tailwind output.css survives" "output.css" \
        -r --include='output.css' -l '' "${WT}/internal" "${WT}/cmd"
    # git's view, not just the filesystem's: an untracked leftover would pass
    # the checks above while still riding along in someone's working tree.
    assert_eq "git tracks nothing under internal/web" "" \
        "$(cd "${WT}" && git ls-files -- internal/web)"
    assert_eq "git tracks nothing under internal/servecmd" "" \
        "$(cd "${WT}" && git ls-files -- internal/servecmd)"
}

# ─── C: no live references remain ───────────────────────────────────────────

# live_refs PATTERN — matching lines in live source/docs that are genuine
# references, printed one per line. Two classes of line are NOT references and
# are filtered out:
#   - comment lines, which is how a per-task suite leaves a tombstone
#     explaining that its check went away with the dashboard;
#   - assert_not_contains / assert_absent lines, which quote the removed name
#     precisely in order to assert nobody uses it.
# Naming a thing to say it is gone is the opposite of depending on it.
live_refs() {
    local pat="$1"
    grep -rn --exclude-dir=.venv --exclude='e-1939-verify.sh' -e "${pat}" \
        src internal cmd tests justfile README.md CLAUDE.md docs/guide .gitignore \
        | grep -v -E '^[^:]+:[0-9]+:[[:space:]]*(#|//|%%)' \
        | grep -v -E 'assert_not_contains|assert_absent'
    return 0
}

test_no_live_references() {
    section "C. References — live source and docs name none of it"
    note "history is exempt: .endless/, demo/, DB_AUDIT.md and dated docs/ archives"
    local pat out
    # 8484 is the dashboard's port — the other name it went by.
    for pat in 'internal/web' 'servecmd' 'templui' 'templ generate' 'tailwindcss' \
               'endless serve' '8484'; do
        out=$(live_refs "${pat}")
        if [[ -z "${out}" ]]; then report_pass "no live reference to '${pat}'"
        else report_fail "no live reference to '${pat}'" "no live use of '${pat}'" \
            "$(printf '%s' "${out}" | head -8)"; fi
    done
}

# ─── D: the Go dispatcher has no serve subcommand ───────────────────────────

test_go_surface() {
    section "D. Go — endless-go no longer dispatches 'serve'"
    local out rc
    out=$("${BIN}" serve 2>&1); rc=$?
    assert_eq "\`endless-go serve\` exits 2 (unknown subcommand)" "2" "${rc}"
    assert_contains "...and says so" 'unknown subcommand "serve"' "${out}"
    out=$("${BIN}" --help 2>&1)
    assert_not_contains "\`endless-go --help\` does not list serve" "serve " "${out}"
    assert_not_contains "...nor the dashboard"  "web dashboard" "${out}"
    # The subcommands that remain must still be listed — a botched edit to the
    # usage block is otherwise indistinguishable from a correct one.
    assert_contains "the other subcommands survive (sandbox)" "sandbox" "${out}"
    assert_contains "the other subcommands survive (tmux)"    "tmux"    "${out}"
}

# ─── E: the Python CLI has no serve command ─────────────────────────────────

test_python_surface() {
    section "E. Python — \`endless serve\` is gone from the CLI"
    local out rc
    out=$(cd "${WT}" && uv run endless --help 2>&1)
    assert_not_contains "\`endless --help\` does not list serve" "  serve" "${out}"
    assert_contains "...while the rest of the CLI is intact" "task" "${out}"
    out=$(cd "${WT}" && uv run endless serve 2>&1); rc=$?
    if [[ ${rc} -ne 0 ]]; then report_pass "\`endless serve\` is refused"
    else report_fail "\`endless serve\` is refused" "non-zero exit" "exit=0"; fi
    assert_contains "...as an unknown command" "No such command" "${out}"
}

# ─── F: the module surface shed the web-only dependencies ───────────────────

test_module_surface() {
    section "F. Modules — the web-only requirements are pruned, goldmark stays"
    local gomod; gomod=$(cat "${WT}/go.mod")
    assert_not_contains "go.mod drops the templ runtime"  "a-h/templ"   "${gomod}"
    assert_not_contains "go.mod drops templUI"            "templui"     "${gomod}"
    assert_not_contains "go.mod drops tailwind-merge-go"  "tailwind"    "${gomod}"
    local gosum; gosum=$(cat "${WT}/go.sum")
    assert_not_contains "go.sum drops the templ runtime"  "a-h/templ"   "${gosum}"
    assert_not_contains "go.sum drops templUI"            "templui"     "${gosum}"
    assert_not_contains "go.sum drops tailwind-merge-go"  "tailwind"    "${gosum}"
    # goldmark was shared with the terminal renderer and must NOT have gone
    # out with the web stack — the analysis called this out specifically.
    assert_contains "go.mod keeps goldmark (internal/mdterm needs it)" "goldmark" "${gomod}"
    local out rc
    out=$(printf '# Heading\n\nSome **bold** text.\n' | "${BIN}" markdown render 2>&1); rc=$?
    assert_eq "the terminal markdown renderer still runs" "0" "${rc}"
    assert_contains "...and still renders the heading" "Heading" "${out}"
    # `go mod tidy` would put anything back that is genuinely still needed;
    # a clean `go mod verify` is the cheap proxy that the graph is consistent.
    out=$(cd "${WT}" && go mod verify 2>&1); rc=$?
    assert_eq "go mod verify passes" "0" "${rc}"
}

# ─── G: the justfile shed the web recipes ───────────────────────────────────

test_justfile_surface() {
    section "G. Build stack — the justfile recipes and help text are gone"
    local recipes; recipes=$(cd "${WT}" && just --list 2>&1)
    local r
    # The trailing space matters: `just --list` pads every name to a common
    # width, so "    dev " cannot match the surviving `dev-sandbox-init`.
    for r in templ tailwind generate css dev _link-templui kill; do
        assert_not_contains "\`just --list\` no longer offers '${r}'" "    ${r} " "${recipes}"
    done
    assert_contains "...while \`just build\` survives" "    build " "${recipes}"
    assert_contains "...and \`just go\` survives"      "    go "    "${recipes}"
    local help; help=$(cd "${WT}" && just help 2>&1)
    assert_not_contains "\`just help\` no longer names templ"     "templ"     "${help}"
    assert_not_contains "\`just help\` no longer names tailwind"  "tailwind"  "${help}"
    assert_not_contains "\`just help\` no longer names the serve process" "serve" "${help}"
    # The templUI symlink was the one gitignored artifact of the stack.
    assert_not_contains ".gitignore drops the templUI symlink entry" "templui" \
        "$(cat "${WT}/.gitignore")"
}

# ─── H: the post-land residue script ────────────────────────────────────────

test_post_land_script() {
    section "H. Post-land — the templUI symlink residue is swept from main"
    local script="${WT}/.endless/hooks/post-land/e-1939.sh"
    if [[ -x "${script}" ]]; then report_pass "post-land script present and executable"
    else report_fail "post-land script present and executable" "an executable at ${script}" \
        "$(ls -l "${script}" 2>&1)"; return; fi

    # A throwaway repo standing in for main after the merge: internal/web holds
    # nothing tracked, only the ignored symlink the merge cannot remove.
    local fake; fake=$(mktemp -d) || { report_fail "post-land fixture" "a temp dir" "mktemp failed"; return; }
    (
        set -e
        cd "${fake}"
        git init -q .
        git config user.email t@t.t; git config user.name t
        mkdir -p internal/web/assets/css
        ln -s /tmp "internal/web/assets/css/templui"
        echo x > keep.txt
        git add keep.txt; git commit -qm init
    ) || { report_fail "post-land fixture" "a seeded temp repo" "setup failed"; rm -rf "${fake}"; return; }

    local out rc
    out=$("${script}" "${fake}" 2>&1); rc=$?
    assert_eq "the script exits 0 on a residual checkout" "0" "${rc}"
    assert_absent_path "...and internal/web is swept" "${fake}/internal/web"
    assert_eq "...leaving tracked files alone" "keep.txt" \
        "$(cd "${fake}" && git ls-files)"

    # Idempotent: the guide's contract is that a re-run finishes a failed land.
    out=$("${script}" "${fake}" 2>&1); rc=$?
    assert_eq "re-running on a swept checkout is a no-op (exit 0)" "0" "${rc}"

    # It must refuse rather than delete when git still tracks something there —
    # that would mean the merge misbehaved, which is not residue to sweep.
    (
        set -e
        cd "${fake}"
        mkdir -p internal/web
        echo tracked > internal/web/still-here.go
        git add internal/web/still-here.go; git commit -qm tracked
    ) || { report_fail "post-land refusal fixture" "a tracked file under internal/web" "setup failed"; rm -rf "${fake}"; return; }
    out=$("${script}" "${fake}" 2>&1); rc=$?
    if [[ ${rc} -ne 0 ]]; then report_pass "the script refuses when internal/web is still tracked"
    else report_fail "the script refuses when internal/web is still tracked" "non-zero exit" "exit=0"; fi
    assert_contains "...and says why" "refusing to remove" "${out}"
    if [[ -f "${fake}/internal/web/still-here.go" ]]; then
        report_pass "...without deleting the tracked file"
    else
        report_fail "...without deleting the tracked file" "still-here.go intact" "deleted"
    fi
    rm -rf "${fake}"
}

# ─── I: project-wide regression ─────────────────────────────────────────────

test_regression() {
    section "I. Regression — the suites that must not have moved"
    local out rc

    out=$(cd "${WT}" && go vet ./... 2>&1); rc=$?
    if [[ ${rc} -eq 0 ]]; then report_pass "go vet ./... is clean"
    else report_fail "go vet ./..." "exit 0" "exit=${rc}"$'\n'"$(printf '%s' "${out}" | tail -20)"; fi

    out=$(cd "${WT}" && go test ./internal/... -count=1 2>&1); rc=$?
    if [[ ${rc} -eq 0 ]]; then report_pass "go test ./internal/... passes"
    else report_fail "go test ./internal/..." "exit 0" "exit=${rc}"$'\n'"$(printf '%s' "${out}" | grep -E '^(---|FAIL|ok.*FAIL)' | head -20)"; fi

    out=$(cd "${WT}" && uv run pytest tests/ -q 2>&1); rc=$?
    if [[ ${rc} -eq 0 ]]; then report_pass "the full Python suite passes"
    else report_fail "the Python suite" "exit 0" "exit=${rc}"$'\n'"$(printf '%s' "${out}" | tail -20)"; fi

    # The guide's command→section map had a `serve` row and a serve.md file;
    # dropping one without the other leaves the map dangling.
    out=$(cd "${WT}" && just guide-check 2>&1); rc=$?
    if [[ ${rc} -eq 0 ]]; then report_pass "the guide command→section map still resolves"
    else report_fail "just guide-check" "exit 0" "exit=${rc}"$'\n'"$(printf '%s' "${out}" | tail -20)"; fi
    assert_not_contains "...with no dangling serve row" "serve" \
        "$(cd "${WT}" && grep -n '| \`serve\`' docs/guide/index.md)"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup
    printf '\n%sE-1939 — the web dashboard and its build stack are excised%s\n' "${BOLD}" "${RESET}"
    printf '%sworktree: %s%s\n' "${DIM}" "${WT}" "${RESET}"
    test_build_without_toolchain
    test_trees_removed
    test_no_live_references
    test_go_surface
    test_python_surface
    test_module_surface
    test_justfile_surface
    test_post_land_script
    test_regression
    summary
}

main "$@"
