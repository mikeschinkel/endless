#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2153 and records what was true when E-2153
# landed. Edit it only if you ARE E-2153. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2153: the coined term "attention board" — and its short form "board" —
# are replaced by the commands they actually name.
#
# WHAT LANDED
#   A session working E-1976 coined "attention board" for the pair `endless
#   project status` (one frame) and `endless project monitor` (the live loop),
#   then used it as though it were the product's name. It reached ~20 sites
#   across three landed tasks, including the user-facing `endless-go --help`
#   line "render the project attention board".
#
#   It names nothing a reader can point at — not a command, not a flag, not a
#   package, not a file, not any help text — and it collapses a snapshot and a
#   loop into one word, so every sentence carrying it is ambiguous about which.
#
#   THE SHORT FORM COUNTS, and it is the larger half. A first pass removed the
#   two-word phrase and left bare "board" — 223 prose uses, thirteen Go
#   identifiers and the filename board.go — on the reasoning that it was an
#   ordinary noun that happened to sit beside a coinage. Mike: "'board' is no
#   better than 'attention board'. 'monitor' and 'project monitor' are the
#   proper terms." He is right. "Board" is the coinage minus its adjective, and
#   every cost above applies to it unchanged; dropping an adjective does not
#   turn a coined term into English. The sweep is over the TERM and all of its
#   short forms, not over one string a task row happened to spell out.
#
#   Each use is now whichever command the sentence means — `project status` for
#   the snapshot and its ranking rules, `project monitor` (or "the monitor")
#   for the live loop, pane and tmux session — or a concrete noun the codebase
#   already uses for the thing itself: a "frame" (liveview.Frame), a "view", a
#   "row". No substitute coinage was introduced.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  BOTH FORMS ARE GONE FROM THE PRODUCT TREE. No tracked file outside
#       `.endless/` still carries "attention board" or a bare "board" — in
#       prose, in an identifier, in output, or as a filename; in any casing,
#       separator or compound; including across a comment line wrap, which a
#       flat grep walks straight past. "dashboard", "keyboard" and "whiteboard"
#       are dictionary words and are deliberately not matched — which is
#       exactly what "board" standing alone is not.
#   C2  GONE FROM OUTPUT. `endless-go --help` no longer speaks the coinage, and
#       the two lines that did now name the commands instead.
#   C3  REPLACED, NOT DELETED. Every file that carried the coinage still names
#       `project status` or `project monitor`; the identifiers that carried the
#       short form were renamed rather than deleted, and the package still
#       compiles and passes its own tests.
#   C4  THE SCOPE BOUNDARY HELD. The landed verify suites of E-1976, E-2091 and
#       E-2105 and the E-1976/E-2105 plan and analysis mirrors still carry the
#       phrase. They record what was true when their tasks landed;
#       .endless/tasks/CLAUDE.md forbids retrofitting them to a later change,
#       and a sweep that had run over `.endless/` would have emptied them.
#
# ISOLATION
#   Nothing here touches a database. It is package tests, a binary built from
#   this tree into a scratch directory, and greps over tracked files.
#
# Layers:
#   A. FAIL-FAST — comments, docstrings, help text, thirteen Go identifiers and
#      two renamed files across ten Go packages, three Python modules and two
#      guide pages. What that can break is a string a test reads, a symbol that
#      no longer resolves, or a doc reference a gate checks. Those run first;
#      nothing below is meaningful if they fail.
#   B. The patterns prove themselves, then the sweeps run — C1.
#   C. The user-facing surface — C2.
#   D. The subject survived, and the renames landed — C3.
#   E. The records the sweep was told to leave alone — C4.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# The coinage, as a pattern rather than a literal. Casing varies ("Attention
# board" opens a sentence) and so does the separator, and a sweep that only
# matched the one spelling the author happened to use would pass while the
# other two survived.
COINED='[Aa]ttention[[:space:]_-]+[Bb]oard|ATTENTION[[:space:]_-]+BOARD'

