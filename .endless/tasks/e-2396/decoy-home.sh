#!/usr/bin/env bash
# E-2396 reproduction: run a suite with HOME and XDG_CONFIG_HOME pointed at a
# decoy home whose main database sits one schema version behind this branch,
# then report whether anything under that home changed.
#
# DO NOT EDIT unless you ARE E-2396 — see .endless/tasks/CLAUDE.md.
#
# Usage: decoy-home.sh <command...>     (run from the worktree root)
#
# Exit 0 when the command passed AND the decoy home is byte-for-byte unchanged;
# 1 when the decoy changed (the diff is printed); the command's own status when
# it failed.
set -uo pipefail

root="$(pwd -P)"
real_gocache="$(go env GOCACHE)"
real_gomodcache="$(go env GOMODCACHE)"
real_gopath="$(go env GOPATH)"
real_goenv="$(go env GOENV)"
real_uvcache="$(uv cache dir 2>/dev/null || true)"

work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/e2396-decoy.XXXXXX")" && pwd -P)"
trap 'rm -rf "$work"' EXIT
decoy="$work/home"
mkdir -p "$decoy/.config/endless"

# The decoy main database: built by this branch's endless-migrate to the newest
# version, then recorded one version back by removing goose's newest row. A
# connect that may migrate would now try to bring it forward.
go build -o "$work/endless-migrate" ./cmd/endless-migrate || exit 2
sqlite3 "$decoy/.config/endless/endless.db" "VACUUM;" || exit 2
"$work/endless-migrate" --db-dir "$decoy/.config/endless" up >/dev/null || exit 2
sqlite3 "$decoy/.config/endless/endless.db" \
  "DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version);" || exit 2
printf '{}\n' > "$decoy/.config/endless/config.json"

# Go's own telemetry counters land under HOME/Library and are not Endless's.
snapshot() {
  (cd "$decoy" && find . -path './Library' -prune -o -print0 | sort -z | xargs -0 shasum 2>/dev/null
   find . -path './Library' -prune -o -print | sort)
}
snapshot > "$work/before"

env HOME="$decoy" XDG_CONFIG_HOME="$decoy/.config" \
  GOCACHE="$real_gocache" GOMODCACHE="$real_gomodcache" GOPATH="$real_gopath" \
  GOENV="$real_goenv" ${real_uvcache:+UV_CACHE_DIR="$real_uvcache"} \
  "$@"
status=$?

snapshot > "$work/after"
if ! diff -u "$work/before" "$work/after"; then
  echo "E-2396: the run changed the decoy home" >&2
  exit 1
fi
exit "$status"
