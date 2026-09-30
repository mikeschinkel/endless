#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1814 and records what was true when E-1814
# landed. Edit it only if you ARE E-1814. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1814 verification — the auto-spawn job starts a Claude session, unasked, on
# a task rated safe to work that way, one per interval, into the tmux session
# the user is actually looking at, and never beyond a per-project cap.
#
# What each section proves, end to end through the real binaries:
#
#   0. The task's unit suites, FAIL-FAST: the selector (one case per
#      eligibility condition, the cap, ordering, every skip), the note column,
#      the argv builders, the SessionStart provenance write, the config split,
#      the monitor marker, the migration.
#   A. Off by default. A project that has not opted in spawns nothing, and the
#      run says why.
#   B. One spawn per due run: urgent before now, detached (-d), into the
#      session of the most recently active attached client, window marked
#      auto-spawned — and the task is underway with a worktree. A second
#      immediate run is not due.
#   C. SessionStart flags the session that binds in that window as
#      auto-spawned, and only it.
#   D. The cap: auto-spawned work underway or unverified blocks the next spawn;
#      settling one frees a slot.
#   E. No attached client: the run skips and the task stays ready.
#   F. The property: every task the selector hands to `task spawn`, `task
#      spawn` accepts. Driven to exhaustion over a mixed set; every run is ok,
#      and exactly the eligible tasks leave `ready`.
#   G. `jobs list` shows the NOTE column; nothing launched Claude.
#
# Isolation: a throwaway git repo registered as the project, the runner's temp
# HOME/XDG (its own DB), and a FAKE tmux first on PATH that records every argv
# and answers only what these paths ask. No real tmux server is touched and no
# Claude starts.
#
#   endless task verify E-1814
#
# Exit 0 on all-passed, 1 on any failure, 2 on a setup problem.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

# -P: macOS's mktemp hands back /var/..., a symlink to /private/var/....
TMP="$(cd "$(mktemp -d)" && pwd -P)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 0. unit gate (fail fast) ────────────────────────────────────────────────
section "0. Unit suites (fail fast)"

if go test ./internal/autospawnjob/ ./internal/jobs/ ./internal/config/ \
        ./internal/tasktype/ ./internal/spawnlaunchcmd/ ./internal/hookcmd/ \
        ./internal/projectstatuscmd/ ./internal/minimizerjob/ ./internal/schema/... \
        >"${TMP}/go.log" 2>&1; then
    report_pass "go test — selector, notes, argv builders, provenance, config, marker, migration"
else
    report_fail "go test (E-1814 packages)" "exit 0" "$(tail -30 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q tests/test_spawn_foreground.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest tests/test_spawn_foreground.py — task spawn --auto / --target-session"
else
    report_fail "pytest tests/test_spawn_foreground.py" "exit 0" "$(tail -30 "${TMP}/py.log")"
    summary
fi

mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the binary every section drives)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -30 "${TMP}/build.log")"
    summary
fi

# ── fixture ─────────────────────────────────────────────────────────────────
REPO="${TMP}/repo"
STUB="${TMP}/stub"
TMUX_LOG="${TMP}/tmux.log"
CLIENTS="${TMP}/clients"          # what `tmux list-clients` answers
WIN_AUTO="${TMP}/win-auto"        # what the window says for @endless_auto_spawned
WIN_TASK="${TMP}/win-task"        # what the window says for @endless_task_id
LAUNCHED="${TMP}/claude-launched"
# The runner's temp HOME, not a directory of our own: the SessionStart hook pins
# the main database at $HOME/.config/endless whatever XDG says, and every
# process here must open the same one.
export XDG_CONFIG_HOME="${HOME}/.config"
export XDG_CACHE_HOME="${TMP}/cache"
unset TMUX TMUX_PANE ENDLESS_SESSION_ID ENDLESS_NO_JOBS 2>/dev/null || true
mkdir -p "${STUB}" "${REPO}" "${XDG_CONFIG_HOME}/endless" "${XDG_CACHE_HOME}"

# The fake tmux. It never runs a window's command, so nothing inside a spawned
# window — spawn-launch, claude — ever executes.
cat >"${STUB}/tmux" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${TMUX_LOG}"
case "\$1" in
  list-clients) cat "${CLIENTS}" 2>/dev/null; exit 0 ;;
  display-message)
    for a in "\$@"; do fmt="\$a"; done
    case "\$fmt" in
      *@endless_auto_spawned*) cat "${WIN_AUTO}" 2>/dev/null ;;
      *@endless_task_id*)      cat "${WIN_TASK}" 2>/dev/null ;;
      *session_id*)            echo '\$1' ;;
    esac
    exit 0 ;;
  new-window|split-window) echo "%\$\$"; exit 0 ;;
