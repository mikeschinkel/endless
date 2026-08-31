#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2090 and records what was true when E-2090
# landed. Edit it only if you ARE E-2090. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2090 verification — the guard on a verify suite is un-forgeable, and it
# reaches the checkouts that need it.
#
# Before: a suite refused a direct run by checking that ENDLESS_VERIFY_RUN was
# set. Nothing ever read its value. `ENDLESS_VERIFY_RUN=anything ./verify.sh`
# walked straight past, so the one visible defence was a claim the caller made
# about itself — and because it looked like enforcement, no enforcement was
# built behind it. The own-task-only rule lived solely in the Go runner, which a
# direct run never reaches.
#
# Worse, the guard was only ever on `main`. 138 of this project's 142 worktrees
# branched before E-2023 landed and still carry the retired tests/tasks/*.sh
# corpus — as many as 203 unguarded, directly-runnable copies each. A guard that
# lives in a file cannot protect a checkout that predates the file.
#
# After:
#
#   1. _guard.sh asks two questions the caller cannot answer for itself. Whose
#      suite is this (the running file's own path names its task), and where is
#      it (the same path names the worktree). Mismatch refuses. Then: is a real
#      config reachable from here — because a direct run has your real $HOME,
#      and that is how a suite wrote into the main database.
#   2. Every suite carries a DO-NOT-EDIT banner naming its own owner, so the
#      rule is met when the file is opened rather than after it is edited.
#   3. `endless worktree sync` closes the drift, which is the only way an
#      in-file guard reaches a branch that predates it.
#
#   endless task verify E-2090        # or, in this repo, `just verify`
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

TASKS="${WT}/.endless/tasks"
TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test ./internal/verifycmd/... >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/verifycmd/..."
else
    report_fail "go test ./internal/verifycmd/..." "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

if uv run pytest tests/test_suite_guard.py tests/test_worktree_sync.py \
        tests/test_suite_rules.py -q >"${TMP}/py.log" 2>&1; then
    report_pass "pytest test_suite_guard, test_worktree_sync, test_suite_rules"
else
    report_fail "pytest guard/sync/rules" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. the marker is gone, and inert where it is remembered ─────────────────
section "2. The forgeable marker is gone"

assert_eq "the harness no longer mentions ENDLESS_VERIFY_RUN" "0" \
    "$(grep -c 'ENDLESS_VERIFY_RUN' "${TASKS}/_harness.sh" || true)"
assert_eq "the runner no longer exports it" "0" \
    "$(grep -c '^\s*EnvRunMarker' "${WT}/internal/verifycmd/script.go" || true)"

# Setting it must change nothing. This is the whole point: a variable that
# used to be the difference between refusing and running is now inert.
out="$(ENDLESS_VERIFY_RUN=/tmp/anything bash "${TASKS}/e-1001/verify.sh" 2>&1)"; rc=$?
assert_eq "a landed foreign suite still refuses WITH the old marker set" "2" "${rc}"
assert_contains "and refuses on ownership, not on a missing variable" \
    "refusing to run E-1001's verification suite" "${out}"

# ── 3. ownership, from the paths alone ──────────────────────────────────────
section "3. A suite runs only in its own task's worktree"

out="$(env -u ENDLESS_VERIFY_RUN bash "${TASKS}/e-1001/verify.sh" 2>&1)"; rc=$?
assert_eq "a foreign suite refuses, exit 2" "2" "${rc}"
assert_contains "the refusal names the suite's owner" "this suite belongs to: E-1001" "${out}"
assert_contains "and where the caller actually is" "E-2090's worktree" "${out}"
assert_contains "and what to run instead" "endless task verify E-2090" "${out}"
assert_not_contains "and nothing of the suite's own ran first" "✓" "${out}"

# A synthetic checkout, so the two path shapes are exercised rather than assumed.
build_checkout() { # <root> <suite-task>
    mkdir -p "$1/.endless/tasks/e-$2"
    cp "${TASKS}/_guard.sh" "$1/.endless/tasks/_guard.sh"
    printf '%s\n%s\n%s\n' '#!/usr/bin/env bash' \
        'source "$(dirname "${BASH_SOURCE[0]}")/../_guard.sh"' 'echo RAN' \
        > "$1/.endless/tasks/e-$2/verify.sh"
}
ISO="HOME=${TMP}/home XDG_CONFIG_HOME=${TMP}/home/.config"
mkdir -p "${TMP}/home"

build_checkout "${TMP}/p/.endless/worktrees/e-1001" 1001
out="$(env ${ISO} bash "${TMP}/p/.endless/worktrees/e-1001/.endless/tasks/e-1001/verify.sh" 2>&1)"
assert_contains "a suite in its OWN worktree runs" "RAN" "${out}"

