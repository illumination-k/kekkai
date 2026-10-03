#!/bin/sh
# bad-examples: the *.bad.kek files under examples/ must be rejected with
# the diagnostic named in their first line (`// kek check: <substring>`).
. "$(dirname "$0")/../lib.sh"
SUITE=bad-examples

one() {
	f=$1
	want=$(head -n 1 "$f" | sed -n 's|^// kek check: ||p')
	if [ -z "$want" ]; then
		case_fail "$f" "first line must be \`// kek check: <expected error>\`"
		return
	fi
	if out=$("$KEK" check "$f" 2>&1); then
		case_fail "$f" "expected a type error containing \"$want\""
	elif printf '%s' "$out" | grep -qF -- "$want"; then
		case_ok "$f"
	else
		case_fail "$f" "expected error containing \"$want\", got:
$out"
	fi
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
run_parallel "$0" examples/*/*.bad.kek
