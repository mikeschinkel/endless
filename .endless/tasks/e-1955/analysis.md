# Reproduction harness (E-1943)

Self-contained. Simulates canAmend's precondition 2 faithfully and, under
LEDGER_ID_TEST=1, the proposed content-identity precondition. Proves the idea;
does NOT exercise real endless code — E-1955's verify script must do that.

Results:

    SHA guard only (today)                     + content-identity test
    S1 no pull, ledger (guard)      land=OK    land=OK
    S2 pull --rebase, ledger        land=CONFLICT   land=OK  (saw task/900 by content)
    S3 pull --rebase, no more       land=OK    land=OK
    S4 pull --rebase, forced append land=OK    land=OK
    S5 pull --merge, ledger         land=OK    land=OK
    S6 no pull, no more             land=OK    land=OK
    S7 rebase, append once, +5      land=OK    land=OK  (amend resumed)
    S8 rebase, guard only,  +5      land=CONFLICT   land=OK  (amend resumed)
    S9 rebase, append once, +20     land=OK    land=OK  (amend resumed)

S8 is the load-bearing one: after the single suppressed amend, the guard
returns to amending and the land stays clean — the fix self-releases.

Run: `LEDGER_ID_TEST=0 bash harness.sh` then `LEDGER_ID_TEST=1 bash harness.sh`

## Script

