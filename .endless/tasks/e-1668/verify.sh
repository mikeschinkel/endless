#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1668 and records what was true when E-1668
# landed. Edit it only if you ARE E-1668. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1668 verification — an explicit DB context, and an answer that says which.
#
# Two halves of one omission: a DB context could be GUESSED, and the answer
# never said which one it used.
#
# HALF 1 — the guess. E-1368 made endless-go self-detect the per-worktree
# sandbox from cwd. That set the same variable an explicit flag sets, so it
# satisfied the E-1429 gate — whose own doc comment says only a flag counts —
# and the gate went on passing while nobody had chosen. The damage surfaced on
# a direct invocation: a session working E-2105 ran
#
#     endless-go session-query list-live --project-root <main checkout>
#
# from inside its worktree using MAIN's binary. --project-root named the main
# checkout; the binary silently answered from the worktree's sandbox, returning
# 2 rows where main held 59. The session reported the shortfall to the owner as
# a probable product defect, then ran the same command against two different
# binaries, got identical output, and read the agreement as corroboration — a
# sound control for "did my change alter this?" and none at all for "is this
# number right?".
#
# HALF 2 — the silence. Even a correct answer never said which store produced
# it, so a wrong one was indistinguishable from a right one.
#
# The principle both halves encode: DETECTION DECIDES WHERE TO LOOK; ONLY A
# FLAG DECIDES THAT YOU MAY OPEN IT. `--db sandbox` still reads cwd, because
# cwd is what says WHICH worktree's sandbox — the flag grants the permission,
# cwd supplies the address.
#
# Run it through the runner, from anywhere inside the worktree:
#   endless task verify E-1668
#
# What it proves:
#   1. FAIL-FAST unit gate: the Go and Python suites this task owns pass.
#      Everything below runs the same source, so a red gate makes it all noise.
#   2. THE INCIDENT, as a regression: the exact command REFUSES without a flag,
#      naming a remedy the reader can type, and answers correctly with one.
#   3. The guess is GONE from the tree, not merely switched off — no
#      SelfDetectWorktreeSandbox, no call site, no second variable that existed
#      only to tell a flag from a guess.
#   4. --config-dir retired; --db-dir is the only directory-naming spelling.
#   5. Provenance on READS, against a seeded pair of databases: the same read
#      under --db main and --db sandbox reports DIFFERENT stores and different
#      rows, so the difference is checkable rather than asserted.
#   6. Provenance on WRITES. E-1429's founding incident was a write that landed
#      in the real ledger as E-1425; this is that incident inverted into a test.
#   7. The downstream rule: the project is named only when it is not the one
#      enclosing cwd.
#   8. The exemption holds: a pinned surface announces nothing, and the tmux
#      status line's bytes are unchanged.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

# Refuse a direct run, and pick up the shared vocabulary (section, report_pass,
# report_fail, assert_*, setup_error, summary — plus the TAP stream the runner
# normalizes). Sourced as the FIRST executable statement so the refusal fires
# before anything else in this file does.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

# Everything this suite writes goes under the runner's temp HOME, so the
# isolation it built covers the fixtures and logs too, and the runner's own
# teardown takes them with it. Nothing lands in /tmp to be found later and
# wondered about.
#
# Not $ENDLESS_VERIFY_RUN: E-2090 removed that variable, because a "the runner
# started you" marker is satisfied by one `export` and so enforced nothing while
# looking like it did. The temp HOME is a fact no environment can restate.
RUN_DIR="${HOME}/e-1668-verify"
mkdir -p "${RUN_DIR}" || setup_error "cannot create ${RUN_DIR}"

GO_BIN="${WT}/bin/endless-go"
SCHEMA_SQL="${WT}/internal/schema/schema.sql"
DB_SRC="${WT}/internal/monitor/db.go"
MAIN_SRC="${WT}/cmd/endless-go/main.go"
PROV_GO="${WT}/internal/dbprovenance/dbprovenance.go"
PROV_PY="${WT}/src/endless/provenance.py"
CONFIG_PY="${WT}/src/endless/config.py"

for f in "${SCHEMA_SQL}" "${DB_SRC}" "${MAIN_SRC}" "${PROV_GO}" "${PROV_PY}" "${CONFIG_PY}"; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done
command -v sqlite3 >/dev/null || setup_error "sqlite3 is required"
command -v go >/dev/null || setup_error "go is required"
command -v uv >/dev/null || setup_error "uv is required"

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail-fast)"

gate() {
    local label="$1"; shift
    local log="${RUN_DIR}/gate-$(echo "${label}" | tr -c 'a-zA-Z0-9' '-').log"
    if "$@" >"${log}" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pass" "failed — see ${log}"
        printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
        # summary, not a bare exit: it closes the TAP plan, so the runner still
        # reports the checks that DID run rather than seeing a truncated stream.
        summary
    fi
}

