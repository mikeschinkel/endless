#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This probe belongs to E-2357 and records what was true when E-2357
# landed. Edit it only if you ARE E-2357. If your change breaks it,
# leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2357 end to end, through a binary built from this tree against a
# throwaway database: the session's own task has landed (status assumed,
# one task_landings row) and blocks a waiting task. The graph still draws
# the own task, at the head of `=>` to what it unblocked, and --json
# carries landings = 1 on it and 0 on the waiting task.
set -eu

WT="$(git rev-parse --show-toplevel)"
cd "${WT}"
TMP="$(mktemp -d)"
TMP="$(cd "${TMP}" && pwd -P)"
trap 'rm -rf "${TMP}"' EXIT
command -v sqlite3 >/dev/null 2>&1 || { echo "sqlite3 is required"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq is required"; exit 2; }

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
DB="${PROBE}/endless.db"
"${BIN}" --db-dir "${PROBE}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${DB}" ]] || { echo "the binary did not create ${DB}"; exit 2; }

sqlite3 "${DB}" <<'SQL'
INSERT INTO projects (id, name, path) VALUES (1, 'e-2357-probe', '/tmp/e-2357-probe-none');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (2357,1,'own task, landed','assumed','now'),
  (2400,1,'the task it unblocked','ready','now');
INSERT INTO sessions (id, project_id, state, task_id) VALUES (1,1,'working',2357);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (1,2357,1,'t','t'), (1,2400,1,'t','t');
INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES
  ('task',2357,'task',2400,'blocks');
INSERT INTO task_landings (task_id, merge_commit_sha, landed_at) VALUES (2357,'sha-1','2026-11-06T00:00:00');
SQL

JSON="$("${BIN}" --db-dir "${PROBE}" session-status --task 2357 --session 1 --cols 200 --json)"
printf '%s\n' "${JSON}" | jq -e '.graph.lines == ["E-2357 => E-2400"]' >/dev/null \
  || { echo "graph lines: $(printf '%s' "${JSON}" | jq -c '.graph.lines')"; exit 1; }
printf '%s\n' "${JSON}" | jq -e '(.graph.nodes[] | select(.id == "E-2357") | .landings) == 1' >/dev/null \
  || { echo "own task landings: $(printf '%s' "${JSON}" | jq -c '.graph.nodes')"; exit 1; }
printf '%s\n' "${JSON}" | jq -e '(.graph.nodes[] | select(.id == "E-2400") | .landings) == 0' >/dev/null \
  || { echo "waiting task landings: $(printf '%s' "${JSON}" | jq -c '.graph.nodes')"; exit 1; }
printf '%s\n' "${JSON}" | jq -e '[.graph.nodes[].landings | type == "number"] | all' >/dev/null \
  || { echo "landings is not a number on every node"; exit 1; }
echo "landed own task drawn: E-2357 => E-2400, landings 1 and 0"
