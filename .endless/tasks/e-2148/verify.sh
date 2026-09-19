#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2148 and records what was true when E-2148
# landed. Edit it only if you ARE E-2148. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2148: rework the errors surface. Seven problems found while reading two
# real incidents, landed as six passes:
#
#   1. The one-line notification was called a "badge" — a word a session
#      coined and later sessions cited as settled product vocabulary. It is
#      the "fault row" now, and the package that renders it is faultrow.
#   2. A code's prefix states its severity: WARN-NNNN or ERR-NNNN. ERR-0001
#      had been severity `warning` since E-698. Numbers did not move.
#   3. `list` lists; `show <id>` shows one, in full. `show` was the listing
#      verb — the only `show` in the CLI that did not mean "one item, in
#      detail" — and the detail view its truncation implied did not exist.
#   4. A listing says what it counted and where, the empty case included, so
#      it can no longer print "no errors" while the fault row reports two.
#   5. The severity word is gone from both the row and the listing, because
#      the code says it; both now measure in display columns and neither
#      wraps.
#   6. Every code carries its remedy — the docs' own "What to do" paragraph,
#      verbatim — and `errors show <id>` prints it.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, then each contract test by name,
#      so deleting one cannot turn a section green by absence.
#   B. The old name is gone from every surface that speaks as the tool.
#   C. The catalog's prefixes match its severities, and no number moved.
#   D. The schema change rewrites the rows already recorded, and running it
#      twice leaves the same rows.
#   E. `list` and `show` are two verbs, in Go and in the Python CLI, and
#      `show` with no id refuses rather than listing.
#   F. BEHAVIOUR, end to end, through a binary built from this tree against a
#      throwaway database: the contradiction that surfaced this task, the
#      remedy reaching the screen, and the listing's shape.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# The scratch database sections D and F build lives under $TMP, never under
# .endless/ — a stray file there blocks `worktree land`.
SQL_BIN="$(command -v sqlite3 || true)"
[[ -n "${SQL_BIN}" ]] || setup_error "sqlite3 is required by sections D and F"

# q <db> <statement> — one statement against a scratch database.
q() {
    "${SQL_BIN}" "$1" "$2"
}

# sweep <phrase> — tracked PRODUCT files still containing it.
#
# `git grep` rather than `grep -r`: it searches tracked files only, so a scratch
# file cannot fail the suite, and it prints repo-relative paths with no leading
# "./" that some greps add and others do not.
#
# Scoped to what ships as the tool — the Go and Python source, the guide, the
# error catalog, the README. Deliberately NOT swept:
#
#   .endless/       all of it is Endless's own records: the ledger, the plan
#                   and analysis mirrors, and landed verify suites including
#                   this one, which has to quote the removed name in order to
#                   assert that it is gone.
#   docs/*-2026-*   dated briefs and research artifacts. The 2026-04-02 design
#                   brief's "status badge" is a web UI card, a different thing
#                   entirely; the 2026-09-17 refusal inventory is a snapshot of
#                   what the tree said on that date. A record of what was true
#                   then is not the tool saying it now.
sweep() {
    git grep -lIF -e "$1" -- \
        internal/ cmd/ src/ tests/ docs/guide/ docs/errors.md README.md \
        2>/dev/null | sort
}

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# faultrow is the renamed package and the reworked row. faults owns the catalog,
# the severity-keyed ids and the remedies. errorscmd owns list/show, the scope
# header and the listing's layout — it had no tests at all before this task.
# The two status views are the row's callers, and their frames are where it is
# asserted to actually appear.

if out=$(go test ./internal/faultrow ./internal/faults ./internal/errorscmd \
                 ./internal/sessionstatuscmd ./internal/projectstatuscmd 2>&1); then
    report_pass "go test: faultrow, faults, errorscmd, sessionstatuscmd, projectstatuscmd"
else
    report_fail "go test: faultrow, faults, errorscmd, sessionstatuscmd, projectstatuscmd" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# Named individually so deleting one cannot turn this section green by absence.
run_named() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}\$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}