# The short form, in every shape it took: prose ("the board", "both boards"), a
# camelCase identifier (boardRows, jsonBoard, seedBoardTask), a SHOUTED one, a
# filename (board.go), a config value (`{{project}}-board`).
#
# Hand-built boundaries, not \b. `\b` is a GNU extension that POSIX ERE does not
# define, so `git grep -E` may or may not honour it — and a boundary that
# silently stops bounding either matches "dashboard" and fails on a clean tree,
# or matches nothing and passes on a dirty one. Both failures look like a
# working check. The bracket forms mean the same thing under every grep.
#
#   (^|[^[:alpha:]])[Bb]oard   "board" not glued to the end of another word,
#                              which is what spares dashboard / keyboard /
#                              whiteboard — real words, which "board" is not.
#   [a-z0-9_]Board             the camelCase join: jsonBoard, TestBoardBadge.
#   BOARD                      the shouted form, used in one comment.
#
# Section B proves this pattern's boundaries on literal strings before trusting
# it to report an absence.
#
# Both patterns carry their own casing and the sweeps below run CASE-SENSITIVE,
# which is not a detail. Under `grep -i`, `[a-z0-9_]Board` folds to
# `[a-z0-9_]board` and matches the "hboard" inside "dashboard" — so the
# case-insensitive flag silently destroys the very boundary the class exists to
# draw, and the sweep fails on a clean tree while looking like it caught
# something real. It did, here, on the first run.
SHORT='(^|[^[:alpha:]])[Bb]oard|[a-z0-9_]Board|BOARD'

# The files that carried the two-word coinage, and had to be rewritten one
# sentence at a time because the right replacement differs per sentence: some
# meant the snapshot, some the loop, some the pair. Typed out rather than
# derived from git, so the list is a claim this suite makes and not a shadow of
# whatever HEAD happens to be rebased onto.
#
# internal/sessionstate/sessionstate.go is on it because the JOINED sweep in
# section B found it and the flat one did not: its instance was split across a
# comment line wrap ("…already the attention\n// board's glyph…"), invisible to
# the grep that enumerated every other site. That is the whole reason the joined
# sweep exists, and it is why this list is checked rather than trusted.
SITES=(
    cmd/endless-go/main.go
    docs/guide/appendix-a.md
    docs/guide/reference.md
    internal/config/README.md
    internal/config/config.go
    internal/faultbadge/faultbadge.go
    internal/hookcmd/notification.go
    internal/hookcmd/notification_test.go
    internal/liveview/liveview.go
    internal/monitor/project_status.go
    internal/monitor/project_status_test.go
    internal/monitor/session.go
    internal/projectstatuscmd/render.go
    internal/sessionstate/sessionstate.go
    internal/sessionstate/transitions.go
    internal/sessionstate/transitions_test.go
    internal/taskstatus/taskstatus.go
    src/endless/cli.py
    src/endless/project_status_cmd.py
    src/endless/rowcap.py
    tests/test_cli.py
    tests/test_project_status.py
    tests/test_setup_notification_hook.py
)

# The identifiers the short form reached, each renamed to say what it is. Listed
# as old→new so the check is "the old name is gone AND the new one is here" —
# half of that alone would pass for a deletion.
RENAMES=(
    "splitBoardArgs:splitMonitorArgs"
    "boardPctOfWindow:monitorPctOfWindow"
    "jsonBoard:jsonDoc"
    "decodeBoard:decodeDoc"
    "boardRows:nonEmptyRows"
    "boardTaskStatuses:projectStatusTaskStatuses"
    "seedBoardTask:seedProjectStatusTask"
    "seedBoardSession:seedProjectStatusSession"
    "bindBoardPane:bindSessionPane"
    "TestEmptyBoard:TestEmptyFrame"
    "TestBoardBadge_MachineWideScopeSeesEverything:TestFrameBadge_MachineWideScopeSeesEverything"
    "TestJSONEmptyBoardStillParses:TestJSONEmptyDocStillParses"
    "TestSplitBoardArgsInsertsAboveTheShell:TestSplitMonitorArgsInsertsAboveTheShell"
)

# The records that must NOT have been swept: landed verify suites and the plan
# and analysis mirrors of the tasks that shipped the coinage.
RECORDS=(
    .endless/tasks/e-1976/verify.sh
    .endless/tasks/e-2091/verify.sh
    .endless/tasks/e-2105/verify.sh
    .endless/plans/E-1976.md
    .endless/analyses/E-2105.md
)

# sweep <extended-regex> — tracked PRODUCT files still matching it, one per line.
#
# `git grep` rather than `grep -r`: it searches tracked files only, so a scratch
# file cannot fail the suite, and it prints repo-relative paths with no leading
# "./" — which `grep -r` prefixes or not depending on which grep is installed.
#
# Everything under `.endless/` is excluded because all of it is Endless's own
# RECORDS: the append-only db-ledger, LESSONS.md, the per-task plan and analysis
# mirrors, and landed verify suites — including this one, which necessarily
# quotes both forms to assert they are gone. A record of what was said then is
# not the tool saying it now. Section E asserts those records kept it.
sweep() {
    git grep -lIE -e "$1" -- ':!.endless/' 2>/dev/null | sort
}

