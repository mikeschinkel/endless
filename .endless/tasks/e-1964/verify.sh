#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1964 and records what was true when E-1964
# landed. Edit it only if you ARE E-1964. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1964: per-worktree sandboxes become universal by moving into the worktree.
#
# WHAT LANDED
#   A sandbox is the isolated state a worktree's task is exercised against — a
#   throwaway database, a fixture file, credentials that must not be the real
#   ones. It used to live at ~/.cache/endless/sandboxes/<name>, and it used to
#   exist only for projects that set "self_dev": true, which was never a design
#   decision so much as an artifact of the sandbox having been built to carry
#   endless's own database.
#
#   It now lives at <worktree>/.endless/sandbox/, is created for EVERY project,
#   self-ignores via a .gitignore containing `*`, and dies with its worktree.
#   Endless creates the directory EMPTY and seeds nothing: what goes in is the
#   project's business, declared by its own post-worktree-create hook.
#   `self_dev` keeps only its remaining job — routing endless's OWN database
#   into the sandbox. The XDG_CONFIG_HOME injection and `sandbox bind` are gone.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  THE PATH IS COMPOSITION. It is the worktree path plus a fixed segment,
#       both languages compute the same answer, and no environment variable
#       moves it. A project may override the parent per-PROJECT.
#   C2  MIGRATION RELOCATES WHAT EXISTS. A rename, contents byte-identical, old
#       path gone. Both locations occupied is a COLLISION: neither touched,
#       reported, exit non-zero. A sandbox with no worktree is an orphan:
#       reported, deleted never.
#   C3  MIGRATION PROVISIONS WHAT NEVER HAD ONE, hook and all. A failing hook is
#       non-fatal and loud, the sandbox stays, and the sweep carries on.
#   C4  --dry-run changes nothing, and its counts are the counts the real run
#       then reports.
#   C5  IT IS IDEMPOTENT. A second run moves nothing, creates nothing, exits 0.
#   C6  THE ONE THIS MUST NOT GET WRONG: after migration, a worktree whose
#       sandbox is deleted by hand makes the resolver REFUSE — naming the
#       worktree and the command — and build nothing. A silent rebuild would
#       hide an anomaly and hand a session a database nobody wrote.
#   C7  IT IS UNIVERSAL. A project that is NOT self-dev gets a sandbox when its
#       worktree is created; the ungating actually fires.
#   C8  THE INJECTION IS GONE — no `sandbox bind`, nothing writes
#       XDG_CONFIG_HOME into a worktree's Claude settings, and migration strips
#       the copies already on disk. But `_guard.sh`'s isolation arm, which READS
#       XDG_CONFIG_HOME for an unrelated reason, still refuses a hand-run suite.
#
# ISOLATION
#   Every fixture is a throwaway project tree under a scratch directory, with
#   HOME and XDG_CACHE_HOME pointed inside it. No fixture is a real worktree,
#   nothing reads or writes the main database, and the only thing written inside
#   this checkout is one probe file under this suite's own directory, removed on
#   exit. `sandbox migrate` resolves its project from cwd, so a fixture run can
#   only ever see the fixture.
#
# Layers:
#   A. FAIL-FAST — build, the Go packages, and this task's Python tests. Nothing
#      below means anything if these fail.
#   B. The resolver — C1.
#   C. Migration — C2, C3, C4, C5.
#   D. The refusal — C6.
#   E. Universality — C7.
#   F. The injection's removal, and the one reader of XDG_CONFIG_HOME that
#      stays — C8.
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
PROBE="${ENDLESS_VERIFY_DIR:-${WT}/.endless/tasks/e-1964}/_guard_probe.sh"
trap 'rm -rf "${TMP}"; rm -f "${PROBE}"' EXIT

GOBIN="${TMP}/endless-go"

# `uv run --project` runs THIS worktree's Python without changing the working
# directory. `--directory` would change it, and every command here resolves its
# project from cwd — so that flag would silently point a fixture run at the real
# endless checkout. It did, once, while this suite was being written.
endless_cli() { uv run --project "${WT}" endless "$@"; }

