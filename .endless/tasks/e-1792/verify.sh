#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1792 and records what was true when E-1792
# landed. Edit it only if you ARE E-1792. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1792 — the verify run-script shell emission and the bare-clone command
# derivation it needed are gone; the Go driver is the only execution path.
#
# Checks, fail-fast in order:
#   1. The task's own Go tests (internal/verify: manifest validation through
#      ParseManifest, ResolvedFormat, the drivers, and goTestCmd's -json form).
#   2. The retired symbols and files no longer exist in internal/verify.
#   3. No doc comment in internal/verify still describes a bare clone,
#      RenderRunScript or E-1792.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
command -v go >/dev/null 2>&1 || setup_error "go not on PATH"

VERIFY_DIR="internal/verify"

section "1. The task's own tests"
if out=$(go vet "./${VERIFY_DIR}/" 2>&1 && go test "./${VERIFY_DIR}/" 2>&1); then
    report_pass "go vet and go test ./${VERIFY_DIR}/"
else
    report_fail "go vet and go test ./${VERIFY_DIR}/" "pass" "${out}"
    summary
fi

section "2. The run-script emission is retired"
for f in runscript.go runscript_test.go; do
    if [[ -e "${VERIFY_DIR}/${f}" ]]; then
        report_fail "${f} is removed" "absent" "present"
    else
        report_pass "${f} is removed"
    fi
done

# Each symbol, as code: a definition or a use, not merely a word in prose.
symbol_hits() {
    grep -rnE "$1" "${VERIFY_DIR}" --include='*.go' || true
}
assert_eq "RenderRunScript is gone" "" "$(symbol_hits '\bRenderRunScript\b')"
assert_eq "Check.ResolvedCommand is gone" "" "$(symbol_hits '\bResolvedCommand\b')"
assert_eq "bareCloneCommand is gone" "" "$(symbol_hits '\bbareCloneCommand\b')"
assert_eq "pytestCmd is gone" "" "$(symbol_hits '\bpytestCmd\b')"
assert_eq "LoadManifest is gone" "" "$(symbol_hits '\bLoadManifest\b')"
assert_eq "TestCheck_ResolvedCommand is gone" "" "$(symbol_hits 'func TestCheck_ResolvedCommand\b')"
assert_eq "goTestCmd takes no jsonStream parameter" "" "$(symbol_hits 'jsonStream')"
assert_contains "goTestCmd takes only the selection" \
    "func goTestCmd(tests, paths []string)" "$(symbol_hits 'func goTestCmd\(')"
assert_contains "ParseManifest remains" \
    "func ParseManifest(" "$(symbol_hits 'func ParseManifest\(')"

section "3. Doc comments no longer describe the retired path"
assert_eq "no mention of a bare clone" "" "$(symbol_hits '[Bb]are[- ]clone')"
assert_eq "no mention of E-1792" "" "$(symbol_hits 'E-1792')"
assert_contains "Manifest is described as what the driver runs" \
    "what the driver runs" "$(grep -n '^// Manifest is' "${VERIFY_DIR}/manifest.go")"

summary
