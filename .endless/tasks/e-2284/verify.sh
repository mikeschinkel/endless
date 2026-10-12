#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2284 and records what was true when E-2284
# landed. Edit it only if you ARE E-2284. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2284: a Go settings registry with doctor metadata; opt-ins become the
# strings "on" / "off" / "unset".
#
# What is verified here:
#   A. Fail-fast: the touched packages' Go tests, this task's contract tests by
#      NAME (so deleting one cannot turn the section green by absence), and the
#      Python tests for `endless project set`.
#   B. End to end through a binary built from this tree, under the runner's
#      temp HOME: a legacy true/false is refused one line per key with the file
#      and the value to write; `settings migrate` rewrites exactly those bytes;
#      the export then reports values and sources; --metadata reads no file.
#   C. This repository's own .endless/config.json loads with the new binary —
#      the in-repo half of the land-time migration.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"
command -v python3 >/dev/null 2>&1 || setup_error "python3 is required to read --json"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/settings ./internal/settingscmd ./internal/config ./internal/jobs
      ./internal/mainsyncjob ./internal/autospawnjob ./internal/triagejob
      ./internal/docsweep ./internal/raterjob ./internal/unlandedjob
      ./internal/backupjob ./internal/minimizerjob ./cmd/endless-go)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: settings, settingscmd, config, jobs, the job packages, cmd/endless-go"
else
    report_fail "go test: settings, settingscmd, config, jobs, the job packages, cmd/endless-go" \
        "exit 0" "$(printf '%s' "${out}" | tail -40)"
    summary
fi

check_named() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}

check_named ./cmd/endless-go TestInitialize
check_named ./cmd/endless-go TestEveryConfigKeyIsRegistered
check_named ./internal/settings TestInitialize_ReportsEveryFailure
check_named ./internal/settings TestAccessorsRefuseBeforeInitialize
check_named ./internal/settings TestParseOptIn
check_named ./internal/jobs TestInitialize_RejectsEveryBadRegistration
check_named ./internal/config TestNormalize_RefusesALegacyBoolean
check_named ./internal/settingscmd TestMigrateFile
check_named ./internal/settingscmd TestBuild_ValuesAndSources

if out=$(uv run pytest tests/test_project_set_registered.py tests/test_project_set_fields_help.py -q 2>&1); then
    report_pass "pytest: project set over the exported registry"
else
    report_fail "pytest: project set over the exported registry" "exit 0" "$(printf '%s' "${out}" | tail -40)"
fi

# ---------------------------------------------------------------------------
section "B. End to end through a binary built from this tree"
# ---------------------------------------------------------------------------

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"

# The machine file as the binary resolves it under the runner's temp HOME.
mkdir -p "${TMP}/empty"
MACHINE="$("${BIN}" settings list --json --project "${TMP}/empty" 2>/dev/null \
    | python3 -c 'import json, sys; print(json.load(sys.stdin)["machine_file"])')" \
    || setup_error "cannot read the machine file path from settings list"
PROJ="${TMP}/proj"
mkdir -p "$(dirname "${MACHINE}")" "${PROJ}/.endless"
PFILE="${PROJ}/.endless/config.json"

printf '%s\n' '{"checks": {"drift_detection": true}, "roots": ["~/x"]}' >"${MACHINE}"
cat >"${PFILE}" <<'EOF'
{
  "name": "proj",
  "main_sync": {"enabled": true},
  "prime": {"enabled": false, "cap": 2},
  "matchers": [{"type": "pivot", "enabled": true}]
}
EOF

OUT="$("${BIN}" settings list --project "${PROJ}" 2>&1)"; RC=$?
assert_eq "a legacy boolean stops settings list" "1" "${RC}"
assert_contains "one line names the machine file, the key and the fix" \
    "${MACHINE}: checks.drift_detection is the legacy boolean true; write \"on\" instead" "${OUT}"
assert_contains "and the project file's true" \
    "${PFILE}: main_sync.enabled is the legacy boolean true; write \"on\" instead" "${OUT}"
assert_contains "and its false" \
    "${PFILE}: prime.enabled is the legacy boolean false; write \"off\" instead" "${OUT}"
assert_contains "and the refusal names the migrate command" "endless-go settings migrate" "${OUT}"

META="$("${BIN}" settings list --json --metadata 2>&1)"; RC=$?
assert_eq "--metadata reads no file, so the broken files do not stop it" "0" "${RC}"

BEFORE="$(cat "${PFILE}")"
MIG="$("${BIN}" settings migrate "${MACHINE}" "${PFILE}" 2>&1)"
assert_contains "migrate reports each rewrite" "main_sync.enabled true -> \"on\"" "${MIG}"
AFTER="$(cat "${PFILE}")"
EXPECTED="$(printf '%s' "${BEFORE}" \
    | sed -e 's/"main_sync": {"enabled": true}/"main_sync": {"enabled": "on"}/' \
          -e 's/"prime": {"enabled": false/"prime": {"enabled": "off"/')"
assert_eq "migrate changes only the opt-in literals (the array's enabled stays true)" "${EXPECTED}" "${AFTER}"
assert_contains "the machine file's check is rewritten too" '"drift_detection": "on"' "$(cat "${MACHINE}")"

AGAIN="$("${BIN}" settings migrate "${PFILE}" 2>&1)"
assert_contains "a second migrate finds nothing" "nothing to migrate" "${AGAIN}"

JSON="$("${BIN}" settings list --json --project "${PROJ}" 2>&1)"; RC=$?
assert_eq "after migrating, settings list succeeds" "0" "${RC}"
field() {
    printf '%s' "${JSON}" | python3 -c '
import json, sys
doc = json.load(sys.stdin)
s = {e["id"]: e for e in doc["settings"]}[sys.argv[1]]
print(s[sys.argv[2]])' "$1" "$2"
}
assert_eq "main_sync.enabled is on" "on" "$(field main_sync.enabled value)"
assert_eq "from the project file" "project" "$(field main_sync.enabled source)"
assert_eq "prime.enabled is an explicit off" "off/project" \
    "$(field prime.enabled value)/$(field prime.enabled source)"
assert_eq "auto_spawn.enabled is unset, so its default" "off/default" \
    "$(field auto_spawn.enabled value)/$(field auto_spawn.enabled source)"
assert_eq "a suggestable opt-in carries a rationale" "True" \
    "$(printf '%s' "${JSON}" | python3 -c '
import json, sys
s = {e["id"]: e for e in json.load(sys.stdin)["settings"]}["fault_triage.enabled"]
print(bool(s["suggestable"] and s["rationale"]))')"
assert_eq "checks.bash_git_writes defaults on" "on/default" \
    "$(field checks.bash_git_writes value)/$(field checks.bash_git_writes source)"
assert_eq "auto_spawn.interval is a machine-file setting" "machine" "$(field auto_spawn.interval files)"

# ---------------------------------------------------------------------------
section "C. This repository's own config loads"
# ---------------------------------------------------------------------------

printf '{}\n' >"${MACHINE}"
OWN="$("${BIN}" settings list --json --project "${WT}" 2>&1)"; RC=$?
assert_eq "settings list over this repo's .endless/config.json succeeds" "0" "${RC}"
JSON="${OWN}"
assert_eq "main_sync.enabled is on, from this repo's file" "on/project" \
    "$(field main_sync.enabled value)/$(field main_sync.enabled source)"
assert_eq "prime.enabled is on, from this repo's file" "on/project" \
    "$(field prime.enabled value)/$(field prime.enabled source)"

summary
