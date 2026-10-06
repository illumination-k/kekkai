#!/bin/sh
# mutate: `kek mutate`. The mutant schema must not change behaviour while
# no mutant is active: every testdata/run program, built with all its
# mutants (mutate-build -main), prints the same output. The report of
# testdata/mutate/calc.kek (survivors, a timeout, a mutant killed by the
# Tx linearity check), testdata/mutate/refine.kek (mutants killed by the
# refinement checker), testdata/mutate/features.kek (bit operations,
# compound assignments, Float, guards, patterns, match arms, break /
# continue), testdata/mutate/modules (module paths in the report), -json, the result cache, -base, -diff (in a
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

# refinement types: mutants that break a proof are killed by types
t_refine() {
	d=$(tmpdir)
	r=
	"$KEK" mutate testdata/mutate/refine.kek >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "killed by types (the mutant does not type-check):" \
		"  refine.kek:12:13  total: \`<\` → \`<=\`" \
		"      while i < n {   →   while i <= n {" \
		"refine.kek:13:18: cannot prove the index is in bounds: \`0 <= i && i < v.len()\`" \
		"  refine.kek:14:15  total: \`+\` → \`-\`" \
		"generated 12: killed by types 3, killed 7, survived 0, timeout 2, no coverage 0")"
	result refine "$r" "$d/out"
	rm -rf "$d"
}

# the mutants of the newer language features
t_features() {
	d=$(tmpdir)
	f=testdata/mutate/features.kek
	r=
	"$KEK" mutate "$f" >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "mutation testing of $f: 143 mutants in 11 functions, 11 tests" \
		"  features.kek:45:10  scale: \`<\` → \`<=\`" \
		"  features.kek:45:12  scale: 1.5 → 2.5" \
		"  features.kek:51:9  grade: range bound 0 → -1" \
		"      0..=59 => \"F\",   →   -1..=59 => \"F\"," \
		"  features.kek:60:13  kind: delete alternative \`2\`" \
		"      1 | 2 => 10,   →   1 => 10," \
		"  features.kek:61:14  kind: remove guard" \
		"      x if x > 50 => 20,   →   x => 20," \
		"features.kek:62:9: unreachable pattern" \
		"generated 143: killed by types 6, killed 130, survived 7, timeout 0, no coverage 0")"
	"$KEK" mutate -json "$f" >"$d/json" 2>&1 || r="$r
-json: exit $?"
	tr -d ' \n' <"$d/json" >"$d/flat"
	r="$r$(contains "$d/flat" \
		'"kind":"bitwise","description":"`&`→`|`","original":"letmutv=a&b;","mutated":"letmutv=a|b;","status":"killed"' \
		'"kind":"bitwise","description":"`|=`→`&=`","original":"v|=1<<8;","mutated":"v&=1<<8;","status":"killed"' \
		'"kind":"bitwise","description":"`^=`→`|=`","original":"v^=a|b;","mutated":"v|=a|b;","status":"killed"' \
		'"kind":"bitwise","description":"`^`→`&`' '"kind":"bitwise","description":"`^`→`|`' \
		'"kind":"bitwise","description":"`>>`→`<<`' \
		'"func":"mean","kind":"arith","description":"`+=`→`-=`","original":"s+=x;","mutated":"s-=x;","status":"killed"' \
		'"func":"mean","kind":"float","description":"0.0→1.0"' \
		'"func":"mean","kind":"result","description":"result→0.0","original":"s/v.len().to_float()","mutated":"0.0","status":"killed"' \
		'"func":"scale","kind":"float","description":"2.0→3.0"' \
		'"func":"scale","kind":"unary","description":"drop`-`"' \
		'"kind":"guard","description":"negateguard","original":"Some(0)ifstrict=>-1,","mutated":"Some(0)if!(strict)=>-1,","status":"killed"' \
		'"kind":"guard","description":"removeguard","original":"Some(0)ifstrict=>-1,","mutated":"Some(0)=>-1,","status":"killed"' \
		'"kind":"pattern","description":"rangebound100→101","original":"80..100=>\"A\",","mutated":"80..101=>\"A\",","status":"killed"' \
		'"kind":"arm","description":"deletematcharm","original":"0..=59=>\"F\",","mutated":"","status":"killed"' \
		'"kind":"jump","description":"`break`→`continue`","original":"break;","mutated":"continue;","status":"killed"' \
		'"func":"prefix","kind":"int","description":"1→0","original":"v[i+1]+=v[i];","mutated":"v[i+0]+=v[i];","status":"killed"')"
	# derived implementations get no mutants
	grep -q '"func":"<Point' "$d/flat" && r="$r
