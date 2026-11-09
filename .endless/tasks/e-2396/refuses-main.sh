#!/usr/bin/env bash
# E-2396: run <binary> against a decoy main database one schema version behind
# the branch, and print REFUSED-UNCHANGED only when the binary refused main with
# the ED-1601 message AND the decoy home is byte-for-byte unchanged.
#
# DO NOT EDIT unless you ARE E-2396 — see .endless/tasks/CLAUDE.md.
#
# Usage: refuses-main.sh <endless-go binary>     (run from the worktree root)
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="$(bash "$here/decoy-home.sh" "$1" --db main errors list 2>&1)"
echo "$out"
if grep -q "ED-1601" <<<"$out" && ! grep -q "changed the decoy home" <<<"$out"; then
  echo "REFUSED-UNCHANGED"
fi
