#!/bin/sh
# similar: kek similar against the goldens in tests/similar/golden (each
# kind of finding, -json, #[allow(similar)], -base, -threshold, -all,
# -tests, -semantic, kekkai.toml, exit codes), and `kek similar -diff <rev>` in a
# temporary git repository.
#
#   UPDATE=1 tests/run.sh similar    rewrite the golden files
#
# A golden file holds stdout, then `--- stderr` and stderr when it is not
# empty, then `--- exit <code>`. Definition hashes are replaced by H.
. "$(dirname "$0")/../lib.sh"
SUITE=similar
# the config and diff cases run ./kek outside the repository, where a mise
# shim would not find the wasmtime version of mise.toml
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null) || WASMTIME=
	[ -n "$WASMTIME" ] && export WASMTIME
fi
golden=$ROOT/tests/similar/golden
tab=$(printf '\t')

basic=testdata/similar/basic.kek

# One case per line: name, then the arguments, separated by tabs.
cases() {
	cat <<EOF2
basic	$basic
basic_json	-json	$basic
all_tests	-all	-tests	$basic
threshold_95	-threshold	95	$basic
threshold_60_json	-json	-threshold=60	$basic
base	-base	testdata/similar/base.kek	$basic
base_json	-json	-base	testdata/similar/base.kek	$basic
min_nodes	-min_nodes	30	$basic
clean	testdata/similar/clean.kek
semantic	-semantic	testdata/similar/semantic.kek
semantic_json	-json	-semantic	testdata/similar/semantic.kek
bad_threshold	-threshold	0	$basic
bad_flag	-bogus	$basic
no_path
bad_program	testdata/check/err_types.kek
bad_base	-base	testdata/check/err_types.kek	$basic
config:config
diff:diff
EOF2
}

# render <out> <err> <code>
render() {
	sed -e 's/"hash": "[0-9a-f]*"/"hash": "H"/' "$1"
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
		mkdir -p "$golden"
		cp "$got" "$golden/$name.txt"
		case_ok "$name" "updated"
		return
	fi
	if [ ! -f "$golden/$name.txt" ]; then
		case_fail "$name" "missing golden file tests/similar/golden/$name.txt (UPDATE=1 to create)"
	elif cmp -s "$got" "$golden/$name.txt"; then
		case_ok "$name"
	else
		case_fail "$name" "output differs from tests/similar/golden/$name.txt:
$(diff "$golden/$name.txt" "$got")"
	fi
}

# config: [similar] threshold and min_nodes from ./kekkai.toml; a flag
# overrides the file.
case_config() {
	d=$1
	cp "$ROOT/$basic" "$d/basic.kek"
	printf '[similar]\nthreshold = 95\nmin_nodes = 20\n' >"$d/kekkai.toml"
	{
		(cd "$d" && "$KEK" similar basic.kek >"$d/o1" 2>"$d/e1")
		render "$d/o1" "$d/e1" $?
		echo "--- with -threshold 80"
		(cd "$d" && "$KEK" similar -threshold 80 basic.kek >"$d/o2" 2>"$d/e2")
		render "$d/o2" "$d/e2" $?
		# larger trees than max_nodes are compared by their label
		# sequences (approx.)
		printf '[similar]\nmax_nodes = 30\n' >"$d/kekkai.toml"
		echo "--- max_nodes = 30"
		(cd "$d" && "$KEK" similar -json basic.kek >"$d/o4" 2>"$d/e4")
		render "$d/o4" "$d/e4" $?
		printf '[similar\n' >"$d/kekkai.toml"
		echo "--- bad kekkai.toml"
		(cd "$d" && "$KEK" similar basic.kek >"$d/o3" 2>"$d/e3")
		render "$d/o3" "$d/e3" $?
	} >"$d/got"
}

# diff: a repository whose second commit adds near copies; -diff HEAD~1
# reports only the findings with a new definition, -diff HEAD none.
case_diff() {
	d=$1
	if ! command -v git >/dev/null 2>&1; then
		echo skip
		return
	fi
	mkdir -p "$d/repo/src"
	cp "$ROOT/testdata/similar/base.kek" "$d/repo/src/app.kek"
	(
		cd "$d/repo" &&
			git init -q &&
			git -c user.name=t -c user.email=t@example.com add . &&
			git -c user.name=t -c user.email=t@example.com commit -q -m base
	) || return 1
	cp "$ROOT/$basic" "$d/repo/src/app.kek"
	(
		cd "$d/repo" &&
			git -c user.name=t -c user.email=t@example.com commit -q -a -m copies
	) || return 1
	{
		(cd "$d/repo" && "$KEK" similar -diff HEAD~1 src >"$d/o1" 2>"$d/e1")
		render "$d/o1" "$d/e1" $?
		echo "--- -diff=HEAD (from a subdirectory)"
		(cd "$d/repo/src" && "$KEK" similar -diff=HEAD -json app.kek >"$d/o2" 2>"$d/e2")
		render "$d/o2" "$d/e2" $?
		echo "--- without -diff"
		(cd "$d/repo" && "$KEK" similar src >"$d/o3" 2>"$d/e3")
		render "$d/o3" "$d/e3" $?
		echo "--- a path that is new since the revision"
		mkdir "$d/repo/new"
		cp "$ROOT/testdata/similar/clean.kek" "$d/repo/new/x.kek"
		(cd "$d/repo" && "$KEK" similar -diff HEAD new >"$d/o4" 2>"$d/e4")
		render "$d/o4" "$d/e4" $?
		echo "--- unknown revision"
		(cd "$d/repo" && "$KEK" similar -diff nosuchrev src >"$d/o5" 2>"$d/e5")
		render "$d/o5" "$d/e5" $?
		echo "--- missing revision"
		(cd "$d/repo" && "$KEK" similar src -diff >"$d/o6" 2>"$d/e6")
		render "$d/o6" "$d/e6" $?
	} >"$d/got"
}

one() {
	set +e
	line=$1
	d=$(tmpdir)
	old_ifs=$IFS
	IFS=$tab
	# shellcheck disable=SC2086
	set -- $line
	IFS=$old_ifs
	name=$1
	shift
	case $name in
	config:*)
		name=${name#config:}
		case_config "$d"
		;;
	diff:*)
		name=${name#diff:}
		if [ "$(case_diff "$d")" = skip ]; then
			case_skip "$name" "git not found"
			rm -rf "$d"
			return
		fi
		;;
	*)
		"$KEK" similar "$@" >"$d/stdout" 2>"$d/stderr"
		code=$?
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
cases >"${TMPDIR:-/tmp}/kek-similar-cases.$$"
set --
while IFS= read -r l; do
	set -- "$@" "$l"
done <"${TMPDIR:-/tmp}/kek-similar-cases.$$"
rm -f "${TMPDIR:-/tmp}/kek-similar-cases.$$"
run_parallel "$0" "$@"
