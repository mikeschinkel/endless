#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-940 and records what was true when E-940
# landed. Edit it only if you ARE E-940. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-940 (merged with E-1703) — the PreToolUse write-target gate: a session
# holding a claimed worktree may not write outside it, whether by Write/Edit or
# by a recognized Bash write, and both are judged by one decision.
#
# Drives THIS worktree's `endless-go hook claude` against an isolated database
# (--db-dir under a temp dir) seeded with a registered project, a git main
# checkout, a linked task worktree, and a working session holding that task.
# Every refusal is asserted as exit 2 PLUS its specific text, and every block
# case is paired with a pass-through control, so a binary that fails to launch
# cannot pass as a gate that fired.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go sqlite3 python3 git; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP=$(mktemp -d)
trap 'rm -rf "${WORK_TMP}"' EXIT
# Resolved, so paths match what the hook compares after resolving symlinks.
WORK_TMP=$(cd "${WORK_TMP}" && pwd -P)

BIN="${WORK_TMP}/endless-go"
CFG="${WORK_TMP}/cfg"
PROJ="${WORK_TMP}/proj"
TID=424240
WT="${PROJ}/.endless/worktrees/e-${TID}"
SID="e940-verify-session"

OUTSIDE="is outside your worktree"
HOOK_OUT=""
HOOK_RC=0

# ── 1. unit tests, fail fast ─────────────────────────────────────────────────
section "1. Unit tests (fail fast)"
if out=$(go test ./internal/hookcmd/ -count=1 \
        -run 'LexShell|BashWrite|WriteTargetDecision|ResolveWriteTarget|CommitDir|CommitRunsOnMain|LandedSuite|DocMirror|CdRedirect' 2>&1); then
    report_pass "go test ./internal/hookcmd (lexer, extraction, decision, equivalence)"
else
    report_fail "go test ./internal/hookcmd" "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if go build -o "${BIN}" ./cmd/endless-go >"${WORK_TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (this worktree's hook)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -20 "${WORK_TMP}/build.log")"
    summary
fi

# ── fixture ──────────────────────────────────────────────────────────────────
git_q() {
    GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
    GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t \
        git "$@" >/dev/null 2>&1
}
mkdir -p "${CFG}" "${WORK_TMP}/prime"
git_q init -q --initial-branch=main "${PROJ}" || setup_error "git init"
printf 'readme\n' >"${PROJ}/README.md"
git_q -C "${PROJ}" add README.md && git_q -C "${PROJ}" commit -q -m init || setup_error "git commit"
git_q -C "${PROJ}" worktree add -q -b "task/${TID}" "${WT}" || setup_error "git worktree add"

# Prime the schema from a neutral directory, with a throwaway session id.
printf '{"session_id":"e940-prime","cwd":"%s","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{}}' \
    "${WORK_TMP}/prime" | "${BIN}" --db-dir "${CFG}" hook claude >/dev/null 2>&1
[[ -f "${CFG}/endless.db" ]] || setup_error "temp database was not created"
db() { sqlite3 "${CFG}/endless.db" "$1"; }
db "INSERT INTO projects (id, name, path) VALUES (940, 'e940demo', '${PROJ}');" || setup_error "seed project"
db "INSERT INTO tasks (id, project_id, title, status) VALUES (${TID}, 940, 'e940 demo', 'underway');" || setup_error "seed task"
db "INSERT INTO sessions (session_id, project_id, task_id, state) VALUES ('${SID}', 940, ${TID}, 'working');" || setup_error "seed session"

# hook TOOL INPUT_JSON [CWD] — one PreToolUse call; sets HOOK_OUT and HOOK_RC.
hook() {
    local payload
    payload=$(python3 -c 'import json,sys; print(json.dumps({"session_id":sys.argv[1],"cwd":sys.argv[2],
        "hook_event_name":"PreToolUse","tool_name":sys.argv[3],"tool_input":json.loads(sys.argv[4])}))' \
        "${HOOK_SID:-${SID}}" "${3:-${WT}}" "$1" "$2")
    HOOK_OUT=$(printf '%s' "${payload}" | "${BIN}" --db-dir "${CFG}" hook claude 2>&1)
    HOOK_RC=$?
}
bash_hook() { hook Bash "$(python3 -c 'import json,sys; print(json.dumps({"command":sys.argv[1]}))' "$1")"; }
write_hook() { hook Write "$(python3 -c 'import json,sys; print(json.dumps({"file_path":sys.argv[1],"content":"x"}))' "$1")"; }

