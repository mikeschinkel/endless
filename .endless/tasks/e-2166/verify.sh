#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2166 and records what was true when E-2166
# landed. Edit it only if you ARE E-2166. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2166: every worktree's Claude hooks run the INSTALLED endless-go.
#
# THE FAILURE. Every task worktree's .claude/settings.local.json pinned all six
# Claude hook events at that worktree's own bin/endless-go, and the installed
# binary self-skipped on seeing the pin — so the candidate was ALL that ran.
# When it broke, nothing was alive to report it: the session went unregistered
# and the error went to a stderr nobody reads. Measured on this machine while
# the task was open: 120 worktrees, 96 of them pinned, roughly a third of those
# binaries stale. A stale one auto-registers stray project rows, and twice
# resurrected dropped schema objects that broke session writes machine-wide.
#
# ED-1596 dropped delegation outright rather than repairing it. Candidate hook
# code is exercised by verify suites driving the real hook binary under this
# runner's temp HOME — deterministically, in isolation — so live delegation
# bought realism at the price of candidate code writing the shared ledger.
#
# WHAT LANDS HERE, in two halves.
#
#   Generator — claude-settings-init stops writing a hooks block at all, and
#   STRIPS one an earlier run left behind. Not "writes the installed path
#   instead": hooks already reach a worktree from the user scope, which
#   `endless setup claude-hook` populates for every user and every tracked
#   project with no self_dev gate, and which Claude Code applies in every
#   directory. A copy in the worktree would be a frozen snapshot contradicting
#   every later `setup claude-hook` repair.
#
#   Sweep — claude-settings-sweep re-runs that recipe over every worktree,
#   because the fix cannot ride in as a commit: bin/ and settings.local.json are
#   both git-ignored, so neither a rebase nor `endless worktree sync` can carry
#   a file git does not track, and a settings.local.json outranks anything
#   committed anyway.
#
#   Plus the machinery that existed only to serve the pin, now deleted:
#   warnForeignHookBuild and monitor.ForeignHookBuild (which would otherwise
#   have warned on every hook event in all 120 worktrees, the state ED-1596
#   declares correct), and shouldSkipForWorktree / shouldSkipForWorktreeAt /
#   worktreeOverrideRegistered (the self-skip that made the pin an override
#   rather than a double fire).
#
# WHAT THIS SUITE DOES NOT ASSERT, deliberately: that the real 96 are swept.
# The sweep invokes MAIN's justfile for each worktree, exactly as
# post-worktree-create.sh does, so it cannot be run with a candidate recipe —
# pointing it at this branch's justfile would still have it call main's old,
# pinning one. The real sweep therefore runs from the main checkout AFTER this
# lands, and section D asserts that ordering is the only one possible rather
# than leaving it a convention someone has to remember.
#
# See E-2166's plan: endless task show E-2166 --all-fields --db main
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# The Go half is deletions, so the proof it is complete is that the tree still
# builds and the two packages it cut into still pass. hookcmd also had its
# shared worktree fixtures moved out of the deleted claude_skip_test.go, which
# only a compile can confirm.

if out=$(go build ./... 2>&1); then
    report_pass "go build: nothing still references the deleted machinery"
else
    report_fail "go build: nothing still references the deleted machinery" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(go test ./internal/hookcmd ./internal/monitor 2>&1); then
    report_pass "go test: hookcmd and monitor, the two packages cut into"
else
    report_fail "go test: hookcmd and monitor, the two packages cut into" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

