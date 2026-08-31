#!/usr/bin/env bash
#
# E-2002 verification script — the Go hook and the Python CLI must normalize a
# project path to the same string.
#
# The bug: `endless project register` stored `Path(path).resolve()` — absolute
# AND symlink-resolved — while the Go hook compared the cwd Claude Code handed
# it after nothing but filepath.Abs. On any project reached through a symlink
# the two strings differed, the hook's exact-match walk missed the registered
# row, and it auto-registered a SECOND project for the same directory. The
# session then bound to the duplicate, which has no tasks, so SessionStart
# injected "No tasks yet" for a project full of them while the log line read
# `auto-registered project: <name> at <unresolved path>`.
#
# Not exotic: on macOS /tmp and /var are symlinks into /private, so every
# project under a temp dir hits it — which is how it surfaced, breaking
# .endless/tasks/e-2001/verify.sh — and so does any user whose projects live under
# a symlinked parent.
#
# Run from anywhere inside the worktree:
#   endless task verify E-2002
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# What this asserts. The unit layers on each side pin their own half of the
# rule; only an end-to-end run can pin that the two halves AGREE, because the
# disagreement was never visible inside either language. So the checks below
# drive the real `endless project register` (Python) and the real
# `endless-go hook claude` (Go) against one directory reached two ways, and
# count the rows in `projects`.
#
# The last section covers the other half: the ledger a user upgrading into this
# already has. `internal/schema/changes/e-2002-normalize-project-paths.go`
# rewrites stored paths to canonical form and merges the duplicate rows the bug
# produced, repointing their history rather than cascading it away.
#
# Isolation: a throwaway git repo under a temp dir, plus a temp XDG_CONFIG_HOME
# (its own DB) and XDG_CACHE_HOME. No real DB, ledger, cache or log is touched.
#
# Fail-fast: the Go and Python unit suites run FIRST. They pin the rule at a
# granularity the shell cannot reach — the non-strict resolve, the legacy-row
# scan, the nearest-project tie-break — so if they fail there is no point
# running the end-to-end checks.
#
# NOTE the fixture does NOT use `pwd -P` on the symlinked spelling. That
# workaround is what e-2001-verify.sh had to do to dodge this bug; here the
# unresolved spelling is the input under test.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}" "${RESET}"
        return 0
    fi
    printf '  %d passed, %s%d failed%s\n' "${PASS_COUNT}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    %s✗%s %s\n' "${RED}" "${RESET}" "$t"; done
    printf '\n'
    return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}

# ─── locate the worktree + binary ───────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EGO="$WT/bin/endless-go"

if [[ ! -x "$EGO" ]]; then
    printf '%sSETUP FAILED%s: %s not built. Run `just build` first.\n' \
        "${RED}" "${RESET}" "$EGO" >&2
    exit 2
fi

# ─── 0. fail-fast unit layer ────────────────────────────────────────────────

section "Normalization unit tests, both halves (fail-fast)"

if go_out="$(cd "$WT" && go test ./internal/monitor/ \
        -run 'ProjectIDForPath|ProjectPath|NormalizeProjectPath|RepairProjectPaths|NeverNormalizes' 2>&1)"; then
    report_pass "go test ./internal/monitor -run '…ProjectIDForPath|RepairProjectPaths…'"
else
    report_fail "go test ./internal/monitor -run '…ProjectIDForPath|RepairProjectPaths…'" \
        "all tests pass" "$go_out"
    summary
    exit 1
fi

if py_out="$(cd "$WT" && uv run --project "$WT" python -m pytest tests/test_project_path.py -q 2>&1)"; then
    report_pass "pytest tests/test_project_path.py"
else
    report_fail "pytest tests/test_project_path.py" "all tests pass" "$py_out"
    summary
    exit 1
fi

# ─── fixture ────────────────────────────────────────────────────────────────

TMP=""; REAL=""; LINKED=""; DBDIR=""
SESS="sess-e2002"

E() { ( cd "$TMP" && uv run --project "$WT" endless "$@" ); }
Q() { E sql "$1" --tsv 2>/dev/null; }
W() { E sql "$1" --write >/dev/null 2>&1; }

