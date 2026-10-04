#!/bin/sh
# mutate: `kek mutate`. The mutant schema must not change behaviour while
# no mutant is active: every testdata/run program, built with all its
# mutants (mutate-build -main), prints the same output. The report of
# testdata/mutate/calc.kek (survivors, a timeout, a mutant killed by the
# Tx linearity check), -json, the result cache, -base, -diff (in a
# temporary git repository), the [mutate] min_score threshold and usage
# errors.
. "$(dirname "$0")/../lib.sh"
SUITE=mutate

# some cases run ./kek from a temporary directory, where a mise shim does
# not know which wasmtime to run
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null || command -v wasmtime)
	export WASMTIME
fi

calc=testdata/mutate/calc.kek
summary="generated 30: killed by types 1, killed 18, survived 8, timeout 3, no coverage 0"

contains() {
	_f=$1
	shift
	for _w in "$@"; do
		grep -qF -- "$_w" "$_f" || printf 'missing %s\n' "$_w"
	done
}

result() {
	if [ -n "$2" ]; then
		case_fail "$1" "$2
--- output
$(cat "$3")"
	else
		case_ok "$1"
	fi
}

# The schema with no active mutant behaves as the original program.
t_semantics() {
	f=$1
	name=$(basename "$f" .kek)
	d=$(tmpdir)
	r=
	if ! "$KEK" mutate-build "$f" "$d" -main >"$d/log" 2>&1; then
		result "same-output/$name" "mutate-build failed" "$d/log"
		rm -rf "$d"
		return
	fi
	want_code=$(head -n 1 "$f" | sed -n 's|^// exit: \(-\{0,1\}[0-9]*\).*|\1|p')
	[ -n "$want_code" ] || want_code=0
	set +e
	${WASMTIME:-wasmtime} run -W gc=y,function-references=y --dir / --env PWD="$PWD" \
		"$d/module.wasm" "$d/scratch.txt" >"$d/out" 2>"$d/log"
	code=$?
	set -e
	[ "$code" = "$want_code" ] || r="exit code $code, want $want_code"
	cmp -s "$d/out" "${f%.kek}.out" || r="$r
stdout differs:
$(diff "${f%.kek}.out" "$d/out")"
	[ -s "$d/mutants.txt" ] || r="$r
no mutants"
	result "same-output/$name" "$r" "$d/log"
	rm -rf "$d"
}

t_report() {
	d=$(tmpdir)
	r=
	"$KEK" mutate "$calc" >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "mutation testing of $calc: 30 mutants in 4 functions, 5 tests" \
		"survived (every test passes with the mutant):" \
		"  calc.kek:8:10  clamp: \`<\` → \`<=\`" \
		"      if x < lo {   →   if x <= lo {" \
		"  calc.kek:29:9  is_adult: \`>=\` → \`>\`" \
		"  calc.kek:36:9  deposit: delete statement" \
		"      tx.put(account, new.to_string())?;   (deleted)" \
		"timeout (counted as detected):" \
		"  calc.kek:23:9  triangle: delete statement" \
		"killed by types (the mutant does not type-check):" \
		"  calc.kek:37:9  deposit: delete statement" \
		"transaction \`tx\` is never committed or rolled back" \
		"$summary" \
		"mutation score 72.4% (21/29; covered mutants 72.4%); killed by types 3.3% of the mutants")"
	result report "$r" "$d/out"
	rm -rf "$d"
}

