#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2144 and records what was true when E-2144
# landed. Edit it only if you ARE E-2144. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2144: `obsolete` is defined as what the word means, not as "it never
# needed doing".
#
# Cambridge: "no longer in use, out of date, or replaced by something newer
# and better." The old gloss — "Made irrelevant by other changes — it never
# needed doing", carried on all six inbound transition labels — excluded the
# commonest real case, work that WAS worth doing when it was filed and has
# since been overtaken, and pushed it toward `declined`, which means an active
# decision not to do something and is a different fact. On 2026-09-14 that
# wording led an agent to question a correct retirement of E-1421, E-1441,
# E-1442 and E-746 after session status and session monitor superseded them.
#
# The Go transition table is the source of truth; docs/status-lifecycle.mmd,
# README.md and docs/guide/index.md's diagram are rendered from it. So the
# change is one edit to the table, one to the guide's status-table gloss, and
# a regenerate — a WORDING change, with no edge added, removed or re-pointed.
#
# What is verified here:
#   A. Fail-fast: this task's own tests — internal/taskstatus (the table and
#      its renderer), the Python sync test that pins the three artifacts
#      together, and `just lifecycle-check`.
#   B. The labels say the new thing, through the real binary, built from this
#      tree.
#   C. Nothing about the LIFECYCLE moved: the same six inbound edges from the
#      same six pre-ship statuses, no inbound edge from a shipped status
#      (E-1956's rule), and the reversal still there.
#   D. The three generated artifacts carry the new label byte-for-byte.
#   E. The guide's status table teaches the corrected definition: it names the
#      superseded-but-was-worth-doing case, names `declined` as the different
#      fact, and keeps E-1956's refusal clause.
#   F. The old phrase is gone from everywhere the tool speaks it.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

OLD='never needed doing'
NEW='no longer needs doing'
LABEL="retires — it ${NEW}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# internal/taskstatus holds the edited table and the renderer that turns it
# into the diagram. tests/test_status_lifecycle_sync.py is the assertion that
# the artifact and its two embedded copies stay in step with the table — the
# thing a wording change most easily breaks. `just lifecycle-check` is the
# same check as the pre-land gate runs it.

if out=$(go test ./internal/taskstatus 2>&1); then
    report_pass "go test: internal/taskstatus"
else
    report_fail "go test: internal/taskstatus" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest tests/test_status_lifecycle_sync.py -q 2>&1); then
    report_pass "pytest: tests/test_status_lifecycle_sync.py"
else
    report_fail "pytest: tests/test_status_lifecycle_sync.py" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(just lifecycle-check 2>&1); then
    report_pass "just lifecycle-check: artifacts match the Go table"
else
    report_fail "just lifecycle-check" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. The labels say what the word means"
# ---------------------------------------------------------------------------
# Built from this tree rather than taken from bin/, so the answers come from
# what is committed here and not from whatever was last installed.

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

EDGES="$("${BIN}" task-status transitions)" \
    || setup_error "task-status transitions failed"

inbound() { printf '%s\n' "${EDGES}" | awk -F'\t' '$2=="obsolete"'; }

assert_eq "six inbound edges into obsolete, as before" \
    "6" "$(inbound | grep -c .)"

assert_eq "every one of them carries the corrected label" \
    "6" "$(inbound | awk -F'\t' -v l="${LABEL}" '$5==l' | grep -c .)"

assert_not_contains "no edge in the whole table still says \"${OLD}\"" \
    "${OLD}" "${EDGES}"

# ---------------------------------------------------------------------------
section "C. The lifecycle itself did not move"
# ---------------------------------------------------------------------------
# A wording fix that quietly loosened the rule would be a different change. The
# inbound set is still exactly the six pre-ship statuses, which is the same
# statement as E-1956's: shipped work is not obsoletable, because the fact to
# record there is a `replaced_by` relation.

assert_eq "inbound obsolete edges come from the six pre-ship statuses" \
    "ready revisit submitted underway unplanned untriaged" \
    "$(inbound | awk -F'\t' '{print $1}' | sort | tr '\n' ' ' | sed 's/ $//')"

for s in $("${BIN}" task-status get shipped); do
    assert_eq "no inbound obsolete edge from shipped status '${s}' (E-1956)" \
        "0" "$(inbound | awk -F'\t' -v s="${s}" '$1==s' | grep -c .)"
done

