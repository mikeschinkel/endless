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
#   ones. It used to live at ~/.cache/endless/sandboxes/<name>, and existed only
#   for projects that set "self_dev": true.
#
#   It now lives at <worktree>/.endless/sandbox/, is created for EVERY project,
#   self-ignores via a .gitignore containing `*`, and dies with its worktree.
#   Endless creates it EMPTY; the project's post-worktree-create hook fills it.
#   `self_dev` keeps only its remaining job — routing endless's OWN database
#   into the sandbox. The XDG_CONFIG_HOME injection and `sandbox bind` are gone.
#
# REVISITED (the first land failed on the real machine)
#   It shipped with `endless sandbox migrate`, a maintained command for what was
#   a one-time move on one machine. Run for real, it failed two ways no fixture
#   had shown: relocated sandboxes arrived without their .gitignore, and the hook
#   — taken from the main checkout — ran `just <recipe>` against each OLD
#   worktree's own justfile, which lacked the recipes or called binaries that no
#   longer exist. The migration was finished on that machine by a one-off
#   script, the command was deleted, and the refusal for a missing sandbox now
#   names `just dev-sandbox-init`. The hook now runs recipes from main's
#   justfile, which it needs regardless: `session resume --reopen` recreates
#   worktrees at old commits and runs the same hook.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  THE PATH IS COMPOSITION. The worktree path plus a fixed segment; both
#       languages compute the same answer; no environment variable moves it; a
#       project may override the parent, per PROJECT.
#   C2  IT IS UNIVERSAL AND INVISIBLE. A NOT-self-dev project's worktree gets a
#       sandbox through the real bootstrap path, created empty, and git cannot
#       see it or anything later put in it.
#   C3  A MISSING SANDBOX REFUSES, and builds nothing. Both languages name the
#       worktree and `just dev-sandbox-init`.
#   C4  THE HOOK RUNS MAIN'S RECIPES AGAINST AN OLD WORKTREE. Proven on a
#       fixture shaped like the failure — a worktree on an older branch whose
#       justfile lacks the recipes — and proven to BITE: the hook as first
#       landed fails on the same fixture.
#   C5  THE MIGRATE COMMAND IS GONE, and so is `sandbox bind`.
#   C6  `_guard.sh`'s isolation arm, which READS XDG_CONFIG_HOME for an
#       unrelated reason, still refuses a hand-run suite.
#
# ISOLATION
#   Every fixture is a throwaway tree under a scratch directory, with HOME
#   pointed inside it where a command could reach for one. Nothing reads or
#   writes the main database, and the only file written inside this checkout
#   is one probe under this suite's own directory, removed on exit.
#
# Layers:
#   A. FAIL-FAST — build, the Go packages, and this task's Python tests.
#   B. C1.   C. C2.   D. C3.   E. C4.   F. C5, C6.
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
# directory. `--directory` would change it, and several checks here resolve
# their project from cwd — so that flag would silently point a fixture run at
# the real checkout.
py() { uv run --project "${WT}" python "$@"; }

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
        tests/test_sandbox_path.py \
        tests/test_endless_worktree_hook.py \
        tests/test_worktree_create.py \
        tests/test_db_gate.py \
        tests/test_provenance.py \
        tests/test_claim_output_format.py \
        tests/test_event_bridge_worktree_binary.py 2>&1); then
    report_pass "uv run pytest: sandbox, hook, gate, provenance and claim suites"
else
    report_fail "uv run pytest: sandbox, hook, gate, provenance and claim suites" \
        "exit 0" "${out}"
    summary
fi

# --------------------------------------------------------------------------- #
section "B. C1 — the path is composition, and both languages agree"
# --------------------------------------------------------------------------- #

mk_project() {
    local dir="$1" self_dev="${2:-true}" extra="${3:-}"
    mkdir -p "${dir}/.endless/worktrees" || return 1
    if [[ -n "${extra}" ]]; then
        printf '{"self_dev": %s, %s}\n' "${self_dev}" "${extra}" > "${dir}/.endless/config.json"
    else
        printf '{"self_dev": %s}\n' "${self_dev}" > "${dir}/.endless/config.json"
    fi
}

