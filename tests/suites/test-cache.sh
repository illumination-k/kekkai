#!/bin/sh
# test-cache: the result cache of `kek test` (keyed by definition hashes),
# -no-cache, parallel runs (-j) in declaration order, `kek test -json`, and
# the build cache of `kek build` / `kek run`.
#
# The programs carry a random type name (a nonce): the type declarations
# are part of every definition hash, so each run of the suite starts with
# cold cache entries in the shared .kek-cache.
. "$(dirname "$0")/../lib.sh"
SUITE=test-cache

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

result() {
	if [ -n "$2" ]; then
		case_fail "$1" "$2
--- output
$(cat "$3")"
	else
		case_ok "$1"
	fi
}

nonce() {
	echo "Nonce$(od -An -N4 -tu4 /dev/urandom | tr -d ' ')x$$"
}

# kt <out> <args...>: kek test; sets $code.
kt() {
	_out=$1
	shift
	set +e
	"$KEK" test "$@" >"$_out" 2>&1
	code=$?
	set -e
}

# write_prog <file> <nonce> <body of b>: two tests, each with its own
# dependency.
write_prog() {
	cat >"$1" <<EOF2
struct $2 {}

fn a() -> Int {
    1
}

fn b() -> Int {
    $3
}

#[test]
fn uses_a() -> Bool {
    a() == 1
}

#[test]
fn uses_b() -> Bool {
    b() == 2
}

#[test]
fn prop_b(n: Int) -> Bool {
    n + b() != n
}
EOF2
}

