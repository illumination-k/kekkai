#!/bin/sh
# kek-test: `kek test` end to end: test discovery (test-build -list), the
# synthesized harness, mock capabilities and their options, failure
# reports (returned false, Err, traps), -run selection, usage errors.
. "$(dirname "$0")/../lib.sh"
SUITE=kek-test

# contains <file> <substring...>: reports the missing substrings.
contains() {
	_f=$1
	shift
	for _w in "$@"; do
		grep -qF -- "$_w" "$_f" || printf 'missing %s\n' "$_w"
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

t_discover() {
	d=$(tmpdir)
	r=
	"$KEK" test-build testdata/test/counter.kek "$d" -list >"$d/log" 2>&1 || r="exit $?"
	want='[{"name":"key_format","caps":[],"result":"bool","pure":true,"params":[],"cases":null,"hash":"H"},{"name":"parse_count_defaults_to_zero","caps":[],"result":"result","pure":true,"params":[],"cases":null,"hash":"H"},{"name":"visits_are_counted","caps":["Db","Log"],"result":"result","pure":false,"params":[],"cases":null,"hash":"H"},{"name":"greeting_falls_back_offline","caps":["Net"],"result":"bool","pure":false,"params":[],"cases":null,"hash":"H"},{"name":"clock_is_fixed","caps":["Clock"],"result":"bool","pure":false,"params":[],"cases":null,"hash":"H"},{"name":"random_in_range","caps":["Random"],"result":"bool","pure":false,"params":[],"cases":null,"hash":"H"}]'
	# hashes are 32 hex digits (their values are checked by the cache tests)
	got=$(sed 's/"hash":"[0-9a-f]\{32\}"/"hash":"H"/g' "$d/tests.json" 2>/dev/null)
	[ "$got" = "$want" ] || r="$r
tests.json: $got
want:       $want"
	[ ! -f "$d/module.wasm" ] || r="$r
-list must not build"
	result discover "$r" "$d/log"
	rm -rf "$d"
}

# The synthesized harness compiles for every result kind and capability mix.
t_harness() {
	d=$(tmpdir)
	cat >"$d/h.kek" <<'EOF2'
#[test]
fn a() {}

#[test]
fn b(db: &Db, log: &Log) -> Result<(), String> {
    log.info("b");
    Ok(())
}

#[test]
fn c(net: &Net) -> Bool {
    true
}
EOF2
	mkdir "$d/out"
	r=
	"$KEK" test-build "$d/h.kek" "$d/out" >"$d/log" 2>&1 || r="exit $?"
	[ -f "$d/out/module.wasm" ] || r="$r
no module.wasm"
	result harness-builds "$r" "$d/log"
	rm -rf "$d"
}

t_passing() {
	d=$(tmpdir)
	r=
	"$KEK" test testdata/test/counter.kek >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "running 6 tests from testdata/test/counter.kek" \
		"test key_format ... ok (pure: hermetic, cacheable;" \
		"test visits_are_counted ... ok (mock Db, Log;" \
		"test result: ok. 6 passed; 0 failed")"
	result run-passing "$r" "$d/out"
	rm -rf "$d"
}

t_failing() {
	d=$(tmpdir)
	r=
	set +e
	"$KEK" test testdata/test/failing.kek >"$d/out" 2>&1
	code=$?
	set -e
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "test passes ... ok" "test returns_false ... FAILED" "    returned false" \
		"test returns_err ... FAILED (mock Log;" "    Err: expected 3, got 2" "    info: about to fail" \
		"test unit_passes ... ok" "test canned_net ... FAILED" \
		"no canned response for GET https://api.example/x" "test result: FAILED. 2 passed; 3 failed")"
	result run-failing "$r" "$d/out"
	# canned &Net responses and -run selection
	printf '{"GET https://api.example/x": "hello"}' >"$d/net.json"
	r=
	"$KEK" test -run '^canned_net$' -net "$d/net.json" testdata/test/failing.kek >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "running 1 test from testdata/test/failing.kek" "test result: ok. 1 passed; 0 failed")"
	result canned-net "$r" "$d/out"
	printf '{"GET https://api.example/x": {"error": "down"}}' >"$d/net.json"
	r=
	set +e
	"$KEK" test -run=canned -net="$d/net.json" testdata/test/failing.kek >"$d/out" 2>&1
	code=$?
	set -e
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "    Err: down" "    net: GET https://api.example/x")"
	result canned-net-error "$r" "$d/out"
	rm -rf "$d"
}

# A program's own #[handler] is demoted so the harness can take its place.
t_handler() {
	d=$(tmpdir)
	cat testdata/e2e/bank.kek - >"$d/bank.kek" <<'EOF2'

#[test]
fn amount_parsing() -> Bool {
    match parse_amount("12") {
        Some(n) => n == 12,
        None => false,
    }
}
EOF2
	r=
	"$KEK" test "$d/bank.kek" >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "test amount_parsing ... ok" "test result: ok. 1 passed; 0 failed")"
	result run-with-handler "$r" "$d/out"
	rm -rf "$d"
}

