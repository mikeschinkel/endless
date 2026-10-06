#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2243 and records what was true when E-2243
# landed. Edit it only if you ARE E-2243. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2243 — every passing verify run's CTRF report is committed to main.
#
#   1. The task's own unit tests (the runner's per-run reports and CTRF line,
#      the commit verb, the Python wrapper's flow), fail-fast.
#   2. End to end through THIS worktree's `endless-go` and editable `endless`,
#      on a throwaway project whose one suite fails or passes on a switch kept
#      outside the repo: a dirty tree is refused before anything runs; failing
#      runs commit nothing and pile up in the cache; the passing run commits
#      exactly one report at verify-<timestamp>-<sha>.ctrf.json, as a commit of
#      its own, empties the cache, and names the report on one CTRF line; a
#      second pass is a second commit, never an amend.
#   3. The commit verb refuses a linked worktree, so a record cannot land on a
#      task branch.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go git uv; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP=$(mktemp -d)
trap 'rm -rf "${WORK_TMP}"' EXIT

section "1. Unit tests (fail-fast)"
GO_PKGS=(./internal/verifycmd/ ./internal/events/)
if go test -count=1 "${GO_PKGS[@]}" >"${WORK_TMP}/go.log" 2>&1; then
    report_pass "go test ${GO_PKGS[*]}"
else
    report_fail "go test ${GO_PKGS[*]}" "$(tail -40 "${WORK_TMP}/go.log")"
    summary
fi
if uv run pytest -q tests/test_verify_cmd.py >"${WORK_TMP}/py.log" 2>&1; then
    report_pass "pytest tests/test_verify_cmd.py"
else
    report_fail "pytest tests/test_verify_cmd.py" "$(tail -40 "${WORK_TMP}/py.log")"
    summary
fi

ENDLESS="${REPO_ROOT}/.venv/bin/endless"
[[ -x "${ENDLESS}" ]] || setup_error "no editable endless at ${ENDLESS} (uv sync)"
BIN_DIR="${WORK_TMP}/bin"
mkdir -p "${BIN_DIR}"
go build -o "${BIN_DIR}/endless-go" ./cmd/endless-go >"${WORK_TMP}/build.log" 2>&1 \
    || setup_error "build endless-go: $(cat "${WORK_TMP}/build.log")"

# Everything below runs on a throwaway project with its own HOME and cache, and
# with this worktree's endless-go first on PATH.
export HOME="${WORK_TMP}/home"
export XDG_CONFIG_HOME="${HOME}/.config"
export XDG_CACHE_HOME="${HOME}/.cache"
export PATH="${BIN_DIR}:${PATH}"
mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

g() { git -C "$1" "${@:2}" >/dev/null 2>&1 || setup_error "git ${*:2} in $1"; }
new_repo() {
    g "${WORK_TMP}" init -q -b main "$1"
    g "$1" config user.email t@example.com
    g "$1" config user.name t
    g "$1" config commit.gpgsign false
}

PROJ="${WORK_TMP}/proj"
SWITCH="${WORK_TMP}/suite-passes"
new_repo "${PROJ}"
mkdir -p "${PROJ}/.endless/tasks/e-1"
printf '{"name":"proj"}\n' >"${PROJ}/.endless/config.json"
cat >"${PROJ}/.endless/tasks/e-1/verify.sh" <<EOF
#!/usr/bin/env bash
[[ -f "${SWITCH}" ]]
EOF
chmod +x "${PROJ}/.endless/tasks/e-1/verify.sh"
g "${PROJ}" add -A
g "${PROJ}" commit -q -m init
REPORT_DIR=$(cd "${PROJ}" && endless-go verify --report-dir E-1) \
    || setup_error "endless-go verify --report-dir E-1"