for t in TestCatalog_ThePrefixStatesTheSeverity \
         TestCatalog_NumbersAreNeverReused \
         TestCatalog_EveryCodeCarriesARemedy \
         TestCatalog_RemediesMatchTheDocs \
         TestCatalog_EveryCodeIsDocumented; do
    run_named ./internal/faults "${t}"
done

for t in TestRowLine_SpendsNoColumnsOnTheSeverityWord \
         TestRowLine_StillColorsByMaxSeverity \
         TestRowLine_TallyGlyphsAreSingleWidth \
         TestRowLine_TalliesSeveritiesInGlyphsWhenSeveralAreOpen \
         TestRowLine_NamesTheDistinctCodesRatherThanOneSummary \
         TestRowLine_CollapsesDuplicateCodes \
         TestRowLine_ShedsCodesIntoACountRatherThanCuttingOne \
         TestRowLine_ShowsTheWholeCodeOrNoRowAtAll \
         TestRowLine_NeverExceedsTheTerminalWidth \
         TestRowLine_DropsTheHintOnlyWhenItCannotFit; do
    run_named ./internal/faultrow "${t}"
done

for t in TestListingLines_NeverExceedTheTerminalWidth \
         TestListingLines_AreColumnAlignedAtEveryWidth \
         TestListingLines_WideRuneFixtureIsActuallyWide \
         TestListingLines_SpendNoColumnOnSeverity \
         TestListingLines_ShedWholeColumnsInAFixedOrder \
         TestListingLines_GiveTheSummaryEveryColumnLeftOver; do
    run_named ./internal/errorscmd "${t}"
done

if out=$(uv run pytest tests/test_go_cli_parity.py tests/test_db_gate.py -q 2>&1); then
    report_pass "python: Go/Click verb parity and the --db gate"
else
    report_fail "python: Go/Click verb parity and the --db gate" \
        "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi

# ---------------------------------------------------------------------------
section "B. The coined name is gone from everything that speaks as the tool"
# ---------------------------------------------------------------------------
# It was never ratified. One session invented it, later sessions cited the
# coinage as though it were settled product vocabulary, and the word appeared
# nowhere Mike had written it.

assert_eq "no package, import or identifier still says faultbadge" \
    "" "$(sweep 'faultbadge')"

for word in 'badge' 'badges' 'badged' 'badging'; do
    assert_eq "no source, help text or guide prose still says \"${word}\"" \
        "" "$(git grep -lIw -e "${word}" -- \
              internal/ cmd/ src/ tests/ docs/guide/ docs/errors.md README.md 2>/dev/null | sort)"
done

assert_contains "the package that renders it is named for what it is" \
    "package faultrow" "$(cat internal/faultrow/faultrow.go)"

# The row's public contract — the one string by which a caller's test can find
# it inside a frame it did not render itself — is unchanged by the rename.
assert_contains "the row's exported identifier survives the rename" \
    'const Hint = "Run eeh"' "$(cat internal/faultrow/faultrow.go)"

# ---------------------------------------------------------------------------
section "C. A code's prefix states its severity, and no number moved"
# ---------------------------------------------------------------------------
# ERR-0001 was severity `warning` from the day it shipped until this task — an
# id that stated one thing and meant another. Section A runs the catalog gates
# that make that impossible to reintroduce; these assert the specific rewrite.

CODES="internal/faults/codes.go"

for code in WARN-0001 WARN-0004 WARN-0005 WARN-0006 WARN-0009 WARN-0012 WARN-0013; do
    assert_contains "${code} carries the warning prefix" "\"${code}\"" "$(cat "${CODES}")"
done
for code in ERR-0002 ERR-0003 ERR-0007 ERR-0008 ERR-0010 ERR-0011 ERR-0014; do
    assert_contains "${code} keeps the error prefix" "\"${code}\"" "$(cat "${CODES}")"
done

# The crux of "numbers do not move": the seven that changed prefix must be
# absent under their OLD ids, and no new number may have appeared.
for old in ERR-0001 ERR-0004 ERR-0005 ERR-0006 ERR-0009 ERR-0012 ERR-0013; do
    assert_not_contains "the catalog no longer declares ${old}" \
        "ID:       \"${old}\"" "$(cat "${CODES}")"
done

