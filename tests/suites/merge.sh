#!/bin/sh
# merge: kek merge on the three-way cases of testdata/merge/<case>/
# ({base,ours,theirs}.<ext>) against the goldens in tests/merge/golden
# (text and .kek tree merges, conflicts, -no-ast, -L / -marker-size,
# writing <ours>, usage), and the git merge driver (scripts/merge-driver.sh,
# .gitattributes) in a temporary repository.
#
#   UPDATE=1 tests/run.sh merge    rewrite the golden files
#
# A golden file holds stdout, then `--- stderr` and stderr when it is not
# empty, then `--- exit <code>`.
. "$(dirname "$0")/../lib.sh"
SUITE=merge
# the driver case runs ./kek in a repository outside this one, where a mise
# shim would not find the wasmtime version of mise.toml
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null) || WASMTIME=
	[ -n "$WASMTIME" ] && export WASMTIME
fi
golden=$ROOT/tests/merge/golden

cases() {
	for d in testdata/merge/*/; do
		basename "$d"
	done
	echo "moved_no_ast"
	echo "labels"
	echo "write"
	echo "usage"
	echo "driver"
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
		mkdir -p "$golden"
		cp "$got" "$golden/$name.txt"
		case_ok "$name" "updated"
		return
	fi
	if [ ! -f "$golden/$name.txt" ]; then
		case_fail "$name" "missing golden file tests/merge/golden/$name.txt (UPDATE=1 to create)"
	elif cmp -s "$got" "$golden/$name.txt"; then
		case_ok "$name"
	else
		case_fail "$name" "output differs from tests/merge/golden/$name.txt:
$(diff "$golden/$name.txt" "$got")"
	fi
}

# ext <case>: the extension of the case's files (".kek", ".md", ...)
ext() {
	f=$(cd "testdata/merge/$1" && ls base.*)
	echo ".${f#base.}"
}

# merge <dir> <case> [flags...]: kek merge -p on a case, rendered
merge() {
	_d=$1
	_c=$2
	shift 2
	_e=$(ext "$_c")
	_t=testdata/merge/$_c
	"$KEK" merge -p -path "$_c$_e" "$@" "$_t/ours$_e" "$_t/base$_e" "$_t/theirs$_e" >"$_d/o" 2>"$_d/e"
	render "$_d/o" "$_d/e" $?
}

git_t() {
	git -c user.name=t -c user.email=t@example.com -c init.defaultBranch=main "$@"
}

# driver: two branches of a repository that uses the merge driver both add
# a command to compiler/main.kek and a row to README.md (git alone
# conflicts on both), and both add `f` to lib.kek with different bodies:
# the merge stops on lib.kek only.
case_driver() {
	d=$1
	r=$d/repo
	mkdir -p "$r/compiler" "$r/scripts"
	cp "$ROOT/testdata/merge/dispatch/base.kek" "$r/compiler/main.kek"
	cp "$ROOT/testdata/merge/table/base.md" "$r/README.md"
	cp "$ROOT/testdata/merge/same_fn/base.kek" "$r/lib.kek"
	cp "$ROOT/.gitattributes" "$r/.gitattributes"
	cp "$ROOT/scripts/merge-driver.sh" "$r/scripts/merge-driver.sh"
	# the driver runs this repository's kek
	export KEK
	(
		cd "$r" &&
			git_t init -q &&
			git config merge.kek.driver 'sh scripts/merge-driver.sh %O %A %B %L %P' &&
			git_t add . &&
			git_t commit -q -m base &&
			git_t checkout -q -b other &&
			cp "$ROOT/testdata/merge/dispatch/theirs.kek" compiler/main.kek &&
			cp "$ROOT/testdata/merge/table/theirs.md" README.md &&
			git_t commit -q -a -m theirs &&
			git_t checkout -q main &&
			cp "$ROOT/testdata/merge/dispatch/ours.kek" compiler/main.kek &&
			cp "$ROOT/testdata/merge/table/ours.md" README.md &&
			git_t commit -q -a -m ours
	) || return 1
	{
		(cd "$r" && git_t merge -q --no-edit other >"$d/o1" 2>&1)
		echo "--- git merge other: exit $?"
		cat "$r/compiler/main.kek" "$r/README.md"
		(cd "$r" && git status --porcelain)
		echo "--- both add f"
		(
			cd "$r" &&
				git_t checkout -q -b f1 HEAD~1 &&
				cp "$ROOT/testdata/merge/same_fn/ours.kek" lib.kek &&
				git_t commit -q -a -m f1 &&
				git_t checkout -q -b f2 HEAD~1 &&
				cp "$ROOT/testdata/merge/same_fn/theirs.kek" lib.kek &&
				git_t commit -q -a -m f2 &&
				git_t checkout -q f1
		) || return 1
		(cd "$r" && git_t merge -q --no-edit f2 >"$d/o2" 2>&1)
		echo "--- git merge f2: exit $?"
		grep CONFLICT "$d/o2"
		(cd "$r" && git status --porcelain)
		cat "$r/lib.kek"
	} >"$d/got"
}

one() {
	set +e
	name=$1
	d=$(tmpdir)
	case $name in
	moved_no_ast)
		merge "$d" moved -no-ast >"$d/got"
		;;
	labels)
		merge "$d" conflict -ours-label HEAD -base-label merge-base -theirs-label feature -marker-size 3 >"$d/got"
		;;
	write)
		# without -p the result goes to <ours>; stdout stays empty
		cp testdata/merge/dispatch/ours.kek "$d/ours.kek"
		"$KEK" merge "$d/ours.kek" testdata/merge/dispatch/base.kek testdata/merge/dispatch/theirs.kek >"$d/o" 2>"$d/e"
		{
			render "$d/o" "$d/e" $?
			cat "$d/ours.kek"
		} >"$d/got"
		;;
	usage)
		{
			"$KEK" merge a b >"$d/o" 2>"$d/e"
			render "$d/o" "$d/e" $?
			"$KEK" merge -bogus a b c >"$d/o" 2>"$d/e"
			render "$d/o" "$d/e" $?
			"$KEK" merge "$d/missing" "$d/missing" "$d/missing" >"$d/o" 2>"$d/e"
			render "$d/o" "$d/e" $? | sed "s|$d|D|g"
		} >"$d/got"
		;;
	driver)
		if ! command -v git >/dev/null 2>&1; then
			case_skip "$name" "git not found"
			rm -rf "$d"
			return
		fi
		case_driver "$d"
		;;
	*)
		merge "$d" "$name" >"$d/got"
		;;
	esac
	compare "$name" "$d/got"
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
# shellcheck disable=SC2046
run_parallel "$0" $(cases)
