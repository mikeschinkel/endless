#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2401 and records what was true when E-2401
# landed. Edit it only if you ARE E-2401. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2401 — the tmux config keys are registered in the settings registry, with
# path, name and command kinds whose shape checks tmuxservers.Resolve shares.
#
# Checks, fail-fast in order:
#   1. The task's own Go and Python tests (the kinds, Resolve's refusals, the
#      Python export, and TestEveryConfigKeyIsRegistered).
#   2. This worktree's endless-go lists the five tmux keys: feature tmux,
#      machine file only, not suggestable, each with its kind and default.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go uv python3; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP=$(mktemp -d)
trap 'rm -rf "${WORK_TMP}"' EXIT

section "1. The task's own tests"
if out=$(go test ./internal/settings/ ./internal/tmuxservers/ ./cmd/endless-go/ 2>&1); then
    report_pass "Go: internal/settings, internal/tmuxservers, cmd/endless-go"
else
    report_fail "Go: internal/settings, internal/tmuxservers, cmd/endless-go" "pass" "${out}"
    summary
fi
if out=$(uv run pytest -q tests/test_project_set_registered.py 2>&1); then
    report_pass "Python: settings_export parses and describes the new kinds"
else
    report_fail "Python: settings_export parses and describes the new kinds" "pass" "${out}"
    summary
fi

section "2. endless-go settings list exports the tmux keys"
BIN="${WORK_TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build endless-go"
EXPORT=$("${BIN}" settings list --json --metadata) || setup_error "settings list --json --metadata failed"

tmux_key() {
    printf '%s' "${EXPORT}" | python3 -c '
import json, sys
s = {e["id"]: e for e in json.load(sys.stdin)["settings"]}.get(sys.argv[1])
if s is None:
    print("missing")
else:
    print(s["feature"], s["files"], s["kind"],
          "suggestable=%s" % s["suggestable"], "default=%r" % s["default"])
' "$1"
}

assert_eq "tmux.socket_dir" "tmux machine path suggestable=False default='/tmp/endless-$(id -u)'" \
    "$(tmux_key tmux.socket_dir)"
assert_eq "tmux.console_server" "tmux machine name suggestable=False default='endless-console'" \
    "$(tmux_key tmux.console_server)"
assert_eq "tmux.tasks_server" "tmux machine name suggestable=False default='endless-tasks'" \
    "$(tmux_key tmux.tasks_server)"
assert_eq "tmux.tasks_session" "tmux machine name suggestable=False default='tasks'" \
    "$(tmux_key tmux.tasks_session)"
assert_eq "tmux.terminal" "tmux machine command suggestable=False default=''" \
    "$(tmux_key tmux.terminal)"
assert_eq "tmux.session_name is not registered" "missing" "$(tmux_key tmux.session_name)"

summary