# --------------------------------------------------------------------------- #
section "A. Fail-fast: the build and this task's tests"
# --------------------------------------------------------------------------- #

if out=$(go build ./... 2>&1); then
    report_pass "go build ./..."
else
    report_fail "go build ./..." "exit 0" "${out}"
    summary
fi

go build -o "${GOBIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

if out=$(go test -count=1 \
        ./internal/monitor/... ./internal/sandboxcmd/... ./internal/eventcmd/... 2>&1); then
    report_pass "go test: monitor, sandboxcmd, eventcmd"
else
    report_fail "go test: monitor, sandboxcmd, eventcmd" "exit 0" "${out}"
    summary
fi

if out=$(uv run pytest -q \
        tests/test_sandbox_migrate.py \
        tests/test_db_gate.py \
        tests/test_provenance.py \
        tests/test_claim_output_format.py \
        tests/test_event_bridge_worktree_binary.py 2>&1); then
    report_pass "uv run pytest: the sandbox, gate, provenance and claim suites"
else
    report_fail "uv run pytest: the sandbox, gate, provenance and claim suites" \
        "exit 0" "${out}"
    summary
fi

# --------------------------------------------------------------------------- #
section "B. C1 — the path is composition, and both languages agree"
# --------------------------------------------------------------------------- #

# A fixture project with one worktree and NO sandbox. Used here for the path,
# and again in layer D for the refusal.
mk_project() {
    local dir="$1" self_dev="${2:-true}" extra="${3:-}"
    mkdir -p "${dir}/.endless/worktrees" || return 1
    if [[ -n "${extra}" ]]; then
        printf '{"self_dev": %s, %s}\n' "${self_dev}" "${extra}" \
            > "${dir}/.endless/config.json"
    else
        printf '{"self_dev": %s}\n' "${self_dev}" > "${dir}/.endless/config.json"
    fi
}

mk_worktree() { mkdir -p "$1/.endless/worktrees/e-$2" && printf '%s' "$1/.endless/worktrees/e-$2"; }

P1="${TMP}/p1"
mk_project "${P1}" true || setup_error "cannot build fixture project p1"
W1="$(mk_worktree "${P1}" 100)" || setup_error "cannot build fixture worktree"

# Ask the resolver directly. There is no CLI verb that prints a sandbox path for
# an arbitrary worktree, and what is under test is the value Python composes,
# not a door it is asked through.
sandbox_path_py() {
    uv run --project "${WT}" python -c \
        'import sys; from pathlib import Path; from endless import config; print(config.sandbox_root(Path(sys.argv[1])))' \
        "$1"
}

PY_PATH="$(sandbox_path_py "${W1}")" \
    || setup_error "cannot resolve the sandbox path from Python"

assert_eq "Python composes <worktree>/.endless/sandbox" \
    "${W1}/.endless/sandbox" "${PY_PATH}"

# Go names the same path in its refusal, which is the only place it prints one.
# Agreement is load-bearing: Python resolves the path for `--db sandbox` and
# hands it to Go, and Go resolves it again from cwd. Two answers would route a
# command and its own subprocesses at different databases.
GO_OUT="$(cd "${W1}" && "${GOBIN}" --db sandbox errors show 2>&1 || true)"
assert_contains "Go resolves the same path" "${PY_PATH}" "${GO_OUT}"

# No environment variable moves it. The whole point of in-tree placement is that
# there is nothing to inherit — the failure that let a stale export outlive the
# worktree it named.
ENV_PATH="$(XDG_CONFIG_HOME="${TMP}/hijack" XDG_CACHE_HOME="${TMP}/hijack" \
    ENDLESS_SANDBOX="${TMP}/hijack" sandbox_path_py "${W1}")" \
    || setup_error "cannot resolve the sandbox path under a hostile env"
assert_eq "no environment variable moves the sandbox" "${PY_PATH}" "${ENV_PATH}"