py_file() { # py_file <path> <label>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "pytest: $2"
    else
        report_fail "pytest: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

py_file tests/test_claude_settings_init.py      "the generator's embedded builder"
py_file tests/test_claude_settings_sweep.py     "the sweep, end to end over a real repo"
py_file tests/test_refusal_inventory_anchors.py "the refusal inventory still resolves"

# ---------------------------------------------------------------------------
section "B. The generator: no pin written, and an existing one removed"
# ---------------------------------------------------------------------------
# Section A already ran these. They are named here so the report states each
# claim rather than collapsing all of them into one green file.

py_claim() { # py_claim <file::test> <claim>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

I=tests/test_claude_settings_init.py

py_claim "${I}::test_no_hooks_key_is_ever_written" \
    "the recipe writes no hooks key at all — hooks come from the user scope"
py_claim "${I}::test_an_existing_pin_is_removed" \
    "and REMOVES a hooks block an earlier run left behind: the sweep's whole mechanism"
py_claim "${I}::test_a_pin_does_not_survive_a_second_pass" \
    "the removal is not a one-shot — re-running never resurrects the block"
py_claim "${I}::test_removal_is_announced" \
    "stripping a pin says so on stdout, so the sweep can count what it changed"
py_claim "${I}::test_hand_written_keys_survive" \
    "hand-written keys (permissions, env) survive regeneration"
py_claim "${I}::test_bg_isolation_written_fresh" \
    "worktree.bgIsolation stays, which is now the file's only generated content"
py_claim "${I}::test_idempotent" \
    "and re-running is byte-identical, so nothing churns"

# ---------------------------------------------------------------------------
section "C. The sweep: reaches every worktree, or stops and says which"
# ---------------------------------------------------------------------------
# A sweep that logged a failure and carried on would leave an unknown number of
# worktrees pinned to a stale binary while reporting success — the same silent
# degrade that made this task necessary. Stopping first is what makes "the sweep
# succeeded" mean every worktree was swept.

S=tests/test_claude_settings_sweep.py

py_claim "${S}::test_every_worktree_is_unpinned" \
    "every worktree it visits loses its pin and keeps its bgIsolation"
py_claim "${S}::test_it_stops_on_the_first_failure" \
    "one it cannot rewrite stops it, named, non-zero — never a partial success"
py_claim "${S}::test_it_reports_what_it_changed" \
    "it reports visited and unpinned counts, and a second pass changes nothing"
py_claim "${S}::test_it_refuses_to_run_from_a_worktree" \
    "it refuses from inside a worktree: it sweeps them all, so that overreaches"
py_claim "${S}::test_an_abandoned_directory_is_neither_swept_nor_a_failure" \
    "enumeration is git worktree list, so a stray directory neither sweeps nor fails"

# ---------------------------------------------------------------------------
section "D. The wiring, read from the tree rather than eyeballed"
# ---------------------------------------------------------------------------

JF="$(cat justfile)"
PWC="$(cat .endless/hooks/post-worktree-create.sh)"

assert_contains "the sweep recipe exists" \
    "claude-settings-sweep:" "${JF}"
assert_contains "and just exposes it, so it is findable without reading the file" \
    "claude-settings-sweep" "$(just --list 2>&1)"

# The pin's own spelling. The generator used to compute this; nothing in the
# recipe may compute a per-worktree binary path again, or the sweep's result
# would be undone by the next worktree bootstrap.
assert_not_contains "the generator computes no worktree binary path for hooks" \
    'new_bin' "${JF}"

# Worktree birth and the sweep must run the SAME recipe, or a swept worktree and
# a new one diverge — two implementations that agree only until one is edited.
assert_contains "worktree birth still runs claude-settings-init" \
    "recipe claude-settings-init" "${PWC}"
assert_contains "and the sweep runs that same recipe, not a copy of its logic" \
    "claude-settings-init" "$(printf '%s' "${JF}" | sed -n '/^claude-settings-sweep:/,/^$/p')"

# Why the real sweep is a POST-land step, asserted rather than remembered: it
# invokes main's justfile per worktree, so run pre-land it would call main's
# still-pinning recipe. The refusal below is the other half — it cannot be run
# from this worktree at all.
SWEEP_BODY="$(printf '%s' "${JF}" | sed -n '/^claude-settings-sweep:/,/^# Seed this worktree/p')"
assert_contains "the sweep invokes main's justfile per worktree, as birth does" \
    '--justfile "${main_checkout}/justfile"' "${SWEEP_BODY}"
assert_contains "and refuses to run from a worktree, so it cannot run pre-land" \
    "refusing to run from a worktree" "${SWEEP_BODY}"

# The deleted machinery, by name. `go build` in section A proves no CALLER
# survives; this proves no DEFINITION does either — a surviving definition would
# be inert code documenting a design ED-1596 dropped, and the foreign-build
# warning specifically would fire on every hook event in all 120 worktrees.
for sym in warnForeignHookBuild ForeignHookBuild WorktreeHookBinary \
           shouldSkipForWorktree shouldSkipForWorktreeAt worktreeOverrideRegistered; do
    if hits="$(grep -rn "${sym}" cmd internal 2>/dev/null)"; then
        report_fail "${sym} is gone from cmd/ and internal/" \
            "no matches" "$(printf '%s' "${hits}" | head -5)"
    else
        report_pass "${sym} is gone from cmd/ and internal/"
    fi
done

# The refusal inventory anchored six log/warning lines to two of those symbols.
# A row naming a symbol that no longer exists is a row nobody can act on, which
# is what E-2155's RETIRED vocabulary is for.
TSV=docs/research-2026-09-17-refusal-inventory.tsv
assert_eq "all eight rows anchored to the deleted symbols are marked retired" \
    "8" "$(grep -c 'RETIRED: E-2166' "${TSV}")"

# ---------------------------------------------------------------------------
section "E. This worktree, as it stands (informational)"
# ---------------------------------------------------------------------------
# The end state on a real file. Read only: the runner isolates HOME and XDG, not
# the project tree, so regenerating here would edit live state — and this
# worktree cannot legitimately be swept yet anyway, because the sweep runs
# main's justfile and main still has the pinning recipe. So a worktree still
# pinned is the EXPECTED pre-land answer and is reported as a skip, not a
# failure; once the sweep has run it becomes a pass. Either way it says which.

LOCAL=".claude/settings.local.json"
if [[ ! -f "${LOCAL}" ]]; then
    report_pass "this worktree's settings.local.json names no binary path"
elif grep -q "${WT}/bin/endless-go" "${LOCAL}"; then
    report_skip "this worktree's settings.local.json names no binary path" \
        "still pinned, as every worktree is until 'just claude-settings-sweep' runs from main after this lands"
else
    report_pass "this worktree's settings.local.json names no binary path"
fi

summary