assert_eq "the catalog still holds fourteen codes and not one more" \
    "14" "$(grep -c '^\t\tID:       "' "${CODES}")"

# docs/errors.md is retitled to match. Section A's docs-sync gate proves the
# two agree; this proves the docs were actually rewritten rather than the gate
# relaxed.
assert_contains "docs/errors.md retitles the first section" \
    "## WARN-0001 — job-failed" "$(cat docs/errors.md)"
assert_not_contains "docs/errors.md no longer documents ERR-0001" \
    "## ERR-0001" "$(cat docs/errors.md)"

# ---------------------------------------------------------------------------
section "D. The change script rewrites recorded rows, twice over"
# ---------------------------------------------------------------------------
# A code is a plain string in the `errors` table and LookupCode resolves it by
# exact id, so an incident recorded before this change would otherwise print a
# bare code with no title and no remedy.

CHANGE="internal/schema/changes/e-2148-severity-keyed-fault-codes.sql"
[[ -f "${CHANGE}" ]] || setup_error "${CHANGE} is missing"

PROBE_DB="${TMP}/change.db"
"${SQL_BIN}" "${PROBE_DB}" <internal/schema/schema.sql >/dev/null 2>&1 \
    || setup_error "cannot build a probe database from schema.sql"

q "${PROBE_DB}" "INSERT INTO errors (code,severity,source,fingerprint,summary)
                 VALUES ('ERR-0001','warning','job:a','f1','recorded before the change'),
                        ('ERR-0013','warning','hook:b','f2','also before'),
                        ('ERR-0002','error','job:c','f3','an error, untouched')" \
    || setup_error "cannot seed the probe database"

"${SQL_BIN}" "${PROBE_DB}" <"${CHANGE}" || setup_error "the change script failed"

assert_eq "a warning recorded as ERR-0001 now resolves as WARN-0001" \
    "WARN-0001" "$(q "${PROBE_DB}" "SELECT code FROM errors WHERE fingerprint='f1'")"
assert_eq "and ERR-0013 as WARN-0013" \
    "WARN-0013" "$(q "${PROBE_DB}" "SELECT code FROM errors WHERE fingerprint='f2'")"
assert_eq "an error-severity code is left exactly as it was" \
    "ERR-0002" "$(q "${PROBE_DB}" "SELECT code FROM errors WHERE fingerprint='f3'")"

# The severity COLUMN is what told us which codes were mislabelled. Rewriting it
# would have changed a fact about what happened while fixing how it is named.
assert_eq "the recorded severities are untouched" \
    "warning|warning|error" \
    "$(q "${PROBE_DB}" "SELECT group_concat(severity,'|') FROM (SELECT severity FROM errors ORDER BY id)")"

# Idempotent BY CONSTRUCTION: every statement matches the OLD id, so a second
# run matches nothing. That must hold without the dispatcher's marker, which is
# not in play when the file is run by hand.
BEFORE="$(q "${PROBE_DB}" "SELECT group_concat(code,'|') FROM (SELECT code FROM errors ORDER BY id)")"
"${SQL_BIN}" "${PROBE_DB}" <"${CHANGE}" || setup_error "the change script failed on its second run"
assert_eq "running it a second time leaves the same rows" \
    "${BEFORE}" "$(q "${PROBE_DB}" "SELECT group_concat(code,'|') FROM (SELECT code FROM errors ORDER BY id)")"

# ---------------------------------------------------------------------------
section "E. list and show are two verbs, on both sides of the CLI"
# ---------------------------------------------------------------------------

GO_ERRORS="$(cat internal/errorscmd/errors.go)"
assert_contains "the Go dispatch routes list to its own handler" \
    'case "list":' "${GO_ERRORS}"
assert_contains "and show to a different one" \
    'case "show":' "${GO_ERRORS}"
assert_not_contains "show is no longer an alias for the listing" \
    'case "show", "list":' "${GO_ERRORS}"

# tests/test_go_cli_parity.py (run in section A) requires every Go verb to be
# reachable from Click. This asserts the specific pair rather than trusting a
# parser that could drift.
PY_VERBS="$(uv run python -c \
    'from endless.cli import main; print(" ".join(sorted(main.commands["errors"].commands)))' 2>&1)"
