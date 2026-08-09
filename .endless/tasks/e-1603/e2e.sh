#!/usr/bin/env sh
# End-to-end assertions for the `endless verify` Tier-0 runner (E-1603), emitted
# as TAP so Endless captures this as a raw `[[check]]` (format = "tap") and merges
# it into the same CTRF report as this suite's gotest checks. This is the
# real proof that the runner drives a suite discover -> isolate -> capture ->
# normalize -> merge end to end, exercised against throwaway fixture suites (not
# E-1603 itself, so there is no recursion).
#
# The system under test is the endless-go binary. Endless runs each check with cwd
# at the project root, where the freshly built ./bin/endless-go lives; a
# bare-clone/CI invocation can point $ENDLESS_GO_BIN at any endless-go instead.
#
# TAP contract: this script prints a plan line and one `ok`/`not ok` per assertion
# to stdout, and exits 0 only when every assertion passed (so a non-zero exit with
# zero reported failures — a runner error — is still caught by the runner).

set -eu

BIN="${ENDLESS_GO_BIN:-$PWD/bin/endless-go}"
if [ ! -x "$BIN" ]; then
	# Bare clone with no prebuilt binary: build one into a scratch dir.
	scratch_bin="$(mktemp -d)/endless-go"
	go build -o "$scratch_bin" ./cmd/endless-go
	BIN="$scratch_bin"
fi

n=0
fails=0

# ok/not_ok emit one TAP result line; not_ok also bumps the failure counter so the
# script's exit code agrees with the TAP body.
ok() {
	n=$((n + 1))
	printf 'ok %d %s\n' "$n" "$1"
}
not_ok() {
	n=$((n + 1))
	fails=$((fails + 1))
	printf 'not ok %d %s\n' "$n" "$1"
	if [ -n "${2:-}" ]; then
		# Keep the diagnostic to one line so it stays a valid TAP comment.
		printf '# %s\n' "$(printf '%s' "$2" | tr '\n' ' ' | cut -c1-200)"
	fi
}

# write_fixture DIR ID <<TOML — materialize a throwaway suite at DIR/.endless/tasks/ID.
write_fixture() {
	dir=$1
	id=$2
	mkdir -p "$dir/.endless/tasks/$id"
	cat >"$dir/.endless/tasks/$id/verify.toml"
}

# A passing fixture suite -> the runner exits 0 and reports PASSED.
pass_dir=$(mktemp -d)
write_fixture "$pass_dir" "E-FIXPASS" <<'TOML'
schema = 1
task   = "E-FIXPASS"
[[check]]
runner  = "sh"
command = "printf '1..2\nok 1 alpha\nok 2 beta\n'"
format  = "tap"
TOML
if out=$(cd "$pass_dir" && "$BIN" verify E-FIXPASS 2>&1) && printf '%s' "$out" | grep -q 'PASSED'; then
	ok "passing fixture suite: exit 0 and PASSED summary"
else
	not_ok "passing fixture suite: exit 0 and PASSED summary" "$out"
fi

# A failing fixture suite -> the runner exits non-zero and reports FAILED.
fail_dir=$(mktemp -d)
write_fixture "$fail_dir" "E-FIXFAIL" <<'TOML'
schema = 1
task   = "E-FIXFAIL"
[[check]]
runner  = "sh"
command = "printf '1..2\nok 1 alpha\nnot ok 2 beta\n'"
format  = "tap"
TOML
if out=$(cd "$fail_dir" && "$BIN" verify E-FIXFAIL 2>&1); then
	not_ok "failing fixture suite: non-zero exit" "unexpected success: $out"
elif printf '%s' "$out" | grep -q 'FAILED'; then
	ok "failing fixture suite: non-zero exit and FAILED summary"
else
	not_ok "failing fixture suite: non-zero exit and FAILED summary" "$out"
fi

# An unknown task id -> the runner refuses loudly (no silent pass).
miss_dir=$(mktemp -d)
mkdir -p "$miss_dir/.endless"
if out=$(cd "$miss_dir" && "$BIN" verify E-NOSUCH 2>&1); then
	not_ok "unknown task id refused" "unexpected success: $out"
else
	ok "unknown task id refused"
fi

printf '1..%d\n' "$n"
[ "$fails" -eq 0 ]