mk_worktree() { mkdir -p "$1/.endless/worktrees/e-$2" && printf '%s' "$1/.endless/worktrees/e-$2"; }

sandbox_path_py() {
    py -c 'import sys; from pathlib import Path; from endless import config; print(config.sandbox_root(Path(sys.argv[1])))' "$1"
}

P1="${TMP}/p1"
mk_project "${P1}" true || setup_error "cannot build fixture project p1"
W1="$(mk_worktree "${P1}" 100)" || setup_error "cannot build fixture worktree"

PY_PATH="$(sandbox_path_py "${W1}")" || setup_error "cannot resolve the sandbox path from Python"
assert_eq "Python composes <worktree>/.endless/sandbox" "${W1}/.endless/sandbox" "${PY_PATH}"

# Go names the path it resolved in its refusal, which is the only place it
# prints one. Agreement is load-bearing: Python resolves the path for
# `--db sandbox` and hands it to Go, and Go resolves it again from cwd.
GO_OUT="$(cd "${W1}" && HOME="${TMP}/home" "${GOBIN}" --db sandbox errors show 2>&1 || true)"
assert_contains "Go resolves the same path" "expected: ${PY_PATH}" "${GO_OUT}"

ENV_PATH="$(XDG_CONFIG_HOME="${TMP}/hijack" XDG_CACHE_HOME="${TMP}/hijack" \
    ENDLESS_SANDBOX="${TMP}/hijack" sandbox_path_py "${W1}")" \
    || setup_error "cannot resolve the sandbox path under a hostile env"
assert_eq "no environment variable moves the sandbox" "${PY_PATH}" "${ENV_PATH}"

P_OVR="${TMP}/povr"
mk_project "${P_OVR}" true "\"sandbox_root\": \"${TMP}/outside\"" \
    || setup_error "cannot build the override fixture"
W_OVR="$(mk_worktree "${P_OVR}" 100)"
assert_eq "a project may move its sandboxes out of tree" \
    "${TMP}/outside/e-100" "$(sandbox_path_py "${W_OVR}")"

# --------------------------------------------------------------------------- #
section "C. C2 — universal, empty, and invisible to git"
# --------------------------------------------------------------------------- #

# The real bootstrap path — the function `task claim` and session recovery both
# call — on a project that is NOT self-dev. Only its database-backed doc
# mirroring is stubbed: a fixture project has no task rows, and that is not what
# is under test.
F="${TMP}/ungate"
mk_project "${F}/proj" false || setup_error "fixture: ungate"
mkdir -p "${F}/home"
cat > "${TMP}/ungate.py" <<'PY'
import sys
from pathlib import Path
from endless import worktree_cmd

root = Path(sys.argv[1])
wt = root / ".endless" / "worktrees" / "e-600"
wt.mkdir(parents=True)
worktree_cmd._materialize_task_docs = lambda *a, **k: None
worktree_cmd._warm_unlanded_cache = lambda *a, **k: None
worktree_cmd._bootstrap_task_worktree(600, wt, "main", "task/600", root)
PY

UNGATE="$( cd "${F}/proj" && HOME="${F}/home" py "${TMP}/ungate.py" "${F}/proj" 2>&1 )" \
    || setup_error "worktree bootstrap failed on a non-self-dev project: ${UNGATE}"

W="${F}/proj/.endless/worktrees/e-600"
SB="${W}/.endless/sandbox"
if [[ -d "${SB}" ]]; then
    report_pass "worktree creation provisions a sandbox for a non-self-dev project"
else
    report_fail "worktree creation provisions a sandbox for a non-self-dev project" \
        "${SB} exists" "${UNGATE}"
fi
assert_eq "the sandbox is created EMPTY" ".gitignore" "$(cd "${SB}" && ls -A)"
assert_not_contains "worktree creation writes no XDG_CONFIG_HOME anywhere" \
    "XDG_CONFIG_HOME" "$(cat "${W}"/.claude/settings*.json 2>/dev/null || true)"