# The project-level override, which is the one branch inside the resolver.
P_OVR="${TMP}/povr"
mk_project "${P_OVR}" true "\"sandbox_root\": \"${TMP}/outside\"" \
    || setup_error "cannot build the override fixture"
W_OVR="$(mk_worktree "${P_OVR}" 100)"
OVR_PATH="$(sandbox_path_py "${W_OVR}")"
assert_eq "a project may move its sandboxes out of tree" \
    "${TMP}/outside/e-100" "${OVR_PATH}"

# --------------------------------------------------------------------------- #
section "C. C2–C5 — migration"
# --------------------------------------------------------------------------- #

# migrate_fixture <name> builds a project with a legacy cache root beside it and
# echoes the project root. Every later command runs with HOME and XDG_CACHE_HOME
# inside it, so the legacy root this sees is the fixture's and never the real one.
migrate_fixture() {
    local d="${TMP}/$1"
    mk_project "${d}/proj" true || return 1
    mkdir -p "${d}/home" "${d}/cache/endless/sandboxes" || return 1
    printf '%s' "${d}"
}

# run_migrate <fixture-dir> [flags...] — the real CLI, in the fixture's world.
run_migrate() {
    local d="$1"; shift
    ( cd "${d}/proj" && HOME="${d}/home" XDG_CACHE_HOME="${d}/cache" \
        endless_cli sandbox migrate "$@" 2>&1 )
}

legacy_sandbox() {
    local d="$1" name="$2" payload="$3"
    mkdir -p "${d}/cache/endless/sandboxes/${name}/endless"
    printf '%s' "${payload}" > "${d}/cache/endless/sandboxes/${name}/endless/endless.db"
}

# ── C4 first: the dry run must be able to predict a run that has not happened.
F="$(migrate_fixture dryrun)" || setup_error "fixture: dryrun"
mk_worktree "${F}/proj" 100 >/dev/null
mk_worktree "${F}/proj" 200 >/dev/null
legacy_sandbox "${F}" e-100 "original bytes"
DRY="$(run_migrate "${F}" --dry-run)"
DRY_LINE="$(printf '%s\n' "${DRY}" | grep -m1 'moved,' || true)"

assert_contains "--dry-run says so" "DRY RUN — nothing changed." "${DRY_LINE}"
if [[ -d "${F}/cache/endless/sandboxes/e-100" \
   && ! -e "${F}/proj/.endless/worktrees/e-100/.endless/sandbox" \
   && ! -e "${F}/proj/.endless/worktrees/e-200/.endless/sandbox" ]]; then
    report_pass "--dry-run moved nothing and created nothing"
else
    report_fail "--dry-run moved nothing and created nothing" \
        "legacy intact, no sandboxes" "$(ls -a "${F}/proj/.endless/worktrees/e-100" 2>&1)"
fi

REAL="$(run_migrate "${F}")"
REAL_LINE="$(printf '%s\n' "${REAL}" | grep -m1 'moved,' || true)"
assert_eq "the dry run's counts are the counts the real run reports" \
    "${REAL_LINE}" "${DRY_LINE#DRY RUN — nothing changed. }"

# ── C2: relocation is a rename, and the contents survive it byte for byte.
assert_eq "the sandbox moved into its worktree, contents intact" \
    "original bytes" \
    "$(cat "${F}/proj/.endless/worktrees/e-100/.endless/sandbox/endless/endless.db" 2>&1)"

if [[ ! -e "${F}/cache/endless/sandboxes/e-100" ]]; then
    report_pass "the old location is gone"
else
    report_fail "the old location is gone" "absent" "still present"
fi

# ── C5: idempotent.
AGAIN="$(run_migrate "${F}")"
assert_contains "a second run moves and creates nothing" \
    "0 moved, 0 provisioned" "${AGAIN}"

# ── C2: a collision is reported, touches nothing, and exits non-zero.
F="$(migrate_fixture collision)" || setup_error "fixture: collision"
W="$(mk_worktree "${F}/proj" 100)"
legacy_sandbox "${F}" e-100 "OLD"
mkdir -p "${W}/.endless/sandbox"
printf 'NEW' > "${W}/.endless/sandbox/marker"

