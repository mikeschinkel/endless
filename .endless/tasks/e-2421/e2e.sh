# ── DO NOT EDIT ─────────────────────────────────────────────────────
# Belongs to E-2421's suite; see verify.toml beside it.
#
# `endless project init`, end to end, against scratch git projects: what it
# writes into .endless/tasks/CLAUDE.md on a first run and on re-runs, what it
# says, and where the record of the write goes. TAP on stdout.

repo=$(pwd)
rules=.endless/tasks/CLAUDE.md
shipped="$repo/$rules"
first="$repo/tests/fixtures/tasks-rules-E-2023.md"
scratch=$(mktemp -d)
export GIT_AUTHOR_NAME=verify GIT_AUTHOR_EMAIL=verify@example.com
export GIT_COMMITTER_NAME=verify GIT_COMMITTER_EMAIL=verify@example.com

n=0
ok() { n=$((n + 1)); echo "ok $n - $1"; }
not_ok() { n=$((n + 1)); echo "not ok $n - $1"; }
check() { if eval "$2" >/dev/null 2>&1; then ok "$1"; else not_ok "$1"; fi; }

project() {
    mkdir -p "$scratch/$1"
    git -C "$scratch/$1" init -q -b main
    git -C "$scratch/$1" commit -q --allow-empty -m init
}
init() { uv run endless --db sandbox project init --infer "$scratch/$1" 2>&1; }
writes() { cat "$scratch/$1"/.endless/db-ledger/*.jsonl 2>/dev/null | grep -c '"shipped_file.written"'; }

project fresh
out=$(init fresh)
check "a first init writes the shipped rules" "cmp -s '$shipped' '$scratch/fresh/$rules'"
check "and says so" "echo \"\$out\" | grep -qF 'Verification-suite rules written to $rules'"
check "and records the write in the project's own ledger" "[ \"\$(writes fresh)\" = 1 ]"

out=$(init fresh)
check "a re-run with nothing to do says nothing about the rules" "! echo \"\$out\" | grep -qF 'Verification-suite rules'"
check "and records nothing more" "[ \"\$(writes fresh)\" = 1 ]"

printf '# Our own rules\n' > "$scratch/fresh/$rules"
out=$(init fresh)
check "an edited copy is left alone" "[ \"\$(cat '$scratch/fresh/$rules')\" = '# Our own rules' ]"
check "and init says it was edited locally and not updated" "echo \"\$out\" | grep -qF '$rules was edited locally and not updated'"

project legacy
mkdir -p "$scratch/legacy/.endless/tasks"
cp "$first" "$scratch/legacy/$rules"
out=$(init legacy)
check "with no record, a copy that is exactly a shipped version is updated" "cmp -s '$shipped' '$scratch/legacy/$rules'"
check "and init says it updated it" "echo \"\$out\" | grep -qF 'Verification-suite rules updated in $rules'"

project edited-legacy
mkdir -p "$scratch/edited-legacy/.endless/tasks"
{ cat "$first"; echo "One line of our own."; } > "$scratch/edited-legacy/$rules"
init edited-legacy >/dev/null
check "with no record, a changed shipped version is left alone" "tail -1 '$scratch/edited-legacy/$rules' | grep -qF 'One line of our own.'"

project branched
git -C "$scratch/branched" checkout -q -b feature
out=$(init branched)
check "off the default branch the rules are written but not recorded" "echo \"\$out\" | grep -qF '(not recorded: the checkout is on feature'"
check "so nothing reaches that branch's ledger" "[ ! -e '$scratch/branched/.endless/db-ledger' ]"

sandbox_db=$(uv run endless db path --db sandbox 2>/dev/null | tail -1)
main_db=$(uv run endless db path --db main 2>/dev/null | tail -1)
check "run from a worktree, the record is in the sandbox database" "[ \"\$(sqlite3 '$sandbox_db' 'SELECT COUNT(*) FROM shipped_file_writes')\" -ge 2 ]"
check "and not in the main one" "[ \"\$(sqlite3 '$main_db' 'SELECT COUNT(*) FROM shipped_file_writes' 2>/dev/null || echo 0)\" = 0 ]"

rm -rf "$scratch"
echo "1..$n"