# Against real git, not by reading the file: the self-ignore is what spares
# every project a .gitignore entry and keeps `worktree land` from tripping.
git init -q -b main "${W}" 2>/dev/null || setup_error "cannot init the fixture repo"
printf 'x' > "${W}/README"
mkdir -p "${SB}/endless"
printf 'TOKEN=shh' > "${SB}/secrets.env"
printf '{}' > "${SB}/endless/config.json"
GIT_STATUS="$(git -C "${W}" status --porcelain --untracked-files=all 2>&1)"
assert_not_contains "git sees nothing in the sandbox" "sandbox" "${GIT_STATUS}"
assert_contains "the fixture repo is otherwise reporting files" "README" "${GIT_STATUS}"

# --------------------------------------------------------------------------- #
section "D. C3 — a missing sandbox REFUSES, and builds nothing"
# --------------------------------------------------------------------------- #

# W1 is a self-dev worktree that has no sandbox.
GO_REFUSAL="$( cd "${W1}" && HOME="${TMP}/home" "${GOBIN}" --db sandbox errors show 2>&1 )"
GO_RC=$?
assert_eq "Go refuses rather than opening a database" "2" "${GO_RC}"
assert_contains "Go's refusal says there is no sandbox" "no sandbox" "${GO_REFUSAL}"
assert_contains "Go's refusal names the worktree" "${W1}" "${GO_REFUSAL}"
assert_contains "Go's refusal names the remedy" "just dev-sandbox-init" "${GO_REFUSAL}"
assert_not_contains "Go's refusal names no deleted command" "sandbox migrate" "${GO_REFUSAL}"

PY_REFUSAL="$( cd "${W1}" && HOME="${TMP}/home" py -c '
from endless import config
try:
    config.apply_db_choice("sandbox")
except ValueError as e:
    print(e)
else:
    print("RESOLVED ANYWAY:", config.RESOLVED_CONFIG_DIR)' 2>&1 )"
assert_contains "Python refuses too, naming the remedy" "just dev-sandbox-init" "${PY_REFUSAL}"
assert_not_contains "Python resolved nothing" "RESOLVED ANYWAY" "${PY_REFUSAL}"

if [[ ! -e "${W1}/.endless/sandbox" ]]; then
    report_pass "neither refusal built a sandbox"
else
    report_fail "neither refusal built a sandbox" "still absent" \
        "$(find "${W1}/.endless/sandbox" 2>&1 | head -5)"
fi

# --------------------------------------------------------------------------- #
section "E. C4 — the hook runs main's recipes against an OLD worktree"
# --------------------------------------------------------------------------- #

# Shaped like the real failure. A "main" repo whose CURRENT justfile has the
# three recipes the hook calls, each stubbed to log which recipe ran and where;
# an OLDER commit whose justfile has none of them; and a task worktree checked
# out on that older commit. The hook is this tree's, run exactly as endless runs
# it: from the main checkout, cwd the worktree, $1 the worktree.
if ! command -v just >/dev/null 2>&1; then
    setup_error "just is not installed; the hook cannot be exercised"
fi

M="${TMP}/hookmain"
mkdir -p "${M}/.endless/hooks" "${M}/bin"
git -C "${TMP}" init -q -b main hookmain || setup_error "cannot init the hook fixture"
git -C "${M}" config user.email t@example.com
git -C "${M}" config user.name t

# The old commit: a justfile that predates every recipe the hook calls.
printf 'hello:\n    echo hi\n' > "${M}/justfile"
git -C "${M}" add -A && git -C "${M}" commit -q -m old || setup_error "fixture: old commit"
OLD_SHA="$(git -C "${M}" rev-parse HEAD)"

LOG="${TMP}/recipes.log"
cat > "${M}/justfile" <<JUST
go-work-init:
    echo "go-work-init \$(pwd)" >> "${LOG}"

dev-sandbox-init:
    echo "dev-sandbox-init \$(pwd)" >> "${LOG}"

claude-settings-init:
    echo "claude-settings-init \$(pwd)" >> "${LOG}"