t_hits() {
	d=$(tmpdir)
	n=$(nonce)
	write_prog "$d/p.kek" "$n" 2
	kt "$d/1" "$d/p.kek"
	r=
	[ $code -eq 0 ] || r="first run: exit $code"
	r="$r$(lacks "$d/1" "(cached)")"
	kt "$d/2" "$d/p.kek"
	[ $code -eq 0 ] || r="$r
second run: exit $code"
	r="$r$(contains "$d/2" "test uses_a ... ok (pure: hermetic, cacheable; " ") (cached)" \
		"test prop_b ... ok (pure: hermetic, cacheable; 100 cases; " "test result: ok. 3 passed; 0 failed (3 cached)")"
	[ "$(grep -c '(cached)$' "$d/2")" = 3 ] || r="$r
want 3 cached tests"
	result hit "$r" "$d/2"
	# a comment and formatting change keeps every entry
	{
		echo "// a comment"
		sed 's/^    1$/        1  /' "$d/p.kek"
	} >"$d/q.kek"
	kt "$d/3" "$d/q.kek"
	r=
	[ "$(grep -c '(cached)$' "$d/3")" = 3 ] || r="want 3 cached tests after a comment-only edit"
	result comment-edit "$r" "$d/3"
	# editing b invalidates exactly the tests that reach it
	write_prog "$d/p.kek" "$n" 3
	kt "$d/4" "$d/p.kek"
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/4" "test uses_b ... FAILED" "test prop_b ... ok (pure: hermetic, cacheable; 100 cases; ")"
	grep -q '^test uses_a .*(cached)$' "$d/4" || r="$r
uses_a should be cached"
	! grep -q '^test uses_b .*(cached)$' "$d/4" || r="$r
uses_b should run"
	! grep -q '^test prop_b .*(cached)$' "$d/4" || r="$r
prop_b should run"
	result dependency-edit "$r" "$d/4"
	# a cached failure is replayed
	kt "$d/5" "$d/p.kek"
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/5" "    returned false" "test result: FAILED. 2 passed; 1 failed (3 cached)")"
	grep -q '^test uses_b ... FAILED.*(cached)$' "$d/5" || r="$r
uses_b should be a cached failure"
	result cached-failure "$r" "$d/5"
	# options are part of the key; -no-cache and KEK_TEST_CACHE=0 skip it
	kt "$d/6" -seed 3 "$d/p.kek"
	r=
	[ "$(grep -c '(cached)$' "$d/6")" = 0 ] || r="-seed 3 must not hit the -seed 0 entries"
	kt "$d/7" -cases 7 -run prop "$d/p.kek"
	[ "$(grep -c '(cached)$' "$d/7")" = 0 ] || r="$r
-cases 7 must not hit"
	kt "$d/8" -no-cache "$d/p.kek"
	[ "$(grep -c '(cached)' "$d/8")" = 0 ] || r="$r
-no-cache must not hit"
	set +e
	KEK_TEST_CACHE=0 "$KEK" test "$d/p.kek" >"$d/9" 2>&1
	set -e
	[ "$(grep -c '(cached)' "$d/9")" = 0 ] || r="$r
KEK_TEST_CACHE=0 must not hit"
	result options "$r" "$d/8"
	rm -rf "$d"
}

# -j runs tests at the same time; the report keeps declaration order.
t_parallel() {
	d=$(tmpdir)
	{
		echo "struct $(nonce) {}"
		echo
		echo "fn spin(n: Int) -> Int {"
		echo "    let mut s = 0;"
		echo "    for i in 0..n {"
		echo "        s = s + i % 7;"
		echo "    }"
		echo "    s"
		echo "}"
		i=1
		while [ $i -le 12 ]; do
			echo
			echo "#[test]"
			echo "fn t$i() -> Bool {"
			echo "    spin($(((13 - i) * 20000))) >= 0"
			echo "}"
			i=$((i + 1))
		done
	} >"$d/par.kek"
	kt "$d/out" -j 4 "$d/par.kek"
	r=
	[ $code -eq 0 ] || r="exit $code"
	grep '^test t' "$d/out" | sed 's/ \.\.\..*//' >"$d/order"
	want=$(i=1; while [ $i -le 12 ]; do echo "test t$i"; i=$((i + 1)); done)
	[ "$(cat "$d/order")" = "$want" ] || r="$r
wrong order: $(tr '\n' ' ' <"$d/order")"
	r="$r$(contains "$d/out" "running 12 tests" "test result: ok. 12 passed; 0 failed")"
	result parallel-order "$r" "$d/out"
	kt "$d/out" -j x "$d/par.kek"
	r=
	[ $code -eq 2 ] || r="exit $code, want 2"
	r="$r$(contains "$d/out" 'invalid value "x" for flag -j')"
	result bad-j "$r" "$d/out"
	rm -rf "$d"
}

t_json() {
	d=$(tmpdir)
	n=$(nonce)
	{
		echo "struct $n {}"
		cat testdata/test/failing.kek
		cat <<'EOF2'

#[test]
fn prop_small(n: Int) -> Bool {
    n < 10
}
EOF2
	} >"$d/f.kek"
	kt "$d/out" -json "$d/f.kek"
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" '{"file":"'"$d"'/f.kek","passed":2,"failed":4,"cached":0,"tests":[' \
		'{"name":"passes","status":"ok","cached":false,"pure":true,"caps":[],"hash":"' \
		'{"name":"returns_err","status":"failed","cached":false,"pure":false,"caps":["Log"],"hash":"' \
		'"output":["Err: expected 3, got 2","info: about to fail"],"counterexample":null}' \
		'"counterexample":"prop_small(n = 10)"}' \
		'"output":["returned false","counterexample: prop_small(n = 10)",')"
	[ "$(wc -l <"$d/out" | tr -d ' ')" = 1 ] || r="$r
want one line of JSON"
	if command -v python3 >/dev/null 2>&1; then
		python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$d/out" 2>"$d/py" || r="$r
invalid JSON: $(cat "$d/py")"
	fi
	result json "$r" "$d/out"
	kt "$d/out2" -json "$d/f.kek"
	r=
	r="$r$(contains "$d/out2" '"passed":2,"failed":4,"cached":6,' '{"name":"passes","status":"ok","cached":true,')"
	result json-cached "$r" "$d/out2"
	kt "$d/out3" -json -run zzz "$d/f.kek"
	r=
	[ "$(cat "$d/out3")" = '{"file":"'"$d"'/f.kek","passed":0,"failed":0,"cached":0,"tests":[]}' ] || r="no tests: $(cat "$d/out3")"
	result json-no-tests "$r" "$d/out3"
	rm -rf "$d"
}

# Trapped tests are reported and cached like failures.
t_trap() {
	d=$(tmpdir)
	cat >"$d/t.kek" <<EOF2
struct $(nonce) {}

fn down(n: Int) -> Int {
    down(n + 1) + 1
}

#[test]
fn overflows() -> Bool {
    down(0) == 0
}
EOF2
	kt "$d/1" "$d/t.kek"
	kt "$d/2" -json "$d/t.kek"
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/1" "test overflows ... FAILED (trapped)" "    trap: ")"
	r="$r$(contains "$d/2" '"status":"trapped","cached":true,' '"ms":null,"output":["trap: ')"
	result trap "$r" "$d/2"
	rm -rf "$d"
}

# find_entry <module.wasm>: the build cache entry holding this module.
find_entry() {
	for _e in "$CACHE"/build/*/*/; do
		[ -f "$_e/module.wasm" ] && cmp -s "$_e/module.wasm" "$1" && echo "${_e%/}" && return 0
	done
	return 1
}

t_build() {
	d=$(tmpdir)
	n=$(nonce)
	cat >"$d/m.kek" <<EOF2
struct $n {}

#[main]
fn main(args: Vec<String>, log: &Log) -> Int {
    log.info("hello");
    log.info("$n");
    0
}
EOF2
	r=
	"$KEK" build -o "$d/o1" "$d/m.kek" >"$d/log" 2>&1 || r="exit $?"
	e=$(find_entry "$d/o1/module.wasm") || r="$r
no cache entry for the module"
	if [ -n "$e" ]; then
		# mark the entry: a hit copies it
		cp "$e/module.wasm" "$d/orig.wasm"
		printf 'x' >>"$e/module.wasm"
		{
			echo "// comment"
			sed 's/log.info("hello");/log.info( "hello" ) ;/' "$d/m.kek"
		} >"$d/m2.kek"
		"$KEK" build -o "$d/o2" "$d/m2.kek" >>"$d/log" 2>&1 || r="$r
exit $?"
		cmp -s "$d/o2/module.wasm" "$e/module.wasm" || r="$r
a comment-only edit must reuse the cached module"
		# a real change misses
		sed 's/"hello"/"bye"/' "$d/m.kek" >"$d/m3.kek"
		"$KEK" build -o "$d/o3" "$d/m3.kek" >>"$d/log" 2>&1 || r="$r
exit $?"
		! cmp -s "$d/o3/module.wasm" "$e/module.wasm" || r="$r
a changed program must not hit"
		# KEK_BUILD_CACHE=0 compiles
		KEK_BUILD_CACHE=0 "$KEK" build -o "$d/o4" "$d/m2.kek" >>"$d/log" 2>&1 || r="$r
exit $?"
		cmp -s "$d/o4/module.wasm" "$d/orig.wasm" || r="$r
KEK_BUILD_CACHE=0 must compile"
		rm -rf "$e"
	fi
	out=$("$KEK" run "$d/m2.kek" 2>&1) || r="$r
kek run: exit $?"
	[ "$out" = "hello
$n" ] || r="$r
kek run printed: $out"
	result build "$r" "$d/log"
	rm -rf "$d"
}

# The glue of a #[handler] program is written on a hit too: a renamed
# parameter (not part of the definition hash) changes worker.js.
t_build_handler() {
	d=$(tmpdir)
	n=$(nonce)
	cat >"$d/h.kek" <<EOF2
struct $n {}

#[handler]
fn handle(req: Request, db: &Db) -> Response {
    Response::text(200, "ok")
}
EOF2
	sed 's/db: &Db/store: \&Db/' "$d/h.kek" >"$d/h2.kek"
	r=
	"$KEK" build -o "$d/o1" "$d/h.kek" >"$d/log" 2>&1 || r="exit $?"
	"$KEK" build -o "$d/o2" "$d/h2.kek" >>"$d/log" 2>&1 || r="$r
exit $?"
	r="$r$(contains "$d/o2/worker.js" '"store": dbCap(db("STORE")')"
	r="$r$(contains "$d/o2/kekkai_meta.js" '"name": "store"')"
	for f in kekkai_meta.js kekkai_runtime.js module.wasm worker.js wrangler.toml; do
		[ -f "$d/o1/$f" ] && [ -f "$d/o2/$f" ] || r="$r
missing $f"
	done
	result build-handler "$r" "$d/log"
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	"t_$2"
	exit 0
fi
run_parallel "$0" hits parallel json trap build build_handler