esac
exit 0
EOF
cat >"${STUB}/claude" <<EOF
#!/bin/sh
echo "\$*" >>"${LAUNCHED}"
EOF
# The job shells `endless`; it must be THIS worktree's CLI.
cat >"${STUB}/endless" <<EOF
#!/bin/sh
exec uv run --quiet --project "${WT}" endless "\$@"
EOF
chmod +x "${STUB}/tmux" "${STUB}/claude" "${STUB}/endless"
export PATH="${STUB}:${TMP}/bin:${PATH}"

E()  { ( cd "${REPO}" && endless "$@" 2>&1 ); }
Q()  { E sql "$1" --tsv 2>/dev/null; }
W()  { E sql "$1" --write >/dev/null 2>&1 || setup_error "sql failed: $1"; }
JOB() { ( cd "${REPO}" && endless-go jobs run --job auto-spawn 2>&1 ); }
new_windows() { [[ -f "${TMUX_LOG}" ]] && grep -c '^new-window' "${TMUX_LOG}" || echo 0; }

git -C "${REPO}" init -q
git -C "${REPO}" symbolic-ref HEAD refs/heads/main
git -C "${REPO}" config user.email verify@test
git -C "${REPO}" config user.name verify
git -C "${REPO}" commit -q --allow-empty -m "initial commit"
E project register "${REPO}" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1
PROJ="$(Q "SELECT id FROM projects WHERE name='probe'")"
[[ -n "${PROJ}" ]] || setup_error "project did not register"
git -C "${REPO}" add -A >/dev/null 2>&1
git -C "${REPO}" commit -q -m "register" >/dev/null 2>&1 || true

set_project_config() {   # set_project_config '<auto_spawn json>'
    python3 - "${REPO}/.endless/config.json" "$1" <<'PY'
import json, sys, pathlib
p = pathlib.Path(sys.argv[1])
cfg = json.loads(p.read_text()) if p.exists() else {}
auto = json.loads(sys.argv[2])
if auto is None:
    cfg.pop("auto_spawn", None)
else:
    cfg["auto_spawn"] = auto
p.parent.mkdir(parents=True, exist_ok=True)
p.write_text(json.dumps(cfg, indent=2))
PY
}

# task ID STATUS PHASE TYPE_ID COMPLEXITY RISK CREATED [PLAN]
task() {
    W "INSERT INTO tasks (id, project_id, title, status, phase, type_id, complexity_id, risk_id, created_at)
       VALUES ($1, ${PROJ}, 'Task $1', '$2', '$3', $4, $5, $6, '$7')"
    if [[ "${8-plan}" != "" ]]; then
        W "INSERT INTO task_content (task_id, name, content) VALUES ($1, 'plan', '# Plan for $1')"
    fi
}
status_of() { Q "SELECT status FROM tasks WHERE id=$1"; }

printf '100 $1\n900 $7\n500 $3\n' >"${CLIENTS}"   # $7 is the most recently active client

# Eligible: 9101 (now, older) and 9102 (urgent, newer) — 9102 must go first.
task 9101 ready now    1 1 1 '2026-09-01T00:00:00'
task 9102 ready urgent 1 1 1 '2026-09-20T00:00:00'
# Ineligible, one reason each.
task 9103 ready now    1 3 1 '2026-08-01T00:00:00'   # medium complexity
task 9104 ready now    3 1 1 '2026-08-01T00:00:00'   # research
task 9105 ready next   1 1 1 '2026-08-01T00:00:00'   # phase next
task 9106 ready now    1 1 1 '2026-08-01T00:00:00' ""  # no plan

# ── A. off by default ───────────────────────────────────────────────────────
section "A. A project that has not opted in spawns nothing"

out="$(JOB)"
assert_contains "the run is ok and says why nothing happened" \
    "no project has opted in" "${out}"
assert_eq "no window was asked for" "0" "$(new_windows)"

set_project_config '{"enabled": false}'
out="$(JOB)"
assert_contains "enabled:false is the kill switch" "no project has opted in" "${out}"

# A user-level enabled:true must not opt the project in.
printf '{"auto_spawn":{"enabled":true}}\n' >"${XDG_CONFIG_HOME}/endless/config.json"
set_project_config null
out="$(JOB)"
assert_contains "a user-level enabled:true opts nothing in" "no project has opted in" "${out}"
printf '{}\n' >"${XDG_CONFIG_HOME}/endless/config.json"

# ── B. one spawn per due run ────────────────────────────────────────────────
section "B. One detached spawn, urgent first, into the active client's session"

set_project_config '{"enabled": true, "cap": 2}'
: >"${TMUX_LOG}"
out="$(JOB)"
assert_contains "the run spawned the urgent task into the active client's session" \
    'spawned E-9102 into tmux session $7' "${out}"
