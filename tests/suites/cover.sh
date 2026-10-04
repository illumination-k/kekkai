#!/bin/sh
# cover: `kek cover`. The probes must not change behaviour: every
# testdata/run program, instrumented (cover-build -main), prints the same
# output; programs with transactions and handlers instrument and compile.
# The report (human, -json, -lcov) of testdata/cover/shapes.kek, -run,
# failing tests, the [cover] min_line threshold and usage errors.
. "$(dirname "$0")/../lib.sh"
SUITE=cover

# some cases run ./kek from a temporary directory, where a mise shim does
# not know which wasmtime to run
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null || command -v wasmtime)
	export WASMTIME
fi

shapes=testdata/cover/shapes.kek

# contains <file> <substring...>: reports the missing substrings.
contains() {
	_f=$1
	shift
	for _w in "$@"; do
		grep -qF -- "$_w" "$_f" || printf 'missing %s\n' "$_w"
	done
}

lacks() {
	_f=$1
	shift
	for _w in "$@"; do
		! grep -qF -- "$_w" "$_f" || printf 'unexpected %s\n' "$_w"
	done
}

# result <name> <report> <output file>
result() {
	if [ -n "$2" ]; then
		case_fail "$1" "$2
--- output
$(cat "$3")"
	else
		case_ok "$1"
	fi
}

# An instrumented #[main] program behaves as the original.
t_semantics() {
	f=$1
	name=$(basename "$f" .kek)
	d=$(tmpdir)
	r=
	if ! "$KEK" cover-build "$f" "$d" -main >"$d/log" 2>&1; then
		result "same-output/$name" "cover-build failed" "$d/log"
		rm -rf "$d"
		return
	fi
	want_code=$(head -n 1 "$f" | sed -n 's|^// exit: \(-\{0,1\}[0-9]*\).*|\1|p')
	[ -n "$want_code" ] || want_code=0
	set +e
	${WASMTIME:-wasmtime} run -W gc=y,function-references=y --dir / --env PWD="$PWD" \
		--env KEK_COVER_OUT="$d/hits" "$d/module.wasm" "$d/scratch.txt" >"$d/out" 2>"$d/log"
	code=$?
	set -e
	[ "$code" = "$want_code" ] || r="exit code $code, want $want_code"
	cmp -s "$d/out" "${f%.kek}.out" || r="$r
stdout differs:
$(diff "${f%.kek}.out" "$d/out")"
	[ -s "$d/hits" ] || r="$r
no probes recorded"
	result "same-output/$name" "$r" "$d/log"
	rm -rf "$d"
}

# Handler programs with transactions: the instrumented program checks and
# compiles (with the test harness).
t_builds() {
	d=$(tmpdir)
	r=
	for f in testdata/e2e/bank.kek examples/payments/payments.kek examples/todo/todo.kek \
		examples/webhooks/webhooks.kek testdata/test/counter.kek; do
		mkdir "$d/o"
		"$KEK" cover-build "$f" "$d/o" >>"$d/log" 2>&1 || r="$r
cover-build $f failed"
		[ -f "$d/o/module.wasm" ] || r="$r
$f: no module.wasm"
		rm -rf "$d/o"
	done
	result builds "$r" "$d/log"
	rm -rf "$d"
}

t_report() {
	d=$(tmpdir)
	r=
	"$KEK" cover "$shapes" >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "coverage of $shapes (3 tests, 3 passed)" \
		"   80.0% 4/5       66.7% 2/3      area (shapes.kek:11)" \
		"   88.9% 8/9       66.7% 4/6      describe (shapes.kek:19)" \
		"   80.0% 4/5                      total (shapes.kek:32)" \
		"    0.0% 0/2                      unused (shapes.kek:40)  never called" \
		"#[rare] (not counted):" \
		"recover_from_disaster (shapes.kek:46)  never called" \
		"  uncovered lines: 13, 21, 35, 40-41" \
		"total: lines 76.2% (16/21), branches 66.7% (6/9), functions 75.0% (3/4)")"
	result report "$r" "$d/out"
	rm -rf "$d"
}

