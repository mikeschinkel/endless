#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1347 and records what was true when E-1347
# landed. Edit it only if you ARE E-1347. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
ENDLESS_GO="${WT}/bin/endless-go"

[[ -x "${ENDLESS_GO}" ]] || setup_error "no ${ENDLESS_GO}; run 'just build' in the worktree first"
command -v git >/dev/null 2>&1 || setup_error "git not on PATH"

section "Unit tests (fail fast)"
# The task's own Go tests, first: everything below rides on them.
if go_out="$(cd "${WT}" && go test ./internal/sandboxcmd/ ./internal/hookcmd/ 2>&1)"; then
    report_pass "go test ./internal/sandboxcmd/ ./internal/hookcmd/"
else
    report_fail "go test ./internal/sandboxcmd/ ./internal/hookcmd/" "all packages ok" "${go_out}"
    summary
fi

# ── Fixture: a repository in the pre-E-1347 state ───────────────────
# A committed .claude/settings.json, two linked worktrees carrying the
# generated override on top of it, and the skip-worktree bit hiding the
# difference — exactly what `sandbox bind` + `claude-settings-init` produced
# before this task.
FIX="$(mktemp -d)"
trap 'rm -rf "${FIX}"' EXIT
MAIN="${FIX}/main"

git init -q -b main "${MAIN}" || setup_error "git init failed"
git -C "${MAIN}" config user.email t@example.com
git -C "${MAIN}" config user.name test
git -C "${MAIN}" config commit.gpgsign false
mkdir -p "${MAIN}/.claude"
printf '{\n  "enabledPlugins": {"x": false}\n}\n' > "${MAIN}/.claude/settings.json"
printf 'settings.local.json\n.endless/worktrees/\n' > "${MAIN}/.gitignore"
git -C "${MAIN}" add -A >/dev/null 2>&1
git -C "${MAIN}" commit -qm base >/dev/null 2>&1 || setup_error "fixture base commit failed"

mkdir -p "${MAIN}/.endless/worktrees"
for n in e-100 e-200; do
    git -C "${MAIN}" worktree add -q -b "task/${n}" ".endless/worktrees/${n}" >/dev/null 2>&1 \
        || setup_error "fixture worktree ${n} failed"
    printf '{\n  "enabledPlugins": {"x": false},\n  "env": {"XDG_CONFIG_HOME": "/tmp/sb-%s"},\n  "hooks": {"SessionStart": [{"hooks": [{"type":"command","command":"/wt/%s/bin/endless-go hook claude"}]}]}\n}\n' \
        "${n}" "${n}" > "${MAIN}/.endless/worktrees/${n}/.claude/settings.json"
    git -C "${MAIN}/.endless/worktrees/${n}" update-index --skip-worktree .claude/settings.json
done

section "The failure this task removes"
# Main commits to the tracked settings.json. skip-worktree tells git the file
# must not be touched, so git refuses to check out any commit that changes it —
# and the refusal names no conflicting file, which is why it read as a mystery
# "rebase conflict / (none reported)".
python3 - "${MAIN}/.claude/settings.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p)); d["autoMemoryEnabled"] = False
json.dump(d, open(p, "w"), indent=2)
PY
git -C "${MAIN}" commit -qam "main changes settings.json" >/dev/null 2>&1
blocked="$(git -C "${MAIN}/.endless/worktrees/e-100" rebase main 2>&1)"
assert_contains "an armed worktree cannot rebase once main touches settings.json" \
    ".claude/settings.json" "${blocked}"

section "claude-settings-repair clears the arming"
repair_out="$(cd "${MAIN}" && "${ENDLESS_GO}" sandbox claude-settings-repair --all 2>&1)"
assert_contains "--all sweeps every worktree under the main checkout" \
    "2 of 2 worktree(s) repaired" "${repair_out}"

for n in e-100 e-200; do
    bit="$(git -C "${MAIN}/.endless/worktrees/${n}" ls-files -v .claude/settings.json | cut -c1)"
    assert_eq "${n}: skip-worktree bit is cleared (H, not S)" "H" "${bit}"
    assert_eq "${n}: the tracked file is clean again" \
        "" "$(git -C "${MAIN}/.endless/worktrees/${n}" status --porcelain -- .claude/settings.json)"
done

# Nothing the arming was hiding may be lost: the generated env block and hook
# override move to the git-ignored local file.
local_json="${MAIN}/.endless/worktrees/e-100/.claude/settings.local.json"
if [[ -f "${local_json}" ]]; then
    assert_eq "the sandbox env block is salvaged into settings.local.json" \
        "/tmp/sb-e-100" \
        "$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['env']['XDG_CONFIG_HOME'])" "${local_json}")"
    assert_eq "the hook override is salvaged into settings.local.json" \
        "SessionStart" \
        "$(python3 -c "import json,sys;print(','.join(sorted(json.load(open(sys.argv[1]))['hooks'])))" "${local_json}")"
else
    report_fail "settings.local.json is written by the repair" "${local_json} exists" "missing"
fi