assert_eq "exactly one window was asked for" "1" "$(new_windows)"
nw="$(grep '^new-window' "${TMUX_LOG}" | head -1)"
assert_contains "the window opens detached (-d) in session \$7" 'new-window -d -t $7:' "${nw}"
assert_contains "in the task's own worktree" "-c ${REPO}/.endless/worktrees/e-9102" "${nw}"
assert_eq "E-9102 is underway" "underway" "$(status_of 9102)"
assert_eq "E-9101 is still ready" "ready" "$(status_of 9101)"
if [[ -d "${REPO}/.endless/worktrees/e-9102" ]]; then
    report_pass "the spawn created E-9102's worktree"
else
    report_fail "the spawn created E-9102's worktree" "a directory" "absent"
fi
spec="$(printf '%s\n' "${nw}" | sed -n 's/.*--spec \([^ ]*\).*/\1/p')"
spec_text="$(cat "${spec}" 2>/dev/null)"
assert_contains "the launch spec marks the window auto-spawned" '"auto_spawned": true' "${spec_text}"
assert_contains "and names no session as its spawner" '"spawned_by": "auto-spawn"' "${spec_text}"
ev="$(grep -h '"id":"9102"' "${REPO}"/.endless/db-ledger/*.jsonl 2>/dev/null | grep '"task.status_changed"' | tail -1)"
assert_contains "the pre-claim is the system's, not a Claude session's (--no-session)" \
    '"actor":{"kind":"system"' "${ev}"

# `jobs run --job` forces the job due, so the interval is read off the
# schedule instead: the next run is one interval (5m by default) away.
due="$( ( cd "${REPO}" && endless-go jobs list 2>&1 ) | grep '^auto-spawn')"
if [[ "${due}" =~ \ in\ [45]m ]]; then
    report_pass "the next run is an interval away — one spawn per interval"
else
    report_fail "the next run is an interval away — one spawn per interval" "next due in ~5m" "${due}"
fi

# ── C. SessionStart flags the session in that window ────────────────────────
section "C. SessionStart marks the session bound in an auto-spawned window"

# hook SESSION_UUID CWD — one real SessionStart through the built binary. Its
# output lands in hook-<uuid>.log, which a failing assertion below quotes.
hook() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"SessionStart","source":"startup"}' "$1" "$2" \
        | ( cd "$2" && TMUX="${TMP}/fake,1,0" TMUX_PANE="%4242" endless-go hook claude >"${TMP}/hook-$1.log" 2>&1 )
}
# assert_session LABEL UUID "TASK|FLAG" — quotes the hook's output on a miss.
assert_session() {
    local got
    got="$(Q "SELECT task_id, auto_spawned FROM sessions WHERE session_id='$2'" | tr '\t' '|')"
    if [[ "${got}" == "$3" ]]; then
        report_pass "$1"
    else
        report_fail "$1" "$3" "${got:-no session row} — hook said: $(head -3 "${TMP}/hook-$2.log")"
    fi
}

echo 1 >"${WIN_AUTO}"; echo 9102 >"${WIN_TASK}"
hook "uuid-auto-9102" "${REPO}/.endless/worktrees/e-9102"
assert_session "the session bound to E-9102 in its auto-spawned window is flagged" \
    uuid-auto-9102 "9102|1"

# A stale marker: the window still says auto-spawned for a DIFFERENT task.
W "INSERT INTO tasks (id, project_id, title, status, phase, type_id) VALUES (9150, ${PROJ}, 'by hand', 'underway', 'now', 1)"
git -C "${REPO}" worktree add -q -b task/9150 "${REPO}/.endless/worktrees/e-9150" >/dev/null 2>&1
mkdir -p "${REPO}/.endless/worktrees/e-9150/.endless"
echo '{}' >"${REPO}/.endless/worktrees/e-9150/.endless/worktree.json"
hook "uuid-manual-9150" "${REPO}/.endless/worktrees/e-9150"
assert_session "a session in a window whose marker names another task is not flagged" \
    uuid-manual-9150 "9150|0"
echo "" >"${WIN_AUTO}"; echo "" >"${WIN_TASK}"

# ── D. the cap ──────────────────────────────────────────────────────────────
section "D. The cap counts auto-spawned work still outstanding"

( cd "${REPO}" && endless-go jobs run --job auto-spawn >"${TMP}/d1.log" 2>&1 )
d1="$(cat "${TMP}/d1.log")"
assert_contains "the second slot (cap 2) takes E-9101" 'spawned E-9101' "${d1}"
# Its session binds and is flagged, as C showed.
echo 1 >"${WIN_AUTO}"; echo 9101 >"${WIN_TASK}"
hook "uuid-auto-9101" "${REPO}/.endless/worktrees/e-9101"
echo "" >"${WIN_AUTO}"; echo "" >"${WIN_TASK}"