verify() { (cd "${PROJ}" && "${ENDLESS}" task verify E-1) >"${WORK_TMP}/out" 2>&1; echo $?; }
cached() { find "${REPORT_DIR}" -name '*.ctrf.json' 2>/dev/null | wc -l | tr -d ' '; }
recorded() { find "${PROJ}/.endless/tasks/e-1" -name 'verify-*.ctrf.json' | wc -l | tr -d ' '; }
head_of() { git -C "${PROJ}" rev-parse HEAD; }

section "2. Through endless task verify, end to end"
# os.UserCacheDir: ~/Library/Caches on macOS, $XDG_CACHE_HOME elsewhere.
assert_contains "the report dir is the task's, in the user cache" "/endless/verify/E-1" "${REPORT_DIR}"
assert_contains "under this run's HOME" "${HOME}/" "${REPORT_DIR}"

printf 'x\n' >"${PROJ}/stray.txt"
BEFORE=$(head_of)
assert_eq "a dirty tree is refused" "1" "$(verify)"
assert_contains "the refusal names the uncommitted file" "stray.txt" "$(cat "${WORK_TMP}/out")"
assert_eq "nothing ran: no report was written" "0" "$(cached)"
rm "${PROJ}/stray.txt"

assert_eq "first failing run exits 1" "1" "$(verify)"
assert_contains "a failing run names its cache report" "CTRF: " "$(cat "${WORK_TMP}/out")"
assert_eq "second failing run exits 1" "1" "$(verify)"
assert_eq "failing runs commit nothing" "${BEFORE}" "$(head_of)"
assert_eq "failed reports accumulate in the cache" "2" "$(cached)"

touch "${SWITCH}"
SHA=$(git -C "${PROJ}" rev-parse --short HEAD)
assert_eq "the passing run exits 0" "0" "$(verify)"
OUT=$(cat "${WORK_TMP}/out")
assert_eq "exactly one report is recorded" "1" "$(recorded)"
REL=$(cd "${PROJ}" && git show --name-only --format= HEAD)
NAME=$(basename "${REL}")
assert_contains "it is committed in the task's directory" ".endless/tasks/e-1/verify-" "${REL}"
assert_contains "its name carries the SHA it tested" "Z-${SHA}.ctrf.json" "${NAME}"
assert_eq "as a commit of its own on main" "Endless: verify E-1 ${NAME}" "$(git -C "${PROJ}" log -1 --format=%s)"
assert_eq "on top of what was there" "${BEFORE}" "$(git -C "${PROJ}" rev-parse HEAD~1)"
assert_eq "the task's cache is emptied" "0" "$(cached)"
assert_eq "one CTRF line" "1" "$(grep -c '^CTRF: ' <<<"${OUT}")"
assert_contains "naming the committed report" "${REL}" "$(grep '^CTRF: ' <<<"${OUT}")"
assert_contains "the report is CTRF" '"results"' "$(cat "${PROJ}/${REL}")"

FIRST=$(head_of)
assert_eq "a second passing run exits 0" "0" "$(verify)"
assert_eq "it is a second record" "2" "$(recorded)"
assert_eq "a new commit, not an amend of the first" "${FIRST}" "$(git -C "${PROJ}" rev-parse HEAD~1)"

section "3. The commit verb, on a linked worktree"
WT="${WORK_TMP}/wt"
g "${PROJ}" worktree add -q -b task/1 "${WT}"
mkdir -p "${WT}/.endless/tasks/e-1"
printf '{}\n' >"${WT}/.endless/tasks/e-1/verify-x.ctrf.json"
WT_HEAD=$(git -C "${WT}" rev-parse HEAD)
endless-go event commit-verify-report --project-root "${WT}" \
    --path .endless/tasks/e-1/verify-x.ctrf.json --task E-1 >"${WORK_TMP}/wt.out" 2>&1
assert_eq "it refuses" "1" "$?"
assert_contains "naming the linked worktree" "linked worktree" "$(cat "${WORK_TMP}/wt.out")"
assert_eq "the task branch did not move" "${WT_HEAD}" "$(git -C "${WT}" rev-parse HEAD)"

summary
