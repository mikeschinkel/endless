#!/usr/bin/env bash
#
# E-1776 verification — `endless session resume <ref>` relaunches a lost Claude
# session in the CURRENT tmux pane: resolve <ref> (task id off the tmux tab, or
# a session id / UUID) → its session UUID + task worktree → cd there → exec
# `claude --resume <uuid>`.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1776
#
# What it checks:
#   1. Resolver unit tests (hermetic; task-first, most-recent, prefix, fallback).
#   2. The `session resume` subcommand is registered (worktree Python).
#   3. End-to-end against the REAL ledger: a discovered resumable session cd's
#      to its worktree and execs `claude --resume <uuid>`. A `claude` stub on
#      PATH stands in so nothing hijacks the terminal. Skips cleanly if no
#      resumable session with an on-disk worktree exists.
#
# Exit 0 on all-passed (or e2e-skipped), 1 on any failure.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO="$(git rev-parse --show-toplevel)" || { echo "not in a git repo" >&2; exit 2; }
cd "$REPO" || exit 2
GO="$REPO/bin/endless-go"
REALCFG="$HOME/.config/endless"

if [[ -t 1 ]]; then GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else GREEN=""; RED=""; DIM=""; RESET=""; fi

pass=0; fail=0
ok()   { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$1"; pass=$((pass+1)); }
bad()  { printf '  %s✗%s %s\n' "$RED" "$RESET" "$1"; fail=$((fail+1)); }
skip() { printf '  %s— %s%s\n' "$DIM" "$1" "$RESET"; }

# ── 1. resolver unit tests ──────────────────────────────────────────────────
if go test ./internal/monitor/ -run TestResolveResumeTarget >/dev/null 2>&1; then
    ok "resolver unit tests pass (task-first / most-recent / prefix / fallback)"
else
    bad "resolver unit tests failed — run: go test ./internal/monitor/ -run TestResolveResumeTarget -v"
fi

# ── 2. command registered ───────────────────────────────────────────────────
if uv run endless session resume --help 2>&1 | grep -q "Relaunch a lost Claude session"; then
    ok "\`endless session resume --help\` is registered"
else
    bad "\`endless session resume\` is not registered"
fi

# ── 3. end-to-end with a claude stub, against the real ledger ───────────────
ref=""; uuid=""; wt=""
for id in $(uv run endless --db main session list 2>/dev/null | awk '$1 ~ /^[0-9]+$/ {print $1}' | head -60); do
    j="$("$GO" --config-dir "$REALCFG" session-query resume-target --ref "$id" 2>/dev/null)" || continue
    u="$(printf '%s' "$j" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("session_id") or "")' 2>/dev/null)"
    w="$(printf '%s' "$j" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("worktree_path") or "")' 2>/dev/null)"
    if [[ -n "$u" && -n "$w" && -d "$w" ]]; then ref="$id"; uuid="$u"; wt="$w"; break; fi
done

if [[ -z "$ref" ]]; then
    skip "no resumable session with an on-disk worktree found — e2e skipped (unit tests still cover the resolver)"
else
    stub="$(mktemp -d)"
    cat > "$stub/claude" <<'SH'
#!/bin/sh
echo "STUB args=[$*] cwd=$(pwd)"
SH
    chmod +x "$stub/claude"
    # Worktree bin first so `endless-go` has resume-target; stub `claude` first
    # so the exec lands on the stub, not a real Claude.
    out="$(PATH="$stub:$REPO/bin:$PATH" uv run endless --db main session resume "$ref" 2>&1)"
    rm -rf "$stub"

    if printf '%s\n' "$out" | grep -q -- "--resume $uuid"; then
        ok "session $ref → exec'd \`claude --resume $uuid\`"
    else
        bad "did not exec claude --resume $uuid; got: $out"
    fi
    if printf '%s\n' "$out" | grep -q "cwd=$wt"; then
        ok "cd'd to the task worktree ($wt)"
    else
        bad "did not cd to worktree $wt; got: $out"
    fi
fi

# ── summary ─────────────────────────────────────────────────────────────────
printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
