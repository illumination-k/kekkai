#!/bin/sh
# agent-cmds: golden tests of the tooling commands meant for agents and
# editors: caps [-json], check -json, search [-json] [-limit n], ir -json,
# and the files written by build (worker.js, wrangler.toml, -target d1|do).
#
#   UPDATE=1 tests/run.sh agent-cmds    rewrite the golden files
#
# A golden file holds stdout, then `--- stderr` and stderr when it is not
# empty, then `--- exit <code>`.
. "$(dirname "$0")/../lib.sh"
SUITE=agent-cmds
golden=$ROOT/tests/agent_cmds/golden
tab=$(printf '\t')

bank=testdata/e2e/bank.kek
todo=examples/todo/todo.kek
lint=tests/agent_cmds/lint.kek

# One case per line, arguments separated by tabs.
cases() {
	cat <<EOF2
caps	$bank
caps	$todo
caps	$lint
caps	testdata/test/counter.kek
caps	testdata/check/err_caps.kek
caps	-json	$bank
caps	-json	$lint
check	-json	$lint
check	-json	$bank
check	-json	tests/agent_cmds/parse_err.kek
check	-json	testdata/check/err_caps.kek
check	-json	testdata/check/err_types.kek
check	-json	testdata/check/err_tx.kek
check	-json	examples/webhooks/net_in_tx.bad.kek
check	-json=false	$bank
ir	-json	testdata/run/collections.kek
ir	-json	testdata/run/strings.kek
ir	-json	$bank
search	String -> Option<Int>	$bank
search	-json	String -> Option<Int>	$bank
search	-limit	5	Int	$lint
search	-json	-limit	0	a -> Option<a>	$lint
search	-json	&Log, String -> ()
search	&Db -> Result<_, TxError>	$todo
search	(Int, Int) -> Int	$lint
search	Shape
search	Shape	$lint
search	-limit=3	String, Int -> String
search	Int ->
search	&Int
search	Option<Int
search	->
search	-limit	x	Int
caps	-x	$bank
build:build_bank_d1	$bank
build:build_todo_do	-target	do	$todo
build:build_webhooks	-target=d1	examples/webhooks/webhooks.kek
build:build_payments_keep_toml	-target	do	examples/payments/payments.kek
build:build_bad_target	-target	bogus	$bank
build:build_main_program	testdata/run/strings.kek
EOF2
}

golden_name() {
	printf '%s' "$1" | tr '\t' ' ' | sed -e 's/&/amp/g' -e 's/</lt/g' -e 's/>/gt/g' -e 's/(/lp/g' \
		-e 's/)/rp/g' -e 's/,/comma/g' -e 's/=/eq/g' -e 's/[^A-Za-z0-9_.-]/_/g'
	printf '.txt'
}

# render <out> <err> <code>
render() {
	cat "$1"
	if [ -s "$2" ]; then
		echo "--- stderr"
		cat "$2"
	fi
	echo "--- exit $3"
}

compare() {
	name=$1
	got=$2
	if [ "${UPDATE:-}" = 1 ]; then
		cp "$got" "$golden/$name"
		case_ok "$name" "updated"
		return
	fi
	if [ ! -f "$golden/$name" ]; then
		case_fail "$name" "missing golden file tests/agent_cmds/golden/$name (UPDATE=1 to create)"
	elif cmp -s "$got" "$golden/$name"; then
		case_ok "$name"
	else
		case_fail "$name" "output differs from tests/agent_cmds/golden/$name:
$(diff "$golden/$name" "$got")"
	fi
}

one() {
	line=$1
	d=$(tmpdir)
	old_ifs=$IFS
	IFS=$tab
	# shellcheck disable=SC2086
	set -- $line
	IFS=$old_ifs
	case $1 in
	build:*)
		name=${1#build:}.txt
		shift
		mkdir "$d/out"
		[ "$name" != build_payments_keep_toml.txt ] || printf '# edited\n' >"$d/out/wrangler.toml"
		set +e
		"$KEK" build -o "$d/out" "$@" >"$d/stdout" 2>"$d/stderr"
		code=$?
		set -e
		{
			render "$d/stdout" "$d/stderr" "$code" | sed -e "s|$d/out|OUT|g" -e 's/([0-9]* bytes of wasm)/(N bytes of wasm)/'
			printf -- '--- files: %s\n' "$(ls "$d/out" | tr '\n' ' ' | sed 's/ $//')"
			for f in worker.js wrangler.toml; do
				if [ -f "$d/out/$f" ]; then
					echo "--- $f"
					cat "$d/out/$f"
				fi
			done
		} >"$d/got"
		;;
	*)
		name=$(golden_name "$line")
		set +e
		"$KEK" "$@" >"$d/stdout" 2>"$d/stderr"
		code=$?
		set -e
		render "$d/stdout" "$d/stderr" "$code" >"$d/got"
		;;
	esac
	compare "$name" "$d/got"
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
case_name() {
	case $1 in
	build:*) echo "${1%%"$tab"*}" ;;
	*)
		golden_name "$1"
		echo
		;;
	esac
}
names=$(cases | while IFS= read -r l; do case_name "$l"; done | sort | uniq -d)
if [ -n "$names" ]; then
	case_fail golden-names "golden file names are not distinct: $names"
fi
cases >"${TMPDIR:-/tmp}/kek-agent-cases.$$"
set --
while IFS= read -r l; do
	set -- "$@" "$l"
done <"${TMPDIR:-/tmp}/kek-agent-cases.$$"
rm -f "${TMPDIR:-/tmp}/kek-agent-cases.$$"
run_parallel "$0" "$@"