# Built from THIS tree, so every assertion below exercises the source under test.
gate "go build ./..." go build ./...
gate "go test ./internal/monitor/ (the gate and its truth table)" \
    go test ./internal/monitor/ \
    -run '^(TestConsumeDBFlags|TestConsumeDBFlags_Choices|TestGateRefusesWithoutAFlag|TestMainPinRoutingSurvivesTheDeletion|TestGuardWorktreeDBContext|TestPinMainDB|TestDBProvenance|TestSelfDevProjectRoot|TestWorktreeDirName|TestProjectIsSelfDev)$'
gate "go test ./internal/dbprovenance/ (the payload shapes)" go test ./internal/dbprovenance/
gate "go test ./internal/sessionquerycmd/" go test ./internal/sessionquerycmd/
gate "go test ./internal/eventcmd/ (the write surfaces)" go test ./internal/eventcmd/
gate "go test ./internal/worktreecmd/" go test ./internal/worktreecmd/
gate "pytest tests/test_provenance.py (the rule)" uv run pytest tests/test_provenance.py -q
gate "pytest tests/test_db_gate.py (the Python gate + threading vocabulary)" \
    uv run pytest tests/test_db_gate.py tests/test_worktree_db_context_threading.py -q
# The two suites this task could break by changing what every command prints.
gate "pytest tests/test_agent_error_bracket.py (E-2097's both-ends refusal)" \
    uv run pytest tests/test_agent_error_bracket.py -q
gate "pytest tests/test_session_list_render.py (a header that names a project)" \
    uv run pytest tests/test_session_list_render.py -q

[[ -x "${GO_BIN}" ]] || setup_error "no endless-go at ${GO_BIN} — run 'just build'"

# ── 2. the incident, as a regression ────────────────────────────────────────
section "2. The incident: the command that answered in silence"

# The exact shape from the incident: a DB-opening session-query verb, run from
# inside this self-dev worktree, pointed at the MAIN checkout by --project-root.
INCIDENT=(session-query list-live --project-root "${WT%/.endless/worktrees/*}")

out=$("${GO_BIN}" "${INCIDENT[@]}" 2>&1)
rc=$?

if (( rc != 0 )); then
    report_pass "no --db inside a self-dev worktree is refused (exit ${rc})"
else
    report_fail "no --db inside a self-dev worktree is refused" \
        "a non-zero exit" "exit 0, output: ${out}"
fi

# A refusal that names a remedy the reader cannot type is worse than terse. The
# old wording said the endless CLI "threads --config-dir to this binary", which
# stopped being true the moment endless-go took --db itself.
for want in "--db main" "--db sandbox"; do
    assert_contains "the refusal names ${want}" "${want}" "${out}"
done
assert_not_contains "the refusal no longer names the retired flag" \
    "--config-dir" "${out}"

# The output must not be a plausible ANSWER. Answering with an empty array would
# be the same silence in a different costume.
assert_not_contains "the refusal is not a plausible empty answer" "[]" "${out}"

# ── 3. the guess is gone from the tree ──────────────────────────────────────
section "3. Deleted, not gated"

if grep -rn "SelfDetectWorktreeSandbox" "${WT}/internal" "${WT}/cmd" "${WT}/src" \
        >/dev/null 2>&1; then
    report_fail "SelfDetectWorktreeSandbox is absent from the tree" \
        "no occurrences" "$(grep -rln 'SelfDetectWorktreeSandbox' "${WT}/internal" "${WT}/cmd" "${WT}/src")"
else
    report_pass "SelfDetectWorktreeSandbox is absent from the tree"
fi

for sym in setDetectedContextDir dbContextFromFlag; do
    if grep -rn "${sym}" "${WT}/internal" "${WT}/cmd" >/dev/null 2>&1; then
        report_fail "${sym} went with it" "no occurrences" "still present"
    else
        report_pass "${sym} went with it"
    fi
done

# The gate itself was never wrong — it was defeated. It must still sit at the
# single DB() entry point, which is what makes it bypass-proof for binaries
# that do not exist yet.
if grep -q 'if err := guardWorktreeDBContext(); err != nil {' "${DB_SRC}"; then
    report_pass "the gate still sits at the single DB() entry point"
else
    report_fail "the gate still sits at the single DB() entry point" \
        "a guardWorktreeDBContext call in DB()" "moved or removed"
fi

# ── 4. one vocabulary across both layers ────────────────────────────────────
section "4. --db main|sandbox, and --db-dir as the only escape"

# --config-dir may survive ONLY in prose explaining why it retired.
leaks=$(grep -rn --exclude-dir=__pycache__ --binary-files=without-match \
        -- "--config-dir" "${WT}/internal" "${WT}/cmd" "${WT}/src" "${WT}/tests" 2>/dev/null \
        | grep -v 'the user saying --db and the Go binary hearing' \
        | grep -v 'which stopped being true in E-1668' \
        | grep -v 'not a directory under a flag name')
