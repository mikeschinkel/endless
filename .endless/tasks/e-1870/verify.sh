#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1870 and records what was true when E-1870
# landed. Edit it only if you ARE E-1870. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1870 verification — "Add the missing commit-your-work step to the guide's
# worktree walkthrough".
#
# The bug: docs/guide/{index,orchestration}.md walked a session from `task
# claim` straight to `worktree land` with no step that says *commit your work*.
# `land` auto-commits only endless-managed files, so a session that followed the
# guide literally reached land with its changes uncommitted — land refuses
# (E-1416) and the work is stranded in the worktree. That is exactly what
# happened on E-1865.
#
# Checks, fail-fast in order:
#   1. The task's own unit tests (guide-map + docs) — fail fast.
#   2. orchestration.md carries a "Committing your work" section, positioned
#      BEFORE "Landing the work" (a commit step after the land step is useless).
#   3. That section says the load-bearing things, and does NOT teach the
#      ledger-poisoning bare `git add -A`.
#   4. The section's quoted refusal strings still match the source that emits
#      them, and its auto-commit table still matches AUTO_COMMIT_GLOBS — the
#      doc must not drift away from the code it describes.
#   5. index.md's happy path has the commit step, in the right position, with
#      contiguous numbering.
#   6. The generated command/topic cross-reference is in sync (no stale topics,
#      index block not stale).
#   7. End-to-end: `endless guide` / `endless guide orchestration` actually
#      RENDER the new content through the CLI, not just on disk.
#   8. Functional: the documented `git add` pathspec really does stage source
#      while excluding the ledger and verbs, proven in a throwaway repo.
#
# Run from anywhere inside the worktree:  endless task verify E-1870
#
# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || { echo "not in a git repo" >&2; exit 1; }
cd "${ROOT}" || exit 1

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; BOLD=""; RESET=""
fi

TMPDIR_E1870=""
cleanup() { [[ -n "${TMPDIR_E1870}" ]] && rm -rf "${TMPDIR_E1870}"; }
trap cleanup EXIT

pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; }
fail() { printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"; [[ -n "${2:-}" ]] && printf '      %s\n' "$2"; exit 1; }
section() { printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"; }

ORCH="docs/guide/orchestration.md"
INDEX="docs/guide/index.md"

# The section under test, extracted once and reused by the content assertions.
COMMIT_SECTION=$(awk '/^### Committing your work$/,/^### Landing the work$/' "${ORCH}")

assert_in_section() { # <label> <fixed-string>
    if grep -qF -- "$2" <<<"${COMMIT_SECTION}"; then pass "$1"; else fail "$1" "missing in the Committing-your-work section: $2"; fi
}

assert_file() { # <label> <fixed-string> <file>
    if grep -qF -- "$2" "$3" 2>/dev/null; then pass "$1"; else fail "$1" "missing in $3: $2"; fi
}

# ── 1. the task's own unit tests, fail-fast ──────────────────────────────────
section "1. Unit tests (fail-fast)"
if uv run pytest -q tests/test_guide_map.py tests/test_docs.py tests/test_agent_help.py; then
    pass "pytest (guide map, docs, agent help)"
else
    fail "pytest for the guide surfaces"
fi

# ── 2. the section exists, and precedes Landing ─────────────────────────────
# Order is the whole point: a commit instruction printed after "Landing the
# work" would be read too late by a session walking the guide top-to-bottom.
section "2. Section present and correctly positioned"
[[ -n "${COMMIT_SECTION}" ]] || fail "orchestration.md has a 'Committing your work' section" "heading not found"
pass "orchestration.md has '### Committing your work'"

commit_ln=$(grep -n '^### Committing your work$' "${ORCH}" | head -1 | cut -d: -f1)
land_ln=$(grep -n '^### Landing the work$'      "${ORCH}" | head -1 | cut -d: -f1)
[[ -n "${land_ln}" ]] || fail "orchestration.md still has '### Landing the work'" "heading not found"
if (( commit_ln < land_ln )); then
    pass "commit step precedes 'Landing the work' (line ${commit_ln} < ${land_ln})"
else
    fail "commit step must precede 'Landing the work'" "commit at ${commit_ln}, land at ${land_ln}"
fi

# ── 3. the section says the load-bearing things ─────────────────────────────
section "3. Section content"
assert_in_section "states endless never commits your work" \
    "Endless never commits your work for you"
assert_in_section "gives the commit command"       'git commit -m "E-<id>: what changed"'
assert_in_section "excludes the DB ledger"         "':!.endless/db-ledger'"
assert_in_section "excludes verbs.jsonl"           "':!.endless/verbs.jsonl'"
assert_in_section "names the land refusal"         "has uncommitted user changes"
assert_in_section "orders commit before unverified" 'before** you flip the task to `unverified`'

# The anti-pattern this task must not introduce: a bare `git add -A` with no
# pathspec exclusions sweeps ledger entries onto the task branch, which makes
# land refuse for a *different* reason (branch-authored ledger segment).
if grep -qE 'git add -A[[:space:]]*($|&&)' <<<"${COMMIT_SECTION}"; then
    fail "section teaches a bare 'git add -A'" "must carry the ':!.endless/...' exclusions"
fi
pass "no bare 'git add -A' taught"

# ── 4. doc/code sync ────────────────────────────────────────────────────────
# Every quoted error string and every path in the auto-commit table is a claim
# about the code. If the code's wording or globs change, this doc goes stale
# silently — so assert the two still agree.
section "4. Doc matches the code it quotes"
assert_file "refusal text still emitted by worktree_cmd" \
    "has uncommitted user changes" src/endless/worktree_cmd.py
assert_in_section "quotes the ledger refusal" "modifying the database ledger"
assert_file "ledger refusal still emitted by worktree_cmd" \
    "modifying the database ledger" src/endless/worktree_cmd.py

globs=$(uv run python -c 'from endless.worktree_cmd import AUTO_COMMIT_GLOBS; print("\n".join(AUTO_COMMIT_GLOBS))') \
    || fail "could not import AUTO_COMMIT_GLOBS"
while IFS= read -r g; do
    [[ -z "${g}" ]] && continue
    if grep -qF -- "${g}" <<<"${COMMIT_SECTION}"; then
        pass "auto-commit glob documented: ${g}"
    else
        fail "auto-commit glob not documented: ${g}" "AUTO_COMMIT_GLOBS and the guide table have drifted"
    fi
done <<<"${globs}"

# ── 5. the happy path carries the step, in position, numbered ───────────────
section "5. index.md happy path"
assert_file "happy path has the commit step" \
    "**Commit your work on the task branch**" "${INDEX}"

# Extract the ordered list and confirm the commit step lands between "do the
# work" and the status flip, and that renumbering left 1..8 contiguous.
happy=$(awk '/^## The happy path$/,/^## Task statuses$/' "${INDEX}")
nums=$(grep -oE '^[0-9]+\.' <<<"${happy}" | tr -d '.' | tr '\n' ' ')
[[ "${nums}" == "1 2 3 4 5 6 7 8 " ]] \
    || fail "happy path numbering is not contiguous 1..8" "got: ${nums}"
pass "happy path renumbered 1..8 contiguously"

work_ln=$(grep -n '^4\. Do the work in the worktree\.' <<<"${happy}" | head -1 | cut -d: -f1)
commit_step=$(grep -n '^5\. \*\*Commit your work on the task branch\*\*' <<<"${happy}" | head -1 | cut -d: -f1)
unver_ln=$(grep -n 'status unverified' <<<"${happy}" | head -1 | cut -d: -f1)
if [[ -n "${work_ln}" && -n "${commit_step}" && -n "${unver_ln}" ]] \
   && (( work_ln < commit_step && commit_step < unver_ln )); then
    pass "commit step sits between 'do the work' and the unverified flip"
else
    fail "commit step is mispositioned in the happy path" \
        "work=${work_ln:-?} commit=${commit_step:-?} unverified=${unver_ln:-?}"
fi

assert_file "land's parenthetical no longer implies it commits your work" \
    "not yours; see step 5" "${INDEX}"

# ── 6. generated cross-reference is in sync ─────────────────────────────────
section "6. Guide cross-reference"
uv run python - <<'PY' || exit 1
import sys
from endless import guide_map as g

r = g.validate()
problems = []
if r.index_stale:
    problems.append("index block is stale — run /regenerate-guide")
if r.stale_topics:
    problems.append(f"stale topics: {r.stale_topics}")
if r.bad_section:
    problems.append(f"bad section refs: {r.bad_section}")
if problems:
    print("  \033[31m✗\033[0m cross-reference out of sync")
    for p in problems:
        print(f"      {p}")
    sys.exit(1)

topics = {t.key for t in g.load_topics()}
if "committing your work" not in topics:
    print("  \033[31m✗\033[0m topic 'committing your work' not registered in help/_topics.md")
    sys.exit(1)

index = g.INDEX_FILE.read_text()
if "| committing your work |" not in index:
    print("  \033[31m✗\033[0m topic row missing from the generated index table")
    sys.exit(1)

print("  \033[32m✓\033[0m index block in sync, topic registered and rendered")
PY

# ── 7. end-to-end through the CLI ───────────────────────────────────────────
# On-disk markdown is not the deliverable; what `endless guide` prints is.
section "7. Rendered by the CLI"
if uv run endless guide orchestration 2>/dev/null | grep -qF "Committing your work"; then
    pass "endless guide orchestration renders the section"
else
    fail "endless guide orchestration does not render the section"
fi
if uv run endless guide 2>/dev/null | grep -qF "Commit your work on the task branch"; then
    pass "endless guide renders the happy-path step"
else
    fail "endless guide does not render the happy-path step"
fi

# ── 8. the documented git command actually does what the doc claims ─────────
# The exclusions are the part most likely to be wrong (pathspec magic syntax is
# easy to get subtly wrong). Prove it in a throwaway repo rather than asserting
# it in prose.
section "8. The documented pathspec works"
TMPDIR_E1870=$(mktemp -d) || fail "mktemp failed"
(
    set -e
    cd "${TMPDIR_E1870}"
    git init -q .
    git config user.email e1870@example.invalid
    git config user.name  "E-1870 verify"
    mkdir -p .endless/db-ledger
    printf '{}\n' > .endless/db-ledger/entry.jsonl
    printf '{}\n' > .endless/verbs.jsonl
    printf 'source\n' > app.py
    git add -A && git commit -qm base
    printf '{"new":1}\n' >> .endless/db-ledger/entry.jsonl
    printf '{"new":1}\n' >> .endless/verbs.jsonl
    printf 'changed\n' >> app.py
    # the exact command the guide prints
    git add -A -- ':!.endless/db-ledger' ':!.endless/verbs.jsonl'
    staged=$(git diff --cached --name-only | sort | tr '\n' ' ')
    [[ "${staged}" == "app.py " ]] || { echo "staged: ${staged}"; exit 1; }
) || fail "documented 'git add' staged the wrong set of files"
pass "documented 'git add' stages source and excludes ledger + verbs"

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
