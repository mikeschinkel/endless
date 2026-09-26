#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2180 and records what was true when E-2180
# landed. Edit it only if you ARE E-2180. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2180 verification — `endless phrase` and the Python pattern-matcher config
# it edited are gone; the verb registry that shared matchers.py is not.
#
# Before: `endless phrase add|remove|enable|disable|list` edited a `matchers`
# array that nothing read, and every verb read seeded claim/complete/release
# regexes into the machine config.json as a side effect.
#
# After: the command is unknown, a fresh machine config never grows a
# `matchers` key, and verbs still seed, register and list through both layers.
#
#   endless task verify E-2180
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

if uv run pytest -q \
        tests/test_verb_gate.py \
        tests/test_verb_update.py \
        tests/test_verb_category_gate.py \
        tests/test_completed_status.py \
        tests/test_project_config_path.py \
        tests/test_project_set_fields_help.py \
        tests/test_agent_help.py \
        tests/test_guide_map.py \
        tests/test_rowcap.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest verb registry + touched suites"
else
    report_fail "pytest verb registry + touched suites" "exit 0" \
        "$(tail -25 "${TMP}/py.log")"
    summary
fi

# The CLI runs from a directory outside any project, so only the machine layer
# (under the runner's temp HOME) is in play and nothing is committed to main.
OUT="${TMP}/out"
mkdir -p "${OUT}"
endless_cli() { (cd "${OUT}" && uv run --quiet --project "${WT}" endless "$@"); }

# ── 2. the command is gone ─────────────────────────────────────────────────
section "2. endless phrase is an unknown command"

out="$(endless_cli phrase list 2>&1)"; rc=$?
assert_eq "endless phrase list exits non-zero" "nonzero" "$([[ ${rc} -ne 0 ]] && echo nonzero || echo zero)"
assert_contains "click reports it as unknown" "No such command 'phrase'" "${out}"
assert_not_contains "top-level help no longer lists phrase" " phrase " "$(endless_cli --help 2>&1)"

# ── 3. no dead symbol survives ─────────────────────────────────────────────
section "3. Pattern-matcher symbols are gone from src, tests and the guide"

hits="$(grep -rn 'phrase_cmd\|add_match_value\|remove_match_value\|get_action_regex\|DEFAULT_MATCHERS\|_migrate_stale_defaults\|load_all_matchers' \
    src tests docs/guide 2>/dev/null)"
assert_eq "no references to the removed matcher API" "" "${hits}"
assert_eq "src/endless/phrase_cmd.py is deleted" "absent" "$([[ -e src/endless/phrase_cmd.py ]] && echo present || echo absent)"
assert_not_contains "project config.json no longer carries matchers" '"matchers"' "$(cat .endless/config.json)"

# ── 4. verbs still work end to end ─────────────────────────────────────────
section "4. Verb registry still seeds, registers and lists"

cfg_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/endless"
list="$(endless_cli verb list --no-limit 2>&1)"; rc=$?
assert_eq "verb list exits 0" "0" "${rc}"
assert_contains "built-in verbs are listed" "refactor" "${list}"
assert_eq "machine verbs.jsonl was seeded" "present" "$([[ -f "${cfg_dir}/verbs.jsonl" ]] && echo present || echo absent)"
if [[ -f "${cfg_dir}/config.json" ]]; then
    assert_not_contains "fresh machine config.json is not seeded with matchers" '"matchers"' "$(cat "${cfg_dir}/config.json")"
else
    report_pass "fresh machine config.json is not seeded with matchers (no file)"
fi

endless_cli verb add ponder --definition "to deliberate over" --machine-only >"${TMP}/add.log" 2>&1; rc=$?
assert_eq "verb add --machine-only exits 0" "0" "${rc}"
assert_contains "the new verb lists" "ponder" "$(endless_cli verb list --no-limit 2>&1)"

# ── 5. guide ───────────────────────────────────────────────────────────────
section "5. Guide cross-reference"

gc="$(just guide-check 2>&1)"; rc=$?
assert_eq "just guide-check exits 0" "0" "${rc}"
assert_not_contains "phrase is off the uncovered-command list" "endless phrase" "${gc}"
assert_not_contains "index.md has no phrase row" '| `phrase` |' "$(cat docs/guide/index.md)"

summary