JUST
printf '#!/bin/sh\nexit 0\n' > "${M}/bin/endless-go"
chmod +x "${M}/bin/endless-go"
git -C "${M}" add justfile && git -C "${M}" commit -q -m new || setup_error "fixture: new commit"

OLDWT="${M}/.endless/worktrees/e-700"
mkdir -p "${M}/.endless/worktrees"
git -C "${M}" worktree add -q --detach "${OLDWT}" "${OLD_SHA}" \
    || setup_error "fixture: cannot add the old worktree"

# Prove the fixture reproduces the failure before trusting it to show a fix:
# the hook as E-1964 first landed it, run the same way.
LANDED_HOOK="${TMP}/landed-hook.sh"
git -C "${WT}" show 904ef52:.endless/hooks/post-worktree-create.sh > "${LANDED_HOOK}" 2>/dev/null \
    || setup_error "cannot read the hook as first landed (904ef52)"
chmod +x "${LANDED_HOOK}"
LANDED_OUT="$( cd "${OLDWT}" && "${LANDED_HOOK}" "${OLDWT}" 2>&1 )"
LANDED_RC=$?
if (( LANDED_RC != 0 )); then
    report_pass "the fixture bites: the hook as first landed fails on an old worktree"
else
    report_fail "the fixture bites: the hook as first landed fails on an old worktree" \
        "non-zero exit" "exit 0: ${LANDED_OUT}"
fi
assert_contains "and fails the way the real run did" "does not contain recipe" "${LANDED_OUT}"

# Now this tree's hook, installed where endless looks for it.
cp "${WT}/.endless/hooks/post-worktree-create.sh" "${M}/.endless/hooks/post-worktree-create.sh"
rm -f "${LOG}"
HOOK_OUT="$( cd "${OLDWT}" && "${M}/.endless/hooks/post-worktree-create.sh" "${OLDWT}" 2>&1 )"
HOOK_RC=$?
assert_eq "this hook succeeds on the same old worktree" "0" "${HOOK_RC}"

RAN="$(cat "${LOG}" 2>/dev/null || true)"
# macOS resolves the scratch dir through /private; compare against both.
OLDWT_REAL="$(cd "${OLDWT}" && pwd -P)"
for r in go-work-init dev-sandbox-init claude-settings-init; do
    if [[ "${RAN}" == *"${r} ${OLDWT}"* || "${RAN}" == *"${r} ${OLDWT_REAL}"* ]]; then
        report_pass "${r} ran from main's justfile, inside the worktree"
    else
        report_fail "${r} ran from main's justfile, inside the worktree" \
            "${r} ${OLDWT}" "${RAN:-<nothing ran>}  /  hook said: ${HOOK_OUT}"
    fi
done

# --------------------------------------------------------------------------- #
section "F. C5, C6 — deleted commands, and the guard that stays"
# --------------------------------------------------------------------------- #

SANDBOX_HELP="$( cd "${TMP}" && HOME="${TMP}/home" uv run --project "${WT}" endless sandbox --help 2>&1 || true )"
assert_contains "'endless sandbox migrate' is gone" "No such command" "${SANDBOX_HELP}"

assert_not_contains "'sandbox bind' is gone from endless-go" "bind" \
    "$("${GOBIN}" sandbox --help 2>&1)"

# _guard.sh's isolation arm READS XDG_CONFIG_HOME, and that reader is not the
# injection this deleted. Removing the two together would have silently stopped
# the arm firing on a hand-run — the one thing it exists to catch.
cat > "${PROBE}" <<'PROBE_EOF'
#!/usr/bin/env bash
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
echo "GUARD DID NOT REFUSE"
PROBE_EOF
chmod +x "${PROBE}"
mkdir -p "${TMP}/realish/.config/endless"
PROBE_OUT="$(HOME="${TMP}/realish" XDG_CONFIG_HOME="${TMP}/realish/.config" \
    bash "${PROBE}" 2>&1 || true)"
assert_not_contains "the guard still refuses a hand-run suite" "GUARD DID NOT REFUSE" "${PROBE_OUT}"
assert_contains "and says why" "verify runner" "${PROBE_OUT}"

summary
