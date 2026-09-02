#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2105 and records what was true when E-2105
# landed. Edit it only if you ARE E-2105. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2105 verification — the session state vocabulary gets an owning package.
#
# WHAT LANDED
#   `sessions.state` carried four values — working, idle, needs_input, ended —
#   and nothing owned them. They were spelled as string literals across ~70
#   non-test sites in Go, Python and SQL: the defect E-1891 fixed for task
#   status, unfixed here.
#
#   internal/sessionstate is its counterpart. Members, labels, glyphs, five
#   curated groups (All, Live, MayWrite, AwaitsHuman, DisplayOrder), SQLList,
#   and a transition table naming which code performs each write. Exposed as
#   `endless-go session-state`; src/endless/session_states.py is a pass-through
#   client holding none of the vocabulary. Folded in: the obsolete duplicate
#   CREATE TABLE sessions in db.py, whose banned CHECK constraint was a fourth
#   copy of the value set.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  PURE REFACTOR. No state added, no group's membership differs from the
#       predicate it replaced, and `session list` renders byte-for-byte what it
#       rendered before — rows, glyphs, legend, sort order, JSON and the
#       --state rejection message. Section F compares against output captured
#       from the pre-change build.
#   C2  ONE OWNER. No session-state literal survives outside
#       internal/sessionstate and the schema, save four documented residents
#       that are not the vocabulary (section D names each and why).
#   C3  THE GATE READS A GROUP. hookcmd.sessionMayWrite answers whatever
#       sessionstate.MayWrite says, for every state including ones that do not
#       exist yet. That is the structural half: a `switch` with a silent
#       `default: return false` is what let a proposal to route permission
#       prompts to `needs_input` nearly ship a gate that refused the session's
#       next write after the user answered.
#   C4  PYTHON HOLDS NOTHING. session_states.py declares no state, no group and
#       no list; cli.py and session_cmd.py carry no literal.
#
# ISOLATION
#   Sections A–E touch no database at all — they are unit tests, a built
#   binary's stdout, and greps over the tree. Section F builds a throwaway git
#   repo and its own XDG_CONFIG_HOME under `mktemp -d`; no real DB, ledger or
#   config is read or written.
#
# Layers:
#   A. FAIL-FAST unit — the new package, its CLI seam, and every package the
#      conversion touched. Nothing below is worth running if these fail.
#   B. The registry's claims — group membership pinned, the gate pinned to a
#      group, the transition table's writers.
#   C. The CLI seam answers, and answers the shapes the client parses.
#   D. The sweep — C2.
#   E. Python holds no vocabulary — C4.
#   F. Byte-identical rendering — C1.
#   G. Project-wide regression.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT=""        # worktree root
EGO=""       # the worktree's freshly built endless-go
TMP=""       # section F scratch

cleanup() { [[ -n "${TMP}" ]] && rm -rf "${TMP}"; return 0; }
trap cleanup EXIT

# ── setup ───────────────────────────────────────────────────────────────────

WT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
[[ -f "${WT}/go.mod" && -d "${WT}/internal/sessionstate" ]] \
    || setup_error "worktree root not found from ${BASH_SOURCE[0]} (got ${WT})"
cd "${WT}" || setup_error "cannot cd to ${WT}"
EGO="${WT}/bin/endless-go"

# assert_ok DESC CMD... — the command exits 0.
assert_ok() {
    local desc="$1"; shift
    local out rc
    out="$("$@" 2>&1)"; rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(printf '%s' "${out}" | tail -15)"
    return 1
}

# ── A. fail-fast unit ───────────────────────────────────────────────────────

section "A — the new package and every package the conversion touched (FAIL-FAST)"

assert_ok "just go builds the worktree binaries" just go
[[ -x "${EGO}" ]] || setup_error "bin/endless-go was not built"

if ! assert_ok "go test ./internal/sessionstate — the registry and its table" \
        go test ./internal/sessionstate/ -count=1; then
    report_fail "FAIL-FAST" "the registry's own tests pass" \
        "they do not; nothing below is meaningful"
    summary
fi
assert_ok "go test ./internal/sessionstatecmd — the CLI seam" \
    go test ./internal/sessionstatecmd/ -count=1
assert_ok "go test ./internal/hookcmd — the declaration gate" \
    go test ./internal/hookcmd/ -count=1
