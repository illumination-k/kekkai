#!/bin/sh
# difftest: random programs (tests/difftest/gen.kek, seeds SEED ..
# SEED+N-1) are run as WasmGC and compared with the Lean reference
# interpreter of the IR.
#
# For every seed the generator writes prog.kek (pure functions f0..fn),
# main.kek (a #[main] driver calling every function on fixed arguments and
# printing `f1(7, 3, true) = 42` per call) and calls.txt (`f1 7 3 true` per
# call). The WasmGC side is one `./kek run` of the directory (prog.kek and
# main.kek together); the reference side is `./kek ir -json prog.kek` and
# one kekkai-ref call per line of calls.txt, formatted like the driver's
# output. The two outputs must be identical.
#
# The reference interpreter is lean/.lake/build/bin/kekkai-ref (built with
# `mise run lean`) or $KEKKAI_REF. Without it, the suite only checks that
# the wasm runs without traps and prints every result.
#
# Failing programs are kept (their directory is printed); KEEP=1 keeps all.
. "$(dirname "$0")/../lib.sh"
SUITE=difftest

# ref_line <ir.json> <fn> <a> <b> <c>: the reference result in the format of
# the driver.
ref_line() {
	_out=$("$REF" "$1" "$2" "$3" "$4" "$5" 2>&1)
	_ok=$(printf '%s\n' "$_out" | sed -n 's/^{"ok":\(-\{0,1\}[0-9][0-9]*\),.*/\1/p')
	if [ -n "$_ok" ]; then
		echo "$2($3, $4, $5) = $_ok"
	else
		echo "$2($3, $4, $5) = reference: $_out"
	fi
}

one() {
	name=$1
	d=$WORK/$name
	set +e
	"$KEK" run "$d" >"$d/wasm.out" 2>"$d/wasm.err"
	code=$?
	set -e
	report=
	if [ "$code" != 0 ]; then
		report="wasm exited $code
$(cat "$d/wasm.err")"
	fi
	want=$(wc -l <"$d/calls.txt" | tr -d ' ')
	got=$(wc -l <"$d/wasm.out" | tr -d ' ')
	[ "$got" = "$want" ] || report="$report
wasm printed $got results, want $want"
	if [ -z "$report" ] && [ -n "$REF" ]; then
		mkdir "$d/ir"
		cp "$d/prog.kek" "$d/ir/prog.kek"
		if "$KEK" ir -json "$d/ir" >"$d/ir.json" 2>"$d/ir.err"; then
			while read -r f a b c; do
				ref_line "$d/ir.json" "$f" "$a" "$b" "$c"
			done <"$d/calls.txt" >"$d/ref.out"
			if ! cmp -s "$d/ref.out" "$d/wasm.out"; then
				report="wasm and reference differ (< reference, > wasm):
$(diff "$d/ref.out" "$d/wasm.out" | grep '^[<>]')"
			fi
		else
			report="ir -json failed:
$(cat "$d/ir.err")"
		fi
	fi
	if [ -n "$report" ]; then
		case_fail "$name" "$report
program kept at $d"
	else
		case_ok "$name" "$(cat "$d/wasm.out")"
		[ "$KEEP" = 1 ] || rm -rf "$d"
	fi
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi

N=${N:-60}
SEED=${SEED:-1}
KEEP=${KEEP:-0}
REF=${KEKKAI_REF:-$ROOT/lean/.lake/build/bin/kekkai-ref}
if [ ! -x "$REF" ]; then
	echo "note: Lean reference interpreter not built (mise run lean, or KEKKAI_REF=...): checking that the wasm runs without traps only"
	REF=
else
	echo "note: comparing with $REF"
fi
tmp=${TMPDIR:-/tmp}
WORK=$(mktemp -d "${tmp%/}/kek-difftest-XXXXXX")
export REF WORK KEEP

seeds=
s=$SEED
while [ "$s" -lt $((SEED + N)) ]; do
	mkdir "$WORK/seed$s"
	seeds="$seeds seed$s"
	s=$((s + 1))
done
if ! "$KEK" run tests/difftest/gen.kek "$WORK" "$SEED" "$N" >"$WORK/gen.out" 2>&1; then
	case_fail generate "$(cat "$WORK/gen.out")"
	rm -rf "$WORK"
	exit 0
fi
rm -f "$WORK/gen.out"
# shellcheck disable=SC2086
run_parallel "$0" $seeds
if [ "$KEEP" = 1 ]; then
	echo "note: programs kept in $WORK"
else
	rmdir "$WORK" 2>/dev/null || true
fi
