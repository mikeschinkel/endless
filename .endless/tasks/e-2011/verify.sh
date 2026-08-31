#!/usr/bin/env bash
#
# E-2011 verification script — project paths are STORED home-relative, and the
# Go hook and the Python CLI agree on that one spelling.
#
# What changed. E-2002 made both halves normalize a project path identically
# and fixed the canonical form as absolute-with-symlinks-resolved. ED-1562 was
# then amended: the canonical form is home-relative — `~/Projects/acme`, and
# absolute only for a directory outside $HOME — because `endless sql` is a
# supported surface and an ad-hoc query over the ledger is far easier to read
# without a column of identical home prefixes.
#
# The trap this has to prove is closed. A tilde string is not a filesystem path
# in either language: `filepath.Join("~/Projects/acme", ".endless")` and
# `Path("~/Projects/acme") / ".endless"` both produce a directory that has never
# existed, and they do it silently, far from the code that read the column. So
# the single accessor was split in two — a STORED form (home-relative, for
# writes and comparisons) and a RESOLVED form (absolute, for anything touching
# disk) — and the checks below drive BOTH through shipped surfaces:
# `endless project register` and `endless project scan` on the Python side,
# `endless-go hook claude` and `endless-go template render` on the Go side.
#
# Run from anywhere inside the worktree:
#   endless task verify E-2011
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a fixture directory under $HOME (the home-relative form cannot be
# exercised anywhere else) plus a throwaway XDG_CONFIG_HOME inside it, so the
# temp DB, cache and every file this writes live under one directory that the
# EXIT trap removes. No real DB, ledger, cache or log is touched. A second
# fixture under the system temp dir supplies the outside-$HOME case.
#
# Fail-fast: the Go and Python unit suites run FIRST. They pin the rule at a
# granularity the shell cannot reach — the round trip, the sibling-of-home
# off-by-one, the unset-$HOME refusal, and the two source-level guards that fail
# the build when a new reader of projects.path forgets to resolve it.

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
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
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

section "The two forms, both halves, unit level (fail-fast)"

if go_out="$(cd "$WT" && go test ./internal/monitor/ \
        -run 'ProjectIDForPath|ProjectPath|StoredProjectPath|ResolvedProjectPath|MatchProjectPath|RepairProjectPaths|NeverNormalizes|EveryProjectsPathReader' 2>&1)"; then
    report_pass "go test ./internal/monitor -run '…StoredProjectPath|EveryProjectsPathReader…'"
else
    report_fail "go test ./internal/monitor -run '…StoredProjectPath|EveryProjectsPathReader…'" \
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

HOMEBASE=""; OUTBASE=""
HOME_R=""; REAL=""; LINKED=""; REL=""; OUTSIDE=""; DBDIR=""

E() { ( cd "$HOMEBASE" && uv run --project "$WT" endless "$@" ); }
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

# PY_STORED <path>: what the Python half computes as the STORED form.
PY_STORED() {
    ( cd "$HOMEBASE" && uv run --project "$WT" python -c \
        'import sys; from endless.project_path import stored; print(stored(sys.argv[1]))' "$1" )
}

setup_fixture() {
    # The home-relative form can only be exercised by a directory that really is
    # under $HOME, so the fixture lives there. `pwd -P` on both sides makes REL
    # below an exact expectation rather than an approximation.
    HOME_R="$(cd "$HOME" && pwd -P)" || return 1
    HOMEBASE="$(cd "$(mktemp -d "$HOME/endless-e2011-verify.XXXXXX")" && pwd -P)" || return 1
    # The other half of the mixed column: a project with no home-relative
    # spelling. On macOS mktemp lands under /var, a symlink into /private.
    OUTBASE="$(cd "$(mktemp -d)" && pwd -P)" || return 1

    REAL="$HOMEBASE/real/probe"
    LINKED="$HOMEBASE/link/probe"
    OUTSIDE="$OUTBASE/outsider"
    DBDIR="$HOMEBASE/xdg/endless"
    export XDG_CONFIG_HOME="$HOMEBASE/xdg"
    export XDG_CACHE_HOME="$HOMEBASE/cache"
    mkdir -p "$REAL" "$OUTSIDE" "$DBDIR" "$HOMEBASE/real/unregistered"
    ln -s "$HOMEBASE/real" "$HOMEBASE/link"

    # The expected stored spelling, derived from the fixture rather than guessed.
    REL="~${REAL#"$HOME_R"}"
    [[ "$REL" == "~/"* && "$REL" != "$REAL" ]] || return 1
    [[ "$LINKED" != "$REAL" ]] || return 1

    for repo in "$REAL" "$OUTSIDE"; do
        git -C "$repo" init -q
        git -C "$repo" config user.email verify@test
        git -C "$repo" config user.name verify
        git -C "$repo" commit -q --allow-empty -m "initial commit"
    done

    # Registered through the SYMLINKED spelling: E-2011 re-points E-2002's
    # invariant, it does not weaken it, so symlinks must still be resolved
    # BEFORE the path is relativized.
    E project register "$LINKED" --name probe --label Probe --desc d --lang Go \
        --status active >/dev/null 2>&1
    E project register "$OUTSIDE" --name outsider --label Out --desc d --lang Go \
        --status active >/dev/null 2>&1

    local pid
    pid="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$pid" ]] || return 1

    # The task-list context renders DESCRIPTIONS, so that is what proves the
    # session reached the registered project rather than an empty duplicate.
    W "INSERT INTO tasks (id, project_id, title, description, status, phase, tier)
       VALUES (7211, $pid, 'Tilde probe task', 'tilde-probe-description', 'ready', 'now', 2)"

    # A project-local template, for the Go read side: rendering it can only work
    # if the `~/…` row was expanded into a real directory.
    mkdir -p "$REAL/.endless/templates"
    printf 'e2011-template-rendered\n' > "$REAL/.endless/templates/e2011probe.md.tmpl"

    [[ "$(Q "SELECT count(*) FROM projects")" == "2" ]] || return 1
    return 0
}