task 9107 ready now 2 1 1 '2026-09-25T00:00:00'   # a bugfix, eligible
before="$(new_windows)"
out="$(JOB)"
assert_contains "two auto-spawned tasks outstanding: the project is at its cap" \
    "every opted-in project is at its cap (1 of 1)" "${out}"
assert_eq "no window was asked for" "${before}" "$(new_windows)"
assert_eq "E-9107 is still ready" "ready" "$(status_of 9107)"

W "UPDATE tasks SET status='unverified' WHERE id=9102"
out="$(JOB)"
assert_contains "unverified still counts against the cap" "at its cap" "${out}"

W "UPDATE tasks SET status='confirmed' WHERE id=9102"
out="$(JOB)"
assert_contains "settling one frees the slot: E-9107 (a bugfix) is spawned" "spawned E-9107" "${out}"

# ── E. nobody attached ──────────────────────────────────────────────────────
section "E. No attached client: skip, and leave the task ready"

set_project_config '{"enabled": true, "cap": 50}'
task 9108 ready now 1 1 1 '2026-09-26T00:00:00'
: >"${CLIENTS}"
before="$(new_windows)"
out="$(JOB)"
assert_contains "the run skips and says why" "no tmux client is attached" "${out}"
assert_not_contains "a skip is not a failure" "FAILED" "${out}"
assert_eq "no window was asked for" "${before}" "$(new_windows)"
assert_eq "E-9108 is still ready" "ready" "$(status_of 9108)"
printf '100 $1\n900 $7\n' >"${CLIENTS}"

# ── F. every candidate is one task spawn accepts ────────────────────────────
section "F. Every task the selector picks, task spawn accepts"

# More eligible shapes, and more near-misses that spawn itself would refuse.
task 9109 ready urgent 2 1 1 '2026-09-27T00:00:00'                  # urgent bugfix
task 9110 ready now    1 1 1 '2026-09-27T00:00:00'                  # blocked by a SETTLED task
W "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES ('task', 9102, 'task', 9110, 'blocks')"
task 9111 ready now    1 1 1 '2026-09-27T00:00:00'                  # an ANSWERED question
W "INSERT INTO task_questions (task_id, series, question, status, answer) VALUES (9111, 1, 'q?', 'answered', 'a')"
task 9112 ready now    1 1 1 '2026-09-27T00:00:00'                  # blocked by an OPEN task
W "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES ('task', 9105, 'task', 9112, 'blocks')"
task 9113 ready now    1 1 1 '2026-09-27T00:00:00'                  # an OPEN question
W "INSERT INTO task_questions (task_id, series, question, status) VALUES (9113, 1, 'which?', 'open')"
task 9114 ready now    1 1 1 '2026-09-27T00:00:00'                  # claimed once, session ended
W "INSERT INTO sessions (session_id, project_id, state, task_id) VALUES ('uuid-old-9114', ${PROJ}, 'ended', 9114)"

runs=0; failed=0; spawned=()
while (( runs < 12 )); do
    out="$(JOB)"; runs=$((runs + 1))
    [[ "${out}" == *FAILED* ]] && failed=$((failed + 1))
    if [[ "${out}" =~ spawned\ E-([0-9]+) ]]; then
        spawned+=("${BASH_REMATCH[1]}")
    else
        break
    fi
done
assert_eq "no run failed — spawn refused nothing the selector picked" "0" "${failed}"
assert_eq "the drive ended because nothing was eligible" "1" \
    "$([[ "${out}" == *"nothing eligible"* ]] && echo 1 || echo 0)"
assert_eq "exactly the eligible tasks were spawned, urgent first then oldest" \
    "9109 9108 9110 9111" "${spawned[*]}"
for id in 9103 9104 9105 9106 9112 9113 9114; do
    assert_eq "E-${id} (ineligible) was left ready" "ready" "$(status_of "${id}")"
done

# ── G. what a person sees ───────────────────────────────────────────────────
section "G. jobs list shows the note; nothing launched Claude"

list="$( ( cd "${REPO}" && endless-go jobs list 2>&1 ) )"
assert_contains "jobs list has a NOTE column" "NOTE" "${list}"
assert_contains "and the auto-spawn row carries the last run's note" "nothing eligible" \
    "$(printf '%s\n' "${list}" | grep '^auto-spawn')"
assert_eq "no Claude was launched by anything above" "no" \
    "$([[ -e "${LAUNCHED}" ]] && echo yes || echo no)"

summary
