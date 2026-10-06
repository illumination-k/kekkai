#!/bin/sh
# actions: the build-system side of kek: `kek affected` and
# `kek test -affected` (what a git change affects), the input-digest action
# cache of `kek build` / `kek test` (no compiler run for unchanged
# sources), and the remote cache (KEK_REMOTE_CACHE, here a file:// URL
# shared by two local caches standing in for two machines).
. "$(dirname "$0")/../lib.sh"
SUITE=actions
# the cases run ./kek outside the repository, where a mise shim would not
# find the wasmtime version of mise.toml
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null) || WASMTIME=
	[ -n "$WASMTIME" ] && export WASMTIME
fi

# contains <file> <substring...>: reports the missing substrings.
contains() {
	_f=$1
	shift
	for _w in "$@"; do
		grep -qF -- "$_w" "$_f" || printf '\nmissing %s' "$_w"
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

# repo <dir>: a git repository with testdata/test/counter.kek committed, then
# the key format of counter_key changed in the working tree.
repo() {
	cp testdata/test/counter.kek "$1/counter.kek"
	(
		cd "$1" &&
			git init -q . &&
			git add counter.kek &&
			git -c user.name=kek -c user.email=kek@example.com commit -q -m base
	)
	sed 's/"visits:" + page/"visit:" + page/' testdata/test/counter.kek >"$1/counter.kek"
}

t_affected() {
	d=$(tmpdir)
	repo "$d"
	r=
	(cd "$d" && "$KEK" affected -diff HEAD counter.kek) >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "changed (1): counter_key" \
		"affected (5): counter_key, visit, handle, key_format, visits_are_counted" \
		"tests to run (2): key_format, visits_are_counted" \
		"5 of 12 definitions affected, 2 tests to run; the build output changes")"
	(cd "$d" && "$KEK" affected -json -diff HEAD counter.kek) >"$d/json" 2>&1 || r="$r
-json: exit $?"
	tr -d ' \n' <"$d/json" >"$d/json1"
	r="$r$(contains "$d/json1" '"build":true' '"changed":["counter_key"]' '"tests":["key_format","visits_are_counted"]')"
	result affected "$r" "$d/out"
	rm -rf "$d"
}

t_unaffected() {
	d=$(tmpdir)
	repo "$d"
	# a comment-only edit affects nothing
	{
		echo "// a comment"
		cat testdata/test/counter.kek
	} >"$d/counter.kek"
	r=
	(cd "$d" && "$KEK" affected -diff HEAD counter.kek) >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "nothing affected")"
	(cd "$d" && "$KEK" test -affected HEAD counter.kek) >>"$d/out" 2>&1 || r="$r
test -affected: exit $?"
	r="$r$(contains "$d/out" "counter.kek: no tests")"
	result unaffected "$r" "$d/out"
	rm -rf "$d"
}

t_test_affected() {
	d=$(tmpdir)
	repo "$d"
	r=
	(cd "$d" && "$KEK" test -affected HEAD counter.kek) >"$d/out" 2>&1 && r="the broken test passed"
	r="$r$(contains "$d/out" "running 2 tests from counter.kek" \
		"test key_format ... FAILED" "test visits_are_counted ... ok" \
		"test result: FAILED. 1 passed; 1 failed")"
	result test_affected "$r" "$d/out"
	rm -rf "$d"
}

# a stage of the compiler for a fresh cache directory (built once by the
# suite's ./kek, copied rather than rebuilt; for the single binary, the
# precompiled compiler)
fresh_cache() {
	mkdir -p "$1"
	for _s in "$CACHE"/stage-* "$CACHE"/modules; do
		[ -d "$_s" ] && cp -R "$_s" "$1/"
	done
}

t_build_digest() {
	d=$(tmpdir)
	fresh_cache "$d/cache"
	r=
	KEK_CACHE=$d/cache "$KEK" build -o "$d/o1" examples/payments >"$d/out" 2>&1 || r="exit $?"
	ls "$d"/cache/build/src-*/ok >/dev/null 2>&1 || r="$r
no action recorded"
	# a hit writes the same files; an existing wrangler.toml is kept
	mkdir "$d/o2"
	echo "# mine" >"$d/o2/wrangler.toml"
	KEK_CACHE=$d/cache "$KEK" build -o "$d/o2" examples/payments >>"$d/out" 2>&1 || r="$r
second build: exit $?"
	for f in module.wasm worker.js kekkai_meta.js kekkai_runtime.js; do
		cmp -s "$d/o1/$f" "$d/o2/$f" || r="$r
$f differs"
	done
	[ "$(cat "$d/o2/wrangler.toml")" = "# mine" ] || r="$r
wrangler.toml overwritten"
	# a #[main] program built into the same directory records only its module
	KEK_CACHE=$d/cache "$KEK" build -o "$d/o2" testdata/run/recursive_enum.kek >>"$d/out" 2>&1 || r="$r
main build: exit $?"
	for e in "$d"/cache/build/src-*; do
		if [ -f "$e/worker.js" ] && [ ! -f "$e/kekkai_meta.js" ]; then
			r="$r
an action recorded stale glue"
		fi
	done
	n=$(ls -d "$d"/cache/build/src-* | wc -l | tr -d ' ')
	[ "$n" = 2 ] || r="$r
$n actions, want 2"
	result build_digest "$r" "$d/out"
	rm -rf "$d"
}

t_remote() {
	d=$(tmpdir)
	fresh_cache "$d/c1"
	fresh_cache "$d/c2"
	r=
	export KEK_REMOTE_CACHE="file://$d/remote"
	KEK_CACHE=$d/c1 "$KEK" test testdata/test/counter.kek >"$d/out" 2>&1 || r="exit $?"
	KEK_CACHE=$d/c1 "$KEK" build -o "$d/o1" examples/todo >>"$d/out" 2>&1 || r="$r
build: exit $?"
	ls "$d"/remote/ac/* >/dev/null 2>&1 || r="$r
nothing uploaded"
	# the second machine gets every result without running a test
	KEK_CACHE=$d/c2 "$KEK" test testdata/test/counter.kek >"$d/out2" 2>&1 || r="$r
second machine: exit $?"
	r="$r$(contains "$d/out2" "test result: ok. 6 passed; 0 failed (6 cached)")"
	[ ! -d "$d/c2/test-list" ] || ls "$d"/c2/test-list/*/ok >/dev/null 2>&1 || r="$r
test list not fetched"
	KEK_CACHE=$d/c2 "$KEK" build -o "$d/o2" examples/todo >>"$d/out2" 2>&1 || r="$r
second build: exit $?"
	cmp -s "$d/o1/module.wasm" "$d/o2/module.wasm" || r="$r
module.wasm differs"
	# an unreachable remote is a miss, not an error
	KEK_REMOTE_CACHE=http://127.0.0.1:9/ KEK_CACHE=$d/c2 "$KEK" test -no-cache testdata/test/counter.kek >>"$d/out2" 2>&1 || r="$r
unreachable remote: exit $?"
	unset KEK_REMOTE_CACHE
	cat "$d/out2" >>"$d/out"
	result remote "$r" "$d/out"
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	"t_$2"
	exit 0
fi
run_parallel "$0" affected unaffected test_affected build_digest remote
