#!/bin/sh
# The git merge driver `kek` (.gitattributes, docs/merge.md):
#
#   scripts/merge-driver.sh %O %A %B %L %P
#
# git's own three-way merge runs first; where it conflicts, `kek merge`
# merges the file instead (both sides' insertions kept, adjacent edits and
# edits of the same line in different words merged, .kek files merged by
# their syntax tree). The result is written to %A; the exit code is 0 when
# clean. If kek cannot run (the compiler of this checkout does not build,
# or predates `kek merge`), git's result with its conflict markers stays.
# Install it with scripts/install-hooks.sh (`mise run hooks`). $KEK
# overrides the launcher (default: ./kek of this checkout).
set -u

base=$1
ours=$2
theirs=$3
size=${4:-7}
path=${5:-$2}
root=$(cd "$(dirname "$0")/.." && pwd)

tmp=$(mktemp "${TMPDIR:-/tmp}/kek-merge-XXXXXX")
err=$(mktemp "${TMPDIR:-/tmp}/kek-merge-XXXXXX")
trap 'rm -f "$tmp" "$err"' EXIT
cp "$ours" "$tmp"
if git merge-file -q --diff3 --marker-size="$size" -L ours -L base -L theirs "$tmp" "$base" "$theirs"; then
	cat "$tmp" >"$ours"
	exit 0
fi
"${KEK:-$root/kek}" merge -marker-size "$size" -path "$path" "$ours" "$base" "$theirs" 2>"$err"
code=$?
if [ "$code" -le 1 ]; then
	exit "$code"
fi
echo "kek merge failed on $path (git's merge is kept):" >&2
tail -n 3 "$err" >&2
cat "$tmp" >"$ours"
exit 1
