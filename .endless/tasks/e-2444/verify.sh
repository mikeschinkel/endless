#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2444 and records what was true when E-2444
# landed. Edit it only if you ARE E-2444. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2444: post-land plan items, the `deferred` verdict, the `landed` status and
# the post-land reconcile that settles it.
#
# The rules live in Go (internal/events/post_land.go, plan_items.go,
# internal/taskstatus, internal/monitor, internal/taskrow) and are pinned by
# their unit tests, folded in first. Then the real CLI — this branch's Python
# entry point and this branch's endless-go — drives a throwaway project on its
# own database through the whole loop: a post-land item reconciled `deferred`,
# a record-only land setting `landed`, the surfaces naming the open item, the
# code-on-the-branch refusal, a failed post-land attempt, and the one that
# settles it; then an unforeseen item marked by `question answer --post-land`,
# and the user's withdrawal settling a landed task.
#
# ISOLATION: a temp HOME/XDG_CONFIG_HOME, its own git repo, and a COPY of this
# branch's endless-go outside the worktree (a binary run from inside a task
# worktree refuses any database it takes for the main one, ED-1601).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "$WT" || setup_error "cannot cd to $WT"
[[ -x "$WT/bin/endless-go" ]] || setup_error "no $WT/bin/endless-go — run \`just build\`"

TMP="$(mktemp -d)" || setup_error "cannot make a temp dir"
trap 'rm -rf "$TMP"' EXIT

# ── Fail fast: this task's own tests ─────────────────────────────────────────
section "This task's own tests (fail fast)"

if go test -count=1 ./internal/planitem/ ./internal/reconcilegate/ ./internal/schema/... \
        ./internal/taskstatus/ ./internal/taskrow/ ./internal/sessionstatuscmd/ \
        ./internal/projectstatuscmd/ >"$TMP/go.log" 2>&1 \
   && go test -count=1 ./internal/events/ -run 'PostLand|PlanItems|PlanBaselined|Reconcile|ValidKinds' >>"$TMP/go.log" 2>&1 \
   && go test -count=1 ./internal/monitor/ -run 'PostLand|SessionStatus|EpicWorkProduct' >>"$TMP/go.log" 2>&1; then
    report_pass "go: post-land events and replay, the landed status and its edges, readers, actions, migration"
else
    report_fail "go tests pass" "ok" "$(tail -40 "$TMP/go.log")"
    summary
fi
if uv run pytest -q tests/test_plan_items.py tests/test_status_lifecycle_sync.py >"$TMP/py.log" 2>&1; then
    report_pass "pytest: the items file's post_land key, the lifecycle diagram's copies"
else
    report_fail "pytest" "exit 0" "$(tail -40 "$TMP/py.log")"
    summary
fi
if out="$(uv run python -m endless.lifecycle_map check 2>&1)"; then
    report_pass "the status-lifecycle diagram is generated from the transition table"
else
    report_fail "lifecycle-check" "exit 0" "$out"
fi
out="$(uv run endless guide tasks/post-land 2>&1)"
assert_contains "endless guide tasks/post-land prints the rules" "Code means a follow-up task" "$out"
assert_contains "the tasks section links to it" "endless guide tasks/post-land" "$(cat docs/guide/tasks.md)"

# ── A throwaway project, driven through the real CLI ─────────────────────────
section "Setup"

ENDLESS="$(uv run python -c 'import sys, pathlib; print(pathlib.Path(sys.executable).parent / "endless")' 2>/dev/null)"
[[ -x "$ENDLESS" ]] || setup_error "no endless entry point at ${ENDLESS:-<unresolved>}"
mkdir -p "$TMP/bin" "$TMP/home/.config" "$TMP/proj"
cp "$WT/bin/endless-go" "$TMP/bin/endless-go" || setup_error "cannot copy endless-go"
PROJ="$TMP/proj"
(
    cd "$PROJ" && git init -q -b main . && git config user.email v@example.com &&
    git config user.name verify && git commit -q --allow-empty -m init
) >"$TMP/git.log" 2>&1 || setup_error "cannot init the throwaway repo: $(cat "$TMP/git.log")"

