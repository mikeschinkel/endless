# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This file belongs to E-2419's suite and records what was true when E-2419
# landed. Edit it only if you ARE E-2419 — see .endless/tasks/CLAUDE.md.
#
# The text checks E-2419's verify.toml would have written as `sh` checks. They
# are TAP here because E-2419 is the task that adds `sh` checks: the runner that
# verifies it is main's, which does not know those keys yet. Run by the
# manifest with cwd at the worktree root; prints TAP on stdout.

n=0
fails=0

# check NAME COMMAND — one TAP line: ok when COMMAND exits 0. Its output goes
# into the TAP diagnostic on failure only.
check() {
    name=$1
    n=$((n + 1))
    if out=$(sh -c "$2" 2>&1); then
        echo "ok $n - $name"
    else
        fails=$((fails + 1))
        echo "not ok $n - $name"
        printf '%s\n' "$out" | sed 's/^/# /'
    fi
}

guide=docs/guide/orchestration.md
header="sed -n '1,/^# See .endless\/tasks\/CLAUDE.md/p' .endless/tasks/_harness.sh"

check "guide: a suite is a verify.toml" \
    "grep -qF 'It is a manifest, \`.endless/tasks/e-<id>/verify.toml\`' $guide"

check "guide: documents sh, name and every matcher" '
for key in "\`name\`" "\`sh\`" "\`contains\` / \`not_contains\`" "\`equals\`"; do
  grep -qF "| $key |" '"$guide"' || { echo "missing row: $key"; exit 1; }
done'

check "guide: the script form is the endless repo's legacy form" \
    "grep -F '| Script | \`verify.sh\` |' $guide | grep -qF \"The endless repo's **legacy** form\""

check "_harness.sh header: new suites are manifests" \
    "$header | grep -qF 'New suites are manifests (verify.toml)'"

check "_harness.sh header: the script template and every helper" '
h=$('"$header"')
for want in "source \"\$(dirname \"\${BASH_SOURCE[0]}\")/../_harness.sh\"" summary \
            section assert_eq assert_contains assert_not_contains report_pass \
            report_fail report_skip setup_error as_agent with_tmux; do
  printf "%s" "$h" | grep -qF -- "$want" || { echo "header lacks: $want"; exit 1; }
done'

check "no mirror text says 'beside that task's own verify.sh'" \
    "! grep -rn 'own verify.sh\|own \`verify.sh\`' src/endless/task_cmd.py $guide docs/guide/reference.md internal/hookcmd/claude.go"

check "this suite is a manifest, with no verify.sh beside it" \
    'test -f "$ENDLESS_VERIFY_DIR/verify.toml" && test ! -e "$ENDLESS_VERIFY_DIR/verify.sh"'

echo "1..$n"
[ "$fails" -eq 0 ]