assert_eq "the tracked file holds exactly its committed content" \
    "$(git -C "${MAIN}/.endless/worktrees/e-100" show HEAD:.claude/settings.json)" \
    "$(cat "${MAIN}/.endless/worktrees/e-100/.claude/settings.json")"

rebased="$(git -C "${MAIN}/.endless/worktrees/e-100" rebase main 2>&1)"
assert_contains "the same rebase now succeeds" "Successfully rebased" "${rebased}"

again="$(cd "${MAIN}" && "${ENDLESS_GO}" sandbox claude-settings-repair --all 2>&1)"
assert_contains "re-running the repair changes nothing" "0 of 2 worktree(s) repaired" "${again}"

section "The repair never touches ordinary user work"
# Without the bit, a modified .claude/settings.json is a visible tracked edit —
# someone's real work. Reverting that to satisfy a migration would be data loss,
# so an un-armed worktree must come out untouched.
UNARMED="${MAIN}/.endless/worktrees/e-200"
printf '{\n  "enabledPlugins": {"x": true}\n}\n' > "${UNARMED}/.claude/settings.json"
rm -f "${UNARMED}/.claude/settings.local.json"
unarmed_out="$(cd "${MAIN}" && "${ENDLESS_GO}" sandbox claude-settings-repair "${UNARMED}" 2>&1)"
assert_contains "an un-armed worktree is reported as needing nothing" \
    "0 of 1 worktree(s) repaired" "${unarmed_out}"
assert_contains "the user's own edit to settings.json survives" \
    '"x": true' "$(cat "${UNARMED}/.claude/settings.json")"
assert_eq "no settings.local.json is invented for it" \
    "absent" "$([[ -f "${UNARMED}/.claude/settings.local.json" ]] && echo present || echo absent)"

section "sandbox bind writes the local file and arms nothing"
BINDWT="${MAIN}/.endless/worktrees/e-100"
SANDBOX="${FIX}/sandbox"
mkdir -p "${SANDBOX}"
printf '{"name":"fixture","mode":"persistent","creator_pid":0,"created_at":"2026-01-01T00:00:00Z"}\n' \
    > "${SANDBOX}/.sandbox-meta.json"
# bind resolves sandboxes under XDG_CACHE_HOME/endless/sandboxes/<name>.
export XDG_CACHE_HOME="${FIX}/cache"
mkdir -p "${XDG_CACHE_HOME}/endless/sandboxes"
cp -R "${SANDBOX}" "${XDG_CACHE_HOME}/endless/sandboxes/fixture"
rm -f "${BINDWT}/.claude/settings.local.json"
bind_out="$("${ENDLESS_GO}" sandbox bind "${BINDWT}" fixture 2>&1)"
assert_contains "bind reports the local file as the one it wrote" \
    "settings.local.json" "${bind_out}"
assert_eq "bind routes XDG_CONFIG_HOME through settings.local.json" \
    "${XDG_CACHE_HOME}/endless/sandboxes/fixture" \
    "$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['env']['XDG_CONFIG_HOME'])" "${BINDWT}/.claude/settings.local.json")"
assert_eq "bind leaves the tracked settings.json unmodified" \
    "" "$(git -C "${BINDWT}" status --porcelain -- .claude/settings.json)"
assert_eq "bind sets no skip-worktree bit" \
    "H" "$(git -C "${BINDWT}" ls-files -v .claude/settings.json | cut -c1)"

section "No writer arms the bit any more"
# Comment lines are excluded on purpose: both files still DESCRIBE the arming —
# the justfile recipe explains why it stopped, and the Go doc comments record
# what the bit did. It is the executable lines that must be free of it. Test
# files are excluded for the same reason: the repair's fixture has to arm a
# worktree in order to have something to repair.
assert_eq "the justfile executes no 'update-index --skip-worktree'" \
    "" "$(grep -v '^[[:space:]]*#' "${WT}/justfile" | grep -n 'update-index --skip-worktree')"
assert_eq "no sandboxcmd source line sets the bit" \
    "" "$(find "${WT}/internal/sandboxcmd" -name '*.go' ! -name '*_test.go' -exec cat {} + \
            | grep -v '^[[:space:]]*//' | grep -n -- '"--skip-worktree"')"
assert_contains "the justfile recipe targets settings.local.json" \
    ".claude/settings.local.json" "$(grep -c 'settings.local.json' "${WT}/justfile" >/dev/null && grep -m1 -o '.claude/settings.local.json' "${WT}/justfile")"

section "The fleet sweep ships with the land"
SWEEP="${WT}/.endless/hooks/post-land/e-1347.sh"
assert_eq "the post-land script exists and is executable" \
    "yes" "$([[ -x "${SWEEP}" ]] && echo yes || echo no)"
assert_contains "it invokes the repair across every worktree" \
    "claude-settings-repair --all" "$(cat "${SWEEP}")"
assert_contains "the subcommand it calls is registered" \
    "claude-settings-repair" "$("${ENDLESS_GO}" sandbox --help 2>&1)"

section "The hook detector follows the override to its new home"
assert_contains "worktreeOverrideRegistered reads settings.local.json" \
    "settings.local.json" "$(cat "${WT}/internal/hookcmd/claude.go")"

summary
