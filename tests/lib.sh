# Helpers shared by the test suites (sourced by tests/suites/*.sh).
#
# A suite script prints one result line per case:
#   ok   <suite>/<case>
#   FAIL <suite>/<case>      followed by the report, indented
#   skip <suite>/<case>: <reason>
# and runs its cases in parallel with run_parallel.
set -u

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
KEK=${KEK:-$ROOT/kek}
# the cache of $KEK (the single binary's is not under the repository)
CACHE=${KEK_CACHE:-$ROOT/.kek-cache}
JOBS=${JOBS:-4}
VERBOSE=${VERBOSE:-0}
SHORT=${SHORT:-0}
cd "$ROOT"

indent() {
	sed 's/^/    /'
}

case_ok() {
	echo "ok   $SUITE/$1"
	if [ "$VERBOSE" = 1 ] && [ -n "${2:-}" ]; then
		printf '%s\n' "$2" | indent
	fi
}

case_fail() {
	echo "FAIL $SUITE/$1"
	printf '%s\n' "$2" | indent
}

case_skip() {
	echo "skip $SUITE/$1: $2"
}

# tmpdir creates a temporary directory (removed by the caller).
tmpdir() {
	mktemp -d "${TMPDIR:-/tmp}/kek-test-XXXXXX"
}

# run_parallel <script> <arg...>: runs `<script> --case <arg>` for every
# argument, JOBS at a time, and prints their outputs in argument order.
run_parallel() {
	_script=$1
	shift
	[ $# -gt 0 ] || return 0
	_pd=$(tmpdir)
	_i=0
	for _a in "$@"; do
		_i=$((_i + 1))
		printf '%05d\0%s\0' "$_i" "$_a"
	done | xargs -0 -n 2 -P "$JOBS" sh -c \
		'"$0" --case "$4" >"$1/$3" 2>&1 || echo "FAIL $2/$4 (case script exited $?)" >>"$1/$3"' \
		"$_script" "$_pd" "$SUITE" || true
	cat "$_pd"/* 2>/dev/null
	rm -rf "$_pd"
}

# kek_out <file> <args...>: runs ./kek, stdout and stderr to separate files
# <file>.out / <file>.err; returns the exit code.
kek_out() {
	_f=$1
	shift
	set +e
	"$KEK" "$@" >"$_f.out" 2>"$_f.err"
	_c=$?
	set -e
	return $_c
}
