#!/bin/sh
# fix: `kek fix [-w] <paths>` (adds the `mut` the mutability rules ask
# for, docs/mutability.md) on the programs in tests/fix:
#   <name>        the diff printed without -w (stdout, stderr, exit code)
#                 against tests/fix/golden/<name>.txt; with -w the file
#                 is rewritten to tests/fix/golden/<name>.kek, keeps its
#                 comments, and a second `kek fix` changes nothing
#   check         a program fixed with -w passes `kek check`
#   usage         no paths / unknown flags are errors
#
#   UPDATE=1 tests/run.sh fix    rewrite the golden files
. "$(dirname "$0")/../lib.sh"
SUITE=fix
golden=$ROOT/tests/fix/golden

# comments <file>: the comment lines of a source, sorted (`//` to the end
# of the line, `/* */` on one line).
comments() {
	grep -o '//.*$\|/\*.*\*/' "$1" | sort
}

one() {
	name=$1
	d=$(tmpdir)
	report=
	case $name in
	usage)
		set +e
		"$KEK" fix >"$d/out" 2>&1
		c1=$?
		"$KEK" fix -x tests/fix/basic.kek >"$d/out2" 2>&1
		c2=$?
		set -e
		[ "$c1" = 2 ] || report="no paths: exit $c1, want 2"
		[ "$c2" = 2 ] || report="$report
unknown flag: exit $c2, want 2"
		grep -q 'usage: kek fix' "$d/out" || report="$report
no usage message: $(cat "$d/out")"
		;;
	check)
		cp tests/fix/basic.kek "$d/basic.kek"
		set +e
		"$KEK" fix -w "$d/basic.kek" >"$d/out" 2>&1
		c=$?
		"$KEK" check "$d/basic.kek" >"$d/check" 2>&1
		cc=$?
		set -e
		[ "$c" = 0 ] || report="fix -w: exit $c
$(cat "$d/out")"
		[ "$cc" = 0 ] || report="$report
check after fix -w: exit $cc
$(cat "$d/check")"
		;;
	*)
		f=tests/fix/$name.kek
		cp "$f" "$d/$name.kek"
		set +e
		(cd "$d" && "$KEK" fix "$name.kek") >"$d/stdout" 2>"$d/stderr"
		code=$?
		set -e
		{
			cat "$d/stdout"
			if [ -s "$d/stderr" ]; then
				echo "--- stderr"
				cat "$d/stderr"
			fi
			echo "--- exit $code"
		} >"$d/got"
		cmp -s "$f" "$d/$name.kek" || report="fix without -w changed the file"
		set +e
		(cd "$d" && "$KEK" fix -w "$name.kek") >"$d/w.out" 2>&1
		(cd "$d" && "$KEK" fix "$name.kek") >"$d/again" 2>&1
		set -e
		if [ "${UPDATE:-}" = 1 ]; then
			mkdir -p "$golden"
			cp "$d/got" "$golden/$name.txt"
			cp "$d/$name.kek" "$golden/$name.kek"
		fi
		if ! cmp -s "$d/got" "$golden/$name.txt"; then
			report="$report
output differs from tests/fix/golden/$name.txt:
$(diff "$golden/$name.txt" "$d/got")"
		fi
		if ! cmp -s "$d/$name.kek" "$golden/$name.kek"; then
			report="$report
fix -w result differs from tests/fix/golden/$name.kek:
$(diff "$golden/$name.kek" "$d/$name.kek")"
		fi
		comments "$f" >"$d/c1"
		comments "$d/$name.kek" >"$d/c2"
		cmp -s "$d/c1" "$d/c2" || report="$report
comments changed:
$(diff "$d/c1" "$d/c2")"
		if grep -q '^+' "$d/again"; then
			report="$report
a second kek fix still changes the file:
$(cat "$d/again")"
		fi
		;;
	esac
	if [ -n "$report" ]; then
		case_fail "$name" "$report"
	else
		case_ok "$name"
	fi
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
run_parallel "$0" basic chain left check usage