# A trap (here: call stack exhaustion) fails only its own test.
t_trap() {
	d=$(tmpdir)
	cat >"$d/trap.kek" <<'EOF2'
fn down(n: Int) -> Int {
    down(n + 1) + 1
}

#[test]
fn overflows() -> Bool {
    down(0) == 0
}

#[test]
fn fine() -> Bool {
    true
}
EOF2
	r=
	set +e
	"$KEK" test "$d/trap.kek" >"$d/out" 2>&1
	code=$?
	set -e
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "test overflows ... FAILED (trapped)" "    trap: " "test fine ... ok" \
		"test result: FAILED. 1 passed; 1 failed")"
	result trap "$r" "$d/out"
	rm -rf "$d"
}

t_no_tests() {
	d=$(tmpdir)
	r=
	"$KEK" test testdata/e2e/bank.kek >"$d/out" 2>"$d/err" || r="exit $?"
	[ "$(cat "$d/out")" = "testdata/e2e/bank.kek: no tests" ] || r="$r
stdout: $(cat "$d/out")"
	"$KEK" test -run zzz testdata/test/counter.kek >"$d/out" 2>>"$d/err" || r="$r exit $?"
	[ "$(cat "$d/out")" = "testdata/test/counter.kek: no tests" ] || r="$r
stdout: $(cat "$d/out")"
	result no-tests "$r" "$d/err"
	rm -rf "$d"
}

# expect <name> <code> <stderr prefix> <args...>
expect() {
	name=$1
	want_code=$2
	want_err=$3
	shift 3
	d=$(tmpdir)
	set +e
	"$KEK" test "$@" >"$d/out" 2>"$d/err"
	code=$?
	set -e
	r=
	[ "$code" = "$want_code" ] || r="exit $code, want $want_code"
	case $(cat "$d/err") in
	"$want_err"*) ;;
	*) r="$r
stderr does not start with: $want_err" ;;
	esac
	result "$name" "$r" "$d/err"
	rm -rf "$d"
}

t_usage() {
	expect type-errors 1 'testdata/check/err_tx.kek:4:9: transaction `tx` is never committed' testdata/check/err_tx.kek
	expect no-file 1 "kek test: expected exactly one .kek file"
	expect unknown-flag 2 "flag provided but not defined: -x
Usage of test:" -x a.kek
	expect bad-seed 2 'invalid value "abc" for flag -seed: parse error' -seed abc a.kek
	expect missing-flag-value 2 "flag needs an argument: -db" -db
	expect missing-path 1 "stat nofile.kek: no such file or directory" nofile.kek
	expect missing-net-file 1 "open nope.json: no such file or directory" -net nope.json testdata/test/counter.kek
}

t_mock_options() {
	d=$(tmpdir)
	cat >"$d/opts.kek" <<'EOF2'
#[test]
fn clock_value(clock: &Clock) -> Bool {
    clock.now_ms() == 1000
}

#[test]
fn db_seeded(db: &Db) -> Result<(), String> {
    match db.get("k") {
        Ok(Some(v)) => if v == "v" { Ok(()) } else { Err("got " + v) },
        Ok(None) => Err("missing"),
        Err(e) => Err(e.message()),
    }
}

#[test]
fn random_is_seeded(r: &Random) -> Bool {
    r.int(0, 1000000) != r.int(0, 1000000)
}
EOF2
	printf '{"k": "v"}' >"$d/db.json"
	r=
	"$KEK" test -clock 1000 -db "$d/db.json" "$d/opts.kek" >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "test result: ok. 3 passed; 0 failed")"
	result mock-options "$r" "$d/out"
	r=
	set +e
	"$KEK" test "$d/opts.kek" >"$d/out" 2>&1
	code=$?
	set -e
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "    returned false" "    Err: missing" "test result: FAILED. 1 passed; 2 failed")"
	result mock-defaults "$r" "$d/out"
	printf '{"k": 1}' >"$d/db.json"
	set +e
	"$KEK" test -db "$d/db.json" "$d/opts.kek" >"$d/out" 2>&1
	code=$?
	set -e
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "expected a JSON object of strings")"
	result bad-db-file "$r" "$d/out"
	rm -rf "$d"
}

t_help() {
	d=$(tmpdir)
	r=
	"$KEK" help >"$d/out" 2>&1 || r="exit $?"
	r="$r$(contains "$d/out" "kek test")"
	result help-lists-test "$r" "$d/out"
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	"t_$2"
	exit 0
fi
run_parallel "$0" discover harness passing failing handler trap no_tests usage mock_options help