COL="$( cd "${F}/proj" && HOME="${F}/home" XDG_CACHE_HOME="${F}/cache" \
        endless_cli sandbox migrate 2>&1 )"
COL_RC=$?
assert_eq "a collision exits non-zero" "1" "${COL_RC}"
assert_contains "a collision is reported" "COLLISION" "${COL}"
assert_eq "the legacy sandbox was not touched" "OLD" \
    "$(cat "${F}/cache/endless/sandboxes/e-100/endless/endless.db" 2>&1)"
assert_eq "the in-worktree sandbox was not touched" "NEW" \
    "$(cat "${W}/.endless/sandbox/marker" 2>&1)"

# ── C2: an orphan is reported and survives. Deleting directories is not
#        something this command does on its own authority.
F="$(migrate_fixture orphan)" || setup_error "fixture: orphan"
legacy_sandbox "${F}" e-999 "orphaned"
ORP="$(run_migrate "${F}")"
assert_contains "an orphan is reported" "orphan" "${ORP}"
assert_eq "an orphan is still on disk afterwards" "orphaned" \
    "$(cat "${F}/cache/endless/sandboxes/e-999/endless/endless.db" 2>&1)"

# ── C3: a worktree that never had one is provisioned, hook and all.
F="$(migrate_fixture provision)" || setup_error "fixture: provision"
W="$(mk_worktree "${F}/proj" 300)"
mkdir -p "${F}/proj/.endless/hooks"
cat > "${F}/proj/.endless/hooks/post-worktree-create.sh" <<HOOK
#!/bin/sh
echo ran > "${F}/hook-ran"
HOOK
chmod +x "${F}/proj/.endless/hooks/post-worktree-create.sh"

PRV="$(run_migrate "${F}")"
assert_contains "a sandbox-less worktree is provisioned" "1 provisioned" "${PRV}"
if [[ -d "${W}/.endless/sandbox" ]]; then
    report_pass "the sandbox directory exists"
else
    report_fail "the sandbox directory exists" "a directory" "absent"
fi
assert_eq "the project's post-worktree-create hook ran" "ran" \
    "$(cat "${F}/hook-ran" 2>&1)"

# The .gitignore is what makes adoption free: no project has to add an entry,
# and `worktree land` never trips over the sandbox. Prove it against real git
# rather than by reading the file.
git init -q -b main "${W}" 2>/dev/null || setup_error "cannot init the fixture repo"
git -C "${W}" config user.email t@example.com
git -C "${W}" config user.name t
printf 'x' > "${W}/README"
printf 'TOKEN=shh' > "${W}/.endless/sandbox/secrets.env"
GIT_STATUS="$(git -C "${W}" status --porcelain --untracked-files=all 2>&1)"
assert_not_contains "git does not see the sandbox" "sandbox" "${GIT_STATUS}"
assert_contains "the fixture repo is otherwise reporting files" "README" "${GIT_STATUS}"

# ── C3: a failing hook is non-fatal and loud, and the sweep carries on.
F="$(migrate_fixture hookfail)" || setup_error "fixture: hookfail"
WA="$(mk_worktree "${F}/proj" 400)"
WB="$(mk_worktree "${F}/proj" 401)"
mkdir -p "${F}/proj/.endless/hooks"
printf '#!/bin/sh\nexit 3\n' > "${F}/proj/.endless/hooks/post-worktree-create.sh"
chmod +x "${F}/proj/.endless/hooks/post-worktree-create.sh"

HF="$(run_migrate "${F}")"
assert_contains "hook failures are counted separately" "2 hook failure(s)" "${HF}"
assert_contains "the failure names the script" "post-worktree-create.sh" "${HF}"
if [[ -d "${WA}/.endless/sandbox" && -d "${WB}/.endless/sandbox" ]]; then
    report_pass "a failing hook keeps the sandbox, and the sweep continues"
else
    report_fail "a failing hook keeps the sandbox, and the sweep continues" \
        "both sandboxes present" "$(ls -a "${WA}/.endless" "${WB}/.endless" 2>&1)"
