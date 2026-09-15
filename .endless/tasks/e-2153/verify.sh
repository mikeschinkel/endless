#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2153 and records what was true when E-2153
# landed. Edit it only if you ARE E-2153. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2153: the coined term "attention board" is replaced by the commands it
# actually names.
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
#   Each use is replaced by whichever command the sentence means, or by both
#   where it means the pair. No substitute coinage.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  GONE FROM THE PRODUCT TREE. No tracked file outside `.endless/` still
#       carries the phrase, in any casing or separator — including across a
#       comment line wrap, which a flat grep would walk straight past.
#   C2  GONE FROM OUTPUT. `endless-go --help` no longer speaks it, and the two
#       lines that did now name the commands instead.
#   C3  REPLACED, NOT DELETED. Every file that carried the coinage still names
#       `project status` or `project monitor` — the sentences kept their
#       subject rather than losing it.
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
#   A. FAIL-FAST — this change is comments, docstrings and help text across
#      eight Go packages, three Python modules and two guide pages. What it can
#      break is a string a test reads or a doc reference a gate resolves, so
#      those run first and nothing below is meaningful if they fail.
#   B. The sweep — C1.
#   C. The user-facing surface — C2.
#   D. The subject survived — C3.
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
COINED='attention[[:space:]_-]+board'

# The files that carried it, and had to be rewritten one sentence at a time
# because the right replacement differs per sentence: some meant the snapshot,
# some the loop, some the pair. Typed out rather than derived from git, so the
# list is a claim this suite makes and not a shadow of whatever HEAD happens to
# be rebased onto.
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
    internal/projectstatuscmd/board.go
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
# quotes the phrase to assert it is gone. A record of what was said then is not
# the tool saying it now. Section E asserts those records kept it.
sweep() {
    git grep -lIiE -e "$1" -- ':!.endless/' 2>/dev/null | sort
}

# wrapped_sweep <extended-regex> — the same question asked of files whose lines
# have been JOINED first.
#
# Almost every site was a comment, and a comment wraps. "…costs a stale glyph on
# the\n// attention board until…" is the phrase, and `sweep` above cannot see it
# because grep matches within one line. Leading comment markers are stripped and
# the file collapsed to a single line before matching, so a wrap hides nothing.
# Restricted to text extensions because joining a binary is meaningless.
wrapped_sweep() {
    local pattern="$1" f
    git ls-files -- '*.go' '*.py' '*.md' '*.sh' '*.toml' '*.json' '*.mmd' '*.txt' \
        ':!.endless/' 2>/dev/null | sort | while IFS= read -r f; do
        [[ -f "${f}" ]] || continue
        if sed -E 's,^[[:space:]]*(//|#|\*)[[:space:]]*,,' "${f}" \
            | tr '\n' ' ' \
            | grep -qiE -e "${pattern}"; then
            printf '%s\n' "${f}"
        fi
    done
}

# ---------------------------------------------------------------------------
section "A. What this change could have broken (fail-fast)"
# ---------------------------------------------------------------------------
# The eight Go packages and three Python modules whose comments, docstrings or
# help text were rewritten, plus the two gates over the guide pages that were
# edited. A rewording that corrupts a string literal, a docstring a test reads,
# or a doc reference `guide-check` resolves fails HERE, and everything below it
# would be measuring a broken tree.

if out=$(go build -o "${TMP}/endless-go" ./cmd/endless-go 2>&1); then
    report_pass "go build ./cmd/endless-go — the --help text compiles"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi

if out=$(go test -count=1 \
        ./internal/config/ ./internal/faultbadge/ ./internal/hookcmd/ \
        ./internal/liveview/ ./internal/monitor/ ./internal/projectstatuscmd/ \
        ./internal/sessionstate/ ./internal/taskstatus/ 2>&1); then
    report_pass "go test: the eight packages whose comments were rewritten"
else
    report_fail "go test: the eight packages whose comments were rewritten" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest -q \
        tests/test_project_status.py tests/test_cli.py \
        tests/test_setup_notification_hook.py 2>&1); then
    report_pass "pytest: the three test modules whose docstrings were rewritten"
else
    report_fail "pytest: the three test modules whose docstrings were rewritten" \
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
section "B. The phrase is gone from the product tree (C1)"
# ---------------------------------------------------------------------------
# A sweep over every tracked file, not a walk of the list in section D. The
# point of the change is that the term is nowhere, and a check that only looks
# where the term was known to be could not tell the difference between "nowhere"
# and "nowhere I remembered to look".

# Guard against a vacuous pass FIRST. Both assertions below are satisfied by an
# empty result, so a sweep that silently found nothing — a broken pathspec, a
# regex the installed grep rejects, a `git grep` run from the wrong directory —
# would read as a clean tree. Each sweep is therefore made to find something it
# SHOULD find before it is trusted to report an absence.
assert_contains "the flat sweep is not vacuous — it finds what it should find" \
    "docs/guide/reference.md" "$(sweep 'project[[:space:]_-]+monitor')"

assert_contains "the joined sweep is not vacuous either" \
    "internal/liveview/liveview.go" "$(wrapped_sweep 'project[[:space:]_-]+monitor')"

assert_eq "no tracked product file still carries the coinage" \
    "" "$(sweep "${COINED}")"

assert_eq "nor does one carry it across a comment line wrap" \
    "" "$(wrapped_sweep "${COINED}")"

# ---------------------------------------------------------------------------
section "C. The one instance that was OUTPUT, not a comment (C2)"
# ---------------------------------------------------------------------------
# `endless-go --help` printed "render the project attention board", so the
# coinage was in a user's terminal and not only in source a reader never opens.
# Built from this tree into scratch rather than read from bin/, so the answer
# comes from what is committed here and not from whatever was last installed.

HELP="$("${TMP}/endless-go" --help 2>&1)" || setup_error "endless-go --help failed"

assert_not_contains "--help no longer speaks the coinage" \
    "attention board" "$(printf '%s' "${HELP}" | tr '[:upper:]' '[:lower:]')"

assert_contains "the project-status line names the command it renders" \
    "project-status render the project status view (--monitor loops it)" "${HELP}"

# The neighbouring line said "the dedicated two-pane tmux session the board
# lives in" — "the board" being the coinage in short form, whose only antecedent
# was the line above. Removing the phrase without this line would have left a
# pronoun pointing at nothing.
assert_contains "the project-window line names the command whose session it creates" \
    "project-window create the two-pane tmux session behind project monitor --tmux" \
    "${HELP}"

# ---------------------------------------------------------------------------
section "D. Every site kept its subject (C3)"
# ---------------------------------------------------------------------------
# Deleting the phrase would also have satisfied section B. What the task asked
# for is a REPLACEMENT — each sentence saying whichever command it meant — so
# every file that carried the coinage must still name at least one of the two.

missing=""
for f in "${SITES[@]}"; do
    [[ -f "${f}" ]] || setup_error "site listed but not present: ${f}"
    grep -qiE 'project[[:space:]_-]+(status|monitor)' "${f}" || missing+=" ${f}"
done
assert_eq "all ${#SITES[@]} sites name \`project status\` or \`project monitor\`" \
    "" "${missing}"

# The config key's documentation is the other place a USER meets this, and it
# described the tmux session by the coinage alone. It has to name the command
# that opens the session, because that is the only way a reader can go look.
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
# sweep in section B was scoped rather than global.

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
