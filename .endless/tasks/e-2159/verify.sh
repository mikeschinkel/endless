#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2159 and records what was true when E-2159
# landed. Edit it only if you ARE E-2159. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2159 — every refusal and warning says whether the agent must report it.
#
# What this proves, in order:
#
#   0. The task's own unit tests pass. Fail-fast: nothing below is meaningful
#      if the two helpers and their goldens are broken.
#   1. The build checks BITE. Both are source scans, and a source scan that
#      silently matches nothing passes forever while enforcing nothing — so
#      each is shown failing on a planted offender, with a message naming the
#      rule.
#   2. A person's stderr did not change. This is the half that is easy to lose
#      and impossible to notice: the verdict and the directive are additive,
#      and they reach one reader only.
#   3. An agent gets the verdict, carrying the class's directive.
#   4. All three spellings of "render for an agent" agree — `--agent`,
#      `--format agent`, and the ENDLESS_AUDIENCE contract endless-go reads.
#   5. A refusal endless-go raised and Python relayed keeps GO's verdict, with
#      no second directive layered on top of it.
#
# Run it:
#   endless task verify E-2159

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── where things are ───────────────────────────────────────────────────────
#
# ENDLESS_VERIFY_DIR is this suite's own directory, .endless/tasks/e-2159, so
# the worktree root is three levels up. Derived rather than typed: a hand-written
# path has to match a directory-casing convention it cannot see.
WT="$(cd "${ENDLESS_VERIFY_DIR}/../../.." && pwd)"
GO_BIN="${WT}/bin/endless-go"

[[ -d "${WT}/internal/refusal" ]] || setup_error "not a worktree of this project: ${WT}"

# ENDLESS_AUDIENCE and the harness variables are what the two renderings turn
# on, so every invocation below states which reader it wants rather than
# inheriting whatever ran the suite. `as_human` is the interesting one: this
# suite is normally run BY an agent, so "no agent" is a state to construct.
#
# One variable drives BOTH halves — that is the contract, not a convenience.
# Python decides the audience and exports it; endless-go reads the same name.
# Setting it here exercises the seam from the outside, the way an operator
# debugging a rendering would.
as_human() {
    env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_SESSION_ID \
        -u AI_AGENT -u __CFBundleIdentifier -u ENDLESS_AUDIENCE "$@"
}
as_agent() {
    as_human ENDLESS_AUDIENCE=agent "$@"
}
# The Python CLI from THIS worktree, not the global install.
py() { (cd "${WT}" && uv run endless "$@"); }

# ─── 0. the task's own unit tests, fail-fast ────────────────────────────────

section "The task's own tests"

go_out="$(cd "${WT}" && go test ./internal/refusal/ 2>&1)" || {
    printf '%s\n' "${go_out}"
    setup_error "internal/refusal's own tests fail; nothing below would mean anything"
}
report_pass "internal/refusal: package tests pass"

# The catalog is the other half of plan item 5: every code must carry a remedy,
# and that remedy must be byte-identical to its "What to do." paragraph in
# docs/errors.md. Asserted in Go because it is a pure catalog fact — and
# fail-fast, because a code whose docs entry drifted is a code whose listing
# tells the user nothing.
faults_out="$(cd "${WT}" && go test ./internal/faults/ 2>&1)" || {
    printf '%s\n' "${faults_out}"
    setup_error "the faults catalog and docs/errors.md disagree"
}
report_pass "internal/faults: every code carries a remedy matching its docs entry"

py_out="$(cd "${WT}" && uv run pytest tests/test_refusal_sites.py tests/test_refusal_verdicts.py -q 2>&1)" || {
    printf '%s\n' "${py_out}"
    setup_error "the Python refusal tests fail; nothing below would mean anything"
}
report_pass "tests/test_refusal_{sites,verdicts}.py: pass"

# ─── 1. the checks bite ─────────────────────────────────────────────────────

section "The build checks refuse an unclassified refusal"