build_checkout "${TMP}/q" 1001
out="$(env ${ISO} bash "${TMP}/q/.endless/tasks/e-1001/verify.sh" 2>&1)"
assert_contains "a suite outside any worktree abstains (reaped worktree, no-worktree project)" \
    "RAN" "${out}"

# ── 4. isolation, from the environment's actual shape ───────────────────────
section "4. A direct run refuses even in your own worktree"

mkdir -p "${TMP}/realhome/.config/endless"
out="$(env HOME="${TMP}/realhome" XDG_CONFIG_HOME="${TMP}/realhome/.config" \
    bash "${TMP}/p/.endless/worktrees/e-1001/.endless/tasks/e-1001/verify.sh" 2>&1)"; rc=$?
assert_eq "owning the suite is not enough when a real config is reachable" "2" "${rc}"
assert_not_contains "the suite did not run" "RAN" "${out}"
assert_contains "and it names the sanctioned command for THIS suite" \
    "endless task verify E-1001" "${out}"

# ── 5. the corpus ───────────────────────────────────────────────────────────
section "5. Every suite in the corpus"

HARNESS_LINE='source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"'
bad_first=0; bad_banner=0; bad_double=0; total=0
for s in "${TASKS}"/e-*/verify.sh; do
    total=$((total + 1))
    id="$(basename "$(dirname "${s}")")"; id="E-${id#e-}"
    first="$(grep -vE '^\s*(#|$)' "${s}" | head -1)"
    [[ "${first}" == "${HARNESS_LINE}" ]] || { bad_first=$((bad_first + 1)); }
    [[ "$(sed -n '2p' "${s}")" == "# ── DO NOT EDIT"* ]] \
        && grep -q "^# This suite belongs to ${id} " "${s}" || bad_banner=$((bad_banner + 1))
    grep -vE '^\s*#' "${s}" | grep -qE '^\s*(source|\.) .*_guard\.sh' && bad_double=$((bad_double + 1))
done
assert_contains "the corpus is the whole corpus, not an empty glob" "yes" \
    "$( ((total > 150)) && echo yes || echo "only ${total}")"
assert_eq "every suite reaches the guard on its first executable line" "0" "${bad_first}"
assert_eq "every suite carries a banner naming its own owner" "0" "${bad_banner}"
assert_eq "and none doubles the guard by sourcing it directly too" "0" "${bad_double}"

# ── 6. the banner sweep is idempotent ───────────────────────────────────────
section "6. Re-running the banner sweep is a no-op"

# Hash the corpus rather than ask git: the property is that the sweep does not
# change a file, which must hold whether or not the tree happens to be committed.
before="$(cat "${TASKS}"/e-*/verify.sh | shasum)"
out="$(just suite-banner 2>&1)"
after="$(cat "${TASKS}"/e-*/verify.sh | shasum)"
assert_contains "no banner is added on a second run" "0 banner(s) added" "${out}"
assert_eq "and not one byte of the corpus changed" "${before}" "${after}"

# ── 7. the sweep that delivers the guard ────────────────────────────────────
section "7. endless worktree sync"

sync_help="$(uv run python -c '
from endless.cli import main
sync = main.commands["worktree"].commands["sync"]
apply = [p for p in sync.params if p.name == "apply"][0]
print("registered", apply.is_flag, apply.default)
' 2>&1)"
assert_contains "sync is a registered worktree subcommand" "registered" "${sync_help}"
assert_contains "and it is a dry run unless --apply is passed" "registered True False" "${sync_help}"

# The decision function drives the sweep; a real git pair proves it rather than
# asserting it. A worktree that is behind is a candidate; one with uncommitted
# work never is, whatever else is true of it.
G="${TMP}/g"; mkdir -p "${G}"
( cd "${G}" && git init -q -b main && git config user.email t@t.t && git config user.name t \
  && echo v1 > shared.txt && git add -A && git commit -qm init \
  && git worktree add -q -b feat "${TMP}/gw" \
  && echo v2 > shared.txt && git commit -qam moved ) >/dev/null 2>&1 \
  || setup_error "could not build the git fixture"

state="$(uv run python -c "
from pathlib import Path
from endless.worktree_cmd import _sync_state
print(_sync_state(Path('${TMP}/gw'), 'main', None)[0])
")"
assert_eq "a worktree behind the base branch is a rebase candidate" "rebase" "${state}"

echo "in flight" > "${TMP}/gw/wip.py"
state="$(uv run python -c "
from pathlib import Path
from endless.worktree_cmd import _sync_state
d, r = _sync_state(Path('${TMP}/gw'), 'main', None)
print(d, r)
")"
assert_contains "one with uncommitted work is skipped, naming the file" "skip 1 uncommitted" "${state}"
assert_contains "and it names the file rather than citing a rule" "wip.py" "${state}"

summary