t_json() {
	d=$(tmpdir)
	r=
	"$KEK" mutate -json "$calc" >"$d/out" 2>&1 || r="exit $?"
	tr -d ' \n' <"$d/out" >"$d/flat"
	r="$r$(contains "$d/flat" \
		'"summary":{"generated":30,"killed_by_types":1,"killed":18,"survived":8,"timeout":3,"no_coverage":0,"score":72.4,"covered_score":72.4,"min_score":null,"ok":true}' \
		'"functions":["clamp","triangle","is_adult","deposit"]' \
		'"func":"triangle","kind":"stmt","description":"deletestatement","original":"i=i+1;","mutated":"","status":"timeout","killed_by":"triangle_of_4"' \
		'"func":"deposit","kind":"stmt","description":"deletestatement","original":"tx.commit()?;","mutated":"","status":"killed_by_types","message":"testdata/mutate/calc.kek:38:9:transaction`tx`isnevercommittedorrolledback' \
		'{"name":"deposits_add_up","status":"ok","ms":')"
	result json "$r" "$d/out"
	rm -rf "$d"
}

# A second run is answered from the cache (.kek-cache/mutate): same report.
t_cache() {
	d=$(tmpdir)
	r=
	"$KEK" mutate "$calc" >"$d/out1" 2>&1 || r="exit $?"
	"$KEK" mutate "$calc" >"$d/out2" 2>&1 || r="$r
second run: exit $?"
	cmp -s "$d/out1" "$d/out2" || r="$r
the reports differ:
$(diff "$d/out1" "$d/out2")"
	ls "$ROOT"/.kek-cache/mutate/run-* >/dev/null 2>&1 || r="$r
no cached results"
	result cache "$r" "$d/out2"
	rm -rf "$d"
}

# -base: only the definitions whose hash changed are mutated.
t_base() {
	d=$(tmpdir)
	sed 's/age >= 18/age >= 21/' "$calc" >"$d/calc.kek"
	r=
	"$KEK" mutate -base "$calc" "$d/calc.kek" >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "5 mutants in 1 function, 5 tests" \
		"unchanged since the base (skipped): clamp, triangle, deposit" \
		"  calc.kek:29:12  is_adult: 21 → 22")"
	result base "$r" "$d/out"
	rm -rf "$d"
}

# -diff <rev>: the base is the program at a git revision.
t_diff() {
	d=$(tmpdir)
	r=
	cp "$calc" "$d/calc.kek"
	(
		cd "$d" &&
			git init -q . &&
			git add calc.kek &&
			git -c user.name=kek -c user.email=kek@example.com commit -q -m base
	) >"$d/out" 2>&1 || r="git: exit $?"
	sed 's/return hi;/return hi + 0;/' "$calc" >"$d/calc.kek"
	(cd "$d" && "$KEK" mutate -diff HEAD calc.kek) >>"$d/out" 2>&1 || r="$r
exit $?"
	r="$r$(contains "$d/out" "in 1 function, 5 tests" \
		"unchanged since the base (skipped): triangle, is_adult, deposit")"
	result diff "$r" "$d/out"
	rm -rf "$d"
}

# [mutate] min_score in ./kekkai.toml
t_threshold() {
	d=$(tmpdir)
	cp "$calc" "$d/calc.kek"
	printf '[mutate]\nmin_score = 80\n' >"$d/kekkai.toml"
	r=
	set +e
	(cd "$d" && "$KEK" mutate calc.kek) >"$d/out" 2>&1
	code=$?
	set -e
	[ "$code" = 1 ] || r="exit code $code, want 1"
	r="$r$(contains "$d/out" "kek mutate: mutation score 72.4% is below [mutate] min_score = 80%")"
	printf '[mutate]\nmin_score = 70\n' >"$d/kekkai.toml"
	(cd "$d" && "$KEK" mutate -json calc.kek) >"$d/out2" 2>&1 || r="$r
min_score = 70: exit $?"
	tr -d ' \n' <"$d/out2" >>"$d/out"
	r="$r$(contains "$d/out" '"min_score":70,"ok":true')"
	result min-score "$r" "$d/out"
	rm -rf "$d"
}

t_usage() {
	d=$(tmpdir)
	r=
	set +e
	"$KEK" mutate -base a.kek -diff HEAD "$calc" >"$d/out" 2>&1
	c1=$?
	"$KEK" mutate -nope "$calc" >>"$d/out" 2>&1
	c2=$?
	"$KEK" mutate -timeout 2x "$calc" >>"$d/out" 2>&1
	c3=$?
	set -e
	[ "$c1" = 1 ] && [ "$c2" = 2 ] && [ "$c3" = 1 ] || r="exit codes $c1 $c2 $c3, want 1 2 1"
	r="$r$(contains "$d/out" "kek mutate: -base and -diff are exclusive" \
		"flag provided but not defined: -nope" "Usage of mutate:" \
		"kek mutate: invalid -timeout 2x")"
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
run_parallel "$0" report json cache base diff threshold usage testdata/run/*.kek