assert_eq "the reversal edge obsolete → untriaged still stands" \
    "1" "$(printf '%s\n' "${EDGES}" | awk -F'\t' '$1=="obsolete" && $2=="untriaged" && $5=="reconsiders"' | grep -c .)"

# ---------------------------------------------------------------------------
section "D. The generated artifacts carry it"
# ---------------------------------------------------------------------------
# The renderer's output is the body of docs/status-lifecycle.mmd, and README.md
# and docs/guide/index.md embed that file byte-identically. All three are
# checked against the live renderer, not against each other, so a hand-edit to
# any one of them fails here.

RENDERED="$("${BIN}" task-status lifecycle)" \
    || setup_error "task-status lifecycle failed"

assert_contains "the renderer emits the corrected label" \
    "untriaged --> obsolete: user ${LABEL}" "${RENDERED}"

for f in docs/status-lifecycle.mmd README.md docs/guide/index.md; do
    [[ -f "${f}" ]] || setup_error "missing generated artifact ${f}"
    body="$(cat "${f}")"
    assert_eq "${f} carries all six corrected edges" \
        "6" "$(grep -c -- "--> obsolete: user ${LABEL}" "${f}")"
    assert_not_contains "${f} no longer says \"${OLD}\"" "${OLD}" "${body}"
done

# ---------------------------------------------------------------------------
section "E. The guide's status table teaches the corrected definition"
# ---------------------------------------------------------------------------
# This row is the prose an agent reads before deciding between `obsolete` and
# `declined` — the decision the old gloss got wrong. It has to do three things:
# define the word, admit the superseded case by name, and point at `declined`
# as the OTHER fact rather than the fallback.

ROW="$(grep '^| `obsolete`' docs/guide/index.md)" \
    || setup_error "no obsolete row in docs/guide/index.md's status table"

assert_contains "the row defines obsolete as no-longer-needed" \
    "No longer needs doing" "${ROW}"
assert_contains "the row names supersession" \
    "superseded by something newer" "${ROW}"
assert_contains "the row admits work that WAS worth doing" \
    "worth doing when it was filed and has since been overtaken" "${ROW}"
assert_contains "the row names \`declined\` as the different fact" \
    "is \`declined\`, a different fact" "${ROW}"
assert_not_contains "the row no longer says \"${OLD}\"" "${OLD}" "${ROW}"

# E-1956's refusal is a separate rule and survives this edit intact.
assert_contains "the row keeps the shipped-work refusal" \
    "Refused on work that already shipped" "${ROW}"
assert_contains "the row keeps the replaced_by remedy" \
    'task replace <old> --by <new>' "${ROW}"

# The duplicates section quotes the same gloss; it moved with it.
assert_contains "docs/guide/tasks.md quotes the corrected gloss" \
    "\`obsolete\` alone says \"${NEW}\"" "$(cat docs/guide/tasks.md)"

# ---------------------------------------------------------------------------
section "F. The old phrase is gone from everything the tool speaks"
# ---------------------------------------------------------------------------
# A sweep rather than a list of files, because the point of the change is that
# nothing SAYS it any more. Four exclusions, each for a reason that is not "it
# was inconvenient":
#   - .endless/db-ledger and .endless/LESSONS.md are append-only history. What
#     was written then was true of then.
#   - .endless/tasks/* are landed suites, which record what was true when THEIR
#     task landed and must not be retrofitted (.endless/tasks/CLAUDE.md).
#   - internal/taskstatus/transitions.go quotes the old phrase once, inside the
#     comment that explains why it went. A change is allowed to name the thing
#     it removed.

STRAY="$(grep -rIl --exclude-dir=.git --exclude-dir=.venv --exclude-dir=node_modules \
            -- "${OLD}" . 2>/dev/null \
         | grep -v '^\./\.endless/db-ledger/' \
         | grep -v '^\./\.endless/LESSONS\.md$' \
         | grep -v '^\./\.endless/tasks/' \
         | grep -v '^\./internal/taskstatus/transitions\.go$' \
         | sort)"

assert_eq "nothing outside history and the explanatory comment says \"${OLD}\"" \
    "" "${STRAY}"

assert_eq "transitions.go quotes it exactly once, in the comment that retires it" \
    "1" "$(grep -c -- "${OLD}" internal/taskstatus/transitions.go)"
assert_contains "and that one mention is a comment, not a label" \
    "// A label saying \"it ${OLD}\" excluded that case" \
    "$(cat internal/taskstatus/transitions.go)"

summary
