#!/bin/sh
# fmt: tests of `kek fmt`.
#
#   golden/<name>     tests/fmt/<name>.in.kek formats to <name>.golden, and
#                     the golden file passes `fmt -check`
#   check-testdata    `fmt -check testdata/test` passes
#   dirs              -check / -w over directories (hidden and vendored
#                     dependency directories are skipped, other files ignored)
#   parse-error       a parse error is reported with its position, exit 1
#   usage             no paths / unknown flags are errors
#   <file>            round trip of every testdata/**/*.kek, compiler/*.kek
#                     and lib/prelude/*.kek
#   generated/seedS   round trip of FMT_GEN random programs (tests/difftest/gen.kek)
#
# A round trip formats a file and requires: formatting the output again
# changes nothing, the AST is unchanged (`kek ast` with the ` @L:C` and
# ` end=L:C` positions stripped), the comments are unchanged, and the
# `kek check` diagnostics are unchanged (positions stripped; the prelude
# files cannot be checked standalone, their failures must be identical).
. "$(dirname "$0")/../lib.sh"
SUITE=fmt

# comments <file>: the comments of a Kekkai source (line and block comments
# with their delimiters, newlines inside block comments written as \n),
# sorted.
comments() {
	awk '
		{ src = src $0 "\n" }
		END {
			n = length(src)
			i = 1
			while (i <= n) {
				c = substr(src, i, 1)
				if (c == "\"") {
					i++
					while (i <= n) {
						c = substr(src, i, 1)
						if (c == "\"" || c == "\n") break
						i += (c == "\\") ? 2 : 1
					}
					i++
				} else if (c == "/" && substr(src, i + 1, 1) == "/") {
					j = i
					while (j <= n && substr(src, j, 1) != "\n") j++
					out(substr(src, i, j - i))
					i = j
				} else if (c == "/" && substr(src, i + 1, 1) == "*") {
					j = i + 2
					while (j <= n && substr(src, j, 2) != "*/") j++
					j = (j > n) ? n + 1 : j + 2
					out(substr(src, i, j - i))
					i = j
				} else {
					i++
				}
			}
		}
		function out(s) {
			sub(/\r+$/, "", s)
			gsub(/\n/, "\\n", s)
			print s
		}' "$1" | LC_ALL=C sort
}

strip_ast() {
	sed -e 's/ @[0-9]*:[0-9]*//g' -e 's/ end=[0-9]*:[0-9]*//g'
}

# check_msgs <file>: diagnostics of `kek check` without file and position,
# sorted.
check_msgs() {
	"$KEK" check "$1" 2>&1 >/dev/null | grep -v '^[[:space:]]*$' |
		sed 's/^[^:]*:[0-9]*:[0-9]*: //' | LC_ALL=C sort
}

# roundtrip <case> <file>
roundtrip() {
	d=$(tmpdir)
	b=$(basename "$2")
	mkdir "$d/before" "$d/after"
	cp "$2" "$d/before/$b"
	report=
	if ! "$KEK" fmt "$d/before/$b" >"$d/after/$b" 2>"$d/err"; then
		case_fail "$1" "kek fmt failed:
$(cat "$d/err")"
		rm -rf "$d"
		return
	fi
	if ! "$KEK" fmt "$d/after/$b" >"$d/again" 2>"$d/err"; then
		report="formatting the output failed:
$(cat "$d/err")"
	elif ! cmp -s "$d/after/$b" "$d/again"; then
		report="not idempotent:
$(diff "$d/after/$b" "$d/again")"
	fi
	set +e
	"$KEK" ast "$d/before/$b" 2>&1 | strip_ast >"$d/ast1"
	"$KEK" ast "$d/after/$b" 2>&1 | strip_ast >"$d/ast2"
	if ! cmp -s "$d/ast1" "$d/ast2"; then
		report="$report
AST changed:
$(diff "$d/ast1" "$d/ast2" | head -n 40)"
	fi
	comments "$d/before/$b" >"$d/com1"
	comments "$d/after/$b" >"$d/com2"
	if ! cmp -s "$d/com1" "$d/com2"; then
		report="$report
comments changed:
$(diff "$d/com1" "$d/com2")"
	fi
	check_msgs "$d/before/$b" >"$d/chk1"
	check_msgs "$d/after/$b" >"$d/chk2"
	if ! cmp -s "$d/chk1" "$d/chk2"; then
		report="$report
type-check result changed:
$(diff "$d/chk1" "$d/chk2" | head -n 40)"
	fi
	if [ -n "$report" ]; then
		case_fail "$1" "$report"
	else
		case_ok "$1"
	fi
	rm -rf "$d"
}

golden() {
	name=$1
	d=$(tmpdir)
	report=
	if ! "$KEK" fmt "tests/fmt/$name.in.kek" >"$d/out" 2>"$d/err"; then
		report="kek fmt failed:
$(cat "$d/err")"
	elif ! cmp -s "$d/out" "tests/fmt/$name.golden"; then
		report="output differs from tests/fmt/$name.golden:
$(diff "tests/fmt/$name.golden" "$d/out")"
	fi
	# formatting is idempotent: the golden file is already formatted
	cp "tests/fmt/$name.golden" "$d/$name.kek"
	if ! "$KEK" fmt -check "$d/$name.kek" >"$d/out" 2>&1; then
		report="$report
fmt -check fails on the golden file:
$(cat "$d/out")"
	fi
	if [ -n "$report" ]; then
		case_fail "golden/$name" "$report"
	else
		case_ok "golden/$name"
	fi
	rm -rf "$d"
}

