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
#   G. The paraphrases are gone too. The first pass fixed the labels and the
#      guide and MISSED two places that said the same wrong thing in different
#      words — the authority banner an agent gets injected on every `task show`
#      of an obsolete task, and the transition group name it took its framing
#      from. Both asserted "the work never shipped", which is not a fact the
#      status carries: E-1421 is an epic that landed twice and is obsolete, so
#      the banner called its own Landed: line imaginary.
#   H. The axis is the RIGHT one. A definition nothing enforces is a comment,
#      so the last pass makes the lifecycle agree with the word: `obsolete`
#      turns on whether anything REPLACED the work, never on whether it
#      shipped. That is the line Endless already draws for decisions, and the
#      two record kinds now match.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

OLD='never needed doing'
NEW='no longer needs doing'
LABEL="retires — it ${NEW}"

# sweep <phrase> — tracked files in the PRODUCT tree that still contain it.
#
# `git grep` rather than `grep -r` for two reasons, both learned the hard way.
# It searches tracked files only, so a scratch file cannot fail the suite; and
# it prints repo-relative paths with no leading "./", which `grep -r` prefixes
# or not depending on which grep is installed. The first version of this sweep
# anchored its exclusions on "^\./" and so excluded NOTHING under a grep that
# omits the prefix (ugrep, for one) — a filter that silently stops filtering is
# worse than no filter, because the assertion still looks like it is checking.
#
# Everything under .endless/ is excluded because all of it is Endless's own
# RECORDS: the append-only db-ledger, LESSONS.md, the per-task plan mirrors, and
# landed verify suites — including this one, which necessarily quotes both
# phrases to assert they are gone. A record of what was said then is not the
# tool saying it now, and .endless/tasks/CLAUDE.md forbids retrofitting landed
# suites in any case.
sweep() {
    git grep -lIF -e "$1" -- ':!.endless/' 2>/dev/null | sort
}

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

assert_eq "the six pre-ship edges carry the corrected label" \
    "6" "$(inbound | awk -F'\t' -v l="${LABEL}" '$5==l' | grep -c .)"

assert_not_contains "no edge in the whole table still says \"${OLD}\"" \
    "${OLD}" "${EDGES}"

# ---------------------------------------------------------------------------
section "C. What the rewording did NOT move"
# ---------------------------------------------------------------------------
# Sections A-G are a wording change and must not have altered behaviour; H is
# the one deliberate behaviour change and is asserted there. What belongs here
# is the invariant that survived both: which statuses reach `obsolete` at all,
# and that the decision stays reversible.
#
# Every status except the two abandonments themselves reaches it. That is the
# shape of the corrected axis — a task is obsoletable whenever nothing replaced
# it, at any point in its life — and it is a stronger claim than listing eleven
# froms, because it says WHY there are eleven.

OBSOLETABLE="$("${BIN}" task-status get all \
               | grep -v -e '^declined$' -e '^obsolete$' | sort | tr '\n' ' ' | sed 's/ $//')"

assert_eq "every status but the two abandonments can reach obsolete" \
    "${OBSOLETABLE}" \
    "$(inbound | awk -F'\t' '{print $1}' | sort | tr '\n' ' ' | sed 's/ $//')"

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

assert_not_contains "the row no longer teaches the removed refusal" \
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

# Guard against a vacuous pass FIRST. Every sweep assertion below is satisfied
# by an empty result, so a sweep that silently found nothing — a broken pathspec,
# a git grep that errored into 2>/dev/null — would report success while checking
# nothing. This is the same failure the "^\./" anchor caused, so it is asserted
# rather than trusted: the sweep must be able to find the phrase that IS there.
assert_contains "the sweep is not vacuous — it finds what it should find" \
    "internal/taskstatus/transitions.go" "$(sweep "${NEW}")"

assert_eq "nothing in the product tree still says \"${OLD}\"" \
    "" "$(sweep "${OLD}" | grep -v '^internal/taskstatus/transitions\.go$')"

assert_eq "transitions.go quotes it exactly once, in the comment that retires it" \
    "1" "$(grep -c -- "${OLD}" internal/taskstatus/transitions.go)"
assert_contains "and that one mention is a comment, not a label" \
    "// \"it ${OLD}\", which excluded that case" \
    "$(cat internal/taskstatus/transitions.go)"