# Planted offenders, removed however this suite ends. A stray probe file would
# fail the very check it was written to exercise, for everyone, forever.
GO_PROBE="${WT}/internal/agentenv/zz_e2159_probe.go"
PY_PROBE="${WT}/src/endless/zz_e2159_probe.py"
cleanup() { rm -f "${GO_PROBE}" "${PY_PROBE}"; }
trap cleanup EXIT

cat > "${GO_PROBE}" <<'PROBE'
package agentenv

import (
	"fmt"
	"os"
)

func zzE2159Probe() { fmt.Fprintln(os.Stderr, "x") }
PROBE

go_check="$(cd "${WT}" && go test ./internal/refusal/ -run TestNoStderrOutsideThisPackage -count=1 2>&1)" && go_check_rc=0 || go_check_rc=1
assert_eq "a planted os.Stderr write fails the Go check" "1" "${go_check_rc}"
assert_contains "the Go failure names the offending file" \
    "internal/agentenv/zz_e2159_probe.go" "${go_check}"
# Matched inside one wrapped line: the rule paragraph is hard-wrapped, so a
# needle spanning the break would never match however right the text was.
assert_contains "the Go failure states the rule, not just the violation" \
    "must say whether the AGENT reading it" "${go_check}"
assert_contains "the Go failure lists the constructors to use instead" \
    "refusal.NoReport(summary, remedy)" "${go_check}"
rm -f "${GO_PROBE}"

cat > "${PY_PROBE}" <<'PROBE'
import click


def zz_e2159_probe():
    raise click.ClickException("x")
PROBE

py_check="$(cd "${WT}" && uv run pytest tests/test_refusal_sites.py -q 2>&1)" && py_check_rc=0 || py_check_rc=1
assert_eq "a planted ClickException fails the Python check" "1" "${py_check_rc}"
assert_contains "the Python failure names the offending file" \
    "zz_e2159_probe.py" "${py_check}"
assert_contains "the Python failure states the rule" \
    "must tell the agent reading it whether to report it" "${py_check}"
assert_contains "the Python failure lists the factories to use instead" \
    "agent_help.no_report(summary, remedy)" "${py_check}"
rm -f "${PY_PROBE}"

# ...and with both probes gone, the tree is clean. This is the half that says
# the checks are not merely loud.
go_clean="$(cd "${WT}" && go test ./internal/refusal/ -run TestNoStderrOutsideThisPackage -count=1 2>&1)" && go_clean_rc=0 || go_clean_rc=1
assert_eq "no unclassified stderr write remains in internal/ or cmd/" "0" "${go_clean_rc}"

py_clean="$(cd "${WT}" && uv run pytest tests/test_refusal_sites.py -q 2>&1)" && py_clean_rc=0 || py_clean_rc=1
assert_eq "no unclassified refusal remains in src/endless/" "0" "${py_clean_rc}"

# ─── 2. a person's stderr did not change ────────────────────────────────────

section "A human reads today's message, unchanged"

human_py="$(as_human bash -c "cd '${WT}' && uv run endless guide nosuchsection" 2>&1 || true)"
assert_eq "the Python refusal is the message it has always been" \
    "Error: Unknown section 'nosuchsection'. Available: appendix-a, decisions, orchestration, reference, sessions, tasks" \
    "${human_py}"

human_go="$(as_human "${GO_BIN}" worktree in-use 2>&1 || true)"
assert_eq "the Go refusal is the message it has always been" \
    "endless-go worktree in-use: --dir is required" \
    "${human_go}"

assert_not_contains "no verdict reaches a human" "[Endless]" "${human_py}"
assert_not_contains "no directive reaches a human" "Handle this yourself" "${human_go}"

# ─── 3. an agent gets the verdict and its directive ─────────────────────────

section "An agent is told whether to report it"

agent_py="$(as_agent bash -c "cd '${WT}' && uv run endless guide nosuchsection" 2>&1 || true)"
assert_contains "the verdict carries the sentinel and the verb" \
    "[Endless] guide:" "${agent_py}"
assert_contains "the verdict states whether anything changed" \
    "Nothing was printed." "${agent_py}"
assert_contains "the verdict carries the remedy" \
    "Re-run with a listed slug" "${agent_py}"
assert_contains "NO-REPORT says not to mention it, now or later" \
    "do not mention this refusal to the user, now or in your summary" "${agent_py}"

