#!/bin/sh
# refine: refinement types in `kek check` (compiler/refine.kek): the
# diagnostics of tests/refine/demo.kek in plain, -v (facts and proved
# conditions) and -json (counterexample, facts) form, and the [refine]
# configuration of kekkai.toml (overflow / division as lints, errors or
# off). Every case runs in a temporary directory holding a copy of the
# fixture and compares the transcript with tests/refine/golden/<case>.txt.
#
#   UPDATE=1 tests/run.sh refine    rewrite the golden files
. "$(dirname "$0")/../lib.sh"
SUITE=refine
golden=$ROOT/tests/refine/golden
# the commands run outside the repository, where a mise shim has no version
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null || echo wasmtime)
	export WASMTIME
fi

# step <args...>: run `kek <args>` in the case directory and append the
# transcript (stdout, then stderr).
step() {
	echo "\$ kek $*" >>"$d/got"
	set +e
	(cd "$d/w" && "$KEK" "$@" >"$d/out" 2>"$d/err")
	_c=$?
	set -e
	cat "$d/out" >>"$d/got"
	if [ -s "$d/err" ]; then
		echo "--- stderr" >>"$d/got"
		cat "$d/err" >>"$d/got"
	fi
	echo "--- exit $_c" >>"$d/got"
	echo >>"$d/got"
}

note() {
	echo "# $*" >>"$d/got"
}

config() {
	printf '%s\n' "$@" >"$d/w/kekkai.toml"
	note "kekkai.toml: $*"
}

case_default() {
	note "errors: index, alias; warnings: overflow in functions that use refinements"
	step check demo.kek
}

case_verbose() {
	note "-v: the facts behind each diagnostic, and the proved conditions as notes"
	step check -v demo.kek
}

case_json() {
	step check -json demo.kek
}

case_config() {
	config "[refine]" 'overflow = "error"' 'division = "lint"'
	step check demo.kek
	config "[refine]" 'overflow = "off"' 'division = "error"'
	step check demo.kek
	step check -json demo.kek
	config "[refine]" 'overflow = "lint"'
	step check demo.kek
	config "[refine]" 'overflow = "always"'
	step check demo.kek
	config "[refine]" 'division = 1'
	step check demo.kek
}

one() {
	name=$1
	d=$(tmpdir)
	mkdir "$d/w"
	cp "$ROOT/tests/refine/demo.kek" "$d/w/"
	: >"$d/got"
	"case_$name"
	if [ "${UPDATE:-}" = 1 ]; then
		mkdir -p "$golden"
		cp "$d/got" "$golden/$name.txt"
		case_ok "$name" "updated"
	elif [ ! -f "$golden/$name.txt" ]; then
		case_fail "$name" "missing golden file tests/refine/golden/$name.txt (UPDATE=1 to create)"
	elif cmp -s "$d/got" "$golden/$name.txt"; then
		case_ok "$name"
	else
		case_fail "$name" "output differs from tests/refine/golden/$name.txt:
$(diff "$golden/$name.txt" "$d/got")"
	fi
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
run_parallel "$0" default verbose json config
