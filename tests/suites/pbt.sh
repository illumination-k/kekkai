#!/bin/sh
# pbt: property-based tests in `kek test`: generated inputs, shrinking to
# a minimal counterexample, reproducibility from -seed, -cases and
# #[test(cases = N)], fresh mocks per case, traps, tests.json.
. "$(dirname "$0")/../lib.sh"
SUITE=pbt

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

# run_test <out> <args...>: kek test without the result cache; sets $code.
run_test() {
	_out=$1
	shift
	set +e
	KEK_TEST_CACHE=0 "$KEK" test "$@" >"$_out" 2>&1
	code=$?
	set -e
}

# untimed <file>: the output without timings.
untimed() {
	sed 's/; [0-9]*\.[0-9]*ms)/)/' "$1"
}

t_props() {
	d=$(tmpdir)
	run_test "$d/out" testdata/test/props.kek
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" \
		"test reverse_twice_is_identity ... ok (pure: hermetic, cacheable; 100 cases;" \
		"test concat_length ... ok (pure: hermetic, cacheable; 100 cases;" \
		"test option_default ... ok (pure: hermetic, cacheable; 20 cases;" \
		"test db_roundtrip ... ok (mock Db; 100 cases;" \
		"test sort_keeps_length ... FAILED" \
		"    counterexample: sort_keeps_length(xs = [1, 1])" \
		"    Err: too big: 101" \
		"    counterexample: small_numbers(n = 101)" \
		"    counterexample: far_points(p = Point { x: 10, y: 0 })" \
		"    counterexample: shapes_are_small(s = Circle(Point { x: 0, y: 0 }, 2))" \
		"    found: case " \
		"    reproduce: kek test -seed 0 -run '^small_numbers\$'" \
		"test result: FAILED. 4 passed; 4 failed")"
	result props "$r" "$d/out"
	rm -rf "$d"
}

# The same seed gives the same cases; -seed changes them.
t_seed() {
	d=$(tmpdir)
	run_test "$d/a" -seed 7 testdata/test/props.kek
	run_test "$d/b" -seed 7 testdata/test/props.kek
	run_test "$d/c" testdata/test/props.kek
	r=
	untimed "$d/a" >"$d/a2"
	untimed "$d/b" >"$d/b2"
	untimed "$d/c" >"$d/c2"
	cmp -s "$d/a2" "$d/b2" || r="two runs with -seed 7 differ:
$(diff "$d/a2" "$d/b2")"
	! cmp -s "$d/a2" "$d/c2" || r="$r
-seed 7 and -seed 0 give the same cases"
	r="$r$(contains "$d/a" "    reproduce: kek test -seed 7 -run '^far_points\$'")"
	result seed "$r" "$d/a"
	rm -rf "$d"
}

# -cases sets the default; #[test(cases = N)] wins.
t_cases() {
	d=$(tmpdir)
	run_test "$d/out" -cases 5 -run 'reverse|option' testdata/test/props.kek
	r=
	[ $code -eq 0 ] || r="exit $code, want 0"
	r="$r$(contains "$d/out" "reverse_twice_is_identity ... ok (pure: hermetic, cacheable; 5 cases;" \
		"option_default ... ok (pure: hermetic, cacheable; 20 cases;")"
	result cases "$r" "$d/out"
	run_test "$d/out" -cases 0 testdata/test/props.kek
	r=
	[ $code -eq 2 ] || r="exit $code, want 2"
	r="$r$(contains "$d/out" 'invalid value "0" for flag -cases')"
	result bad-cases "$r" "$d/out"
	rm -rf "$d"
}

# Every run of a property gets fresh mocks, and a failure shows the mock
# state (log lines) of the counterexample's run only.
t_mocks() {
	d=$(tmpdir)
	cat >"$d/m.kek" <<'EOF2'
#[test]
fn db_starts_empty(db: &Db, k: String) -> Result<(), String> {
    match db.get(k) {
        Ok(Some(_)) => {
            return Err("not fresh");
        }
        _ => {}
    }
    db.transaction(|tx| {
        tx.put(k, "x")?;
        tx.commit()
    }).unwrap_or(());
    Ok(())
}

#[test]
fn logs_last_run(log: &Log, n: Int) -> Bool {
    log.info("n is " + n.to_string());
    n < 5
}

#[test]
fn random_is_replayed(r: &Random, n: Int) -> Bool {
    let a = r.int(0, 1000000);
    a == a && n == n
}
EOF2
	run_test "$d/out" "$d/m.kek"
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "test db_starts_empty ... ok (mock Db; 100 cases;" \
		"    counterexample: logs_last_run(n = 5)" "    info: n is 5" \
		"test random_is_replayed ... ok" "test result: FAILED. 2 passed; 1 failed")"
	[ "$(grep -c 'info: n is' "$d/out")" = 1 ] || r="$r
expected exactly one log line"
	result mocks "$r" "$d/out"
	rm -rf "$d"
}

# A trap ends the process: the last input noted is reported.
t_trap() {
	d=$(tmpdir)
	cat >"$d/t.kek" <<'EOF2'
fn down(n: Int) -> Int {
    if n <= 0 {
        return 0;
    }
    down(n + 1) + 1
}

#[test]
fn deep(n: Int) {
    let x = down(n);
}
EOF2
	run_test "$d/out" "$d/t.kek"
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "test deep ... FAILED (trapped)" "    trap: " "    last input: case " "deep(n = ")"
	result trap "$r" "$d/out"
	rm -rf "$d"
}

# User types: structs, enums (recursive, generic), tuples, Result, ().
t_types() {
	d=$(tmpdir)
	cat >"$d/u.kek" <<'EOF2'
struct Pair<A, B> {
    first: A,
    mut second: B,
}

enum List<T> {
    Nil,
    Cons(T, List<T>),
}

struct Empty {}

fn len(l: List<Int>) -> Int {
    match l {
        List::Nil => 0,
        List::Cons(_, rest) => 1 + len(rest),
    }
}

#[test]
fn short_lists(l: List<Int>) -> Bool {
    len(l) < 2
}

#[test]
fn pairs(p: Pair<String, Vec<Bool>>, e: Empty, t: (Int, ()), r: Result<Int, String>) -> Bool {
    p.second.len() < 2
}
EOF2
	run_test "$d/out" "$d/u.kek"
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" "    counterexample: short_lists(l = Cons(0, Cons(0, Nil)))" \
		'    counterexample: pairs(p = Pair { first: "", second: [false, false] }, e = Empty, t = (0, ()), r = Err(""))')"
	result types "$r" "$d/out"
	mkdir "$d/o"
	r=
	"$KEK" test-build "$d/u.kek" "$d/o" -list >"$d/log" 2>&1 || r="exit $?"
	# (json_quote writes < and > as unicode escapes)
	sed -e 's/\\u003c/</g' -e 's/\\u003e/>/g' "$d/o/tests.json" >"$d/tests.json"
	r="$r$(contains "$d/tests.json" '"params":[{"name":"l","type":"List<Int>"}],"cases":null' \
		'"params":[{"name":"p","type":"Pair<String, Vec<Bool>>"},{"name":"e","type":"Empty"},{"name":"t","type":"(Int, ())"},{"name":"r","type":"Result<Int, String>"}]')"
	result tests-json "$r" "$d/o/tests.json"
	rm -rf "$d"
}

# refined: parameters of refined alias types get values satisfying the
# predicate, shrinking stays within it
t_refined() {
	d=$(tmpdir)
	run_test "$d/out" testdata/test/refined.kek
	r=
	[ $code -eq 1 ] || r="exit $code, want 1"
	r="$r$(contains "$d/out" \
		"test ports_are_unprivileged ... ok (pure: hermetic, cacheable; 100 cases" \
		"test first_is_an_element ... ok (pure: hermetic, cacheable; 100 cases" \
		"test small_ports ... FAILED" \
		"    counterexample: small_ports(p = 2000)" \
		"test result: FAILED. 2 passed; 1 failed")"
	result refined "$r" "$d/out"
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	"t_$2"
	exit 0
fi
run_parallel "$0" props seed cases mocks trap types refined