assert_ok "go test ./internal/monitor — the session lifecycle writers and readers" \
    go test ./internal/monitor/ -count=1
assert_ok "go test ./internal/events — the claim revive and the live-session lookups" \
    go test ./internal/events/ -count=1
assert_ok "go test ./internal/projectstatuscmd — the attention board's classify()" \
    go test ./internal/projectstatuscmd/ -count=1
assert_ok "go test ./internal/sandboxcmd — the worktree sandbox seed" \
    go test ./internal/sandboxcmd/ -count=1

# ── B. the registry's claims ────────────────────────────────────────────────

section "B — the claims the registry makes about itself"

assert_ok "group membership is pinned exactly (C1: relocation, not redefinition)" \
    go test ./internal/sessionstate/ -count=1 -run TestGroupMembershipIsPinned
assert_ok "labels and glyphs moved from session_cmd.py unchanged" \
    go test ./internal/sessionstate/ -count=1 -run TestLabelsAndGlyphsArePinned
assert_ok "Live is All minus ended — a membership list, not a negation" \
    go test ./internal/sessionstate/ -count=1 -run TestLiveIsAllMinusEnded
assert_ok "MayWrite is a subset of Live — an ended session may never write" \
    go test ./internal/sessionstate/ -count=1 -run TestMayWriteIsASubsetOfLive
assert_ok "every state has a display rank, so no row sorts by NULL" \
    go test ./internal/sessionstate/ -count=1 -run TestDisplayOrderCoversAll
assert_ok "C3: the declaration gate answers whatever MayWrite says, for every state" \
    go test ./internal/hookcmd/ -count=1 -run TestSessionMayWriteFollowsTheGroup
assert_ok "the transition table: every state is written, and every state has a way out" \
    go test ./internal/sessionstate/ -count=1 -run 'TestEveryStateIsWritten|TestEveryStateHasAWayOut'
assert_ok "every transition names the function that performs the write" \
    go test ./internal/sessionstate/ -count=1 -run TestEveryTriggerNamesItsWriter

# Each writer the table names must still exist under the name it names. The
# table is documentation; documentation that has gone stale is worse than none,
# and these are the six functions the whole conversion turns on.
section "B2 — every writer the transition table names still exists"
for writer in \
    "monitor.InitSession:internal/monitor/session.go:func InitSession" \
    "monitor.TouchSession:internal/monitor/session.go:func TouchSession" \
    "monitor.BindSessionToTask:internal/monitor/session.go:func BindSessionToTask" \
    "monitor.StartChatSession:internal/monitor/session.go:func StartChatSession" \
    "monitor.WakeSession:internal/monitor/session.go:func WakeSession" \
    "monitor.IdleSession:internal/monitor/session.go:func IdleSession" \
    "monitor.EndSession:internal/monitor/session.go:func EndSession" \
    "monitor.CompleteTask:internal/monitor/session.go:func CompleteTask" \
    "events.execTaskClaimed:internal/events/executor.go:func execTaskClaimed" \
    "sandboxcmd.seedFromWorktree:internal/sandboxcmd/seed_worktree.go:func seedFromWorktree"
do
    name="${writer%%:*}"; rest="${writer#*:}"
    file="${rest%%:*}"; decl="${rest#*:}"
    if grep -q "^${decl}(" "${WT}/${file}" 2>/dev/null; then
        report_pass "${name} exists in ${file}"
    else
        report_fail "${name} exists in ${file}" "${decl}( in ${file}" "not found"
    fi
    if grep -qF "${name}" "${WT}/internal/sessionstate/transitions.go"; then
        report_pass "${name} is named in the transition table"
    else
        report_fail "${name} is named in the transition table" \
            "a Trigger mentioning ${name}" "absent"
    fi
done

# ── C. the CLI seam ─────────────────────────────────────────────────────────

section "C — endless-go session-state answers, in the shapes the client parses"

assert_eq "groups lists every group name, one per line" \
    "all
awaits-human
display-order
live
may-write" "$("${EGO}" session-state groups)"

assert_eq "get all is the vocabulary in lifecycle order" \
    "working
idle
needs_input
ended" "$("${EGO}" session-state get all)"

assert_eq "get display-order is the reading order, not the lifecycle order" \
    "working