# HOOK <session_id> <cwd> <event>: run the real hook and echo its stdout.
#
# CLAUDE_CODE_ENTRYPOINT=cli is REQUIRED, not decoration (E-1962): the hook is
# gated on the harness and returns immediately — exit 0, no stdout — when the
# environment is not a supported agent host, and a bare shell is not one.
# TMUX_PANE is stripped so the probe does not read the spawn marker of whatever
# live session is running this script and bind itself to that session's task.
HOOK() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"%s","source":"startup","prompt":"probe"}' \
        "$1" "$2" "$3" \
    | env -u TMUX_PANE CLAUDE_CODE_ENTRYPOINT=cli \
        "$EGO" --config-dir "$DBDIR" hook claude 2>/dev/null
}

# CONTEXT <json>: the additionalContext the hook injected, or "" if none.
CONTEXT() {
    python3 -c '
import json, sys
raw = sys.argv[1].strip()
if not raw:
    print(""); sys.exit(0)
try:
    doc = json.loads(raw)
except Exception as e:
    print("<unparseable: %s>" % e); sys.exit(0)
print((doc.get("hookSpecificOutput") or {}).get("additionalContext", ""))
' "$1"
}

setup_fixture() {
    # -P here is deliberate and is NOT the workaround: it makes TMP a known
    # fully-resolved base, so REAL below is the canonical spelling and LINKED
    # is provably a different string for the same directory. Without it the
    # test could not tell which spelling anything stored.
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    REAL="$TMP/real/probe"
    LINKED="$TMP/link/probe"
    DBDIR="$TMP/xdg/endless"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    mkdir -p "$REAL" "$DBDIR" "$TMP/real/unregistered"
    ln -s "$TMP/real" "$TMP/link"

    # The two spellings must really differ, or every check below is vacuous.
    [[ "$LINKED" != "$REAL" ]] || return 1

    git -C "$REAL" init -q
    git -C "$REAL" config user.email verify@test
    git -C "$REAL" config user.name verify
    git -C "$REAL" commit -q --allow-empty -m "initial commit"

    # Registered through the SYMLINKED spelling — the input the product has to
    # canonicalize on the way in.
    E project register "$LINKED" --name probe --label Probe --desc d --lang Go \
        --status active >/dev/null 2>&1

    local pid
    pid="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$pid" ]] || return 1

    # The task-list context renders DESCRIPTIONS, so that is what proves the
    # session reached the registered project rather than an empty duplicate.
    W "INSERT INTO tasks (id, project_id, title, description, status, phase, tier)
       VALUES (7202, $pid, 'Path probe task', 'path-probe-description', 'ready', 'now', 2)"
    [[ "$(Q "SELECT count(*) FROM projects")" == "1" ]] || return 1
    return 0
}

teardown_fixture() { [[ -n "$TMP" && -d "$TMP" ]] && rm -rf "$TMP"; }

# ─── end-to-end ─────────────────────────────────────────────────────────────

if ! setup_fixture; then
    printf '%sSETUP FAILED%s: could not build the isolated fixture.\n' "${RED}" "${RESET}" >&2
    teardown_fixture
    exit 2
fi
trap teardown_fixture EXIT

section "The write side — what the Python CLI stores"

assert_eq "registering through a symlink stores the resolved path" \
    "$REAL" "$(Q "SELECT path FROM projects WHERE name='probe'")"

section "The read side — the Go hook fired with a symlinked cwd"

out="$(HOOK "$SESS" "$LINKED" SessionStart)"

assert_eq "no second project was auto-registered for the same directory" \
    "1" "$(Q "SELECT count(*) FROM projects")"
assert_eq "the session bound to the REGISTERED project" \
    "probe" "$(Q "SELECT p.name FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.session_id='$SESS'")"
assert_contains "...so its task list is the project's, not an empty duplicate's" \
    "path-probe-description" "$(CONTEXT "$out")"

section "A subdirectory reached through the symlink"

out="$(HOOK "sess-e2002-sub" "$LINKED/.git" SessionStart)"