# blocked DESC NEEDLE… — exit 2 and every needle present.
blocked() {
    local desc="$1"; shift
    local n
    for n in "$@"; do
        if [[ "${HOOK_OUT}" != *"${n}"* ]]; then
            report_fail "${desc}" "exit 2 with: ${n}" "rc=${HOOK_RC} | ${HOOK_OUT}"
            return
        fi
    done
    if [[ "${HOOK_RC}" -eq 2 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit 2" "rc=${HOOK_RC} | ${HOOK_OUT}"
    fi
}
# allowed DESC — exit 0, and the containment refusal nowhere in the output.
allowed() {
    if [[ "${HOOK_RC}" -eq 0 && "${HOOK_OUT}" != *"${OUTSIDE}"* ]]; then
        report_pass "$1"
    else
        report_fail "$1" "exit 0, no refusal" "rc=${HOOK_RC} | ${HOOK_OUT}"
    fi
}

# ── 2. Bash writes outside the worktree are refused ─────────────────────────
section "2. Bash writes to the main checkout are refused, naming the construct"
bash_hook "sed -i 's/a/b/' ${PROJ}/README.md"
blocked "sed -i on main's README" "\`sed -i\` would write" "README.md ${OUTSIDE}" "E-${TID}"
bash_hook "echo x > ${PROJ}/foo"
blocked "echo > main/foo" "\`>\` would write" "${OUTSIDE}"
bash_hook $'cat > '"${PROJ}"$'/foo <<\'EOF\'\nbody\nEOF'
blocked "cat > main/foo <<EOF (heredoc to a file)" "\`>\` would write" "${OUTSIDE}"
bash_hook "cd ${PROJ} && rm README.md"
blocked "cd main && rm README.md (directory tracking)" "\`rm\` would write" "${OUTSIDE}"
bash_hook "cd ${PROJ} && git restore ."
blocked "git restore in main (on trial) names its off switch" "\`git restore\` would write" '"bash_git_writes": false'

# ── 3. …and everything the gate must leave alone is allowed ─────────────────
section "3. Worktree writes, temp, devices, reads and mentions are allowed"
bash_hook "echo x > ./foo"
allowed "echo > ./foo inside the worktree"
bash_hook "echo x > ${WT}/sub/foo"
allowed "absolute path inside the worktree"
bash_hook "echo x > /tmp/e940-x; echo y > ${WORK_TMP}/scratch-x"
allowed "> /tmp and > \$TMPDIR scratch"
bash_hook "ls > /dev/null 2>&1"
allowed "> /dev/null"
bash_hook "cat ${PROJ}/README.md; grep x ${PROJ}/README.md < ${PROJ}/README.md"
allowed "reading main (cat, grep, < input)"
bash_hook "git commit -m 'a > b'"
allowed "git commit -m \"a > b\" in the worktree"
bash_hook "echo \"sed -i x ${PROJ}/f\""
allowed "echo \"sed -i … main\" (a mention, not an invocation)"
bash_hook "git restore ."
allowed "git restore inside the worktree"
bash_hook "echo x > \"\$OUT\""
allowed "dynamic target > \"\$OUT\" (unresolvable: allowed)"

# ── 4. Write/Edit: the same decision, the same message ──────────────────────
section "4. Write/Edit targets — the same decision and the same message"
write_hook "${PROJ}/foo"
write_out="${HOOK_OUT}"
blocked "Write to main/foo" "BLOCKED: " "${OUTSIDE}" "E-${TID}"
bash_hook "echo x > ${PROJ}/foo"
if [[ "${HOOK_OUT}" == *"${write_out}" ]]; then
    report_pass "Bash refusal carries the Write refusal's body verbatim"
else
    report_fail "Bash refusal carries the Write refusal's body verbatim" "${write_out}" "${HOOK_OUT}"
fi
write_hook "${WT}/new.go"
allowed "Write inside the worktree"
write_hook "${WORK_TMP}/scratch.txt"
allowed "Write to a temp dir outside the project"
write_hook "${WT}/.endless/tasks/e-${TID}/plan.md"
blocked "Write to a doc mirror in the worktree keeps its own refusal" "document mirror"

# ── 5. Scope: only a session holding a claimed worktree ─────────────────────
section "5. Taskless and terminal sessions keep the rules they had"
db "UPDATE tasks SET status='confirmed' WHERE id=${TID};"
bash_hook "echo x > ${PROJ}/foo"
allowed "terminal task: Bash write to main not judged by this gate"
db "UPDATE tasks SET status='underway' WHERE id=${TID};"
db "INSERT INTO sessions (session_id, project_id, state) VALUES ('e940-taskless', 940, 'working');"
HOOK_SID=e940-taskless bash_hook "cd ${PROJ} && echo x > foo"
allowed "taskless session: Bash write to main not judged by this gate"

# ── 6. The on-trial git rule turns off with one config line ─────────────────
section "6. bash_git_writes=false turns the git mutators off, nothing else"
mkdir -p "${PROJ}/.endless"
printf '{"checks": {"bash_git_writes": false}}\n' >"${PROJ}/.endless/config.json"
bash_hook "cd ${PROJ} && git restore ."
allowed "git restore in main allowed with the key off"
bash_hook "cd ${PROJ} && rm README.md"
blocked "rm in main still refused with the key off" "\`rm\` would write" "${OUTSIDE}"
rm -f "${PROJ}/.endless/config.json"

summary