needs_input
idle
ended" "$("${EGO}" session-state get display-order)"

assert_eq "sql-list live renders the 29-site predicate as a membership list" \
    "'working','idle','needs_input'" "$("${EGO}" session-state sql-list live)"

assert_eq "sql-list may-write is the declaration gate's admission set" \
    "'working','idle'" "$("${EGO}" session-state sql-list may-write)"

"${EGO}" session-state has may-write idle >/dev/null 2>&1
assert_eq "has may-write idle exits 0" "0" "$?"
"${EGO}" session-state has may-write needs_input >/dev/null 2>&1
assert_eq "has may-write needs_input exits 1 — an ANSWER, not an error" "1" "$?"
"${EGO}" session-state has may-write prompted >/dev/null 2>&1
assert_eq "has may-write <not-a-state> exits 2 — a typo is never a silent no" "2" "$?"

assert_eq "glyph working is the glyph session list prints" "⟳" \
    "$("${EGO}" session-state glyph working)"
assert_eq "glyph of a non-state is the ⁇ marker — how Python gets it without a copy" \
    "⁇" "$("${EGO}" session-state glyph "")"
assert_eq "label needs_input" "Needs Input" "$("${EGO}" session-state label needs_input)"
assert_eq "rank display-order ended" "3" "$("${EGO}" session-state rank display-order ended)"
assert_eq "rank of a non-member is -1, not an error" "-1" \
    "$("${EGO}" session-state rank may-write ended)"

assert_eq "transitions emits one tab-separated edge per line" "7" \
    "$("${EGO}" session-state transitions | wc -l | tr -d ' ')"
assert_contains "the revive edge (E-1686) is in the table" \
    "ended	needs_input	" "$("${EGO}" session-state transitions)"
assert_contains "a creating write leaves the from column empty" \
    "	working	\`task claim\`" "$("${EGO}" session-state transitions)"

assert_contains "the usage text derives its group list from the registry" \
    "awaits-human" "$("${EGO}" session-state --help 2>&1)"

# ── D. the sweep (C2) ───────────────────────────────────────────────────────

section "D — no session-state literal survives outside the package and the schema"

# Go and Python only. The schema is excluded by design: internal/schema/*.sql
# cannot reference a Go constant, and internal/schema/changes/ is frozen
# history — a landed migration describes the database as it was, and editing one
# rewrites the past. Test files are excluded because a test that pins a literal
# is pinning the literal on purpose.
sweep() {
    grep -r --include='*.go' --include='*.py' -E "['\"](working|idle|needs_input|ended)['\"]" \
        internal cmd src 2>/dev/null \
        | grep -v '^internal/sessionstate/' \
        | grep -v '^internal/sessionstatecmd/' \
        | grep -v '_test\.go:' \
        | grep -v '^internal/schema/changes/' \
        | grep -vE '^[^:]+:[[:space:]]*(//|#|--)' \
        | sort
}

# The four residents, and why each is not the vocabulary. Compared as a set so
# a NEW literal fails, and so does one of these quietly disappearing — that
# would mean somebody wired one of them to the registry, which is a decision
# worth making in the open rather than by drift.
EXPECTED_RESIDENTS="internal/monitor/project_status.go
internal/monitor/usermachinelog.go
internal/projectstatuscmd/board.go
internal/projectstatuscmd/board.go"

assert_eq "the sweep leaves exactly the four documented residents" \
    "${EXPECTED_RESIDENTS}" "$(sweep | cut -d: -f1)"

assert_contains "resident 1: boardSessionStates — a temporary exclusion of needs_input, not a durable group (E-2091 deletes it)" \
    "const boardSessionStates" "$(sweep)"
assert_contains "resident 2: SessionLogIdle — a session-log REASON slug that happens to spell 'idle'" \
    "SessionLogIdle" "$(sweep)"
assert_contains "resident 3+4: the board's actionMeta nouns — display words, as E-1891 left 'unverified' and 'submitted' beside them" \
    "actDoing:" "$(sweep)"

# The negation the groups replaced must be gone from live code: it is the shape
# that silently classifies a new state as live.
assert_eq "no live Go or Python code still asks \`state != 'ended'\`" "" \
    "$(grep -r --include='*.go' --include='*.py' -E "state ?!= ?'ended'" internal cmd src 2>/dev/null \
        | grep -v '_test\.go:' | grep -v '^internal/schema/changes/' \
        | grep -v '^internal/sessionstate/' \
        | grep -vE '^[^:]+:[[:space:]]*(//|#|--)')"