assert_eq "the walk up normalized ancestors still lands on the project" \
    "probe" "$(Q "SELECT p.name FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.session_id='sess-e2002-sub'")"
assert_eq "and still auto-registers nothing" \
    "1" "$(Q "SELECT count(*) FROM projects")"

section "A ledger row written before this rule existed"

# The other direction, and the reason no data migration ships with this: a row
# holding an unresolved path must still be matched by a canonical cwd. If it
# were not, every project registered before E-2002 would grow a duplicate on
# the first event after the upgrade — a worse outcome than the bug.
W "UPDATE projects SET path='$LINKED' WHERE name='probe'"
out="$(HOOK "sess-e2002-legacy" "$REAL" SessionStart)"

assert_eq "an unresolved row is matched by a canonical cwd" \
    "probe" "$(Q "SELECT p.name FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.session_id='sess-e2002-legacy'")"
assert_eq "...without auto-registering alongside it" \
    "1" "$(Q "SELECT count(*) FROM projects")"
W "UPDATE projects SET path='$REAL' WHERE name='probe'"

section "The two halves agree on one string"

# The parity check proper. An unregistered directory makes the Go side WRITE
# its own normalization (auto-registration stores it), which is the only way to
# read Go's answer through a shipped surface. Compare it to what the Python
# half computes for the same input.
out="$(HOOK "sess-e2002-parity" "$TMP/link/unregistered" SessionStart)"
go_normalized="$(Q "SELECT path FROM projects WHERE name='unregistered'")"
py_normalized="$(cd "$TMP" && uv run --project "$WT" python -c \
    'import sys; from endless.project_path import normalize; print(normalize(sys.argv[1]))' \
    "$TMP/link/unregistered")"

assert_eq "Go normalizes the symlinked path to the real one" \
    "$TMP/real/unregistered" "$go_normalized"
assert_eq "...and Python agrees, byte for byte" \
    "$go_normalized" "$py_normalized"

section "The ledger the bug already damaged"

# The data half. Everything above is about a ledger going forward; this is the
# one a user upgrading into the fix actually has — the genuine registration, the
# auto-registered twin the hook created for the same directory, and history
# hanging off the twin. `endless db apply-change` runs at land time; this drives
# the same script against the fixture DB.
# probe holds the canonical path; probe-2 is the twin, stored in the OTHER
# spelling of the same directory — which is the only way both rows can exist,
# since projects.path is UNIQUE.
W "INSERT INTO projects (id, name, path) VALUES (770, 'probe-2', '$LINKED')"
W "INSERT INTO tasks (id, project_id, title, description, status, phase, tier)
   VALUES (7203, 770, 'Stranded task', 'stranded-description', 'ready', 'now', 2)"

assert_eq "the fixture really has two rows for one directory" \
    "2" "$(Q "SELECT count(*) FROM projects WHERE name LIKE 'probe%'")"

change_out="$(cd "$WT" && ENDLESS_CHANGE_DB="$DBDIR/endless.db" \
    go run internal/schema/changes/e-2002-normalize-project-paths.go 2>&1)"

assert_contains "the change script reports what it repaired" \
    "1 duplicate row(s) merged" "$change_out"
assert_eq "the duplicate is gone" \
    "1" "$(Q "SELECT count(*) FROM projects WHERE name LIKE 'probe%'")"
assert_eq "...and the surviving row holds the canonical path" \
    "$REAL" "$(Q "SELECT path FROM projects WHERE name='probe'")"
assert_eq "the twin's task was repointed, not cascaded away" \
    "probe" "$(Q "SELECT p.name FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=7203")"

# Re-running must be a no-op, because a land can apply changes more than once
# across retries and a repair that is not idempotent is a repair nobody can
# safely re-run.
change_out="$(cd "$WT" && ENDLESS_CHANGE_DB="$DBDIR/endless.db" \
    go run internal/schema/changes/e-2002-normalize-project-paths.go 2>&1)"

assert_contains "a second run is refused by the _schema_version marker" \
    "already applied" "$change_out"

summary