fi

# --------------------------------------------------------------------------- #
section "D. C6 — a missing sandbox REFUSES, and builds nothing"
# --------------------------------------------------------------------------- #

# The one this must not get wrong. Migration is the last moment a missing
# sandbox is expected; after it, an absent one is an anomaly, and a resolver
# that quietly rebuilt would hand a session a database nobody wrote and hide
# whatever removed the directory.
F="$(migrate_fixture refuse)" || setup_error "fixture: refuse"
W="$(mk_worktree "${F}/proj" 500)"
run_migrate "${F}" >/dev/null
[[ -d "${W}/.endless/sandbox" ]] || setup_error "fixture: migration did not provision"
rm -rf "${W}/.endless/sandbox"

GO_REFUSAL="$( cd "${W}" && HOME="${F}/home" XDG_CACHE_HOME="${F}/cache" \
    "${GOBIN}" --db sandbox errors show 2>&1 )"
GO_RC=$?
assert_eq "Go refuses rather than opening a database" "2" "${GO_RC}"
assert_contains "the refusal says there is no sandbox" "no sandbox" "${GO_REFUSAL}"
assert_contains "the refusal names the worktree" "${W}" "${GO_REFUSAL}"
assert_contains "the refusal names the command that fixes it" \
    "endless sandbox migrate" "${GO_REFUSAL}"

PY_REFUSAL="$( cd "${W}" && HOME="${F}/home" XDG_CACHE_HOME="${F}/cache" \
    uv run --project "${WT}" python -c \
    'from endless import config
try:
    config.apply_db_choice("sandbox")
except ValueError as e:
    print(e)
else:
    print("RESOLVED ANYWAY:", config.RESOLVED_CONFIG_DIR)' 2>&1 )"
assert_contains "Python refuses too, with the same remedy" \
    "endless sandbox migrate" "${PY_REFUSAL}"
assert_contains "and Python says what is missing" "no sandbox" "${PY_REFUSAL}"

if [[ ! -e "${W}/.endless/sandbox" ]]; then
    report_pass "nothing was rebuilt — the refusal creates no directory"
else
    report_fail "nothing was rebuilt — the refusal creates no directory" \
        "still absent" "$(find "${W}/.endless/sandbox" 2>&1 | head -5)"
fi

# --------------------------------------------------------------------------- #
section "E. C7 — universal: a NON-self-dev project gets one too"
# --------------------------------------------------------------------------- #

# The ungating, on the real worktree-creation path rather than on the
# provisioner in isolation. _bootstrap_worktree is the function `task claim`
# and session recovery both call; only its DB-backed doc mirroring is stubbed,
# because a fixture project has no task rows and that is not what is under test.
F_UNGATE="${TMP}/ungate"
F="${F_UNGATE}"
mk_project "${F}/proj" false || setup_error "fixture: ungate"
mkdir -p "${F}/home"
cat > "${TMP}/ungate.py" <<'PY'
import sys
from pathlib import Path
from endless import worktree_cmd

root = Path(sys.argv[1])
wt = root / ".endless" / "worktrees" / "e-600"
wt.mkdir(parents=True)

# The task docs come from the database; a fixture project has none, and the
# claim under test is about the sandbox.
worktree_cmd._materialize_task_docs = lambda *a, **k: None
worktree_cmd._warm_unlanded_cache = lambda *a, **k: None

worktree_cmd._bootstrap_task_worktree(600, wt, "main", "task/600-x", root)
print(wt / ".endless" / "sandbox")
PY

UNGATE="$( cd "${F}/proj" && HOME="${F}/home" \
    uv run --project "${WT}" python "${TMP}/ungate.py" "${F}/proj" 2>&1 )" \
    || setup_error "worktree bootstrap failed on a non-self-dev project: ${UNGATE}"

SB="${F}/proj/.endless/worktrees/e-600/.endless/sandbox"
if [[ -d "${SB}" ]]; then
    report_pass "worktree creation provisions a sandbox for a non-self-dev project"
