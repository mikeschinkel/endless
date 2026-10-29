#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2480 and records what was true when E-2480
# landed. Edit it only if you ARE E-2480. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2480 — endless's go.mod declares go 1.27.0, so endless keeps building
# against go-cfgstore's local checkout once go-cfgstore (E-2412) requires Go
# 1.27. The bump is the go line alone: no require, replace or go.sum change.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
command -v go >/dev/null 2>&1 || setup_error "go not on PATH"

# go.mod's replaces name ../go-pkgs relative to the main checkout.
MAIN_CHECKOUT=$(cd "$(git rev-parse --git-common-dir)/.." && pwd -P)
GO_PKGS=$(cd "${MAIN_CHECKOUT}/.." && pwd -P)/go-pkgs
[ -d "${GO_PKGS}/go-cfgstore" ] || setup_error "go-cfgstore checkout not found at ${GO_PKGS}/go-cfgstore"
E2412_WT="${GO_PKGS}/go-cfgstore/.endless/worktrees/e-2412"

WORK_TMP=$(mktemp -d)
trap 'rm -rf "${WORK_TMP}"' EXIT

# A scratch -modfile with every ../go-pkgs replace made absolute, and the
# go line set to $2 and the go-cfgstore replace pointed at $3. $1 names it.
scratch_modfile() {
    local name=$1 goline=$2 cfgstore=$3
    sed -e "s|=> \.\./go-pkgs/|=> ${GO_PKGS}/|" -e "s|^go .*|go ${goline}|" go.mod >"${WORK_TMP}/${name}.mod"
    cp go.sum "${WORK_TMP}/${name}.sum"
    go mod edit -modfile="${WORK_TMP}/${name}.mod" \
        -replace="github.com/mikeschinkel/go-cfgstore=${cfgstore}"
    printf '%s' "${WORK_TMP}/${name}.mod"
}

# ── 1. Go test suite, fail fast ──────────────────────────────────────────────
section "1. Go test suite (fail fast)"
if out=$(go test ./... -count=1 2>&1); then
    report_pass "go test ./..."
else
    report_fail "go test ./..." "ok" "$(printf '%s' "${out}" | grep -v '^ok' | tail -30)"
    summary
fi

# ── 2. The go line, and nothing else ─────────────────────────────────────────
section "2. go.mod declares go 1.27.0 and changes nothing else"
assert_eq "go.mod's go directive" "go 1.27.0" "$(grep '^go ' go.mod)"
changed=$(git diff main...HEAD -- go.mod go.sum | grep -E '^[+-]' | grep -vE '^(\+\+\+|---) ')
assert_eq "the diff against main touches only the go line" \
    "$(printf -- '-go 1.26.2\n+go 1.27.0')" "${changed}"

# ── 3. Builds against go-cfgstore's main checkout ────────────────────────────
section "3. Builds against go-cfgstore's main checkout"
mf=$(scratch_modfile main 1.27.0 "${GO_PKGS}/go-cfgstore")
if out=$(GOWORK=off go build -modfile="${mf}" ./... 2>&1); then
    report_pass "go build ./... against ${GO_PKGS}/go-cfgstore"
else
    report_fail "go build ./... against go-cfgstore main" "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi

# ── 4. Builds against E-2412's branch, which requires Go 1.27 ────────────────
section "4. Builds against E-2412's go-cfgstore branch"
if [ ! -d "${E2412_WT}" ]; then
    report_skip "E-2412 worktree gone (landed: section 3 covers its go line)"
else
    assert_eq "E-2412's go-cfgstore declares go 1.27.0" "go 1.27.0" "$(grep '^go ' "${E2412_WT}/go.mod")"
    mf=$(scratch_modfile e2412 1.27.0 "${E2412_WT}")
    if out=$(GOWORK=off go build -modfile="${mf}" ./... 2>&1); then
        report_pass "go build ./... against E-2412's worktree"
    else
        report_fail "go build ./... against E-2412's worktree" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
    assert_eq "go.sum unchanged by that build" "" "$(diff go.sum "${WORK_TMP}/e2412.sum")"

    # Control: the old go line must fail, or the check above proves nothing.
    mf=$(scratch_modfile control 1.26.2 "${E2412_WT}")
    out=$(GOWORK=off go build -modfile="${mf}" ./... 2>&1)
    assert_contains "control: go 1.26.2 against E-2412 refuses to build" \
        "updates to go.mod needed" "${out}"
fi

summary
