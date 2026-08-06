#!/usr/bin/env bash
#
# E-1872 verification — "Document the guide's memory-only conventions in one
# pass".
#
# The gap: five conventions every session is expected to follow lived only in
# the handoff template, in git history, or in one user directive — nowhere in
# `endless guide`. A session that lost its spawn prompt to a context compaction
# and re-read the guide would invent its own commit subjects, land unasked, fix
# drive-bys inline, misread `FULL STATUS` as a mode, and split one piece of work
# into five tasks. This suite asserts all five are now in the *rendered* guide.
#
# Checks, fail-fast in order:
#   1. The guide's own unit tests + `guide-check` — fail fast.
#   2. Rule 1 — the `E-<id>: <verb-first summary>` commit form, inside the
#      "Committing your work" section.
#   3. Rule 2 — don't land/drop without asking, stated as a RULE inside
#      "Landing the work" (not merely described in the handoff-template
#      section), and positioned before the `worktree land` command block.
#      Plus E-1885, folded into this branch: that section's step 4 no longer
#      claims land removes the worktree (it doesn't; a reaper does, later).
#   4. Rule 3 — the four-case test for drive-by discoveries (E-1889 replaced
#      this rule's flat "file it; don't fix it" default), with `--cleans-up`.
#   5. Rule 4 — `FULL STATUS` licenses one response, not a sticky mode.
#   6. Rule 5 — lean toward FEWER tasks, with the cost rationale attached.
#   7. Doc/source sync: the wording each rule documents still matches the
#      template, the CLI flag, and the git history it is a claim about.
#   8. End-to-end: every rule RENDERS through `endless guide <section>`.
#   9. The generated command/topic cross-reference is in sync and carries the
#      new topics.
#
# Run from anywhere inside the worktree:  ./tests/tasks/e-1872-verify.sh
#
set -u

ROOT=$(git rev-parse --show-toplevel) || { echo "not in a git repo" >&2; exit 1; }
cd "${ROOT}" || exit 1

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; BOLD=""; RESET=""
fi

pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; }
fail() { printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"; [[ -n "${2:-}" ]] && printf '      %s\n' "$2"; exit 1; }
section() { printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"; }

ORCH="docs/guide/orchestration.md"
TASKS="docs/guide/tasks.md"
INDEX="docs/guide/index.md"
CLOSE_TMPL="internal/templatecmd/templates/handoff/_close.tmpl"

# Each rule is asserted against the *section it must live in*, not the file —
# a rule in the wrong section is invisible to a session reading the section
# that governs what it is about to do.
sect() { # <file> <start-heading-regex> <end-heading-regex> -> lines from start through end
    awk -v s="$2" -v e="$3" '
        !f && $0 ~ s { f = 1; print; next }
        f  && $0 ~ e { print; exit }
        f            { print }
    ' "$1"
}

assert_in() { # <label> <haystack-var-content> <fixed-string>
    if grep -qF -- "$3" <<<"$2"; then pass "$1"; else fail "$1" "missing: $3"; fi
}

assert_file() { # <label> <fixed-string> <file>
    if grep -qF -- "$2" "$3" 2>/dev/null; then pass "$1"; else fail "$1" "missing in $3: $2"; fi
}

# ── 1. unit tests, fail-fast ────────────────────────────────────────────────
section "1. Unit tests (fail-fast)"
if uv run pytest -q tests/test_guide_map.py tests/test_docs.py tests/test_agent_help.py; then
    pass "pytest (guide map, docs, agent help)"
else
    fail "pytest for the guide surfaces"
fi
if just guide-check >/dev/null 2>&1; then
    pass "just guide-check (map coverage, index freshness)"
else
    fail "just guide-check" "run 'just guide-index' then re-check"
fi

# ── 2. rule 1: the commit-message convention ────────────────────────────────
section "2. Commit-message convention"
COMMIT_SECTION=$(sect "${ORCH}" '^### Committing your work$' '^### Landing the work$')
[[ -n "${COMMIT_SECTION}" ]] || fail "orchestration.md has 'Committing your work'" "heading not found"

assert_in "states the subject-line form" "${COMMIT_SECTION}" \
    '`E-<id>: <verb-first summary>`'
assert_in "gives a worked example"       "${COMMIT_SECTION}" \
    'E-1871: route closed tasks'
assert_in "says why the id prefix matters" "${COMMIT_SECTION}" \
    "git log --grep"
assert_in "distinguishes endless's own auto-commits" "${COMMIT_SECTION}" \
    'Endless: '

# ── 3. rule 2: don't land/drop without asking ───────────────────────────────
# The gap this closes was *placement*: the only trace of the rule lived in the
# section that DESCRIBES the handoff template. A rule a session only meets
# while reading about templates is not a rule it will follow before landing.
section "3. Landing requires the user's OK"
LAND_SECTION=$(sect "${ORCH}" '^### Landing the work$' '^### Abandoning a worktree$')
[[ -n "${LAND_SECTION}" ]] || fail "orchestration.md has 'Landing the work'" "heading not found"

assert_in "stated as a rule in 'Landing the work'" "${LAND_SECTION}" \
    "without asking your user first"
assert_in "covers drop as well as land"            "${LAND_SECTION}" \
    'worktree drop'
assert_in "survives a context compaction"          "${LAND_SECTION}" \
    "standing rule"
assert_in "explains it is the user's verification" "${LAND_SECTION}" \
    "awaiting the user's verification"
assert_in "keeps --dry-run usable"                 "${LAND_SECTION}" \
    '`--dry-run` is the exception'

# E-1885, folded into this branch: the same section's step 4 claimed land
# "Removes the worktree." It never has — a reaper does, later — and index.md
# said the opposite. A session reading a false claim about what land does to
# its worktree is the same failure mode as the missing rules above.
assert_in "step 4 says the worktree is retained" "${LAND_SECTION}" \
    "worktree directory and its branch stay put"
assert_in "names the reaper and its TTL"         "${LAND_SECTION}" \
    'worktree_ttl'
if grep -qF "Removes the worktree." <<<"${LAND_SECTION}"; then
    fail "step 4 still claims land removes the worktree" \
         "land_worktree() leaves the dir and branch; the reaper removes them after worktree_ttl"
fi
pass "no stale 'Removes the worktree' claim"

# Position: the rule must precede the command it governs, or a session that
# stops reading at the first code block has already run it.
rule_ln=$(grep -n 'without asking your user first' "${ORCH}" | head -1 | cut -d: -f1)
cmd_ln=$(grep -n '^endless worktree land <id>$'    "${ORCH}" | head -1 | cut -d: -f1)
if [[ -n "${rule_ln}" && -n "${cmd_ln}" ]] && (( rule_ln < cmd_ln )); then
    pass "rule precedes the 'worktree land' command block (line ${rule_ln} < ${cmd_ln})"
else
    fail "rule must precede the 'worktree land' command block" \
        "rule=${rule_ln:-?} command=${cmd_ln:-?}"
fi

# ...and it must NOT be load-bearing only in the handoff-template description.
HANDOFF_SECTION=$(sect "${ORCH}" '^### The handoff is generated, not authored$' '^### `endless task spawn`$')
if grep -qF "without asking your user first" <<<"${HANDOFF_SECTION}"; then
    fail "the rule is stated in the handoff-template section" \
         "it belongs in 'Landing the work'; the template section only describes what the template carries"
fi
pass "not merely a handoff-template description"

# The 'Abandoning a worktree' section points back at the same rule.
DROP_SECTION=$(sect "${ORCH}" '^### Abandoning a worktree$' '^### Commit-to-main policy$')
assert_in "drop section carries the ask-first rule" "${DROP_SECTION}" "ask-first"

# The happy path in index.md is what a compacted session re-reads first.
assert_file "index.md's land line requires the user's go-ahead" \
    "and your user has told you to land it" "${INDEX}"

# ── 4. rule 3: what to do with a drive-by discovery ─────────────────────────
# E-1889 replaced the flat "file it; don't fix it" default with a four-case
# test (do-it-now / reopen-your-own-landed-work / file / share-a-root-cause).
# The diff-cost and blocked-anyway clauses survive as the bounds on case 1, so
# they are still asserted here.
section "4. Four-case test for drive-by discoveries"
FILING_SECTION=$(sect "${TASKS}" '^### Work you discover mid-task' '^### Lean toward FEWER tasks$')
[[ -n "${FILING_SECTION}" ]] || fail "tasks.md has a discovered-work section" "heading not found"

assert_in "denies that filing is the default" "${FILING_SECTION}" "not the default"
assert_in "case 1 — do it inside the work underway" "${FILING_SECTION}" "inside the work already underway"
assert_in "case 2 — reopen your own landed work" "${FILING_SECTION}" "--status revisit"
assert_in "gives the --cleans-up command"  "${FILING_SECTION}" "--cleans-up <current_id>"
assert_in "case 4 — file the cause, not each symptom" "${FILING_SECTION}" "file the cause, not each symptom"
assert_in "explains the diff cost"         "${FILING_SECTION}" "inflates the diff"
assert_in "names the blocked-anyway exception" "${FILING_SECTION}" "cannot complete the task without"

# ── 5. rule 4: the FULL STATUS escape hatch ─────────────────────────────────
section "5. FULL STATUS escape hatch"
FS_SECTION=$(sect "${TASKS}" '^### The `FULL STATUS` escape hatch$' '^## Removing and moving$')
[[ -n "${FS_SECTION}" ]] || fail "tasks.md has a 'FULL STATUS' section" "heading not found"

assert_in "licenses exactly one response" "${FS_SECTION}" "licenses **one** response"
assert_in "denies it is a mode switch"    "${FS_SECTION}" "not a mode switch"
assert_in "says the next response reverts" "${FS_SECTION}" "returns to the default"

# ── 6. rule 5: lean toward fewer tasks ──────────────────────────────────────
section "6. Lean toward FEWER tasks"
FEWER_SECTION=$(sect "${TASKS}" '^### Lean toward FEWER tasks$' '^### Research-type gate$')
[[ -n "${FEWER_SECTION}" ]] || fail "tasks.md has a 'Lean toward FEWER tasks' section" "heading not found"

# The plan is explicit that this one must carry its rationale: an agent that
# does not know *why* will re-split the next batch.
assert_in "states the preference"      "${FEWER_SECTION}" "prefer **one** task over several"
assert_in "attaches the cost rationale" "${FEWER_SECTION}" "attention"
assert_in "names the split criteria"   "${FEWER_SECTION}" "different land timing"
assert_in "gives the rule of thumb"    "${FEWER_SECTION}" "same reviewer = **one** task"

# ── 7. doc/source sync ──────────────────────────────────────────────────────
# Each rule is a claim about something outside the guide. If that something
# changes, the guide goes stale silently — so assert they still agree.
section "7. Doc matches the sources it documents"

assert_file "land still retains the worktree (source of the E-1885 fix)" \
    "Worktree dir and branch stay" src/endless/worktree_cmd.py
assert_file "the reaper's TTL default is still what the guide prints" \
    "default 14d" src/endless/cli.py

assert_file "FULL STATUS is still emitted by the handoff template" \
    "FULL STATUS" "${CLOSE_TMPL}"
assert_file "template still says one response, not a sticky mode" \
    "licenses one response, not a sticky mode" "${CLOSE_TMPL}"

if uv run endless task add --help 2>&1 | grep -q -- '--cleans-up'; then
    pass "'task add --cleans-up' is still a real flag"
else
    fail "'task add --cleans-up' no longer exists" "the filing rule documents a flag that is gone"
fi

# The commit form is a claim about git history. Every task commit that has
# actually landed on main should match it; if the convention documented here
# is not the convention practiced, one of the two is wrong.
bad=$(git log --no-merges --format='%s' -400 \
      | grep -E '^E-[0-9]+' \
      | grep -vE '^E-[0-9]+: [^A-Z ].*[^.]$' | head -5)
if [[ -z "${bad}" ]]; then
    pass "landed task commits match the documented form"
else
    fail "git history disagrees with the documented commit form" "offenders: ${bad//$'\n'/ | }"
fi

# ── 8. end-to-end through the CLI ───────────────────────────────────────────
# On-disk markdown is not the deliverable; what `endless guide` prints is.
section "8. Rendered by the CLI"
orch_out=$(uv run endless guide orchestration 2>/dev/null) || fail "endless guide orchestration failed"
tasks_out=$(uv run endless guide tasks 2>/dev/null)        || fail "endless guide tasks failed"
index_out=$(uv run endless guide 2>/dev/null)              || fail "endless guide failed"

assert_in "renders the commit-message form"   "${orch_out}"  '`E-<id>: <verb-first summary>`'
assert_in "renders the ask-before-landing rule" "${orch_out}" "without asking your user first"
assert_in "renders the four-case discovery test" "${tasks_out}" \
    "Filing is one of four answers, not the default."
assert_in "renders the FULL STATUS rule"      "${tasks_out}" "licenses **one** response"
assert_in "renders lean-toward-fewer-tasks"   "${tasks_out}" "prefer **one** task over several"
assert_in "renders the happy path's ask-first land" "${index_out}" \
    "and your user has told you to land it"

# ── 9. cross-reference registration ─────────────────────────────────────────
# A rule nobody can find is a rule nobody follows: each one gets a row in the
# generated command/topic → section table.
section "9. Guide cross-reference"
uv run python - <<'PY' || exit 1
import sys
from endless import guide_map as g

GREEN, RED, RESET = "\033[32m", "\033[31m", "\033[0m"
r = g.validate()
problems = []
if r.index_stale:
    problems.append("index block is stale — run 'just guide-index'")
if r.stale_topics:
    problems.append(f"stale topics: {r.stale_topics}")
if r.bad_section:
    problems.append(f"bad section refs: {r.bad_section}")
if problems:
    print(f"  {RED}✗{RESET} cross-reference out of sync")
    for p in problems:
        print(f"      {p}")
    sys.exit(1)

expected = {
    "commit message convention": "orchestration",
    "landing is the user's call (ask first)": "orchestration",
    "work you discover mid-task (do it, reopen, or file it)": "tasks",
    "lean toward fewer tasks": "tasks",
    "FULL STATUS": "tasks",
}
topics = {t.key: t for t in g.load_topics()}
index = g.INDEX_FILE.read_text()
for key, want_section in expected.items():
    t = topics.get(key)
    if t is None:
        print(f"  {RED}✗{RESET} topic not registered in help/_topics.md: {key}")
        sys.exit(1)
    if want_section not in t.sections:
        print(f"  {RED}✗{RESET} topic '{key}' maps to {t.sections}, expected '{want_section}'")
        sys.exit(1)
    if f"| {key} |" not in index:
        print(f"  {RED}✗{RESET} topic row missing from the generated index table: {key}")
        sys.exit(1)
    print(f"  {GREEN}✓{RESET} topic registered and rendered: {key}")
PY

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