t_json() {
	d=$(tmpdir)
	r=
	"$KEK" cover -json "$shapes" >"$d/out" 2>&1 || r="exit $?"
	tr -d ' \n' <"$d/out" >"$d/flat"
	r="$r$(contains "$d/flat" '"summary":{"lines":{"hit":16,"total":21,"percent":76.2},"branches":{"hit":6,"total":9,"percent":66.7},"functions":{"hit":3,"total":4,"percent":75.0}}' \
		'"min_line":null,"ok":true' \
		'"uncovered":[13,21,35,40,41]' \
		'"name":"recover_from_disaster","file":"testdata/cover/shapes.kek","line":46,"rare":true,"hits":0' \
		'"kind":"arm","branch":0,"decision_line":12,"lines":[13],"tests":[]' \
		'"kind":"arm","branch":1,"decision_line":12,"lines":[14],"tests":["areas"]' \
		'"tests":[{"name":"areas","status":"ok"},{"name":"descriptions","status":"ok"},{"name":"empty_total","status":"ok"}]')"
	result json "$r" "$d/out"
	rm -rf "$d"
}

t_lcov() {
	d=$(tmpdir)
	r=
	"$KEK" cover -lcov "$d/cov.lcov" "$shapes" >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/cov.lcov" "SF:$shapes" "FN:11,area" "FNDA:0,unused" "FNF:4" "FNH:3" \
		"BRDA:12,1,0,0" "BRDA:12,1,1,1" "BRDA:20,2,0,0" "BRF:9" "BRH:6" \
		"DA:13,0" "DA:14,1" "LF:21" "LH:16" "end_of_record")"
	r="$r$(lacks "$d/cov.lcov" "recover_from_disaster")"
	r="$r$(contains "$d/out" "lcov: $d/cov.lcov")"
	result lcov "$r" "$d/cov.lcov"
	rm -rf "$d"
}

# Transactions, mocks and -run.
t_counter() {
	d=$(tmpdir)
	r=
	"$KEK" cover testdata/test/counter.kek >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "(6 tests, 6 passed)" \
		"  100.0% 7/7                      visit (counter.kek:18)" \
		"handle (counter.kek:40)  never called")"
	"$KEK" cover -run '^key_' testdata/test/counter.kek >"$d/out2" 2>&1 || r="$r
-run: exit $?"
	r="$r$(contains "$d/out2" "(1 test, 1 passed)" \
		"  100.0% 2/2                      counter_key (counter.kek:7)" \
		"visit (counter.kek:18)  never called")"
	cat "$d/out2" >>"$d/out"
	result counter-run "$r" "$d/out"
	rm -rf "$d"
}

t_failing() {
	d=$(tmpdir)
	r=
	set +e
	"$KEK" cover testdata/test/failing.kek >"$d/out" 2>&1
	code=$?
	set -e
	[ "$code" = 1 ] || r="exit code $code, want 1"
	r="$r$(contains "$d/out" "failing tests (their coverage still counts):")"
	result failing "$r" "$d/out"
	rm -rf "$d"
}

# [cover] min_line in ./kekkai.toml
t_threshold() {
	d=$(tmpdir)
	cp "$shapes" "$d/shapes.kek"
	printf '[cover]\nmin_line = 80\n' >"$d/kekkai.toml"
	r=
	set +e
	(cd "$d" && "$KEK" cover shapes.kek) >"$d/out" 2>&1
	code=$?
	set -e
	[ "$code" = 1 ] || r="exit code $code, want 1"
	r="$r$(contains "$d/out" "kek cover: line coverage 76.2% is below [cover] min_line = 80%")"
	printf '[cover]\nmin_line = 70\n' >"$d/kekkai.toml"
	(cd "$d" && "$KEK" cover shapes.kek) >>"$d/out" 2>&1 || r="$r
min_line = 70: exit $?"
	result min-line "$r" "$d/out"
	rm -rf "$d"
}

t_usage() {
	d=$(tmpdir)
	r=
	set +e
	"$KEK" cover -nope "$shapes" >"$d/out" 2>&1
	code=$?
	set -e
	[ "$code" = 2 ] || r="exit code $code, want 2"
	r="$r$(contains "$d/out" "flag provided but not defined: -nope" "Usage of cover:")"
	result usage "$r" "$d/out"
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	case $2 in
	*.kek) t_semantics "$2" ;;
	*) "t_$2" ;;
	esac
	exit 0
fi
run_parallel "$0" builds report json lcov counter failing threshold usage testdata/run/*.kek
