#!/bin/sh
# smt: the QF_LIA solver of refinement types (compiler/smt.kek) through the
# hidden command `kek smt`:
#   - testdata/smt/*.smt against the goldens testdata/smt/*.out (stdout,
#     then `--- stderr` and stderr when not empty, then `--- exit <code>`)
#   - random: random formulas over a, b, c against brute force over
#     [-4, 4]^3 (seeded, so deterministic): verdicts, unsat cores, models
#   - bench: 10,000 random problems, timed (the time is printed with -v)
#
#   UPDATE=1 tests/run.sh smt    rewrite the golden files
. "$(dirname "$0")/../lib.sh"
SUITE=smt

one() {
	set +e
	name=$1
	d=$(tmpdir)
	case $name in
	random)
		n=3000
		[ "$SHORT" = 1 ] && n=1000
		out=$("$KEK" smt -random "$n" -seed 1 2>&1)
		code=$?
		out2=$("$KEK" smt -random "$n" -seed 7 2>&1)
		code2=$?
		if [ $code = 0 ] && [ $code2 = 0 ]; then
			case_ok random "$out
$out2"
		else
			case_fail random "$out
$out2"
		fi
		;;
	bench)
		start=$(date +%s)
		out=$("$KEK" smt -bench 10000 -seed 3 2>&1)
		code=$?
		secs=$(($(date +%s) - start))
		if [ $code = 0 ]; then
			case_ok bench "$out (${secs}s including startup)"
		else
			case_fail bench "$out"
		fi
		;;
	*)
		f=testdata/smt/$name.smt
		"$KEK" smt "$f" >"$d/stdout" 2>"$d/stderr"
		code=$?
		{
			cat "$d/stdout"
			if [ -s "$d/stderr" ]; then
				echo "--- stderr"
				cat "$d/stderr"
			fi
			echo "--- exit $code"
		} >"$d/got"
		golden=testdata/smt/$name.out
		if [ "${UPDATE:-}" = 1 ]; then
			cp "$d/got" "$golden"
			case_ok "$name" "updated"
		elif [ ! -f "$golden" ]; then
			case_fail "$name" "missing golden file $golden (UPDATE=1 to create)"
		elif cmp -s "$d/got" "$golden"; then
			case_ok "$name"
		else
			case_fail "$name" "output differs from $golden:
$(diff "$golden" "$d/got")"
		fi
		;;
	esac
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
set --
for f in testdata/smt/*.smt; do
	n=${f##*/}
	set -- "$@" "${n%.smt}"
done
run_parallel "$0" "$@" random bench