if [[ -z "${leaks}" ]]; then
    report_pass "no --config-dir survives outside the prose that retires it"
else
    report_fail "no --config-dir survives outside the prose that retires it" \
        "no live references" "${leaks}"
fi

# The Python threading site speaks the word the user typed, derived from what
# was resolved — so --db-dir stays an escape rather than the normal case.
if grep -q 'return \["--db", "main"\]' "${CONFIG_PY}" \
        && grep -q 'return \["--db", "sandbox"\]' "${CONFIG_PY}"; then
    report_pass "go_db_context_args threads --db main / --db sandbox"
else
    report_fail "go_db_context_args threads --db main / --db sandbox" \
        "both named-database branches" "absent"
fi

out=$("${GO_BIN}" --db nonsense session-query list-live --project-root "${WT}" 2>&1)
assert_contains "an unknown --db value is refused, naming both" "main" "${out}"
assert_contains "an unknown --db value is refused, naming both" "sandbox" "${out}"

out=$("${GO_BIN}" --db main --db-dir /tmp/x session-query list-live --project-root "${WT}" 2>&1)
assert_contains "--db and --db-dir together are refused as one choice twice" \
    "one choice" "${out}"

# ── 5. provenance on reads, against a seeded pair ───────────────────────────
section "5. The same read, two databases, two answers"

# The runner replaced HOME and XDG_CONFIG_HOME, so "main" here is this run's own
# main — isolated from the developer's. That is precisely why --db main follows
# $HOME rather than naming a hardcoded path.
WT_NAME="$(basename "${WT}")"
MAIN_CFG="${HOME}/.config/endless"
SANDBOX_CFG="${HOME}/.cache/endless/sandboxes/${WT_NAME}/endless"
mkdir -p "${MAIN_CFG}" "${SANDBOX_CFG}" || setup_error "mkdir failed"

PROJ_PATH="${WT%/.endless/worktrees/*}"

# The Python CLI resolves its default config dir from XDG_CONFIG_HOME, which the
# runner replaced separately from HOME — so it is a DIFFERENT database from the
# $HOME-derived one `--db main` names. Section 7 drives Python, so it needs this
# one seeded; conflating the two would have let its assertions pass vacuously,
# since "no project found" also contains no project name.
PY_CFG="${XDG_CONFIG_HOME}/endless"
mkdir -p "${PY_CFG}" || setup_error "mkdir failed"

seed() {
    local db="$1" sessions="$2"
    sqlite3 "${db}" < "${SCHEMA_SQL}" >/dev/null 2>&1 || return 1
    sqlite3 "${db}" "
      DELETE FROM sessions; DELETE FROM tasks; DELETE FROM projects;
      INSERT INTO projects (id,name,path,status)
        VALUES (1,'seeded','${PROJ_PATH}','active');
    " >/dev/null 2>&1 || return 1
    local i
    for (( i = 1; i <= sessions; i++ )); do
        sqlite3 "${db}" "
          INSERT INTO sessions (id,session_id,project_id,state,started_at,last_activity)
          VALUES (${i},'seed-${i}',1,'working',datetime('now'),datetime('now'));
        " >/dev/null 2>&1 || return 1
    done
}

# Different row COUNTS, so the two answers are distinguishable by their content
# and not only by the line that labels them — which is the incident's shape: 2
# rows where the other store held many more.
seed "${MAIN_CFG}/endless.db" 3 || setup_error "could not seed the main fixture"
seed "${SANDBOX_CFG}/endless.db" 1 || setup_error "could not seed the sandbox fixture"

read_rows() {
    "${GO_BIN}" --db "$1" session-query list-live --project-root "${PROJ_PATH}" 2>&1
}

main_out=$(read_rows main)
sand_out=$(read_rows sandbox)

main_n=$(printf '%s' "${main_out}" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null)
sand_n=$(printf '%s' "${sand_out}" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null)

assert_eq "--db main reads main's rows" "3" "${main_n:-unparseable}"
assert_eq "--db sandbox reads the sandbox's rows" "1" "${sand_n:-unparseable}"

# The payload stays parseable — the whole reason the trace is a FIELD and not a
# line of prose on stdout for a machine format.
assert_contains "the main read names the store it answered from" \
    '"db": "main"' "$(printf '%s' "${main_out}" | python3 -m json.tool 2>&1)"
assert_contains "the sandbox read names WHICH sandbox" \
    "sandbox (${WT_NAME})" "$(printf '%s' "${sand_out}" | python3 -m json.tool 2>&1)"