check_testdata() {
	if out=$("$KEK" fmt -check testdata/test 2>&1); then
		case_ok check-testdata
	else
		case_fail check-testdata "$out"
	fi
}

dirs() {
	d=$(tmpdir)
	ugly='fn main()->Int{1}'
	pretty='fn main() -> Int {
    1
}'
	mkdir "$d/sub" "$d/.hidden"
	echo "$ugly" >"$d/sub/a.kek"
	echo "$ugly" >"$d/.hidden/b.kek"
	echo "$ugly" >"$d/d.txt"
	echo "$pretty" >"$d/ok.kek"
	report=
	set +e
	"$KEK" fmt -check "$d" >"$d/out" 2>"$d/err"
	code=$?
	[ "$code" = 1 ] || report="fmt -check: exit $code, want 1"
	[ "$(cat "$d/out")" = "$d/sub/a.kek" ] || report="$report
fmt -check: stdout $(cat "$d/out"), want $d/sub/a.kek"
	grep -q '1 file(s) need formatting' "$d/err" || report="$report
fmt -check: stderr $(cat "$d/err"), want 1 file(s) need formatting"
	if ! "$KEK" fmt -w "$d" >"$d/out" 2>"$d/err"; then
		report="$report
fmt -w failed:
$(cat "$d/err")"
	fi
	[ "$(cat "$d/sub/a.kek")" = "$pretty" ] || report="$report
fmt -w: sub/a.kek is
$(cat "$d/sub/a.kek")"
	[ "$(cat "$d/.hidden/b.kek")" = "$ugly" ] || report="$report
fmt -w changed .hidden/b.kek"
	[ "$(cat "$d/d.txt")" = "$ugly" ] || report="$report
fmt -w changed d.txt"
	"$KEK" fmt -check "$d" >"$d/out" 2>&1 || report="$report
fmt -check after -w failed:
$(cat "$d/out")"
	if [ -n "$report" ]; then
		case_fail dirs "$report"
	else
		case_ok dirs
	fi
	rm -rf "$d"
}

parse_error() {
	d=$(tmpdir)
	echo 'fn main( {' >"$d/bad.kek"
	set +e
	"$KEK" fmt "$d/bad.kek" >"$d/out" 2>"$d/err"
	code=$?
	report=
	[ "$code" = 1 ] || report="exit $code, want 1"
	case $(head -n 1 "$d/err") in
	"$d/bad.kek:1:"*) ;;
	*) report="$report
the first line of stderr does not start with $d/bad.kek:1:" ;;
	esac
	grep -q 'some files could not be parsed' "$d/err" || report="$report
stderr does not say: some files could not be parsed"
	if [ -n "$report" ]; then
		case_fail parse-error "$report
$(cat "$d/err")"
	else
		case_ok parse-error
	fi
	rm -rf "$d"
}

usage() {
	report=
	"$KEK" fmt >/dev/null 2>&1
	code=$?
	[ "$code" = 1 ] || report="kek fmt: exit $code, want 1"
	"$KEK" fmt -bogus x.kek >/dev/null 2>&1
	code=$?
	[ "$code" = 2 ] || report="$report
kek fmt -bogus x.kek: exit $code, want 2"
	if [ -n "$report" ]; then
		case_fail usage "$report"
	else
		case_ok usage
	fi
}

if [ "${1:-}" = --case ]; then
	set +e
	case $2 in
	golden:*) golden "${2#golden:}" ;;
	check-testdata) check_testdata ;;
	dirs) dirs ;;
	parse-error) parse_error ;;
	usage) usage ;;
	gen:*)
		f=${2#gen:}
		roundtrip "generated/$(basename "$(dirname "$f")")" "$f"
		;;
	*) roundtrip "$2" "$2" ;;
	esac
	exit 0
fi

FMT_GEN=${FMT_GEN:-40}
set --
for f in tests/fmt/*.in.kek; do
	n=$(basename "$f" .in.kek)
	set -- "$@" "golden:$n"
done
set -- "$@" check-testdata dirs parse-error usage
for f in $(find testdata compiler lib/prelude -name '*.kek' | LC_ALL=C sort); do
	set -- "$@" "$f"
done
tmp=${TMPDIR:-/tmp}
gen=$(mktemp -d "${tmp%/}/kek-fmt-gen-XXXXXX")
if [ "$FMT_GEN" -gt 0 ]; then
	s=1
	while [ "$s" -le "$FMT_GEN" ]; do
		mkdir "$gen/seed$s"
		set -- "$@" "gen:$gen/seed$s/prog.kek"
		s=$((s + 1))
	done
	if ! "$KEK" run tests/difftest/gen.kek "$gen" 1 "$FMT_GEN" >"$gen/out" 2>&1; then
		case_fail generate "$(cat "$gen/out")"
	fi
fi
run_parallel "$0" "$@"
rm -rf "$gen"
