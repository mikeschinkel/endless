#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2217 and records what was true when E-2217
# landed. Edit it only if you ARE E-2217. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2217 — separate a project's committed identity from the machine-local slug.
#
# What shipped:
#   - identity = the repo's root commit (oldest of several), stored in
#     .endless/config.json at registration; projects.identity (UNIQUE).
#   - projects.slug (UNIQUE), machine-local, = name by default, kept across a
#     rename, never written to a committed file. Migration 22 adds both.
#   - `project register` refuses: no repo / no commits; a second clone or fork
#     of a registered project (names the path, points at worktrees, says forks
#     are unsupported); a name another registered project holds.
#   - project sync no longer repoints a project at a second clone.
#   - `project backfill-identity` records identities for older registrations.
#
# ISOLATION: throwaway repos under a temp HOME/XDG_CONFIG_HOME, and a COPY of
# this branch's endless-go outside the worktree (a binary run from inside a
# task worktree refuses any database it takes for the main one, ED-1601).
# Nothing here reads or writes this worktree's state or a real database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT="$(git -C "${ENDLESS_VERIFY_DIR}" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"
for tool in go uv git just; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP="$(mktemp -d)" || setup_error "cannot make a temp dir"
trap 'rm -rf "${WORK_TMP}"' EXIT
WORK_TMP="$(cd "${WORK_TMP}" && pwd -P)"

# ── 1. this task's own tests, fail fast ──────────────────────────────────────
section "1. This task's own tests (fail fast)"
if go test -count=1 ./internal/schema/... >"${WORK_TMP}/go.log" 2>&1; then
    report_pass "go: migration 22 and schema.sql parity"
else
    report_fail "go test ./internal/schema/..." "ok" "$(tail -30 "${WORK_TMP}/go.log")"
    summary
fi
if just build >"${WORK_TMP}/build.log" 2>&1; then
    report_pass "just build"
else
    report_fail "just build" "exit 0" "$(tail -20 "${WORK_TMP}/build.log")"
    summary
fi
if uv run pytest -q -p no:cacheprovider tests/test_project_identity.py \
        tests/test_register.py tests/test_project_sync.py \
        tests/test_ignored_projects.py tests/test_cli.py >"${WORK_TMP}/py.log" 2>&1; then
    report_pass "pytest: identity, register, project sync, ignored projects, cli"
else
    report_fail "pytest" "exit 0" "$(tail -30 "${WORK_TMP}/py.log")"
    summary
fi

# ── a throwaway machine, driven through the real CLI ─────────────────────────
ENDLESS="$(uv run python -c 'import sys, pathlib; print(pathlib.Path(sys.executable).parent / "endless")' 2>/dev/null)"
[[ -x "${ENDLESS}" ]] || setup_error "no endless entry point at ${ENDLESS:-<unresolved>}"
mkdir -p "${WORK_TMP}/bin" "${WORK_TMP}/home/.config"
cp "${ROOT}/bin/endless-go" "${WORK_TMP}/bin/endless-go" || setup_error "cannot copy endless-go"
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@e.x GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@e.x

P="${WORK_TMP}/Projects"
mkdir -p "${P}"

# es_in <dir> <args...>: this branch's CLI, run from <dir>, against the temp
# machine. es runs it from the projects root.
es_in() {
    local dir="$1"; shift
    ( cd "${dir}" &&
      env -u ENDLESS_SESSION_ID -u ENDLESS_DB_CHOICE \
          HOME="${WORK_TMP}/home" XDG_CONFIG_HOME="${WORK_TMP}/home/.config" \
          PATH="${WORK_TMP}/bin:${PATH}" ENDLESS_AUTO_MIGRATE=1 ENDLESS_NO_TRIAGE=1 \
          "${ENDLESS}" --no-session "$@" 2>&1 )
}
es() { es_in "${P}" "$@"; }
sql() { es sql "$1"; }
new_repo() {
    mkdir -p "$1"
    git -C "$1" init -q -b main
    git -C "$1" -c commit.gpgsign=false commit -q --allow-empty -m "root of $1"
}

# ── 2. registering records the identity ──────────────────────────────────────
section "2. Register records the root commit as the identity"
new_repo "${P}/alpha"
git -C "${P}/alpha" -c commit.gpgsign=false commit -q --allow-empty -m "second"
ROOT_SHA="$(git -C "${P}/alpha" rev-list --max-parents=0 HEAD)"
out="$(es project register "${P}/alpha" --infer)"
assert_contains "alpha registers" "Registered" "${out}"
cfg="$(cat "${P}/alpha/.endless/config.json" 2>&1)"
assert_contains "config.json carries the root commit, not HEAD" "\"identity\": \"${ROOT_SHA}\"" "${cfg}"
assert_contains "the row carries the same identity" "${ROOT_SHA}" "$(sql "SELECT identity FROM projects WHERE name='alpha'")"
assert_contains "the slug defaults to the name" "alpha" "$(sql "SELECT slug FROM projects WHERE name='alpha'")"
assert_contains "project info shows the identity" "${ROOT_SHA}" "$(es_in "${P}/alpha" project info)"
git -C "${P}/alpha" add .endless/config.json
git -C "${P}/alpha" -c commit.gpgsign=false commit -q -m "register"