assert_contains "endless errors list is reachable from the Python CLI" "list" "${PY_VERBS}"
assert_contains "endless errors show is reachable from the Python CLI" "show" "${PY_VERBS}"

# The shell helper the fault row points at must name the listing, or `Run eeh`
# sends a reader to a usage error.
assert_contains "the eeh shell helper runs the listing" \
    '_endless_run errors list "$@"' "$(cat src/endless/cli.py)"

# ---------------------------------------------------------------------------
section "F. The surface, end to end, from a binary built from this tree"
# ---------------------------------------------------------------------------
# Built here rather than taken from bin/, so the answers come from what is
# committed in this worktree and not from whatever was last installed.
#
# --db-dir names a throwaway database outright, which is the only way to keep
# this probe out of both the real record and this worktree's sandbox.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"

DBDIR="${TMP}/db"
mkdir -p "${DBDIR}"
EN=("${BIN}" --db-dir "${DBDIR}")

"${EN[@]}" errors raise --summary "a seed incident" >/dev/null 2>&1 \
    || setup_error "errors raise failed against the probe database"

# Two projects, and every incident in the SECOND one. This is the exact shape
# that surfaced the task: standing in one project, with everything wrong in
# another.
q "${DBDIR}/endless.db" "
    INSERT INTO projects (id,name,path) VALUES (1,'alpha','/tmp/e2148-alpha'),(2,'beta','/tmp/e2148-beta');
    DELETE FROM errors;
    INSERT INTO errors (project_id,code,severity,source,fingerprint,summary,occurrences,first_seen_at,last_seen_at)
    VALUES (2,'ERR-0002','error','job:x','f1','a job in beta panicked',1,'2026-09-19T06:00:00','2026-09-19T06:00:00'),
           (2,'WARN-0001','warning','job:y','f2','a job in beta returned an error, with a summary long enough that a narrow terminal has to cut it somewhere',4,'2026-09-19T06:00:00','2026-09-19T06:00:00');
" || setup_error "cannot seed the probe projects and incidents"

# --- the contradiction --------------------------------------------------
# `errors show` printed a bare "no errors" from inside one project while the
# fault row simultaneously reported `1 error, 1 warning` from another. Two
# surfaces flatly disagreeing about whether anything is wrong.
EMPTY="$("${EN[@]}" errors list --project alpha 2>&1)"
# The WHOLE line, not a substring: "no errors in alpha — 2 elsewhere" contains
# the old sentence and is the fix for it, so only an exact match distinguishes
# the two.
assert_eq "a scoped empty listing never prints a bare \"no errors\"" \
    "not the bare sentence" \
    "$(head -1 <<<"${EMPTY}" | grep -qx 'no errors' && echo 'no errors' || echo 'not the bare sentence')"
assert_contains "it says which project it means" "no errors in alpha" "${EMPTY}"
assert_contains "it counts what is open elsewhere" "2 elsewhere" "${EMPTY}"
assert_contains "and hands over the command that shows them" \
    "endless errors list --all-projects" "${EMPTY}"

FULL="$("${EN[@]}" errors list --project beta 2>&1)"
assert_contains "a scoped listing with rows names the project and the count" \
    "2 errors in beta" "${FULL}"
assert_contains "and drops the PROJECT column it would repeat on every row" \
    "ID  CODE" "${FULL}"
assert_not_contains "the scoped listing carries no PROJECT column" "PROJECT" "${FULL}"

WIDE="$("${EN[@]}" errors list --all-projects 2>&1)"
assert_contains "a machine-wide listing says so" "across every project" "${WIDE}"
assert_contains "and restores the PROJECT column" "PROJECT" "${WIDE}"

# --- no severity word ---------------------------------------------------
assert_not_contains "the listing spends no column on SEVERITY" "SEVERITY" "${FULL}"
assert_contains "the code carries the severity instead" "WARN-0001" "${FULL}"

# --- list vs show -------------------------------------------------------
NOID="$("${EN[@]}" errors show 2>&1 || true)"
assert_contains "errors show with no id refuses rather than listing" \
    "Usage: endless-go errors show <id>" "${NOID}"