# wrapped_sweep <extended-regex> — the same question asked of files whose lines
# have been JOINED first.
#
# Almost every site was a comment, and a comment wraps. "…costs a stale glyph on
# the\n// attention board until…" is the phrase, and `sweep` above cannot see it
# because grep matches within one line — which is exactly how the site in
# internal/sessionstate/sessionstate.go survived the first pass. Leading comment
# markers are stripped and the file collapsed to a single line before matching,
# so a wrap hides nothing. Restricted to text extensions because joining a
# binary is meaningless.
wrapped_sweep() {
    local pattern="$1" f
    git ls-files -- '*.go' '*.py' '*.md' '*.sh' '*.toml' '*.json' '*.mmd' '*.txt' \
        ':!.endless/' 2>/dev/null | sort | while IFS= read -r f; do
        [[ -f "${f}" ]] || continue
        if sed -E 's,^[[:space:]]*(//|#|\*)[[:space:]]*,,' "${f}" \
            | tr '\n' ' ' \
            | grep -qE -e "${pattern}"; then
            printf '%s\n' "${f}"
        fi
    done
}

# ---------------------------------------------------------------------------
section "A. What this change could have broken (fail-fast)"
# ---------------------------------------------------------------------------
# Ten Go packages and three Python modules were rewritten; two files were
# renamed and thirteen symbols with them. A rewording that corrupts a string
# literal, a rename that misses a call site, or a doc reference `guide-check`
# resolves fails HERE, and everything below it would be measuring a broken tree.

if out=$(go build ./... 2>&1); then
    report_pass "go build ./... — every rename resolves"
else
    report_fail "go build ./... — every rename resolves" "exit 0" \
        "$(printf '%s' "${out}" | tail -20)"
    summary
fi

go build -o "${TMP}/endless-go" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

if out=$(go test -count=1 \
        ./internal/config/ ./internal/faultbadge/ ./internal/hookcmd/ \
        ./internal/liveview/ ./internal/monitor/ ./internal/projectstatuscmd/ \
        ./internal/schema/... ./internal/sessionstate/ ./internal/sessionstatuscmd/ \
        ./internal/taskstatus/ 2>&1); then
    report_pass "go test: the ten packages this touched"
else
    report_fail "go test: the ten packages this touched" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest -q \
        tests/test_project_status.py tests/test_cli.py \
        tests/test_setup_notification_hook.py 2>&1); then
    report_pass "pytest: the three test modules this touched"
else
    report_fail "pytest: the three test modules this touched" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(just guide-check 2>&1); then
    report_pass "just guide-check: appendix-a and reference still resolve"
else
    report_fail "just guide-check" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi

if out=$(just lifecycle-check 2>&1); then
    report_pass "just lifecycle-check: taskstatus's artifacts still match its table"
else
    report_fail "just lifecycle-check" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. Both forms are gone from the product tree (C1)"
# ---------------------------------------------------------------------------
# A sweep over every tracked file, not a walk of section D's list. The point of
# the change is that the term is nowhere, and a check that only looks where the
# term was known to be cannot tell "nowhere" from "nowhere I remembered to
# look" — which is precisely how bare "board" survived the first pass.

# The SHORT pattern proves its own boundaries before it is trusted to report an
# absence. Every assertion below is satisfied by an empty result, and this
# pattern's whole difficulty is its boundaries: one that over-matches fails on a
# clean tree (visibly), one that under-matches passes on a dirty one (silently).
# The second is the dangerous one, so it is pinned against literal strings here,
# where the answer does not depend on the state of the tree.
assert_eq "the short-form pattern catches every shape the term took" \
    "board boards Board BOARD board.go boardRows jsonBoard seedBoardTask project-board" \
    "$(for w in board boards Board BOARD board.go boardRows jsonBoard seedBoardTask project-board; do
           printf '%s' " ${w}" | grep -qE "${SHORT}" && printf '%s ' "${w}"
       done | sed 's/ $//')"

assert_eq "and spares the dictionary words that merely contain it" \
    "" \
    "$(for w in dashboard Dashboard keyboard KeyboardInterrupt whiteboard cardboard; do
           printf '%s' " ${w}" | grep -qE "${SHORT}" && printf '%s ' "${w}"
       done | sed 's/ $//')"

# Guard the sweep FUNCTIONS the same way — a broken pathspec, a regex the
# installed grep rejects, or a run from the wrong directory would report a clean
# tree just as convincingly as a clean tree does.
assert_contains "the flat sweep is not vacuous — it finds what it should find" \
    "docs/guide/reference.md" "$(sweep 'project[[:space:]_-]+monitor')"

assert_contains "the joined sweep is not vacuous either" \
    "internal/liveview/liveview.go" "$(wrapped_sweep 'project[[:space:]_-]+monitor')"

assert_eq "no tracked product file still carries the coinage" \
    "" "$(sweep "${COINED}")"

assert_eq "nor does one carry it across a comment line wrap" \
    "" "$(wrapped_sweep "${COINED}")"