agent_go="$(as_agent "${GO_BIN}" worktree in-use 2>&1 || true)"
assert_contains "endless-go renders the same shape" "[Endless] worktree in-use:" "${agent_go}"
assert_contains "a FAULT tells the agent to tell the user" \
    "Endless itself failed: tell the user" "${agent_go}"
assert_not_contains "the verdict does not repeat the binary prefix" \
    "in-use: endless-go worktree in-use" "${agent_go}"

# ─── 4. the three spellings agree ───────────────────────────────────────────

section "--agent, --format agent and ENDLESS_AUDIENCE are one setting"

flag_agent="$(as_human bash -c "cd '${WT}' && uv run endless task unsettled --agent" 2>&1 || true)"
flag_format="$(as_human bash -c "cd '${WT}' && uv run endless task unsettled --format agent" 2>&1 || true)"
assert_eq "--agent and --format agent render identically" "${flag_agent}" "${flag_format}"
assert_contains "and both render for an agent" "[Endless] task unsettled:" "${flag_agent}"

flag_human="$(as_human bash -c "cd '${WT}' && uv run endless task unsettled" 2>&1 || true)"
assert_not_contains "while the same command without them does not" \
    "[Endless]" "${flag_human}"

# The environment contract, on its own, is what carries the audience across the
# language boundary into endless-go.
assert_contains "ENDLESS_AUDIENCE=agent alone drives endless-go" \
    "[Endless] worktree in-use:" "${agent_go}"

# ─── 5. a relayed Go refusal keeps Go's verdict ─────────────────────────────

section "A relayed refusal is not classified twice"

relayed="$(as_agent bash -c "cd '${WT}' && uv run python -c '
from endless import agent_help
go = (\"[Endless] event emit: illegal status change. Nothing was written. \"
      \"Handle this yourself: do not mention this refusal to the user, now or \"
      \"in your summary.\")
r = agent_help.relay(go + chr(10), exit_code=1)
print(r.text.strip())
'" 2>&1 || true)"
assert_contains "Go's verdict survives verbatim" \
    "[Endless] event emit: illegal status change." "${relayed}"
assert_eq "exactly one verdict — Python adds no second directive" \
    "1" "$(printf '%s' "${relayed}" | grep -c '\[Endless\]' || true)"
assert_not_contains "and no Error: prefix is put in front of Go's sentinel" \
    "Error: [Endless]" "${relayed}"

# ─── 6. a warning the USER must act on leaves stderr entirely ───────────────
#
# Plan item 5. A warning that blocks nothing and that only the user can resolve
# does not belong in the agent's reply: it goes to the errors channel, where the
# user meets it on the session-status badge and in `endless errors show`, and
# the agent spends nothing on it. These are the five such warnings that still
# have a site (the sixth, a worktree directory not git-ignored, went with
# internal/sandboxcmd/bind.go).

section "A user-only warning goes to the errors channel, not the reply"

for code in WARN-0021 WARN-0022 WARN-0023 WARN-0024 WARN-0025; do
    assert_contains "${code} is in the catalog" "${code}" \
        "$(as_human "${GO_BIN}" errors codes 2>&1 || true)"
done

# The channel end to end: record one, and read it back off the listing. The
# runner gives this suite its own HOME and config, so this writes nowhere real.
as_human "${GO_BIN}" errors record \
    --code WARN-0024 --summary "verify: a reply went out unminimized" \
    --source report:minimizer >/dev/null 2>&1 || true
listing="$(as_human "${GO_BIN}" errors list 2>&1 || true)"
assert_contains "a recorded warning reaches the errors listing" \
    "verify: a reply went out unminimized" "${listing}"
assert_contains "and it is listed under its code" "WARN-0024" "${listing}"

# An unknown code is refused rather than invented, which is what stops this
# channel becoming a way to ship a classification with no docs entry.
bogus_rc=0
as_human "${GO_BIN}" errors record --code WARN-9999 --summary x >/dev/null 2>&1 || bogus_rc=$?
assert_eq "an uncatalogued code is refused" "2" "${bogus_rc}"

summary
