#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1921 and records what was true when E-1921
# landed. Edit it only if you ARE E-1921. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1921 — build endless-go once per pytest session and point every test's
# binary lookup at that build.
#
# The claim: neither `<checkout>/bin/endless-go` nor any endless-go on PATH can
# influence which binary a Python test runs. Proved by poisoning both with a
# binary that logs its invocation and exits 99, then requiring the suite to pass
# AND the poison log to stay empty. A test that still reached either one would
# fail, or — if it tolerated the failure — would leave a line in the log.
#
#   1. Fail fast: the tests this task changed or added.
#   2. THE INCIDENT, inverted: the FULL suite with bin/ and PATH poisoned. This
#      is also the project-wide regression gate, run once rather than twice.
#   3. bin/endless-go removed entirely: the previously exposed files pass with
#      zero skips (the silent-skip route is gone).
#   4. Structure: the only `go build` of ./cmd/endless-go under tests/ is
#      conftest's.
#
# bin/endless-go is restored (or left absent, if it was) by an EXIT trap.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || setup_error "not inside a git worktree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"
for tool in go uv git; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP=$(mktemp -d)
CHECKOUT_BIN="${ROOT}/bin/endless-go"
SAVED_BIN="${WORK_TMP}/saved-endless-go"
HAD_BIN=0
if [[ -e "${CHECKOUT_BIN}" ]]; then
    HAD_BIN=1
    cp -p "${CHECKOUT_BIN}" "${SAVED_BIN}" || setup_error "cannot save ${CHECKOUT_BIN}"
fi
restore_bin() {
    if (( HAD_BIN )); then
        mkdir -p "${ROOT}/bin"
        cp -p "${SAVED_BIN}" "${CHECKOUT_BIN}"
    else
        rm -f "${CHECKOUT_BIN}"
    fi
    rm -rf "${WORK_TMP}"
}
trap restore_bin EXIT

POISON_LOG="${WORK_TMP}/poison.log"
POISON_PATH_DIR="${WORK_TMP}/poison-path"
mkdir -p "${POISON_PATH_DIR}"
write_poison() {  # write_poison <dest> <route-label>
    cat >"$1" <<EOF
#!/bin/sh
printf '%s\t%s\t%s\n' "$2" "\${PYTEST_CURRENT_TEST:-<collection>}" "\$*" >>"${POISON_LOG}"
exit 99
EOF
    chmod +x "$1"
}

EXPOSED=(tests/test_template_materialize.py tests/test_land_conflict.py
         tests/test_guide_conditionals.py tests/test_worktree_land_migration_gate.py
         tests/test_endless_go_bin.py)
CHANGED=("${EXPOSED[@]}" tests/test_default_branch_parity.py tests/test_epic_cmd.py
         tests/test_status_registry_client.py tests/test_session_state_lazy_lookup.py)

# pytest's closing summary line, e.g. "== 128 passed, 2 skipped in 3.1s ==".
summary_line() { grep -E '[0-9]+ (passed|failed|error)' "$1" | tail -1; }

# ── 1. fail fast ─────────────────────────────────────────────────────────────
section "1. The tests this task changed (fail fast)"
if uv run pytest -q -p no:cacheprovider -rs "${CHANGED[@]}" >"${WORK_TMP}/changed.log" 2>&1; then
    report_pass "changed tests: $(summary_line "${WORK_TMP}/changed.log")"
else
    report_fail "changed tests pass" "exit 0" "$(tail -40 "${WORK_TMP}/changed.log")"
    summary
fi

# ── 2. full suite, bin/ and PATH poisoned ────────────────────────────────────
section "2. Full suite with bin/endless-go and PATH's endless-go poisoned"
mkdir -p "${ROOT}/bin"
write_poison "${CHECKOUT_BIN}" "checkout-bin"
write_poison "${POISON_PATH_DIR}/endless-go" "path"
: >"${POISON_LOG}"
if PATH="${POISON_PATH_DIR}:${PATH}" uv run pytest -q -p no:cacheprovider tests/ \
        >"${WORK_TMP}/full.log" 2>&1; then
    report_pass "full suite passes: $(summary_line "${WORK_TMP}/full.log")"
else
    report_fail "full suite passes with bin/ and PATH poisoned" "exit 0" \
        "$(tail -60 "${WORK_TMP}/full.log")"
fi
assert_eq "no test or collection step ran the poisoned bin/ or PATH binary" \
    "" "$(sort "${POISON_LOG}" | uniq -c | head -20)"

# ── 3. bin/endless-go absent ─────────────────────────────────────────────────
section "3. bin/endless-go absent: the exposed files pass with zero skips"
rm -f "${CHECKOUT_BIN}"
if uv run pytest -q -p no:cacheprovider -rs "${EXPOSED[@]}" >"${WORK_TMP}/absent.log" 2>&1; then
    report_pass "exposed files pass without bin/endless-go: $(summary_line "${WORK_TMP}/absent.log")"
else
    report_fail "exposed files pass without bin/endless-go" "exit 0" \
        "$(tail -40 "${WORK_TMP}/absent.log")"
fi
assert_not_contains "no exposed test skips" "skipped" "$(summary_line "${WORK_TMP}/absent.log")"

# ── 4. structure ─────────────────────────────────────────────────────────────
section "4. One build site"
assert_eq "the only go build of ./cmd/endless-go under tests/ is conftest's" \
    "tests/conftest.py" \
    "$(grep -rlE --include='*.py' '"build".*"\./cmd/endless-go"' tests/ | sort | tr '\n' ' ' | sed 's/ $//')"
assert_eq "no test file resolves <checkout>/bin/endless-go itself" "" \
    "$(grep -rnE --include='test_*.py' '(__file__|REPO_ROOT).*["'"'"']bin["'"'"']' tests/ || true)"

summary
