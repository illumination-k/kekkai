#!/bin/sh
# scripts/bench.sh [ref]: time the tooling on the benchmark program
# (testdata/bench/mutate_bench.kek: 125 functions, 100 tests, 1768 mutants)
# and on the compiler itself, cold (an empty cache but for the compiler
# stage) and warm (a second run). With a git ref, the same is measured at
# that ref in a temporary worktree first, to compare a change.
#
#   scripts/bench.sh            this tree
#   scripts/bench.sh main       main, then this tree
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)

# now_ms: milliseconds since the epoch
now_ms() {
	perl -MTime::HiRes=time -e 'printf "%d\n", time * 1000'
}

# bench <tree>: prints "<what>\tcold ms\twarm ms" for each command.
bench() {
	_t=$1
	(cd "$_t" && ./kek help >/dev/null) # build the compiler stage
	_c=$(mktemp -d "${TMPDIR:-/tmp}/kek-bench-XXXXXX")
	_o=$(mktemp -d "${TMPDIR:-/tmp}/kek-bench-out-XXXXXX")
	for _s in "$_t"/.kek-cache/stage-*; do
		[ -d "$_s" ] && cp -R "$_s" "$_c/"
	done
	b=testdata/bench/mutate_bench.kek
	while IFS='|' read -r _name _cmd; do
		_a=$(now_ms)
		(cd "$_t" && KEK_CACHE=$_c sh -c "$_cmd" >/dev/null 2>&1) || true
		_m=$(now_ms)
		(cd "$_t" && KEK_CACHE=$_c sh -c "$_cmd" >/dev/null 2>&1) || true
		_e=$(now_ms)
		printf '%s\t%d\t%d\n' "$_name" $((_m - _a)) $((_e - _m))
	done <<LIST
mutate bench|./kek mutate $b
test bench|./kek test $b
cover bench|./kek cover $b
hash compiler|./kek hash compiler
build compiler|./kek build -o $_o compiler
LIST
	rm -rf "$_c" "$_o"
}

if [ $# -ge 1 ]; then
	_w=$(mktemp -d "${TMPDIR:-/tmp}/kek-bench-ref-XXXXXX")
	git -C "$root" worktree add -q --detach "$_w" "$1"
	cp "$root/testdata/bench/mutate_bench.kek" "$_w/testdata/bench/" 2>/dev/null ||
		{ mkdir -p "$_w/testdata/bench" && cp "$root/testdata/bench/mutate_bench.kek" "$_w/testdata/bench/"; }
	echo "== $1"
	printf 'what\tcold ms\twarm ms\n'
	bench "$_w"
	git -C "$root" worktree remove --force "$_w"
fi
echo "== this tree"
printf 'what\tcold ms\twarm ms\n'
bench "$root"