assert_eq "no tracked product file still carries the short form either" \
    "" "$(sweep "${SHORT}")"

assert_eq "nor does one carry that across a wrap" \
    "" "$(wrapped_sweep "${SHORT}")"

# A filename is not searched by either sweep, and board.go was one.
assert_eq "no tracked file is still NAMED for the term" \
    "" "$(git ls-files | grep -E "${SHORT}" || true)"

# ---------------------------------------------------------------------------
section "C. The instance that was OUTPUT, not a comment (C2)"
# ---------------------------------------------------------------------------
# `endless-go --help` printed "render the project attention board", so the
# coinage was in a user's terminal and not only in source a reader never opens.
# Built from this tree into scratch rather than read from bin/, so the answer
# comes from what is committed here and not from whatever was last installed.

HELP="$("${TMP}/endless-go" --help 2>&1)" || setup_error "endless-go --help failed"
HELP_LC="$(printf '%s' "${HELP}" | tr '[:upper:]' '[:lower:]')"

assert_not_contains "--help no longer speaks the coinage" "attention board" "${HELP_LC}"
assert_not_contains "nor the short form" " board" "${HELP_LC}"

assert_contains "the project-status line names the command it renders" \
    "project-status render the project status view (--monitor loops it)" "${HELP}"

# The neighbouring line said "the dedicated two-pane tmux session the board
# lives in" — the coinage in short form, whose only antecedent was the line
# above. Removing the phrase without this line would have left a pronoun
# pointing at nothing, which is the whole shape of the miss this task corrects.
assert_contains "the project-window line names the command whose session it creates" \
    "project-window create the two-pane tmux session behind project monitor --tmux" \
    "${HELP}"

# ---------------------------------------------------------------------------
section "D. The subject survived, and the renames landed (C3)"
# ---------------------------------------------------------------------------
# Deleting the term would also have satisfied section B. What the task asked for
# is a REPLACEMENT — each sentence saying whichever command it meant — so every
# file that carried the two-word coinage must still name at least one of the
# two, and every renamed symbol must exist under its new name.

missing=""
for f in "${SITES[@]}"; do
    [[ -f "${f}" ]] || setup_error "site listed but not present: ${f}"
    grep -qiE 'project[[:space:]_-]+(status|monitor)' "${f}" || missing+=" ${f}"
done
assert_eq "all ${#SITES[@]} coinage sites name \`project status\` or \`project monitor\`" \
    "" "${missing}"

gone_missing=""
still_there=""
for pair in "${RENAMES[@]}"; do
    old="${pair%%:*}"; new="${pair##*:}"
    git grep -qI -e "${old}" -- ':!.endless/' 2>/dev/null && still_there+=" ${old}"
    git grep -qI -e "${new}" -- ':!.endless/' 2>/dev/null || gone_missing+=" ${new}"
done
assert_eq "all ${#RENAMES[@]} renamed symbols are gone under their old names" \
    "" "${still_there}"
assert_eq "and present under their new ones — renamed, not deleted" \
    "" "${gone_missing}"

assert_eq "the file named for the term is gone" \
    "" "$(git ls-files -- 'internal/projectstatuscmd/board*.go')"
assert_eq "and its contents live under a name that says what they do" \
    "internal/projectstatuscmd/render.go internal/projectstatuscmd/render_test.go" \
    "$(git ls-files -- 'internal/projectstatuscmd/render*.go' | tr '\n' ' ' | sed 's/ $//')"

# The config key's documentation is the other place a USER meets this. It
# described the tmux session by the coinage alone, and offered `{{project}}-board`
# as the worked example — teaching the word in the one place a reader is about to
# type a value.
assert_contains "internal/config/README.md names the command that opens the session" \
    'naming the tmux session `endless project monitor --tmux` opens for a project' \
    "$(grep '^| `session_name`' internal/config/README.md)"

# ---------------------------------------------------------------------------
section "E. The records the sweep was told to leave alone (C4)"
# ---------------------------------------------------------------------------
# A landed suite and a plan mirror record what was true when their task landed.
# Retrofitting either to a later change rewrites that history, and a terminology
# sweep is exactly the later change that rule exists to keep out. They still say
# "attention board", on purpose — which is also the strongest evidence that the
# sweeps in section B were scoped rather than global.

for f in "${RECORDS[@]}"; do
    [[ -f "${f}" ]] || setup_error "record listed but not present: ${f}"
    if grep -qiE -e "${COINED}" "${f}"; then
        report_pass "${f} still records the term it landed with"
    else
        report_fail "${f} still records the term it landed with" \
            "the phrase, untouched" "it was swept — that history is now rewritten"
    fi
done

summary