a derived implementation was mutated"
	result features "$r" "$d/out"
	rm -rf "$d"
}

# module items are named by their paths; dyn dispatch is not mutated
t_modules() {
	d=$(tmpdir)
	r=
	"$KEK" mutate -json testdata/mutate/modules >"$d/out" 2>&1 || r="exit $?"
	tr -d ' \n' <"$d/out" >"$d/flat"
	r="$r$(contains "$d/flat" \
		'"functions":["total","geo::area","<geo::Rectasgeo::Shape>::area"]' \
		'"func":"geo::area","kind":"arith"' \
		'"generated":7,"killed_by_types":0,"killed":7')"
	grep -q '\$' "$d/flat" && r="$r
a global name (\$) in the report"
	result modules "$r" "$d/out"
	rm -rf "$d"
}

t_json() {
	d=$(tmpdir)
	r=
	"$KEK" mutate -json "$calc" >"$d/out" 2>&1 || r="exit $?"
	tr -d ' \n' <"$d/out" >"$d/flat"
	r="$r$(contains "$d/flat" \
		'"summary":{"generated":30,"killed_by_types":1,"killed":18,"survived":8,"timeout":3,"no_coverage":0,"skipped":0,"score":72.4,"covered_score":72.4,"min_score":null,"ok":true}' \
		'"functions":["clamp","triangle","is_adult","deposit"]' \
		'"func":"triangle","kind":"stmt","description":"deletestatement","original":"i=i+1;","mutated":"","status":"timeout","killed_by":"triangle_of_4"' \
		'"func":"deposit","kind":"stmt","description":"deletestatement","original":"tx.commit()?;","mutated":"","status":"killed_by_types","message":"testdata/mutate/calc.kek:38:9:transaction`tx`isnevercommittedorrolledback' \
		'{"name":"deposits_add_up","status":"ok","ticks":')"
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
	[ -s "$(ls "$CACHE"/mutate/pairs-*.tsv 2>/dev/null | head -n 1)" ] || r="$r
no cached results"
	result cache "$r" "$d/out2"
	rm -rf "$d"
}

# -shard i/n runs a third of the mutants each; -merge of their -results
# reports the same as one run. TEST_SHARD_INDEX / TEST_TOTAL_SHARDS (Bazel)
# select a shard too.
t_shard() {
	d=$(tmpdir)
	r=
	"$KEK" mutate -json "$calc" >"$d/all" 2>&1 || r="exit $?"
	for i in 0 1 2; do
		"$KEK" mutate -shard "$i/3" -results "$d/r$i" "$calc" >"$d/s$i" 2>&1 || r="$r
shard $i: exit $?"
	done
	r="$r$(contains "$d/s1" "in other shards)")"
	"$KEK" mutate -json -merge "$d/r0,$d/r1,$d/r2" "$calc" >"$d/merged" 2>&1 || r="$r
merge: exit $?"
	cmp -s "$d/all" "$d/merged" || r="$r
merged report differs:
$(diff "$d/all" "$d/merged")"
	TEST_SHARD_INDEX=2 TEST_TOTAL_SHARDS=3 TEST_SHARD_STATUS_FILE="$d/status" "$KEK" mutate "$calc" >"$d/env" 2>&1
	cmp -s "$d/s2" "$d/env" || r="$r
TEST_SHARD_INDEX=2 differs from -shard 2/3"
	[ -f "$d/status" ] || r="$r
TEST_SHARD_STATUS_FILE not touched"
	"$KEK" mutate -shard 3/3 "$calc" >"$d/bad" 2>&1 && r="$r
-shard 3/3 accepted"
	result shard "$r" "$d/all"
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
run_parallel "$0" report refine features modules json cache shard base diff threshold usage testdata/run/*.kek
