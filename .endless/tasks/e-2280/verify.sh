#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2280 and records what was true when E-2280
# landed. Edit it only if you ARE E-2280. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2280: itemized plans and the reconciliation that gates a hand-over.
#
# The rules live in Go (internal/events/plan_items.go, internal/reconcilegate)
# and are pinned by their unit tests, folded in first. Then the real CLI —
# this branch's Python entry point and this branch's endless-go — drives a
# throwaway project on its own database through the whole loop: items from a
# file, approval as a baseline, a material item edit, a reconciliation filing
# a kind-tagged question, the gate holding until it is answered, an earlier
# answer that must be kept or re-asked, a code change making it stale, and the
# items.toml mirror.
#
# ISOLATION: a temp HOME/XDG_CONFIG_HOME, its own git repo, and a COPY of this
# branch's endless-go outside the worktree (a binary run from inside a task
# worktree refuses any database it takes for the main one, ED-1601). Nothing
# here reads or writes this worktree's state or a real endless database.
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
        ./internal/docmirror/ ./internal/docsweep/ ./internal/templatecmd/ >"$TMP/go.log" 2>&1 \
   && go test -count=1 ./internal/events/ -run 'PlanItems|PlanBaselined|Reconcile|ValidKinds' >>"$TMP/go.log" 2>&1 \
   && go test -count=1 ./internal/eventcmd/ -run 'Rebuild' >>"$TMP/go.log" 2>&1; then
    report_pass "go: plan items, the gate, the migration, the mirror and the handoff wording"
else
    report_fail "go tests pass" "ok" "$(tail -40 "$TMP/go.log")"
    summary
fi
if uv run pytest -q tests/test_plan_items.py >"$TMP/py.log" 2>&1; then
    report_pass "pytest: the file formats, the CLI surfaces and the sandbox check"
else
    report_fail "pytest tests/test_plan_items.py" "exit 0" "$(tail -40 "$TMP/py.log")"
    summary
fi

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
gate() {  # gate <task-id> — the gate's verdict as JSON
    ego session-query reconcile-gate --id "$1" --repo "$PROJ"
}

out="$(es project register . --name probe --infer)" \
    || setup_error "cannot register the throwaway project: $out"
report_pass "registered a throwaway project on its own database"

cat >"$TMP/items.toml" <<'TOML'
[[item]]
statement = "The widget renders"
verify = "go test ./widget"

[[item]]
kind = "exclusion"
statement = "Does not change the ledger format"
TOML

out="$(es task add "Add a widget" --description "Add a widget to the probe project" \
        --plan "Write the widget." --items-file "$TMP/items.toml" --complexity low --risk low)"
assert_contains "task add accepts --items-file" "added P1, P2" "$out"

section "Items, the mirror and the show"
out="$(es task plan list E-1)"
assert_contains "the deliverable is P1" "P1      The widget renders" "$out"
assert_contains "the exclusion is P2" "P2 excl Does not change the ledger format" "$out"
mirror="$PROJ/.endless/tasks/e-1/items.toml"
assert_eq "the items.toml mirror is written on the main checkout" "yes" "$([[ -f "$mirror" ]] && echo yes || echo no)"
assert_contains "the mirror is the items-file format" 'id = "P2"' "$(cat "$mirror" 2>/dev/null)"
assert_contains "the mirror is committed" "plan items for E-1" "$(git -C "$PROJ" log --format=%s -1 -- .endless/tasks/e-1/items.toml)"
out="$(es task plan list E-1 --json)"
cp "$mirror" "$TMP/reload.toml" 2>/dev/null
out2="$(es task update E-1 --items-file "$TMP/reload.toml" --keep-status)"
assert_not_contains "re-loading the mirror unchanged changes nothing" "change" "$out2"
out="$(es task show E-1 --plan)"
assert_contains "task show --plan renders the items" "— Plan items —" "$out"
plan_items_before_prose="$(printf '%s' "$out" | python3 -c 'import sys; s=sys.stdin.read(); print(s.index("— Plan items —") < s.index("— Plan —"))' 2>&1)"
assert_eq "the items come before the prose" "True" "$plan_items_before_prose"

section "Approval is of the items"
es task add "Add a gadget" --description "Add a gadget to the probe project" --plan "Prose only." \
    --complexity low --risk low >/dev/null