```bash
#!/usr/bin/env bash
# E-1943 harness: does `git pull --rebase` alone break a land, or only pull+amend?
#
# Re-implements canAmend precondition 1+2 (subject match; for-each-ref --contains
# HEAD skipping the current branch), reports its decision, then attempts land's
# rebase and reports OK / CONFLICT.
set -uo pipefail

ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT

LEDGER=".endless/db-ledger/db-entries-aaaa-000001.jsonl"
SUBJ="Endless: record ledger entry"
DECISION=""
SAW=""
N=0
TAIL=0

g() { git -C "$@"; }

can_amend() {
  local repo head_subj cur_ref refs r
  repo="$1"
  head_subj="$(git -C "$repo" log -1 --format=%s 2>/dev/null)" || return 1
  [ "$head_subj" = "$SUBJ" ] || return 1
  cur_ref="$(git -C "$repo" symbolic-ref --quiet HEAD 2>/dev/null)"
  refs="$(git -C "$repo" for-each-ref --contains HEAD --format='%(refname)' 2>/dev/null)"
  SAW=""
  while IFS= read -r r; do
    [ -z "$r" ] && continue
    [ "$r" = "$cur_ref" ] && continue
    SAW="$r(sha)"
    return 1
  done <<< "$refs"
  if [ "${LEDGER_ID_TEST:-0}" = "1" ]; then
    local tip_tree b b_tree
    tip_tree="$(git -C "$repo" rev-parse "HEAD:.endless/db-ledger" 2>/dev/null)"
    if [ -n "$tip_tree" ]; then
      for b in $(git -C "$repo" for-each-ref --format='%(refname:short)' refs/heads/task 2>/dev/null); do
        b_tree="$(git -C "$repo" rev-parse "$b:.endless/db-ledger" 2>/dev/null)"
        if [ "$b_tree" = "$tip_tree" ]; then
          SAW="$b(content)"
          return 1
        fi
      done
    fi
  fi
  return 0
}

record_event() {
  local repo mode payload
  repo="$1"; mode="$2"; payload="$3"
  mkdir -p "$repo/.endless/db-ledger"
  echo "{\"ev\":\"$payload\"}" >> "$repo/$LEDGER"
  git -C "$repo" add "$LEDGER"
  if [ "$mode" = "guard" ] && can_amend "$repo"; then
    git -C "$repo" commit -q --amend --no-edit
    DECISION="AMEND"
  else
    git -C "$repo" commit -q -m "$SUBJ"
    DECISION="APPEND"
  fi
}

REPO=""; WT=""; UP=""
setup() {
  N=$((N+1))
  UP="$ROOT/up$N"
  REPO="$ROOT/repo$N"
  WT="$ROOT/wt$N"
  git init -q --bare "$UP"
  git init -q -b main "$REPO"
  git -C "$REPO" config user.email t@t
  git -C "$REPO" config user.name t
  git -C "$REPO" config commit.gpgsign false
  mkdir -p "$REPO/.endless/db-ledger"
  echo base > "$REPO/src.txt"
  git -C "$REPO" add -A
  git -C "$REPO" commit -q -m "base commit"
  git -C "$REPO" remote add origin "$UP"
  git -C "$REPO" push -q -u origin main
}

upstream_advance() {
  local tmp
  tmp="$ROOT/clone$N"
  git clone -q "$UP" "$tmp"
  git -C "$tmp" config user.email o@o
  git -C "$tmp" config user.name o
  git -C "$tmp" config commit.gpgsign false
  echo upstream >> "$tmp/other.txt"
  git -C "$tmp" add -A
  git -C "$tmp" commit -q -m "upstream: someone else's work"
  git -C "$tmp" push -q origin main
  rm -rf "$tmp"
}

make_task_branch() {
  git -C "$REPO" branch task/900
  git -C "$REPO" worktree add -q "$WT" task/900
  echo "task work" >> "$WT/src.txt"
  git -C "$WT" add -A
  git -C "$WT" commit -q -m "E-900: real user work"
}

try_land() {
  local files
  if git -C "$WT" rebase main >/dev/null 2>&1; then
    echo "OK"
  else
    files="$(git -C "$WT" diff --name-only --diff-filter=U 2>/dev/null | tr '\n' ' ')"
    git -C "$WT" rebase --abort >/dev/null 2>&1
    echo "CONFLICT [${files% }]"
  fi
}

run() {
  local label pull post pre_dec land
  label="$1"; pull="$2"; post="$3"; TAIL="${4:-0}"
  setup

  # ledger events BEFORE the branch exists: no other ref contains HEAD, so the
  # guard permits amend — this is how main's tip becomes an amended commit.
  record_event "$REPO" guard e1
  record_event "$REPO" guard e2

  # Branch forks AT main's ledger tip and stays there — the real shape.
  make_task_branch

  # Probe the guard without mutating anything: expect APPEND (branch is visible).
  if can_amend "$REPO"; then pre_dec="AMEND"; else pre_dec="APPEND"; fi

  upstream_advance
  case "$pull" in
    rebase) git -C "$REPO" -c pull.rebase=true  pull -q --rebase origin main >/dev/null 2>&1 ;;
    merge)  git -C "$REPO" -c pull.rebase=false pull -q --no-rebase --no-edit origin main >/dev/null 2>&1 ;;
    none) : ;;
  esac

  DECISION="-"; SAW=""
  if [ "$post" != "none" ]; then
    record_event "$REPO" "$post" e3
  fi

  # TAIL: further ledger events, always guard-driven, AFTER the post-pull one.
  # Tests whether a single append is enough to re-anchor main permanently.
  local i
  for (( i=0; i<TAIL; i++ )); do
    record_event "$REPO" guard "tail$i"
  done

  land="$(try_land)"
  printf '%-38s | pull=%-6s | post-ledger=%-6s | pre-pull guard=%-6s | post-pull guard=%-6s%s | land=%s\n' \
    "$label" "$pull" "$post" "$pre_dec" "$DECISION" \
    "$([ -n "$SAW" ] && echo " (saw ${SAW##*/})" || echo "")" "$land"
}

run "S1 no pull, ledger (guard)"        none   guard
run "S2 pull --rebase, ledger (guard)"  rebase guard
run "S3 pull --rebase, NO more ledger"  rebase none
run "S4 pull --rebase, FORCED append"   rebase append
run "S5 pull --merge, ledger (guard)"   merge  guard
run "S6 no pull, NO more ledger"        none   none
echo
echo "--- does ONE append after the rewrite re-anchor main permanently? ---"
run "S7 rebase, append once, +5 guard"  rebase append 5
run "S8 rebase, guard only, +5 guard"   rebase guard  5
run "S9 rebase, append once, +20 guard" rebase append 20
```