# ---------------------------------------------------------------------------
section "G. The paraphrases are gone too"
# ---------------------------------------------------------------------------
# "Retired before the work ever shipped" is the same claim as "it never needed
# doing" — it just does not reuse the words, which is exactly why the first pass
# grepped past it. It is also the WORST copy, because it is not documentation:
# it is injected into an agent's context as an authority banner every time an
# obsolete task is shown.
#
# And it is not merely a stale gloss, it is false. `for_task` sees a status and
# two relations. It cannot see landings. E-1421 is an epic that landed twice
# (18fe0f0), reached obsolete without passing the Python gate because epic status
# is derived in Go, and carried a banner telling every agent its work never
# shipped — directly above its own `Landed:` line.

OLD_PARA='before the work ever shipped'

BANNER="$(uv run python -c '
import endless.authority as a
print(a.for_task("obsolete", None, None).summary("E-1"))
print(a.for_task("declined", None, None).summary("E-1"))
' 2>&1)" || setup_error "cannot evaluate endless.authority.for_task"

assert_not_contains "the obsolete banner no longer claims the work never shipped" \
    "${OLD_PARA}" "${BANNER}"
assert_contains "the obsolete banner states what the status actually licenses" \
    "it is obsolete: retired as no longer needed" "${BANNER}"
assert_contains "the declined banner is untouched" \
    "it is declined: an active decision not to do the work" "${BANNER}"

# The group name is where the banner took its framing from, so it stops being
# readable as a definition of the word.
assert_not_contains "the transition group name is no longer a definition" \
    "${OLD_PARA}" "$("${BIN}" task-status transitions; "${BIN}" task-status lifecycle)"

for f in docs/status-lifecycle.mmd README.md docs/guide/index.md; do
    assert_not_contains "${f} carries the regenerated group name" \
        "${OLD_PARA}" "$(cat "${f}")"
done

# Same sweep as F, for the paraphrase. Only the two comments that explain the
# removal may still say it.
assert_eq "nothing in the product tree still says \"${OLD_PARA}\"" \
    "" "$(sweep "${OLD_PARA}" \
          | grep -v -e '^src/endless/authority\.py$' \
                    -e '^internal/taskstatus/transitions\.go$')"

# ---------------------------------------------------------------------------
section "H. The axis is whether anything replaced it, not whether it shipped"
# ---------------------------------------------------------------------------
# E-1956 refused `obsolete` from every shipped status, reasoning that the word
# "reads as never happened". That reading was an artifact of the gloss sections
# A-G removed, so the gate went with it. Three things had to become true
# together, or the definition would just be a comment nothing enforces: the Go
# table has to ALLOW the edges, the Python path must not refuse them, and the
# guide has to teach the axis.

for s_ in $("${BIN}" task-status get shipped); do
    assert_eq "the table allows ${s_} → obsolete" \
        "1" "$(inbound | awk -F'\t' -v s="${s_}" '$1==s' | grep -c .)"
done

assert_eq "shipped edges say what is true of shipped work" \
    "5" "$(inbound | awk -F'\t' '$5=="retires — the shipped work is no longer in use"' | grep -c .)"

assert_eq "eleven inbound edges now: six pre-ship, five shipped" \
    "11" "$(inbound | grep -c .)"

# The Go legality check is what refused this at the event seam, so it is asked
# directly rather than inferred from the table it reads.
assert_contains "obsolete is reachable from a shipped status" \
    "obsolete" "$("${BIN}" task-status transitions | awk -F'\t' '$1=="assumed"{print $2}')"

# The Python gate is gone, not merely unreferenced.
assert_eq "_refuse_obsolete_on_shipped_work no longer exists" \
    "" "$(sweep '_refuse_obsolete_on_shipped_work')"

# `declined` is untouched: it still means an active decision not to DO the work,
# and it still reaches back from every shipped status. This change moved one
# status, not both.
assert_eq "declined still has its five shipped edges" \
    "5" "$(printf '%s\n' "${EDGES}" | awk -F'\t' '$2=="declined" && $5=="declines — the shipped work is not being kept"' | grep -c .)"

# The guide teaches the axis, and no longer teaches the refusal.
TASKS_MD="$(cat docs/guide/tasks.md)"
assert_contains "the guide says shipped work can be obsolete" \
    "Shipped work CAN be \`obsolete\`" "${TASKS_MD}"
assert_contains "the guide names the replacement axis" \
    "no longer needed, and nothing replaced it" "$(cat docs/guide/index.md)"
assert_not_contains "the guide no longer teaches the removed refusal" \
    "obsolete\` is refused on a task that already shipped" "${TASKS_MD}"

# Endless drew this line for decisions first; the two record kinds must agree,
# because an agent that learns the rule from one applies it to the other.
assert_contains "decisions still draw the same line" \
    "it stopped applying and nothing replaced it" "$(cat docs/guide/decisions.md)"

summary
