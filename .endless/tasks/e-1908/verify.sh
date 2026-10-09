#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1908 and records what was true when E-1908
# landed. Edit it only if you ARE E-1908. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1908 — a redirected HOME must not force cold Go builds.
#
# Two places redirected HOME and then built Go against the empty GOCACHE that
# HOME implies on macOS (~/Library/Caches/go-build moves under the temp HOME):
#   - internal/sandboxcmd's destroy tests, which `t.Setenv("HOME", tmp)` and
#     then `go build` — two cold builds that exhausted the package's 10-minute
#     timeout and took `go test ./internal/...` down with it;
#   - the verify runner, whose per-run temp HOME made every suite that runs Go
#     (or uv) compile cold.
#
# Checks, fail-fast first:
#   1. The task's unit tests (sandboxcmd + verifycmd), under a 180s budget,
#      with the elapsed time printed so a regression shows as slow, not just red.
#   2. The destroy tests' behavioral assertions still run and pass, and still
#      assert what they asserted — not weakened to go fast.
#   3. Grep guard: no _test.go combines t.Setenv("HOME" with an in-test go build.
#   4. Live: THIS suite runs under the runner's temp HOME, so `go env GOCACHE`
#      here must be the caller's cache, not one under $HOME.
#   5. Fold-in regression: `go test ./internal/...` completes, no timeout panic.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || setup_error "not inside a git worktree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"
command -v go >/dev/null 2>&1 || setup_error "go not on PATH"

WORK_TMP=$(mktemp -d)
trap 'rm -rf "${WORK_TMP}"' EXIT

# ── 1. unit tests, fail fast ─────────────────────────────────────────────────
section "1. Unit tests (fail fast, 180s budget)"
start=${SECONDS}
if go test ./internal/sandboxcmd/ ./internal/verifycmd/ -count=1 -timeout 180s >"${WORK_TMP}/unit.log" 2>&1; then
    report_pass "go test ./internal/sandboxcmd ./internal/verifycmd in $((SECONDS - start))s (was: sandboxcmd could not finish in 10m)"
else
    report_fail "go test ./internal/sandboxcmd ./internal/verifycmd" "ok within 180s" "$(tail -30 "${WORK_TMP}/unit.log")"
    summary
fi

# ── 2. the destroy behavior is still tested ──────────────────────────────────
section "2. Destroy tests still run and still assert the same behavior"
go test ./internal/sandboxcmd/ -count=1 -timeout 180s -v \
    -run '^(TestDestroyRefusesWithLiveWriter|TestDestroyForceOverridesLiveWriterCheck|TestDestroyExistingWithIfExistsStillDestroysAndReports)$' \
    >"${WORK_TMP}/destroy.log" 2>&1
for name in TestDestroyRefusesWithLiveWriter TestDestroyForceOverridesLiveWriterCheck TestDestroyExistingWithIfExistsStillDestroysAndReports; do
    assert_contains "${name} passes" "--- PASS: ${name} " "$(cat "${WORK_TMP}/destroy.log")"
done
lw=$(cat internal/sandboxcmd/livewriters_test.go)
assert_contains "refusal names the live writer" 'contains(out, "refusing to destroy")' "${lw}"
assert_contains "refusal names --force" 'contains(out, "--force")' "${lw}"
assert_contains "--force destroys and confirms" 'contains(out, "Destroyed")' "${lw}"

# ── 3. the pattern cannot come back ──────────────────────────────────────────
section "3. No test redirects HOME and builds Go in-process"
offenders=""
while IFS= read -r f; do
    grep -q 'exec.Command("go", "build"' "${f}" && offenders+="${f} "
done < <(git ls-files '*_test.go' | xargs grep -l 't.Setenv("HOME"' 2>/dev/null)
assert_eq "no _test.go combines t.Setenv(\"HOME\") with go build" "" "${offenders}"

# ── 4. the runner pins the caller's build cache ──────────────────────────────
section "4. This suite's go builds against the caller's cache"
gocache=$(go env GOCACHE)
case "${gocache}" in
    "${HOME}"/*) report_fail "GOCACHE is outside the runner's temp HOME" "not under ${HOME}" "${gocache}" ;;
    *)           report_pass "GOCACHE=${gocache} (temp HOME=${HOME})" ;;
esac
assert_contains "the temp HOME is still in place" "endless-verify-" "${HOME}"

# ── 5. fold-in regression ────────────────────────────────────────────────────
section "5. go test ./internal/... completes"
start=${SECONDS}
if go test ./internal/... -count=1 >"${WORK_TMP}/all.log" 2>&1; then
    report_pass "go test ./internal/... in $((SECONDS - start))s"
else
    report_fail "go test ./internal/..." "ok" "$(grep -v '^ok\|no test files' "${WORK_TMP}/all.log" | tail -30)"
fi
assert_not_contains "no package timeout panic" "panic: test timed out" "$(cat "${WORK_TMP}/all.log")"

summary
