#!/bin/sh
# daemon: `kek daemon` (the compiler kept running, compiler/serve.kek): the
# same outputs, diagnostics and exit statuses as without it, edits are
# seen, parses are reused, a busy or stopped daemon falls back to running
# the compiler directly. The daemon runs in a cache directory of its own.
. "$(dirname "$0")/../lib.sh"
SUITE=daemon
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null) || WASMTIME=
	[ -n "$WASMTIME" ] && export WASMTIME
fi

t_daemon() {
	d=$(tmpdir)
	mkdir -p "$d/cache"
	for s in "$ROOT"/.kek-cache/stage-*; do
		[ -d "$s" ] && cp -R "$s" "$d/cache/"
	done
	export KEK_CACHE="$d/cache"
	r=
	KEK_DAEMON=0 "$KEK" build -o "$d/direct" examples/payments >"$d/direct.out" 2>&1 || r="direct build: exit $?"
	"$KEK" daemon start >"$d/out" 2>&1 || r="$r
start: exit $?"
	"$KEK" daemon status >>"$d/out" 2>&1 || r="$r
status: exit $?"
	KEK_BUILD_CACHE=0 "$KEK" build -o "$d/via" examples/payments >"$d/via.out" 2>&1 || r="$r
daemon build: exit $?"
	for f in module.wasm worker.js kekkai_meta.js wrangler.toml; do
		cmp -s "$d/direct/$f" "$d/via/$f" || r="$r
$f differs from a direct build"
	done
	# diagnostics on stderr, the exit status, a relative path
	mkdir "$d/p"
	printf 'fn f() -> Int {\n    "x"\n}\n' >"$d/p/bad.kek"
	(cd "$d/p" && "$KEK" check bad.kek) >"$d/bad.out" 2>"$d/bad.err"
	c=$?
	[ "$c" = 1 ] || r="$r
check of a bad program: exit $c, want 1"
	[ ! -s "$d/bad.out" ] || r="$r
diagnostics on stdout"
	grep -q 'bad.kek:2:5: mismatched types' "$d/bad.err" || r="$r
diagnostics: $(cat "$d/bad.err")"
	# an edit is seen
	printf 'fn f() -> Int {\n    1\n}\n' >"$d/p/bad.kek"
	(cd "$d/p" && "$KEK" check bad.kek) >"$d/ok.out" 2>&1 || r="$r
check after the fix: exit $?"
	grep -q "bad.kek: ok" "$d/ok.out" || r="$r
check after the fix: $(cat "$d/ok.out")"
	# a #[main] program runs
	"$KEK" run testdata/run/recursive_enum.kek >"$d/run.out" 2>&1 || r="$r
run: exit $?"
	cmp -s "$d/run.out" testdata/run/recursive_enum.out || r="$r
run output differs"
	# parses are reused (the prelude, the program)
	"$KEK" daemon stats >"$d/stats" 2>&1 || r="$r
stats: exit $?"
	grep -q "parses reused [1-9]" "$d/stats" || r="$r
stats: $(cat "$d/stats")"
	# busy: the launch runs the compiler itself
	dd=$(ls -d "$d"/cache/daemon/*)
	mkdir "$dd/lock"
	"$KEK" check testdata/test/counter.kek >"$d/busy.out" 2>&1 || r="$r
check while busy: exit $?"
	rmdir "$dd/lock"
	"$KEK" daemon stop >>"$d/out" 2>&1 || r="$r
stop: exit $?"
	"$KEK" daemon status >>"$d/out" 2>&1 && r="$r
status after stop: running"
	"$KEK" check testdata/test/counter.kek >>"$d/out" 2>&1 || r="$r
check after stop: exit $?"
	unset KEK_CACHE
	if [ -n "$r" ]; then
		case_fail daemon "$r
--- output
$(cat "$d/out" "$d"/*.out 2>/dev/null)"
	else
		case_ok daemon
	fi
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	"t_$2"
	exit 0
fi
run_parallel "$0" daemon
