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

# The worktree's sandbox database outlives a run, so each run's projects get
# names of their own.
run=r$(date +%s)$$

n=0
ok() { n=$((n + 1)); echo "ok $n - $1"; }
not_ok() { n=$((n + 1)); echo "not ok $n - $1"; }
check() { if eval "$2" >/dev/null 2>&1; then ok "$1"; else not_ok "$1"; fi; }

project() {
    mkdir -p "$scratch/$1"
    git -C "$scratch/$1" init -q -b main
    # A project's identity is its root commit, so each needs one of its own.
    git -C "$scratch/$1" commit -q --allow-empty -m "init $1 $run"
}
init() { uv run endless --db sandbox project init --infer --name "$1-$run" "$scratch/$1" 2>&1; }

# In a sandbox the ledger lives beside the sandbox database rather than in the
# project (E-1729), so that is where this run's records are counted.
sandbox_db=$(uv run endless db path --db sandbox 2>/dev/null | tail -1)
main_db=$(uv run endless db path --db main 2>/dev/null | tail -1)
writes() {
    cat "$(dirname "$sandbox_db")"/.endless/db-ledger/*.jsonl 2>/dev/null |
        grep '"shipped_file.written"' | grep -c "\"project\":\"$1-$run\""
}
rows() { sqlite3 "$1" "SELECT COUNT(*) FROM shipped_file_writes w JOIN projects p ON p.id = w.project_id WHERE p.name LIKE '%-$run'" 2>/dev/null || echo 0; }

project fresh
out=$(init fresh)
check "a first init writes the shipped rules" "cmp -s '$shipped' '$scratch/fresh/$rules'"
check "and says so" "echo \"\$out\" | grep -qF 'Verification-suite rules written to $rules'"
check "and records the write in the ledger" "[ \"\$(writes fresh)\" = 1 ]"

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
check "off the default branch init refuses and says to switch" "echo \"\$out\" | grep -qF 'not the default branch main'"
check "and writes nothing at all" "[ ! -e '$scratch/branched/.endless' ] && [ -z \"\$(git -C '$scratch/branched' status --porcelain)\" ]"

project worked
mkdir -p "$scratch/worked/.endless/tasks"
cp "$first" "$scratch/worked/$rules"
git -C "$scratch/worked" add "$rules"
git -C "$scratch/worked" commit -q -m "rules an older Endless shipped"
wt="$scratch/worked-task"
git -C "$scratch/worked" worktree add -q -b task/1 "$wt"
out=$(init worked)
check "a worktree's unedited copy is updated too" "cmp -s '$shipped' '$wt/$rules'"
check "and committed on its branch, leaving it clean" "[ -z \"\$(git -C '$wt' status --porcelain)\" ] && git -C '$wt' log -1 --format=%s | grep -qF 'verification-suite rules'"
check "and the land's rebase drops that commit" "git -C '$wt' rebase -q main && [ \"\$(git -C '$wt' rev-list --count main..HEAD)\" = 0 ]"

check "run from a worktree, the records are in the sandbox database" "[ \"\$(rows '$sandbox_db')\" = 3 ]"
check "and not in the main one" "[ \"\$(rows '$main_db')\" = 0 ]"

rm -rf "$scratch"
echo "1..$n"