teardown_fixture() {
    [[ -n "$HOMEBASE" && -d "$HOMEBASE" ]] && rm -rf "$HOMEBASE"
    [[ -n "$OUTBASE" && -d "$OUTBASE" ]] && rm -rf "$OUTBASE"
    return 0
}

if ! setup_fixture; then
    printf '%sSETUP FAILED%s: could not build the isolated fixture.\n' "${RED}" "${RESET}" >&2
    teardown_fixture
    exit 2
fi
trap teardown_fixture EXIT

# ─── the write side ─────────────────────────────────────────────────────────

section "The write side — what the Python CLI stores"

assert_eq "a project under \$HOME is stored home-relative" \
    "$REL" "$(Q "SELECT path FROM projects WHERE name='probe'")"

assert_eq "...with symlinks resolved BEFORE relativizing (E-2002 still holds)" \
    "$REL" "$(PY_STORED "$LINKED")"

assert_eq "a project outside \$HOME stays absolute — the column is mixed by design" \
    "$OUTSIDE" "$(Q "SELECT path FROM projects WHERE name='outsider'")"

section "Why the form was chosen — legibility of \`endless sql\`"

# ED-1562's stated reason, asserted rather than assumed: the supported ad-hoc
# query surface returns something a human can scan.
assert_contains "\`endless sql\` renders the project path with a tilde" \
    "~/" "$(Q "SELECT path FROM projects WHERE name='probe'")"

# ─── the read side, Go ──────────────────────────────────────────────────────

section "The read side — the Go hook fired with an absolute cwd"

out="$(HOOK "sess-e2011" "$REAL" SessionStart)"

assert_eq "no second project was auto-registered for the same directory" \
    "2" "$(Q "SELECT count(*) FROM projects")"
assert_eq "the session bound to the REGISTERED project" \
    "probe" "$(Q "SELECT p.name FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.session_id='sess-e2011'")"
assert_contains "...so its task list is the project's, not an empty duplicate's" \
    "tilde-probe-description" "$(CONTEXT "$out")"

section "A subdirectory, reached through a symlink"

out="$(HOOK "sess-e2011-sub" "$LINKED/.git" SessionStart)"

assert_eq "the walk up ancestors converts each rung to the stored form" \
    "probe" "$(Q "SELECT p.name FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.session_id='sess-e2011-sub'")"
assert_eq "and still auto-registers nothing" \
    "2" "$(Q "SELECT count(*) FROM projects")"

section "The two halves agree on one string"

# The parity check proper. An unregistered directory makes the Go side WRITE its
# own stored form (auto-registration stores it), which is the only way to read
# Go's answer through a shipped surface. Compare it to what the Python half
# computes for the same input.
out="$(HOOK "sess-e2011-parity" "$HOMEBASE/link/unregistered" SessionStart)"
go_stored="$(Q "SELECT path FROM projects WHERE name='unregistered'")"

assert_eq "Go auto-registers in the home-relative form" \
    "~${HOMEBASE#"$HOME_R"}/real/unregistered" "$go_stored"
assert_eq "...and Python agrees, byte for byte" \
    "$go_stored" "$(PY_STORED "$HOMEBASE/link/unregistered")"

