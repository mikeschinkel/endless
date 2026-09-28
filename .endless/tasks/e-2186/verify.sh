#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2186 and records what was true when E-2186
# landed. Edit it only if you ARE E-2186. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2186 verification — Endless no longer routes a database through
# XDG_CONFIG_HOME.
#
# Before: default resolution honoured XDG_CONFIG_HOME while `--db main` ignored
# it (two databases for one user who set it), and Endless SET the variable to
# route child processes (triagejob, minimizerjob, triage.py spawn_detached).
#
# After: XDG_CONFIG_HOME is the user's own setting. The default and `--db main`
# are one rule ($XDG_CONFIG_HOME/endless, else ~/.config/endless) in Go and
# Python alike; children are routed by `--db`, never by environment; the only
# remaining setter is the user-entered `sandbox enter/run` subshell.
#
#   endless task verify E-2186
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test \
        ./internal/dbcontext/ \
        ./internal/triagejob/ \
        ./internal/minimizerjob/ \
        ./internal/verifycmd/ \
        ./internal/sandboxcmd/ \
        >"${TMP}/go.log" 2>&1 \
   && go test ./internal/monitor/ \
        -run 'TestChildDBRoute|TestPinMainDB|TestConsumeDBFlags|TestMainPinRouting|TestIsSandboxActive|TestDBProvenance|TestGuard' \
        >>"${TMP}/go.log" 2>&1 \
   && go test ./internal/schemachange/ -run 'TestMigrateExecutable' \
        >>"${TMP}/go.log" 2>&1; then
    report_pass "go test: dbcontext, jobs, monitor routing, endless-migrate"
else
    report_fail "go test: dbcontext, jobs, monitor routing, endless-migrate" "exit 0" \
        "$(tail -25 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q tests/test_triage.py tests/test_suite_guard.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: triage spawn routing, suite guard"
else
    report_fail "pytest: triage spawn routing, suite guard" "exit 0" \
        "$(tail -25 "${TMP}/py.log")"
    summary
fi

# A binary built OUTSIDE the worktree, so it owns whatever database it opens
# (a worktree build is schema-passive against main, E-1975) and so the
# resolution below is the deployed rule, not a candidate's.
go build -o "${TMP}/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1 \
    || setup_error "building endless-go: $(tail -5 "${TMP}/build.log")"

NEUTRAL="${TMP}/neutral"
mkdir -p "${NEUTRAL}" "${TMP}/home" "${TMP}/xdg"

# `event migrate` prints the database it resolved as {"db": "..."}.
migrate_db() {
    (cd "${NEUTRAL}" && env "$@" "${TMP}/endless-go" ${DBFLAG:-} event migrate) \
        | sed -n 's/.*"db":"\([^"]*\)".*/\1/p'
}

# ── 2. Go: default and --db main are one database ──────────────────────────
section "2. Go resolves main from XDG_CONFIG_HOME, then HOME — default included"

assert_eq "--db main with XDG_CONFIG_HOME set" \
    "${TMP}/xdg/endless/endless.db" \
    "$(DBFLAG="--db main" migrate_db HOME="${TMP}/home" XDG_CONFIG_HOME="${TMP}/xdg")"
assert_eq "no flag with XDG_CONFIG_HOME set (same database)" \
    "${TMP}/xdg/endless/endless.db" \
    "$(migrate_db HOME="${TMP}/home" XDG_CONFIG_HOME="${TMP}/xdg")"
assert_eq "--db main with XDG_CONFIG_HOME unset" \
    "${TMP}/home/.config/endless/endless.db" \
    "$(DBFLAG="--db main" migrate_db HOME="${TMP}/home" XDG_CONFIG_HOME=)"

# ── 3. Python agrees ───────────────────────────────────────────────────────
section "3. Python's main_config_dir is the same rule"

py_main() {
    env "$@" uv run --quiet --project "${WT}" python -c \
        'from endless import config; print(config.main_config_dir(), config.CONFIG_DIR)'
}
assert_eq "main and default, XDG_CONFIG_HOME set" \
    "${TMP}/xdg/endless ${TMP}/xdg/endless" \
    "$(py_main HOME="${TMP}/home" XDG_CONFIG_HOME="${TMP}/xdg")"
assert_eq "main and default, XDG_CONFIG_HOME unset" \
    "${TMP}/home/.config/endless ${TMP}/home/.config/endless" \
    "$(py_main HOME="${TMP}/home" XDG_CONFIG_HOME=)"

# ── 4. nothing routes a child through the environment ──────────────────────
section "4. Endless sets XDG_CONFIG_HOME only for the sandbox subshell and verify isolation"

setters="$(git grep -lE '"XDG_CONFIG_HOME="|env\["XDG_CONFIG_HOME"\]|const key = "XDG_CONFIG_HOME"' \
    -- 'src/endless/*.py' 'internal/*.go' 'cmd/*.go' ':!*_test.go' | sort | tr '\n' ' ')"
assert_eq "the only setters left" \
    "internal/sandboxcmd/sandbox.go internal/verifycmd/env.go " \
    "${setters}"

assert_not_contains "triagejob no longer builds a child env" \
    "childEnv" "$(cat internal/triagejob/triagejob.go)"
assert_not_contains "minimizerjob no longer builds a child env" \
    "childEnv" "$(cat internal/minimizerjob/minimizerjob.go)"
assert_not_contains "ForceRealDB (the XDG escape hatch) is gone" \
    "func ForceRealDB" "$(cat internal/monitor/db.go)"

# ── 5. the stale claim that hid E-1608 ─────────────────────────────────────
section "5. verify.go no longer claims isolation hands a suite a fresh DB"

assert_not_contains "no 'fresh DB for free' claim" \
    "fresh DB for free" "$(cat internal/verifycmd/verify.go)"

summary
