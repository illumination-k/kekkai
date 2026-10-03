#!/bin/sh
# check: every testdata/check/*.kek is run through `./kek check`. Lines
# annotated with `// ERROR "substring"` must produce a matching diagnostic
# on that line; no other diagnostics are allowed.
. "$(dirname "$0")/../lib.sh"
SUITE=check

one() {
	f=$1
	d=$(tmpdir)
	set +e
	"$KEK" check "$f" >"$d/out" 2>"$d/err"
	code=$?
	set -e
	# expected: "line<TAB>substring"
	awk '{
		s = $0
		while (match(s, /ERROR "[^"]*"/)) {
			m = substr(s, RSTART + 7, RLENGTH - 8)
			print NR "\t" m
			s = substr(s, RSTART + RLENGTH)
		}
	}' "$f" >"$d/want"
	# got: "line<TAB>message"
	grep -v '^[[:space:]]*$' "$d/err" | sed -n 's/^[^:]*:\([0-9]*\):[0-9]*: \(.*\)$/\1	\2/p' >"$d/got"
	report=$(awk -F '\t' -v code="$code" '
		FNR == NR { want[NR] = $0; wl[NR] = $1; ws[NR] = $2; nw = NR; next }
		{ got[FNR] = $0; gl[FNR] = $1; gm[FNR] = $2; ng = FNR }
		END {
			if (code != 0 && ng == 0) print "check exited " code " without diagnostics"
			if (code == 0 && ng > 0) print "check exited 0 but printed diagnostics"
			for (i = 1; i <= nw; i++) {
				found = 0
				for (j = 1; j <= ng; j++) {
					if (!used[j] && gl[j] == wl[i] && index(gm[j], ws[i]) > 0) { used[j] = 1; found = 1; break }
				}
				if (!found) print wl[i] ": missing error matching \"" ws[i] "\""
			}
			for (j = 1; j <= ng; j++) if (!used[j]) print "unexpected error: " gl[j] ": " gm[j]
		}' "$d/want" "$d/got")
	if [ -n "$report" ]; then
		case_fail "$(basename "$f")" "$report
$(cat "$d/err")"
	else
		case_ok "$(basename "$f")"
	fi
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
run_parallel "$0" testdata/check/*.kek
