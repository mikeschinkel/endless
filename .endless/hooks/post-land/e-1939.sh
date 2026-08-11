#!/usr/bin/env bash
#
# E-1939 post-land: remove the web dashboard's untracked residue from main.
#
# Endless runs this once, right after E-1939's merge advances main, with:
#   - cwd     = the main checkout
#   - argv[1] = the main checkout's absolute path
#
# Why a script at all: the land deletes every TRACKED file under internal/web,
# but `internal/web/assets/css/templui` was a gitignored symlink into the Go
# module cache (created by the old `just _link-templui`). A merge only moves
# tracked content, so the symlink — and the now-empty directories above it —
# survive the land as untracked residue. This branch also drops the .gitignore
# line that hid it, so without this script the path would sit untracked in
# `git status` and a later land could sweep it into a commit. That is exactly
# what endless's post-land residue check reports, so removing it here is the
# sanctioned fix rather than a manual cleanup step.
#
# Idempotent: `rm -rf` on an absent path is a no-op, so re-running after a
# failed land (or on a checkout that never had the symlink) does nothing.

set -euo pipefail

main_root="${1:?usage: e-1939.sh <main-checkout-path>}"
cd "${main_root}"

web_dir="${main_root}/internal/web"

if [[ ! -e "${web_dir}" ]]; then
    echo "e-1939 post-land: ${web_dir} already gone; nothing to do"
    exit 0
fi

# Refuse to delete anything git still tracks — if the merge left tracked
# content behind, that is a land problem to surface, not residue to sweep.
if [[ -n "$(git ls-files -- internal/web)" ]]; then
    echo "e-1939 post-land: internal/web still has tracked files; refusing to remove" >&2
    git ls-files -- internal/web >&2
    exit 1
fi

echo "e-1939 post-land: removing untracked web residue at ${web_dir}"
rm -rf "${web_dir}"
