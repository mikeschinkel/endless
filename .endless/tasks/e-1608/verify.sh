#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1608 and records what was true when E-1608
# landed. Edit it only if you ARE E-1608. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1608: every verify run starts from a fresh sandbox, via `endless sandbox
# reset` (clear → .gitignore → the worktree's .endless/hooks/seed-sandbox.sh).
#
# Everything runs through this branch's own bin/endless-go against throwaway
# worktree layouts. Whichever runner started THIS suite may be an older install
# that predates the reset, so this run's own sandbox proves nothing either way.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(cd "${ENDLESS_VERIFY_DIR}/../../.." && pwd)"
BIN="${WT}/bin/endless-go"

fx="$(mktemp -d)"
trap 'rm -rf "${fx}"' EXIT

# ── Unit tests, fail-fast.
section "Unit tests"

out=$(cd "${WT}" && go test ./internal/sandboxcmd/ -run 'TestReset' -count=1 2>&1); rc=$?
[[ ${rc} -eq 0 ]] && report_pass "go: sandboxcmd Reset" \
    || { report_fail "go: sandboxcmd Reset" "exit 0" "$(tail -5 <<<"${out}")"; summary; exit 1; }
out=$(cd "${WT}" && go test ./internal/verifycmd/ -run 'TestResetSandbox' -count=1 2>&1); rc=$?
[[ ${rc} -eq 0 ]] && report_pass "go: verify resets the sandbox" \
    || { report_fail "go: verify resets the sandbox" "exit 0" "$(tail -5 <<<"${out}")"; summary; exit 1; }
out=$(cd "${WT}" && uv run --quiet pytest -q tests/test_sandbox_cmd.py 2>&1); rc=$?
[[ ${rc} -eq 0 ]] && report_pass "py: sandbox reset shim and worktree-create seeding" \
    || { report_fail "py: sandbox reset shim and worktree-create seeding" "exit 0" "$(tail -5 <<<"${out}")"; summary; exit 1; }

# ── The reset command itself.
section "endless-go sandbox reset"

fwt="${fx}/proj/.endless/worktrees/e-7"
fsb="${fwt}/.endless/sandbox"
mkdir -p "${fsb}" "${fwt}/.endless/hooks"
touch "${fsb}/stale"
printf '#!/usr/bin/env bash\nprintf "%%s|%%s\\n" "$1" "$2" > "$2/seeded"\n' \
    > "${fwt}/.endless/hooks/seed-sandbox.sh"
chmod +x "${fwt}/.endless/hooks/seed-sandbox.sh"

out=$(cd "${fwt}" && "${BIN}" sandbox reset 2>/dev/null); rc=$?
assert_eq "sandbox reset exits 0" "0" "${rc}"
assert_eq "stdout is exactly the sandbox path" "${fsb}" "${out}"
assert_eq "stale content is cleared" "absent" "$([[ -e ${fsb}/stale ]] && echo present || echo absent)"
assert_eq "the self-ignoring .gitignore is written" "*" "$(tail -1 "${fsb}/.gitignore" 2>/dev/null)"
assert_eq "the worktree's seed hook ran with worktree and sandbox" "${fwt}|${fsb}" "$(cat "${fsb}/seeded" 2>/dev/null)"

printf '#!/usr/bin/env bash\nexit 5\n' > "${fwt}/.endless/hooks/seed-sandbox.sh"
out=$(cd "${fwt}" && "${BIN}" sandbox reset 2>&1); rc=$?
assert_eq "a failing seed hook fails the reset" "1" "${rc}"
assert_contains "the failure names the hook" "seed-sandbox.sh" "${out}"

rm "${fwt}/.endless/hooks/seed-sandbox.sh"
out=$(cd "${fwt}" && "${BIN}" sandbox reset 2>&1); rc=$?
assert_eq "no seed hook: reset still succeeds" "0" "${rc}"
assert_eq "no seed hook: only the .gitignore remains" ".gitignore" "$(ls -A "${fsb}")"

out=$(cd "${fx}" && "${BIN}" sandbox reset 2>&1); rc=$?
assert_eq "outside a task worktree: refused" "1" "${rc}"

# ── The runner calls it before every run.
section "endless-go verify starts every run from a fresh sandbox"

rwt="${fx}/proj2/.endless/worktrees/e-9"
rsb="${rwt}/.endless/sandbox"
mkdir -p "${rsb}" "${rwt}/.endless/hooks" "${rwt}/.endless/tasks/e-9"
touch "${rsb}/left-by-last-run"
printf '#!/usr/bin/env bash\ntouch "$2/seeded"\n' > "${rwt}/.endless/hooks/seed-sandbox.sh"
chmod +x "${rwt}/.endless/hooks/seed-sandbox.sh"
# The fixture suite records what it found, then dirties the sandbox for the
# next run to clear.
cat > "${rwt}/.endless/tasks/e-9/verify.sh" <<SUITE
#!/usr/bin/env bash
{ [[ -e "${rsb}/left-by-last-run" ]] && echo stale || echo fresh
  [[ -e "${rsb}/seeded" ]] && echo seeded || echo unseeded; } | tr '\n' ' ' > "${fx}/observed"
touch "${rsb}/left-by-last-run"
SUITE
chmod +x "${rwt}/.endless/tasks/e-9/verify.sh"

for run in 1 2; do
    rm -f "${fx}/observed"
    (cd "${rwt}" && "${BIN}" verify E-9 >/dev/null 2>&1)
    assert_eq "run ${run}: the suite saw a cleared, reseeded sandbox" \
        "fresh seeded " "$(cat "${fx}/observed" 2>/dev/null)"
done

printf '#!/usr/bin/env bash\nexit 3\n' > "${rwt}/.endless/hooks/seed-sandbox.sh"
rm -f "${fx}/observed"
out=$(cd "${rwt}" && "${BIN}" verify E-9 2>&1); rc=$?
assert_eq "a failing seed hook aborts the run" "1" "${rc}"
assert_eq "…before the suite runs" "not run" "$([[ -e ${fx}/observed ]] && echo ran || echo 'not run')"
assert_contains "…and says the reset failed" "resetting the worktree's sandbox" "${out}"

# ── Endless's own hooks.
section "Endless's hooks"

assert_not_contains "post-worktree-create.sh no longer seeds (it runs once; seeding runs every verify)" \
    "recipe dev-sandbox-init" "$(cat "${WT}/.endless/hooks/post-worktree-create.sh")"
assert_eq "seed-sandbox.sh is executable" "yes" \
    "$([[ -x ${WT}/.endless/hooks/seed-sandbox.sh ]] && echo yes || echo no)"

summary
