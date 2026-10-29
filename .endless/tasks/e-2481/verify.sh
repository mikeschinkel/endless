#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2481 and records what was true when E-2481
# landed. Edit it only if you ARE E-2481. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2481: retire the cache-directory (named) sandboxes.
#
# The unit layer runs this branch's Go and Python tests against the worktree
# source. The end-to-end layer builds this branch's endless-go into a temp dir
# and drives it under a temp XDG_CACHE_HOME — the reproduction was `sandbox init
# repro-x` creating <cache>/endless/sandboxes/repro-x. The grep checks read the
# committed tree.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

cd "$(git -C "${ENDLESS_VERIFY_DIR}" rev-parse --show-toplevel)" \
    || setup_error "cannot find the worktree root"

PYTESTS=(tests/test_db_error_diagnostic.py tests/test_agent_env.py tests/test_sandbox_path.py)

section "Fail-fast: this task's tests"
if ! out="$(go test -count=1 ./internal/sandboxcmd/ 2>&1)"; then
    report_fail "internal/sandboxcmd tests pass" "ok" "$(printf '%s\n' "$out" | tail -20)"
    summary
fi
report_pass "internal/sandboxcmd tests pass"
if ! out="$(go test -count=1 -run 'Reap' ./internal/monitor/ 2>&1)"; then
    report_fail "internal/monitor reap tests pass" "ok" "$(printf '%s\n' "$out" | tail -20)"
    summary
fi
report_pass "internal/monitor reap tests pass"
if ! out="$(uv run --quiet pytest -q -x -p no:cacheprovider "${PYTESTS[@]}" 2>&1)"; then
    report_fail "touched Python tests pass" "all pass" "$(printf '%s\n' "$out" | tail -20)"
    summary
fi
report_pass "test_db_error_diagnostic, test_agent_env and test_sandbox_path pass"

tmp="$(mktemp -d)" || setup_error "mktemp failed"
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/endless-go" ./cmd/endless-go || setup_error "cannot build endless-go"
cache="$tmp/cache"

# sandbox_run <args...> — runs this branch's endless-go sandbox in a scratch dir
# under the temp cache; prints combined output and leaves the exit code in rc.
sandbox_run() {
    out="$(cd "$tmp" && XDG_CACHE_HOME="$cache" "$tmp/endless-go" sandbox "$@" 2>&1)"
    rc=$?
}

section "P1, P12: sandbox offers only seed, reset and claude-settings-repair"
sandbox_run --help
assert_contains "usage offers seed" "seed    [--force]" "$out"
assert_contains "usage offers reset" "reset" "$out"
assert_contains "usage offers claude-settings-repair" "claude-settings-repair" "$out"
for gone in "run " enter init list prune destroy "<name>" "--mode"; do
    assert_not_contains "usage does not name '${gone}'" "$gone" "$out"
done
for verb in run enter list prune destroy init; do
    sandbox_run "$verb" e-1
    assert_eq "sandbox $verb e-1 exits 1" "1" "$rc"
    assert_contains "sandbox $verb is an unknown command" "unknown command \"$verb\"" "$out"
done
sandbox_run init --mode empty repro-x
assert_contains "sandbox init --mode empty <name> is an unknown command" 'unknown command "init"' "$out"
if [[ -e "$cache/endless/sandboxes" ]]; then
    report_fail "nothing was written under <cache>/endless/sandboxes" "absent" \
        "$(find "$cache" | head -10)"
else
    report_pass "nothing was written under <cache>/endless/sandboxes"
fi

section "P12: seeding is endless-go sandbox seed"
out="$("$tmp/endless-go" --help 2>&1)"
assert_contains "endless-go's usage lists sandbox seed" "sandbox        seed|reset|claude-settings-repair" "$out"
sandbox_run seed
assert_eq "sandbox seed outside a worktree exits 1" "1" "$rc"
assert_contains "and says there is no sandbox to seed" "is not inside a task worktree" "$out"
sandbox_run seed --help
assert_eq "sandbox seed --help exits 0 (the seed hook's capability probe)" "0" "$rc"
assert_contains "Endless's seed hook calls sandbox seed" '"${bin}" sandbox seed >/dev/null' \
    "$(cat .endless/hooks/seed-sandbox.sh)"