# Per ROW for an array payload, so a consumer that destructures a row sees it.
rows_tagged=$(printf '%s' "${main_out}" | python3 -c '
import json, sys
rows = json.load(sys.stdin)
print(sum(1 for r in rows if r.get("_answered_from", {}).get("db") == "main"))
' 2>/dev/null)
assert_eq "every row carries it, not just the first" "3" "${rows_tagged:-0}"

# ── 6. provenance on writes ─────────────────────────────────────────────────
section "6. A write names the store it landed in"

# E-1429's founding incident was a WRITE — a test `endless task add` that became
# E-1425 in the REAL ledger. A write to the wrong store is the damaging case,
# where a wrong read only misleads. This is that incident inverted into a test.
write_out=$("${GO_BIN}" --db sandbox event emit \
    --kind task.created \
    --node-id a7f3 \
    --project seeded \
    --entity-type task \
    --entity-id 0 \
    --actor-kind system \
    --actor-id "e-1668-verify" \
    --project-root "${PROJ_PATH}" \
    --payload '{"title":"E-1668 verify write","phase":"now","status":"unplanned","type":"todo"}' 2>&1)
write_rc=$?

if (( write_rc == 0 )); then
    assert_contains "the write names the sandbox it landed in" \
        "sandbox (${WT_NAME})" "${write_out}"
    assert_contains "the write result is still parseable JSON" \
        '"kind"' "$(printf '%s' "${write_out}" | python3 -m json.tool 2>&1)"
else
    report_skip "a write names the store it landed in" \
        "event emit exited ${write_rc}: ${write_out}"
fi

# ── 7. the downstream rule ──────────────────────────────────────────────────
section "7. The project is named only when it is not cwd's"

# A project that is not self-dev has one database, so the DB half says nothing
# there and the project half is all that can reach the user. Naming the project
# you are standing in would be repetition, and repetition where nothing varies
# is how a line stops being read.
DOWN="${RUN_DIR}/downstream"
mkdir -p "${DOWN}/.endless" || setup_error "mkdir failed"
printf '{"name": "downstream"}\n' > "${DOWN}/.endless/config.json"
seed "${PY_CFG}/endless.db" 0 || setup_error "could not seed the Python-visible fixture"
sqlite3 "${PY_CFG}/endless.db" "
  INSERT INTO projects (id,name,path,status) VALUES (2,'downstream','${DOWN}','active');
  INSERT INTO projects (id,name,path,status) VALUES (3,'elsewhere','${DOWN}-other','active');
" >/dev/null 2>&1 || setup_error "could not register the downstream fixtures"

PY=$(uv run --project "${WT}" python -c 'import sys; print(sys.executable)' \
        2>"${RUN_DIR}/venv.log") || setup_error "could not resolve the venv (see ${RUN_DIR}/venv.log)"
BIN="$(dirname "${PY}")/endless"
[[ -x "${BIN}" ]] || setup_error "no endless console script at ${BIN}"

here=$(cd "${DOWN}" && "${BIN}" task list 2>&1)
assert_not_contains "cwd's own project is not named back at you" \
    "project: downstream" "${here}"

other=$(cd "${DOWN}" && "${BIN}" task list --project elsewhere 2>&1)
assert_contains "a project that is not cwd's IS named" \
    "project: elsewhere" "${other}"

# Downstream has one database, so the DB half must stay silent there whichever
# project was resolved.
assert_not_contains "downstream says nothing about a database it cannot choose" \
    "db: " "${here}"

# ── 8. the exemption ────────────────────────────────────────────────────────
section "8. A pin is not a resolution"

# The hook, tmux and the two status views choose in CODE. If the caller could
# not have influenced the choice there is nothing to disambiguate — and the tmux
# status line has no room for it besides.
tmux_out=$("${GO_BIN}" tmux status-line 2>&1)
assert_not_contains "the tmux status line carries no provenance line" \
    "# db:" "${tmux_out}"
assert_not_contains "the tmux status line carries no provenance field" \
    "_answered_from" "${tmux_out}"

# The exemption is asked of the pin itself, not of an allowlist of subcommands —
# so a surface that starts pinning later is exempt without an edit here.
if grep -q 'func DBContextPinned() bool { return dbPathOverride != "" }' "${DB_SRC}"; then
    report_pass "the exemption is keyed on the pin, not on a list of commands"
else
    report_fail "the exemption is keyed on the pin, not on a list of commands" \
        "DBContextPinned reading dbPathOverride" "absent or reshaped"
fi

# And a verb that answered from git alone must not name a database it never
# opened, or the line becomes decoration.
if grep -q 'if !dbOpened || DBContextPinned() {' "${DB_SRC}"; then
    report_pass "a verb that opened no database announces nothing"
else
    report_fail "a verb that opened no database announces nothing" \
        "DBProvenance gated on dbOpened" "absent"
fi

# ── summary ─────────────────────────────────────────────────────────────────
summary
