#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1934 and records what was true when E-1934
# landed. Edit it only if you ARE E-1934. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

section "Unit tests for the gate and its config (fail-fast)"
if out="$(cd "$WT" && uv run pytest -q \
        tests/test_content_flag_gate.py \
        tests/test_config.py \
        tests/test_no_self_dev_ids.py 2>&1)"; then
    report_pass "gate, config and runtime-string suites pass"
else
    report_fail "unit suites failed: $(printf '%s' "$out" | tail -3)"
    summary
fi

# Exercises the real seam every content flag funnels through, so the checks
# below prove the wiring and not just the predicate.
probe() {
    (cd "$WT" && uv run python -c '
import sys
from endless.cli import _resolve_content_flag
mode, name, value = sys.argv[1], sys.argv[2], sys.argv[3]
if mode == "file":
    import tempfile, os
    fd, path = tempfile.mkstemp(suffix=".md")
    os.write(fd, value.encode()); os.close(fd)
    args = (None, path)
else:
    args = (value, None)
try:
    _resolve_content_flag(*args, name)
    print("ACCEPTED")
except Exception as e:
    print("REFUSED: %s" % e)
' "$@" 2>&1 | tr '\n' ' ')
}

section "A line citation is refused, inline and from a file"
assert_contains "inline citation refused" "cites a line number" \
    "$(probe inline text 'the gate is in cli.py:2249 today')"
assert_contains "file-loaded citation refused — the placement this task moved" \
    "cites a line number" \
    "$(probe file text 'Rewrite the helper in src/endless/cli.py:2249 and retest.')"
assert_contains "a range citation is refused" "cites a line number" \
    "$(probe inline analysis 'see cli.py:12-40 for the block')"
assert_contains "a line:col citation is refused" "cites a line number" \
    "$(probe inline outcome 'landed at cli.py:12:5')"

section "The refusal routes the reader and offers no escape"
cite_msg="$(probe inline text 'the gate is in cli.py:2249 today')"
assert_contains "explains why line numbers are refused" "go stale" "$cite_msg"
assert_contains "names the durable alternative" "function, command or symbol" "$cite_msg"
assert_contains "says there is no escape flag" "no --allow flag" "$cite_msg"

section "Colon-number shapes that are not citations still pass"
assert_contains "a clock time passes" "ACCEPTED" \
    "$(probe inline text 'ES-1060 ran 06:00:57 to 06:01:27, thirty seconds')"
assert_contains "a host and port passes" "ACCEPTED" \
    "$(probe inline text 'bind to localhost:8080 in development')"
assert_contains "a bare colon-number passes" "ACCEPTED" \
    "$(probe inline text 'a bare :2279 is not a citation anyone writes')"
assert_contains "a URL carrying a source extension passes" "ACCEPTED" \
    "$(probe inline text 'see https://example.com/a/b.py:80 for the source')"

section "Absolute paths are now judged in a file too"
assert_contains "file-loaded absolute path refused" "absolute path" \
    "$(probe file text 'the sandbox lives at /tmp/anything and that is fine here')"
assert_contains "the remedy no longer points at the file flag" "project-relative" \
    "$(probe inline text 'see /tmp/x.md here')"

section "Clean content still loads"
assert_contains "prose naming a function is accepted" "ACCEPTED" \
    "$(probe file text 'Rewrite _guard_content_rules in the resolver, then retest.')"

section "The guide states the rule"
assert_contains "tasks guide carries the no-time-frozen-specifics rule" \
    "No time-frozen specifics" \
    "$(cd "$WT" && uv run endless guide tasks 2>/dev/null)"
assert_not_contains "the stale 'file paths' wording is gone" \
    "including approach, file paths, verification steps" \
    "$(cd "$WT" && uv run endless guide tasks 2>/dev/null)"

summary