assert_eq "hookcmd no longer keeps its own copies of the state names" "" \
    "$(grep -rn 'stateWorking\|stateNeedsInput' internal/hookcmd/ 2>/dev/null)"

# ── E. Python holds no vocabulary (C4) ──────────────────────────────────────

section "E — the Python client is a pass-through and nothing more"

PY_AST_CHECK=$(cat <<'PYEOF'
import ast, sys
STATES = {"working", "idle", "needs_input", "ended"}
src = open("src/endless/session_states.py").read()
tree = ast.parse(src)
problems = []
for node in ast.walk(tree):
    if isinstance(node, ast.Import):
        for a in node.names:
            if a.name.split(".")[0] in {"sqlite3"} or a.name.endswith(".db"):
                problems.append(f"imports {a.name}")
    if isinstance(node, ast.ImportFrom):
        mod = node.module or ""
        for a in node.names:
            if a.name == "db" or mod.endswith(".db") or mod == "sqlite3":
                problems.append(f"imports {mod}.{a.name}")
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        if node.value in STATES:
            problems.append(f"names the state {node.value!r} as a literal")
print("\n".join(problems))
PYEOF
)
assert_eq "session_states.py declares no state and touches no database" "" \
    "$(cd "${WT}" && python3 -c "${PY_AST_CHECK}")"

assert_eq "session_states.py holds no group list either" "" \
    "$(grep -nE "^(SESSION_STATE_GROUPS|GROUPS) *=" src/endless/session_states.py)"

assert_contains "cli.py builds --state's choice from the registry" \
    "click.Choice(SESSION_STATES)" "$(cat src/endless/cli.py)"

assert_contains "session_cmd.py builds its glyph map from the registry" \
    "session_states.glyph(state)" "$(cat src/endless/session_cmd.py)"

assert_contains "session_cmd.py builds the sort CASE from DisplayOrder's rank" \
    'session_states.get("display-order")' "$(cat src/endless/session_cmd.py)"

# §7, folded in: the obsolete duplicate table with the banned CHECK.
assert_eq "db.py no longer carries a second CREATE TABLE sessions" "" \
    "$(grep -n 'CREATE TABLE IF NOT EXISTS sessions' src/endless/db.py)"
assert_eq "no CHECK constraint on state survives in Python" "" \
    "$(grep -n 'CHECK (state IN' src/endless/db.py)"

# ── F. byte-identical rendering (C1) ────────────────────────────────────────

section "F — session list renders byte-for-byte what it rendered before"

TMP="$(mktemp -d)" || setup_error "mktemp failed"
REPO="${TMP}/repo"
mkdir -p "${REPO}" "${TMP}/xdg/endless"
# The runner already isolated HOME and XDG_CONFIG_HOME; this narrows further to
# a database of this section's own making, and hands the runner's back before G
# so the regression layer runs in the environment the runner built for it.
SAVED_XDG_CONFIG="${XDG_CONFIG_HOME:-}"
SAVED_XDG_CACHE="${XDG_CACHE_HOME:-}"
export XDG_CONFIG_HOME="${TMP}/xdg"
export XDG_CACHE_HOME="${TMP}/cache"
export PATH="${WT}/bin:${PATH}"

git -C "${REPO}" init -q                    >/dev/null 2>&1
git -C "${REPO}" config user.email verify@test
git -C "${REPO}" config user.name verify
git -C "${REPO}" commit -q --allow-empty -m init >/dev/null 2>&1

E() { ( cd "${REPO}" && uv run --project "${WT}" endless "$@" ); }

E project register "${REPO}" --name probe --label Probe --desc d \
    --lang Go --status active >/dev/null 2>&1
PID="$(E sql "SELECT id FROM projects WHERE name='probe'" --tsv 2>/dev/null)"
[[ -n "${PID}" ]] || setup_error "could not register the throwaway project"

E sql "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
   (8001,${PID},'Working session task','underway','now'),
   (8002,${PID},'Idle session task','underway','now'),
   (8003,${PID},'Needs input session task','underway','now'),
   (8004,${PID},'Ended session task','underway','now')" --write >/dev/null 2>&1