# ─── the resolved form, both halves ─────────────────────────────────────────

section "The RESOLVED form — a \`~/…\` row used as a directory"

# The trap the split exists to close. Both of these read projects.path and then
# touch the filesystem with it; before the split each would have reached for
# `<cwd>/~/…` and failed somewhere else entirely.
scan_out="$(E project scan probe 2>&1)"
assert_not_contains "Python: \`project scan\` finds the directory behind the tilde" \
    "Project path missing" "$scan_out"

tmpl_out="$( ( cd "$HOMEBASE" && printf '{}' | "$EGO" --config-dir "$DBDIR" \
    template render --project probe e2011probe ) 2>&1 )"
assert_contains "Go: \`template render --project\` resolves the row to a real root" \
    "e2011-template-rendered" "$tmpl_out"

section "A \`~/…\` row handed to a command that shells out with it"

# The regression this section exists for. \`endless-go session-query
# triage-context\` puts the project root on the wire, and the Python triage path
# passes it straight to \`endless-go event --project-root\`, which uses it as a
# git work tree. Carrying the column verbatim made every triage run die with
# \`project root "~/Projects/endless" is not a git work tree\` and leave the task
# untriaged — a ledger write failing, nowhere near the code that read the row.
ctx_root="$( ( cd "$HOMEBASE" && "$EGO" --config-dir "$DBDIR" \
    session-query triage-context --id 7211 ) 2>/dev/null \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["project_root"])' )"

assert_eq "the triage context carries the RESOLVED root, not the stored tilde" \
    "$REAL" "$ctx_root"

# ...and it is a real work tree, which is the property git actually needs.
assert_eq "...which git accepts as a work tree" \
    "true" "$(git -C "$ctx_root" rev-parse --is-inside-work-tree 2>&1)"

# ─── the upgrade path ───────────────────────────────────────────────────────

section "A ledger repaired by E-2002 but not yet by E-2011"

# The row is in the PREVIOUS canonical form: absolute. It must still match, or
# the first hook event after the upgrade auto-registers a duplicate for every
# project the user has — a worse outcome than the thing being fixed.
W "UPDATE projects SET path='$REAL' WHERE name='probe'"
out="$(HOOK "sess-e2011-legacy" "$REAL" SessionStart)"

assert_eq "an absolute row is still matched by the resolved-form fallback" \
    "probe" "$(Q "SELECT p.name FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.session_id='sess-e2011-legacy'")"
assert_eq "...without auto-registering alongside it" \
    "3" "$(Q "SELECT count(*) FROM projects")"

# ─── the change file ────────────────────────────────────────────────────────

section "The ledger a user upgrades with"

# probe is left absolute (above); probe-2 is the twin, written in the OTHER
# spelling of the same directory — which is the only way both rows can exist,
# since projects.path is UNIQUE. The change re-points the survivor to the
# home-relative form and folds the twin into it, history and all.
W "INSERT INTO projects (id, name, path) VALUES (7710, 'probe-2', '$REL')"
W "INSERT INTO tasks (id, project_id, title, description, status, phase, tier)
   VALUES (7212, 7710, 'Stranded task', 'stranded-description', 'ready', 'now', 2)"

assert_eq "the fixture really has two rows for one directory" \
    "2" "$(Q "SELECT count(*) FROM projects WHERE name LIKE 'probe%'")"

change_out="$(cd "$WT" && ENDLESS_CHANGE_DB="$DBDIR/endless.db" \
    go run internal/schema/changes/e-2011-home-relative-project-paths.go 2>&1)"

assert_contains "the change script reports what it repaired" \
    "1 duplicate row(s) merged" "$change_out"
assert_eq "the duplicate is gone" \
    "1" "$(Q "SELECT count(*) FROM projects WHERE name LIKE 'probe%'")"
assert_eq "...and the surviving row is home-relative" \
    "$REL" "$(Q "SELECT path FROM projects WHERE name='probe'")"
assert_eq "the row outside \$HOME was left absolute, not mangled" \
    "$OUTSIDE" "$(Q "SELECT path FROM projects WHERE name='outsider'")"
assert_eq "the twin's task was repointed, not cascaded away" \
    "probe" "$(Q "SELECT p.name FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=7212")"

# Re-running must be a no-op, because a land can apply changes more than once
# across retries and a repair that is not idempotent is a repair nobody can
# safely re-run.
change_out="$(cd "$WT" && ENDLESS_CHANGE_DB="$DBDIR/endless.db" \
    go run internal/schema/changes/e-2011-home-relative-project-paths.go 2>&1)"

assert_contains "a second run is refused by the _schema_version marker" \
    "already applied" "$change_out"

summary