es() {
    ( cd "$PROJ" &&
      env -u ENDLESS_SESSION_ID -u ENDLESS_DB_CHOICE \
          HOME="$TMP/home" XDG_CONFIG_HOME="$TMP/home/.config" \
          PATH="$TMP/bin:$PATH" ENDLESS_AUTO_MIGRATE=1 ENDLESS_NO_TRIAGE=1 \
          "$ENDLESS" --no-session "$@" 2>&1 )
}
ego() {
    ( cd "$PROJ" &&
      env HOME="$TMP/home" XDG_CONFIG_HOME="$TMP/home/.config" \
          "$TMP/bin/endless-go" "$@" 2>&1 )
}
field() {  # field <task-id> <python expr over d>
    es task show "$1" --json | python3 -c "import json,sys; d=json.load(sys.stdin); d=d[0] if isinstance(d,list) else d; print($2)" 2>&1
}
gate() { ego session-query reconcile-gate --id "$1" --repo "$PROJ"; }
board() { es project status --all --sort id | grep -E " E-$1 "; }
head_sha() { git -C "$PROJ" rev-parse HEAD; }

out="$(es project register . --name probe --infer)" \
    || setup_error "cannot register the throwaway project: $out"
report_pass "registered a throwaway project on its own database"

# ── A planned post-land item ─────────────────────────────────────────────────
section "Marking a post-land item, and deferring it"

cat >"$TMP/items.toml" <<'TOML'
[[item]]
statement = "The widget renders"

[[item]]
statement = "A widget row is written after the install"
post_land = true

[[item]]
kind = "exclusion"
statement = "Does not change the ledger format"
TOML
out="$(es task add "Add a widget" --description "Add a widget to the probe project" \
        --plan "Write the widget." --items-file "$TMP/items.toml" --complexity low --risk low)"
assert_contains "an items file may mark an item post_land" "added P1, P2, P3" "$out"
assert_contains "task plan list flags it" "[post-land" "$(es task plan list E-1)"
es task approve E-1 --include-plan-items >/dev/null
( cd "$PROJ" && git checkout -q -b task/1 && echo 'package widget' >widget.go &&
  git add widget.go && git commit -qm "widget" ) >"$TMP/git.log" 2>&1 \
    || setup_error "cannot start the task branch: $(cat "$TMP/git.log")"
es task update E-1 --status underway >/dev/null

out="$(es task reconcile E-1)"
assert_contains "the reconcile file hints deferred on a post-land item" "post-land: \`deferred\`" "$out"
cat >"$TMP/bad.toml" <<'TOML'
[[entry]]
item = "P1"
verdict = "deferred"
[[entry]]
item = "P2"
verdict = "deferred"
[[entry]]
item = "P3"
verdict = "kept"
TOML
out="$(es task reconcile E-1 --file "$TMP/bad.toml")"
assert_contains "deferred is refused on an unmarked item" "fits only a post-land item" "$out"
cat >"$TMP/r1.toml" <<'TOML'
[[entry]]
item = "P1"
verdict = "as_planned"
[[entry]]
item = "P2"
verdict = "deferred"
[[entry]]
item = "P3"
verdict = "kept"
TOML
out="$(es task reconcile E-1 --file "$TMP/r1.toml")"
assert_contains "deferring says the land will set landed" "will set it \`landed\`, not \`assumed\`" "$out"
assert_contains "deferred files no question" "No discrepancies are waiting" "$out"
assert_contains "the gate passes a deferred item" '"pass":true' "$(gate 1)"
es task update E-1 --status unverified >/dev/null
es task update E-1 --status unlanded >/dev/null
assert_eq "E-1 is unlanded" "unlanded" "$(field E-1 'd["status"]')"

# ── The land ─────────────────────────────────────────────────────────────────
section "Landing with a post-land item open"
out="$(es worktree land E-1 --record-only --sha "$(head_sha)")"
assert_contains "the land sets landed" "E-1 is now landed" "$out"
assert_contains "and lists the open item" "P2 A widget row is written after the install" "$out"
assert_eq "E-1 is landed" "landed" "$(field E-1 'd["status"]')"
assert_contains "task show names the open item" "P2" "$(field E-1 'd["open_post_land"]')"
assert_contains "task list names it" "post_land_open=P2" "$(es task list --agent)"
assert_contains "task active lists landed tasks" "E-1" "$(es task active --agent)"
assert_contains "the board shows ⊡ post-land" "⊡ T E-1" "$(board 1)"
assert_contains "task assume is refused" "not set to assumed by hand" "$(es task assume E-1)"
assert_contains "task update --status assumed is refused" "not set to assumed by hand" \
    "$(es task update E-1 --status assumed)"
assert_contains "task update --status confirmed is refused" "not set to confirmed by hand" \
    "$(es task update E-1 --status confirmed)"
assert_contains "the items are frozen" "frozen" "$(es task plan add E-1 'Another thing')"