out="$(es task approve E-2)"
assert_contains "approve refuses a todo with no deliverable item" "no active deliverable plan item" "$out"
out="$(with_tmux es task spawn E-2)"
assert_contains "spawn refuses a todo with a prose-only plan" "so it cannot be spawned" "$out"
es task approve E-1 >/dev/null
assert_eq "approve moves E-1 to ready" "ready" "$(field E-1 'd["status"]')"
out="$(es task plan list E-1)"
assert_not_contains "approval baselines every item" "unapproved" "$out"
es task update E-1 --plan "Write the widget, carefully." >/dev/null
assert_eq "a prose edit on a ready task keeps it ready" "ready" "$(field E-1 'd["status"]')"
out="$(es task plan edit E-1 P1 --statement "The widget renders quickly")"
assert_contains "an item edit on a ready task drops its approval" "→ submitted" "$out"
assert_eq "it is submitted again" "submitted" "$(field E-1 'd["status"]')"
out="$(with_tmux es task spawn E-1)"
assert_contains "spawn refuses items not approved as they stand" "have not been approved" "$out"
es task approve E-1 >/dev/null
assert_eq "re-approved" "ready" "$(field E-1 'd["status"]')"

section "Reconciling, and the gate"
# The work lives on a task branch, as it does in a worktree; the fingerprint is
# that branch's own changes against main.
( cd "$PROJ" && git checkout -q -b task/1 && echo 'package widget' >widget.go &&
  git add widget.go && git commit -qm "widget" ) >"$TMP/git.log" 2>&1 \
    || setup_error "cannot start the task branch: $(cat "$TMP/git.log")"
assert_contains "the gate refuses an unreconciled task" "has not been reconciled" "$(gate 1)"
out="$(es task update E-1 --status unverified)"
assert_contains "from ready the lifecycle's refusal is the one shown" "not a legal status change" "$out"
es task update E-1 --status underway >/dev/null
assert_eq "the work is underway" "underway" "$(field E-1 'd["status"]')"
out="$(es task update E-1 --status unverified)"
assert_contains "setting unverified runs the gate" "cannot move to unverified yet" "$out"

out="$(es task reconcile E-1)"
assert_contains "reconcile without --file prints a file to fill in" 'item = "P1"' "$out"

cat >"$TMP/r1.toml" <<'TOML'
[[entry]]
item = "P1"
verdict = "differently"
note = "Renders through a template"

[[entry]]
item = "P2"
verdict = "kept"
TOML
out="$(es task reconcile E-1 --file "$TMP/r1.toml")"
assert_contains "a discrepancy is filed as a question" "Filed EQ-1" "$out"
assert_contains "the question carries its kind" "P1 (different)" "$out"
out="$(es question list E-1)"
assert_contains "it is an open question on the task" "EQ-1     open" "$out"
assert_contains "the gate holds for the user's answer" "waiting for the user" "$(gate 1)"

es question answer EQ-1 "Fine, keep the template" --by user >/dev/null
assert_contains "the gate passes once it is answered" '"pass":true' "$(gate 1)"

out="$(es task reconcile E-1 --file "$TMP/r1.toml")"
assert_contains "reconciling again must say whether the earlier answer holds" "answer_holds" "$out"
cat >"$TMP/r2.toml" <<'TOML'
[[entry]]
item = "P1"
verdict = "differently"
note = "Renders through a template"
answer_holds = true
reason = "Nothing the template touches changed"

[[entry]]
item = "P2"
verdict = "kept"
TOML
out="$(es task reconcile E-1 --file "$TMP/r2.toml")"
assert_contains "a kept earlier answer files no question" "No discrepancies are waiting" "$out"
assert_contains "and the gate passes" '"pass":true' "$(gate 1)"
out="$(es task show E-1 --plan)"
assert_contains "show --plan carries the verdict and the answer" "EQ-1 answered: Fine, keep the template" "$out"

( cd "$PROJ" && mkdir -p .endless/x && echo x >.endless/x/f && git add .endless/x && git commit -qm "endless only" )
assert_contains "a commit touching only .endless/ leaves it current" '"pass":true' "$(gate 1)"
( cd "$PROJ" && echo 'func Render() {}' >>widget.go && git add widget.go && git commit -qm "code" )
assert_contains "a code change makes it stale" "code changed since the last reconciliation" "$(gate 1)"

section "The old redirect"
out="$(es plan list)"
assert_not_contains "'endless plan' no longer redirects to 'task'" "renamed to 'task'" "$out"

summary
