#!/bin/sh
# run: the #[main] programs in testdata/run are run with `./kek run` and a
# temporary file path as the only argument; stdout must equal the .out
# file and the exit code the `// exit: N` comment on the first line.
. "$(dirname "$0")/../lib.sh"
SUITE=run

one() {
	f=$1
	name=$(basename "$f" .kek)
	want_code=$(head -n 1 "$f" | sed -n 's|^// exit: \(-\{0,1\}[0-9]*\).*|\1|p')
	[ -n "$want_code" ] || want_code=0
	d=$(tmpdir)
	set +e
	"$KEK" run "$f" "$d/scratch.txt" >"$d/out" 2>"$d/err"
	code=$?
	set -e
	report=
	[ "$code" = "$want_code" ] || report="exit code $code, want $want_code
$(cat "$d/err")"
	if ! cmp -s "$d/out" "${f%.kek}.out"; then
		report="$report
stdout differs:
$(diff "${f%.kek}.out" "$d/out")
stderr:
$(cat "$d/err")"
	fi
	if [ -n "$report" ]; then
		case_fail "$name" "$report"
	else
		case_ok "$name"
	fi
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
run_parallel "$0" testdata/run/*.kek