# ── The post-land reconcile ──────────────────────────────────────────────────
section "The post-land reconcile"
out="$(es task reconcile E-1)"
assert_contains "code on the branch since the land is refused" "--cleans-up E-1" "$out"
( cd "$PROJ" && git checkout -q main && git merge -q --ff-only task/1 ) >"$TMP/git.log" 2>&1 \
    || setup_error "cannot fast-forward main: $(cat "$TMP/git.log")"
out="$(es task reconcile E-1)"
assert_contains "the post-land context shows the post-land items" "Post-land reconcile of E-1" "$out"
assert_not_contains "and only them" 'item = "P1"' "$out"
cat >"$TMP/p0.toml" <<'TOML'
[[entry]]
item = "P1"
verdict = "as_planned"
TOML
assert_contains "an entry for an ordinary item is refused" "not a post-land item" \
    "$(es task reconcile E-1 --file "$TMP/p0.toml")"
cat >"$TMP/p1.toml" <<'TOML'
[[entry]]
item = "P2"
verdict = "not_done"
note = "The install had not run"
TOML
out="$(es task reconcile E-1 --file "$TMP/p1.toml")"
assert_contains "a failed attempt files a question" "Filed EQ-1" "$out"
assert_contains "and keeps the task landed" "stays \`landed\`" "$out"
assert_eq "E-1 is still landed" "landed" "$(field E-1 'd["status"]')"
es question answer EQ-1 "Run it after the install" --by user >/dev/null
cat >"$TMP/p2.toml" <<'TOML'
[[entry]]
item = "P2"
verdict = "as_planned"
TOML
out="$(es task reconcile E-1 --file "$TMP/p2.toml")"
assert_contains "the reconcile that settles the last item moves it on" "E-1 is now assumed" "$out"
assert_eq "E-1 is assumed" "assumed" "$(field E-1 'd["status"]')"
out="$(es task plan list E-1)"
assert_contains "the current verdict is the newest post-land row" "post-land #2: as planned" "$out"
rows="$(es sql "SELECT count(*) AS n FROM task_post_land_entries")"
assert_contains "two post-land rows, nothing copied" "2" "$rows"
assert_contains "the board shows ⏚ shipped" "⏚ T E-1" "$(board 1)"

# ── An unforeseen item, and the user's withdrawal ────────────────────────────
section "Unforeseen: question answer --post-land, then the user withdraws it"
cat >"$TMP/items2.toml" <<'TOML'
[[item]]
statement = "The gadget renders"

[[item]]
statement = "The gadget is smoke-tested on the installed build"
TOML
es task add "Add a gadget" --description "Add a gadget to the probe project" \
    --plan "Write the gadget." --items-file "$TMP/items2.toml" --complexity low --risk low >/dev/null
es task approve E-2 --include-plan-items >/dev/null
es task update E-2 --status underway >/dev/null
cat >"$TMP/g1.toml" <<'TOML'
[[entry]]
item = "P1"
verdict = "as_planned"
[[entry]]
item = "P2"
verdict = "blocked"
note = "Needs the installed build"
TOML
out="$(es task reconcile E-2 --file "$TMP/g1.toml")"
q="$(printf '%s' "$out" | grep -oE 'EQ-[0-9]+' | head -1)"
assert_contains "blocked files a question" "(blocked)" "$out"
out="$(es question answer "$q" "Do it after the land" --by user --post-land)"
assert_contains "--post-land marks the item, approved by the user" "P2 of E-2 is post-land, approved by the user" "$out"
assert_not_contains "the mark needs no separate approval" "unapproved" "$(es task plan list E-2)"
cat >"$TMP/g2.toml" <<'TOML'
[[entry]]
item = "P1"
verdict = "as_planned"
[[entry]]
item = "P2"
verdict = "deferred"
TOML
es task reconcile E-2 --file "$TMP/g2.toml" >/dev/null
assert_contains "the gate passes" '"pass":true' "$(gate 2)"
es task update E-2 --status unverified >/dev/null
es task update E-2 --status unlanded >/dev/null
out="$(es worktree land E-2 --record-only --sha "$(head_sha)")"
assert_contains "E-2 lands to landed" "E-2 is now landed" "$out"
assert_contains "an agent may not withdraw it" "the user's to do" "$(as_agent es task plan withdraw E-2 P2)"
assert_eq "E-2 is still landed" "landed" "$(field E-2 'd["status"]')"
out="$(es task plan withdraw E-2 P2)"
assert_contains "the user's withdrawal settles it" "E-2 is now assumed" "$out"

summary