else
    report_fail "worktree creation provisions a sandbox for a non-self-dev project" \
        "${SB} exists" "${UNGATE}"
fi
assert_contains "and it self-ignores" "*" "$(cat "${SB}/.gitignore" 2>&1)"

# Endless seeds nothing: contents are the project's business.
assert_eq "the sandbox is created EMPTY" ".gitignore" \
    "$(cd "${SB}" && ls -A)"

# --------------------------------------------------------------------------- #
section "F. C8 — the injection is gone; the guard that reads XDG stays"
# --------------------------------------------------------------------------- #

if [[ ! -e "${WT}/internal/sandboxcmd/bind.go" ]]; then
    report_pass "sandbox bind is deleted"
else
    report_fail "sandbox bind is deleted" "absent" "internal/sandboxcmd/bind.go present"
fi

BIND_USAGE="$("${GOBIN}" sandbox --help 2>&1)"
assert_not_contains "bind is gone from the sandbox usage" "bind" "${BIND_USAGE}"

# Nothing WRITES XDG_CONFIG_HOME into a worktree's Claude settings any more.
# Checked as behaviour rather than as a grep: plenty of shipped code legitimately
# reads the variable, and several places set it in a CHILD PROCESS's environment
# (the triage child, the verify runner's isolation, an ephemeral `sandbox run`).
# None of those is the injection this deleted, which was a settings-file env
# block every process a Claude session spawned inherited for the session's life.
#
# Layer E just created a worktree through the real bootstrap. If anything still
# injected, it would be in that worktree's settings now.
CREATED_SETTINGS="$(cat "${F_UNGATE}/proj/.endless/worktrees/e-600/.claude/settings.json" 2>/dev/null || true)"
assert_not_contains "worktree creation writes no XDG_CONFIG_HOME env block" \
    "XDG_CONFIG_HOME" "${CREATED_SETTINGS}"

# And the writer itself is gone, rather than merely unreached.
assert_eq "the settings-injection writer is deleted" "" \
    "$(git grep -l 'updateClaudeSettings' -- internal cmd src 2>/dev/null || true)"

# Migration strips the copies already on disk. After relocation the injected
# value names a directory that is no longer there, and a process whose cwd is
# outside the worktree would follow it and create a fresh empty config at the
# vacated path.
F="$(migrate_fixture unpin)" || setup_error "fixture: unpin"
W="$(mk_worktree "${F}/proj" 700)"
mkdir -p "${W}/.claude"
cat > "${W}/.claude/settings.json" <<JSON
{"env": {"XDG_CONFIG_HOME": "${F}/cache/endless/sandboxes/e-700"},
 "enabledPlugins": ["keep-me"]}
JSON
legacy_sandbox "${F}" e-700 "payload"
run_migrate "${F}" >/dev/null

SETTINGS="$(cat "${W}/.claude/settings.json")"
assert_not_contains "the dead XDG_CONFIG_HOME injection is stripped" \
    "XDG_CONFIG_HOME" "${SETTINGS}"
assert_contains "other settings keys survive" "keep-me" "${SETTINGS}"

# _guard.sh's isolation arm READS XDG_CONFIG_HOME, and that reader is not the
# injection. Deleting the two together was the specific mistake this checks
# against: the arm would silently stop firing on a hand-run, which is the one
# thing it exists to catch.
cat > "${PROBE}" <<'PROBE_EOF'
#!/usr/bin/env bash
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
echo "GUARD DID NOT REFUSE"
PROBE_EOF
chmod +x "${PROBE}"
mkdir -p "${TMP}/realish/.config/endless"
PROBE_OUT="$(HOME="${TMP}/realish" XDG_CONFIG_HOME="${TMP}/realish/.config" \
    bash "${PROBE}" 2>&1 || true)"
assert_not_contains "the guard still refuses a hand-run suite" \
    "GUARD DID NOT REFUSE" "${PROBE_OUT}"
assert_contains "and says why" "verify runner" "${PROBE_OUT}"

summary