# One session per state, inserted OLDEST-state-first by id so the rendered order
# can only come from the sort, never from insertion order.
E sql "INSERT INTO sessions (id, session_id, project_id, state, task_id, started_at, last_activity) VALUES
   (8101,'uuid-8101-aaaaaaaa',${PID},'ended',      8004,'2026-01-01T00:00:01','2026-01-01T00:00:01'),
   (8102,'uuid-8102-bbbbbbbb',${PID},'idle',       8002,'2026-01-01T00:00:02','2026-01-01T00:00:02'),
   (8103,'uuid-8103-cccccccc',${PID},'needs_input',8003,'2026-01-01T00:00:03','2026-01-01T00:00:03'),
   (8104,'uuid-8104-dddddddd',${PID},'working',    8001,'2026-01-01T00:00:04','2026-01-01T00:00:04')" \
   --write >/dev/null 2>&1

E sql "INSERT INTO session_messages (session_id, role, content, created_at) VALUES
   ('uuid-8101-aaaaaaaa','user','hello','2026-01-01T00:00:01'),
   ('uuid-8102-bbbbbbbb','user','hello','2026-01-01T00:00:02'),
   ('uuid-8103-cccccccc','user','hello','2026-01-01T00:00:03'),
   ('uuid-8104-dddddddd','user','hello','2026-01-01T00:00:04')" --write >/dev/null 2>&1

[[ "$(E sql "SELECT count(*) FROM sessions" --tsv 2>/dev/null)" == "4" ]] \
    || setup_error "the throwaway database was not seeded"

# The expectations below were captured by running this same fixture against the
# PRE-CHANGE build (git archive of the parent commit, its own endless-go, its
# own src/ on PYTHONPATH). They are what "pure refactor" means, stated as bytes.
EXPECT_TABLE="$(cat <<'GOLDEN'

Sessions — project: probe
ID    ◆  Task    Msgs  Title
────  ─  ──────  ────  ─────────────────────────────────────────────────────────────────────────────────────────────────
8104  ⟳  E-8001     1  Working session task
8103  ?  E-8003     1  Needs input session task
8102  ‖  E-8002     1  Idle session task
8101  ␥  E-8004     1  Ended session task

⟳ working   ‖ idle   ? needs input   ␥ ended
GOLDEN
)"

assert_eq "session list --all: rows, glyphs, sort order and legend are unchanged" \
    "${EXPECT_TABLE}" "$(E session list --all 2>&1)"

EXPECT_FILTERED="$(cat <<'GOLDEN'

Sessions — project: probe
ID    ◆  Task    Msgs  Title
────  ─  ──────  ────  ─────────────────────────────────────────────────────────────────────────────────────────────────
8103  ?  E-8003     1  Needs input session task

⟳ working   ‖ idle   ? needs input   ␥ ended
GOLDEN
)"

assert_eq "session list --state needs_input: the Click choice still accepts every state" \
    "${EXPECT_FILTERED}" "$(E session list --all --state needs_input 2>&1)"

assert_contains "session list --json still emits the raw state word, not the glyph" \
    '"state": "needs_input"' "$(E session list --all --json 2>&1)"
assert_eq "session list --json orders rows exactly as the table does" \
    "working needs_input idle ended" \
    "$(E session list --all --json 2>&1 | grep '"state"' | sed 's/.*: "//;s/".*//' | tr '\n' ' ' | sed 's/ $//')"

assert_contains "an unknown --state is rejected naming every real state" \
    "'bogus' is not one of 'working', 'idle', 'needs_input', 'ended'" \
    "$(E session list --all --state bogus 2>&1 || true)"

export XDG_CONFIG_HOME="${SAVED_XDG_CONFIG}"
export XDG_CACHE_HOME="${SAVED_XDG_CACHE}"

# ── G. project-wide regression ──────────────────────────────────────────────

section "G — project-wide regression"

assert_ok "go vet ./..." go vet ./...
assert_ok "go test ./... (the whole Go suite)" go test ./... -count=1
assert_ok "just test (the whole Python suite)" just test
assert_ok "just guide-check (the guide cross-reference is current)" just guide-check
assert_ok "just lifecycle-check (the status diagram has not drifted)" just lifecycle-check

summary