# ── 3. a second checkout is refused ──────────────────────────────────────────
section "3. A second clone (or a fork) is refused"
git clone -q "${P}/alpha" "${P}/alpha-clone"
out="$(es project register "${P}/alpha-clone" --infer --name alpha-two)"; rc=$?
assert_eq "registering a clone exits non-zero" "1" "${rc}"
assert_contains "the refusal names the registered path" "${P}/alpha" "${out}"
assert_contains "the refusal points at worktrees" "worktree" "${out}"
assert_contains "the refusal says forks are unsupported" "Forks are not yet supported" "${out}"
assert_not_contains "the clone was not registered" "alpha-two" "$(sql "SELECT name FROM projects")"
assert_eq "the clone was left untouched" "" "$(git -C "${P}/alpha-clone" status --porcelain)"

# ── 4. a clashing name is refused ────────────────────────────────────────────
section "4. An unrelated project with a clashing name is refused"
new_repo "${WORK_TMP}/elsewhere/alpha"
out="$(es project register "${WORK_TMP}/elsewhere/alpha" --infer)"; rc=$?
assert_eq "registering the clash exits non-zero" "1" "${rc}"
assert_contains "the refusal names the clashing path" "${P}/alpha" "${out}"
assert_contains "the refusal names the fix" "--name" "${out}"
out="$(es project register "${WORK_TMP}/elsewhere/alpha" --infer --name alpha-other)"
assert_contains "it registers under another name" "Registered" "${out}"

# ── 5. no repo, no commits ───────────────────────────────────────────────────
section "5. A directory with no root commit is refused"
mkdir -p "${P}/plain"
out="$(es project register "${P}/plain" --infer)"; rc=$?
assert_eq "a plain directory is refused" "1" "${rc}"
assert_contains "it says why" "Not a git repository" "${out}"
mkdir -p "${P}/unborn"; git -C "${P}/unborn" init -q
out="$(es project register "${P}/unborn" --infer)"; rc=$?
assert_eq "a repo with no commits is refused" "1" "${rc}"
assert_contains "it says why" "No commits yet" "${out}"

# ── 6. project sync leaves the registration alone ────────────────────────────
section "6. Project sync does not hand the project to the clone"
es project list --no-limit >/dev/null
path_now="$(sql "SELECT path FROM projects WHERE name='alpha'")"
assert_contains "alpha is still registered at its own checkout" "${P}/alpha" "${path_now}"
assert_not_contains "alpha was not repointed at the clone" "alpha-clone" "${path_now}"

# ── 7. the slug stays machine-local ──────────────────────────────────────────
section "7. The slug survives a rename and never reaches a committed file"
es project rename alpha omega >/dev/null
assert_contains "the slug kept the registered name" "alpha" "$(sql "SELECT slug FROM projects WHERE name='omega'")"
cfg="$(cat "${P}/alpha/.endless/config.json")"
assert_not_contains "config.json carries no slug" "slug" "${cfg}"
assert_not_contains "config.json does not carry the old name" "\"alpha\"" "${cfg}"

# ── 8. backfill ──────────────────────────────────────────────────────────────
section "8. backfill-identity records older registrations"
new_repo "${P}/legacy"
mkdir -p "${P}/legacy/.endless"
printf '{"name": "legacy", "status": "active"}\n' > "${P}/legacy/.endless/config.json"
git -C "${P}/legacy" add .endless/config.json
git -C "${P}/legacy" -c commit.gpgsign=false commit -q -m "config"
es sql --write "INSERT INTO projects (name, slug, path, status) VALUES ('legacy', 'legacy', '${P}/legacy', 'active')" >/dev/null
LEGACY_SHA="$(git -C "${P}/legacy" rev-list --max-parents=0 HEAD)"
before="$(git -C "${P}/legacy" rev-parse HEAD)"
out="$(es project backfill-identity)"
assert_contains "it reports the commit" "committed .endless/config.json" "${out}"
assert_contains "config.json now carries the identity" "${LEGACY_SHA}" "$(cat "${P}/legacy/.endless/config.json")"
assert_eq "exactly one commit was made" "${before}" "$(git -C "${P}/legacy" rev-parse HEAD~1)"
assert_eq "it committed only config.json" ".endless/config.json" "$(git -C "${P}/legacy" show --name-only --format= HEAD)"
assert_contains "the row carries the identity" "${LEGACY_SHA}" "$(sql "SELECT identity FROM projects WHERE name='legacy'")"
head="$(git -C "${P}/legacy" rev-parse HEAD)"
out="$(es project backfill-identity)"
assert_contains "a second run changes nothing" "legacy: already recorded" "${out}"
assert_eq "and commits nothing" "${head}" "$(git -C "${P}/legacy" rev-parse HEAD)"

summary