assert_contains "and names the verb that does list" "endless errors list" "${NOID}"
assert_not_contains "it does not print a listing instead" "LAST SEEN" "${NOID}"

ID="$(q "${DBDIR}/endless.db" "SELECT id FROM errors WHERE fingerprint='f2'")"
[[ -n "${ID}" ]] || setup_error "could not read back the seeded incident id"

ONE="$("${EN[@]}" errors show "${ID}" 2>&1)"
assert_contains "errors show <id> names the incident" "WARN-0001" "${ONE}"
assert_contains "it names the project the incident belongs to" "Project:     beta" "${ONE}"
assert_contains "it counts the occurrences" "Occurrences: 4" "${ONE}"

# The whole summary — this view exists BECAUSE the listing truncates.
assert_contains "it prints the summary the listing had to cut" \
    "long enough that a narrow terminal has to cut it somewhere" "${ONE}"

# An id is honoured whichever project the row belongs to: refusing because it
# belongs to another project would make `show 7` fail right after a listing
# displayed row 7.
assert_contains "an id is honoured across the scope" "WARN-0001" \
    "$("${EN[@]}" errors show "${ID}" 2>&1)"

# --detail is the deprecated spelling's last user; it stays accepted and stays
# out of the usage text.
assert_contains "--id remains accepted for callers that already type it" \
    "WARN-0001" "$("${EN[@]}" errors show --id "${ID}" 2>&1)"
assert_not_contains "and is not taught in the usage text" "--id" "${NOID}"

# --- remedies -----------------------------------------------------------
# The footer used to name only `errors clear`, which it is careful to say is
# not a retry — so the one action the surface named was the one that changes
# nothing.
assert_contains "errors show <id> says what to do about it" "What to do:" "${ONE}"
assert_contains "and the remedy is the catalog's own text" \
    "endless jobs retry <name>" "${ONE}"
assert_contains "it still says how to dismiss it afterwards" \
    "endless errors clear ${ID}" "${ONE}"
assert_contains "and still says dismissing is not retrying" \
    "not a retry" "${ONE}"

assert_contains "the listing points at show for the rest" \
    "endless errors show <id>" "${FULL}"
assert_contains "the listing still names clear" "endless errors clear" "${FULL}"

# --- the fault row ------------------------------------------------------
# Rendered through session-status, which is where a person actually meets it.
# ANSI stripped, and located by the row's own public identifier.
row() {
    "${EN[@]}" session-status --cols 140 2>/dev/null \
        | sed 's/\x1b\[[0-9;]*m//g' \
        | grep -F 'Run eeh' || true
}

ROW="$(row)"
assert_contains "the fault row renders with incidents open" "Run eeh" "${ROW}"
assert_not_contains "it spends no columns spelling out WARNING" "WARNING" "${ROW}"
assert_not_contains "nor ERROR" "ERROR" "${ROW}"
assert_contains "it tallies the error in a glyph" "✕1" "${ROW}"
assert_contains "and the warning" "⚠1" "${ROW}"
assert_contains "it names the distinct codes" "ERR-0002" "${ROW}"
assert_contains "both of them" "WARN-0001" "${ROW}"

# With several open, naming ONE summary reads as the whole story when it is a
# fraction of it.
assert_not_contains "it names no single incident's summary while several are open" \
    "panicked" "${ROW}"

# Clear the error and the row falls back to the one-incident shape: code and
# summary, which is what a single open incident makes honest again.
"${EN[@]}" errors clear "$(q "${DBDIR}/endless.db" "SELECT id FROM errors WHERE fingerprint='f1'")" \
    >/dev/null 2>&1 || setup_error "errors clear failed"

LONE="$(row)"
assert_contains "one open incident gets its code" "WARN-0001" "${LONE}"
assert_contains "and its summary back" "a job in beta returned an error" "${LONE}"
assert_not_contains "and no tally, which a single incident does not need" "⚠1" "${LONE}"

# Clearing remains the only exit — inherited from E-2151, cheap to keep, and
# this task moved every line the assertion rests on.
"${EN[@]}" errors clear >/dev/null 2>&1 || setup_error "errors clear (all) failed"
assert_eq "clearing the last incident is what removes the row" "" "$(row)"

summary