assert_contains "just dev-sandbox-init calls sandbox seed" '"$sandbox_bin" sandbox seed' \
    "$(cat justfile)"

section "P13: the bridge across the land, until E-2484"
sandbox_run init --mode worktree
assert_contains "sandbox init --mode worktree reaches seed" "endless-go sandbox seed:" "$out"
assert_not_contains "and is not refused as an unknown command" "unknown command" "$out"
assert_contains "the seed hook falls back on a binary without seed" \
    'sandbox init --mode worktree >/dev/null' "$(cat .endless/hooks/seed-sandbox.sh)"
assert_contains "the fallback is chosen by asking the binary" 'sandbox seed --help' \
    "$(cat .endless/hooks/seed-sandbox.sh)"

# git_grep_none <label> <pattern> <pathspec...>
git_grep_none() {
    local label="$1" pattern="$2"; shift 2
    local hits
    hits="$(git grep -nE "$pattern" -- "$@" 2>/dev/null)"
    if [[ -z "$hits" ]]; then
        report_pass "$label"
    else
        report_fail "$label" "no matches" "$hits"
    fi
}

section "P2: no code reads or writes the cache sandboxes directory"
git_grep_none "no sandboxesDir and no \"sandboxes\" path segment" \
    'sandboxesDir|"sandboxes"|CacheDir\(\)/sandboxes' \
    internal cmd src ':!*_test.go'

section "P3: the sandbox reap hook is gone"
git_grep_none "neither ReapSandbox nor ReapSandboxForWorktree exists" \
    'ReapSandbox' internal cmd
for f in reap reapguard livewriters orphan supervisor run enter enter_inject list prune destroy sandbox; do
    if [[ -e "internal/sandboxcmd/$f.go" ]]; then
        report_fail "internal/sandboxcmd/$f.go is gone" "absent" "present"
    else
        report_pass "internal/sandboxcmd/$f.go is gone"
    fi
done

section "P4: removing a worktree removes its in-tree sandbox"
if out="$(go test -count=1 -run 'TestWorktreeRemovalTakesItsSandbox' -v ./internal/sandboxcmd/ 2>&1)" \
        && grep -q -- '--- PASS: TestWorktreeRemovalTakesItsSandbox' <<<"$out"; then
    report_pass "drop and reap's git worktree remove takes the sandbox along"
else
    report_fail "drop and reap's git worktree remove takes the sandbox along" "PASS" \
        "$(printf '%s\n' "$out" | tail -15)"
fi

section "P5: the Python CLI no longer refuses under ENDLESS_SANDBOX"
git_grep_none "src/ names neither ENDLESS_SANDBOX nor SANDBOX_SAFE_SUBCOMMANDS" \
    'ENDLESS_SANDBOX|SANDBOX_SAFE_SUBCOMMANDS' src
if [[ -e tests/test_sandbox_gate.py ]]; then
    report_fail "tests/test_sandbox_gate.py is gone" "absent" "present"
else
    report_pass "tests/test_sandbox_gate.py is gone"
fi
out="$(ENDLESS_SANDBOX="$tmp/sb" uv run --quiet endless task --help 2>&1)"; rc=$?
assert_eq "endless task --help under ENDLESS_SANDBOX exits 0" "0" "$rc"
assert_not_contains "and is not refused" "refusing to run" "$out"

section "P6: Endless sets XDG_CONFIG_HOME nowhere outside the verify runner"
# The verify runner's own suite isolation (internal/verifycmd) is the one
# deliberate setter; see the E-2481 reconciliation.
git_grep_none "no assignment of XDG_CONFIG_HOME in internal/, cmd/ or src/" \
    '"XDG_CONFIG_HOME="|XDG_CONFIG_HOME"\] *=|setenv\("XDG_CONFIG_HOME"|Setenv\("XDG_CONFIG_HOME"|"XDG_CONFIG_HOME": ' \
    internal cmd src ':!*_test.go' ':!internal/verifycmd'

section "P8: no guide page or CLI text names a removed subcommand"
git_grep_none "docs/guide and src/ name no removed sandbox verb" \
    'sandbox (run|enter|list|prune|destroy)\b|sandbox init (--mode (empty|seed|clone)|<name>)' \
    docs/guide src
git_grep_none "nothing points at a sandbox subshell" \
    'endless-go sandbox` subshell|sandbox subshell' docs/guide src/endless/db.py

summary
