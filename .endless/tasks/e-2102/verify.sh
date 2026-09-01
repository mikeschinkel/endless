#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2102 and records what was true when E-2102
# landed. Edit it only if you ARE E-2102. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2102 verification — a task's tmux window is named for the task, and
# nothing else.
#
# Before: `task spawn` named its window `<project>_<one-or-two-words>[E-NNNN]`,
# spending most of a narrow tab on words the user already knows, and
# `session goto --resume` passed no `-n` at all, so tmux fell back to naming
# the window after its command and every resumed window on screen read
# `claude`.
#
# After: both are exactly `E-NNNN`.
#
# The part that is not cosmetic is `internal/sandboxcmd/reapguard.go`. It reads
# tmux window names to decide which DB sandboxes to SPARE — one of only two
# protections that outlive the worktree directory — and it matched the
# bracketed id specifically. Renaming without moving that regex would have let
# a sandbox be reaped while its window was open, with no error and no signal.
# So sections 3 and 4 below drive the REAL guard, through the REAL binary,
# against a REAL tmux server, on every name form that matters.
#
#   endless task verify E-2102        # or, in this repo, `just verify`
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

command -v tmux >/dev/null 2>&1 || setup_error "tmux is not installed"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
cleanup() {
    local sock
    for sock in "${TMP}"/*.sock; do
        [[ -S "${sock}" ]] && tmux -S "${sock}" kill-server 2>/dev/null
    done
    rm -rf "${TMP}"
}
trap cleanup EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# The durable coverage: the guard's name-form table, the spawn argv, and the
# `-n` on the resume path. A failure here makes everything below meaningless,
# so the suite stops rather than reporting a cascade.
section "1. Unit gate (fail fast)"

if go test ./internal/sandboxcmd/ -run TestReapGuard >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/sandboxcmd/ -run TestReapGuard"
else
    report_fail "go test ./internal/sandboxcmd/ -run TestReapGuard" \
        "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q tests/test_spawn_foreground.py tests/test_session_goto_back.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest test_spawn_foreground, test_session_goto_back"
else
    report_fail "pytest spawn/goto" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. the name itself, from the code that builds it ────────────────────────
section "2. The name both surfaces build"

if ! name="$(uv run python -c \
    'from endless.task_cmd import tmux_window_name; print(tmux_window_name(2102))' \
    2>"${TMP}/name.err")"; then
    name="import failed: $(tr '\n' ' ' <"${TMP}/name.err" | tail -c 200)"
fi
assert_eq "tmux_window_name(2102) is the task id alone" "E-2102" "${name}"

# The reason the old format used '_' as its separator, still load-bearing:
# tmux reads ':' as session:window and '.' as window.pane in a -t target, so
# either character in a window name breaks `select-window -t <name>`. `E-NNNN`
# satisfies that by construction — this pins that it keeps doing so.
assert_not_contains "the name carries no tmux session:window separator" ":" "${name}"
assert_not_contains "the name carries no tmux window.pane separator" "." "${name}"

# ── 3. real tmux: -n sticks, and its absence is what said `claude` ──────────
section "3. Real tmux keeps the name it is given"

sock="${TMP}/named.sock"
named_pane="$(tmux -S "${sock}" new-session -d -s p -n 'E-2102' -P -F '#{pane_id}' \
    'sleep 120')" || setup_error "could not start a tmux server on ${sock}"
unnamed_pane="$(tmux -S "${sock}" new-window -d -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not open an unnamed tmux window"
named="$(tmux -S "${sock}" display-message -p -t "${named_pane}" '#{window_name}')"
unnamed="$(tmux -S "${sock}" display-message -p -t "${unnamed_pane}" '#{window_name}')"
tmux -S "${sock}" kill-server 2>/dev/null

assert_eq "a window created with -n E-2102 is named exactly that" "E-2102" "${named}"

# The pre-fix `session goto --resume` behaviour, demonstrated rather than
# asserted from memory: with no -n, tmux picks the name itself off the running
# command — which is why every resumed window on screen read `claude`. Exactly
# which word tmux picks is tmux's business and varies; that it is not the task
# is the whole defect.
assert_not_contains \
    "a window created WITHOUT -n is named by tmux ('${unnamed}'), not for its task" \
    "E-2102" "${unnamed}"

# ── 4. the reap guard, end to end ───────────────────────────────────────────
# A worktree-bound sandbox with no worktree directory, no git-tracked worktree
# and no unmerged branch is orphaned — UNLESS a live tmux window names its
# task. So `sandbox list`'s state for e-2102 is a direct read of the guard's
# tmux condition, through the compiled binary, with nothing stubbed.
section "4. The reap guard, against the real binary and a real tmux server"

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${TMP}/build.log" 2>&1 \
    || setup_error "could not build endless-go: $(tail -5 "${TMP}/build.log")"

REPO="${TMP}/repo"
mkdir -p "${REPO}" "${TMP}/home" "${TMP}/cache/endless/sandboxes/e-2102"
(
    cd "${REPO}" &&
    git init -q -b main . &&
    git -c user.email=v@v -c user.name=v commit -q --allow-empty -m init
) || setup_error "could not build the probe repo"

cat > "${TMP}/cache/endless/sandboxes/e-2102/.sandbox-meta.json" <<'JSON'
{"created_at":"2020-01-01T00:00:00Z","mode":"keep","creator_pid":1,"name":"e-2102"}
JSON

# guard_state <socket-label> <window-name> — the state `sandbox list` reports
# for sandbox e-2102 while a tmux window by that name is open. Each probe gets
# its own socket: reusing one races the kill against the next server's start,
# and a guard that cannot reach tmux falls back to reporting in-use, which
# would read as a pass.
guard_state() {
    local sock="${TMP}/$1.sock" out
    tmux -S "${sock}" new-session -d -s p -n "$2" 'sleep 120' || {
        printf 'tmux-failed\n'; return
    }
    out=$(
        cd "${REPO}" && env \
            HOME="${TMP}/home" XDG_CONFIG_HOME="${TMP}/home/.config" \
            XDG_CACHE_HOME="${TMP}/cache" TMUX="${sock},1,0" \
            "${BIN}" sandbox list 2>"${TMP}/$1.err"
    )
    tmux -S "${sock}" kill-server 2>/dev/null
    # A guard that failed to build reports every worktree-bound sandbox as
    # in-use. Surface that instead of letting it pass as protection.
    if [[ -s "${TMP}/$1.err" ]]; then
        printf 'guard-error: %s\n' "$(tr '\n' ' ' <"${TMP}/$1.err")"
        return
    fi
    printf '%s\n' "${out}" | awk '$1 == "e-2102" { print $3 }'
}

assert_eq "a window named E-2102 spares its sandbox" \
    "in-use" "$(guard_state bare 'E-2102')"
assert_eq "a window still carrying the pre-E-2102 form spares it too" \
    "in-use" "$(guard_state legacy 'endless_probe[E-2102]')"
assert_eq "a window that merely mentions the id spares nothing" \
    "orphaned" "$(guard_state mention 'notes on E-2102')"
assert_eq "an id with anything after it spares nothing" \
    "orphaned" "$(guard_state trailing 'E-2102 scratch')"
assert_eq "a longer id starting with it spares nothing" \
    "orphaned" "$(guard_state longer 'E-21020')"
assert_eq "the name resumed windows used to get spares nothing" \
    "orphaned" "$(guard_state claude 'claude')"

# ── 5. nothing else resolves a task from the window name ────────────────────
# `endless-go tmux active-id` reads the pane's session out of the database. If
# it had ever been implemented off the window name, this would print E-2102
# instead of failing on an unknown pane.
section "5. active-id still resolves by pane, not by window name"

sock="${TMP}/active.sock"
tmux -S "${sock}" new-session -d -s p -n 'E-2102' 'sleep 120' \
    || setup_error "could not start a tmux server on ${sock}"
out=$(
    cd "${REPO}" && env \
        HOME="${TMP}/home" XDG_CONFIG_HOME="${TMP}/home/.config" \
        XDG_CACHE_HOME="${TMP}/cache" TMUX="${sock},1,0" \
        "${BIN}" tmux active-id --pane '%e2102verify' 2>/dev/null
); rc=$?
tmux -S "${sock}" kill-server 2>/dev/null

assert_eq "an unknown pane has no task, even beside a window named E-2102" "1" "${rc}"
assert_eq "and nothing was read off the window name" "" "${out}"

summary
