#!/bin/sh
# lsp: `kek lsp` (the language server, compiler/lsp.kek). A session of
# framed requests on tests/lsp (a program of two files, a directory of
# single-file programs) is piped to the server, and its messages, one per
# line, are compared with tests/lsp/golden.txt: diagnostics on open and
# change, hover, definition (across files, fields), references, document
# and workspace symbols, UTF-16 positions, an unknown method, shutdown.
# KEK_UPDATE_GOLDEN=1 rewrites the golden.
. "$(dirname "$0")/../lib.sh"
SUITE=lsp

# msg <json>: one framed message (Content-Length counts bytes)
msg() {
	printf 'Content-Length: %d\r\n\r\n%s' "$(printf '%s' "$1" | LC_ALL=C wc -c | tr -d ' ')" "$1"
}

# json_text <file>: the file's text as a JSON string
json_text() {
	awk 'BEGIN { ORS = "" } { gsub(/\\/, "\\\\"); gsub(/"/, "\\\""); print $0 "\\n" }' "$1" |
		sed 's/^/"/; s/$/"/'
}

t_session() {
	d=$(tmpdir)
	base=file://$ROOT/tests/lsp
	a=$base/prog/a.kek
	b=$base/prog/b.kek
	one=$base/single/one.kek
	bad=$(sed 's/    0$/    let n: String = norm1(p);\n    0/' tests/lsp/prog/b.kek >"$d/bad.kek" && json_text "$d/bad.kek")
	{
		msg '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":"'"$base"'","capabilities":{}}}'
		msg '{"jsonrpc":"2.0","method":"initialized","params":{}}'
		msg '{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"'"$b"'","languageId":"kekkai","version":1,"text":'"$(json_text tests/lsp/prog/b.kek)"'}}}'
		# norm1 after "é→" on the line: UTF-16 characters 20-25 (bytes 24-29)
		msg '{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"'"$b"'"},"position":{"line":3,"character":24}}}'
		msg '{"jsonrpc":"2.0","id":3,"method":"textDocument/definition","params":{"textDocument":{"uri":"'"$b"'"},"position":{"line":3,"character":22}}}'
		msg '{"jsonrpc":"2.0","id":4,"method":"textDocument/hover","params":{"textDocument":{"uri":"'"$b"'"},"position":{"line":2,"character":8}}}'
		msg '{"jsonrpc":"2.0","id":5,"method":"textDocument/definition","params":{"textDocument":{"uri":"'"$a"'"},"position":{"line":8,"character":6}}}'
		msg '{"jsonrpc":"2.0","id":6,"method":"textDocument/references","params":{"textDocument":{"uri":"'"$a"'"},"position":{"line":1,"character":8},"context":{"includeDeclaration":true}}}'
		msg '{"jsonrpc":"2.0","id":7,"method":"textDocument/hover","params":{"textDocument":{"uri":"'"$b"'"},"position":{"line":3,"character":9}}}'
		msg '{"jsonrpc":"2.0","id":8,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"'"$a"'"}}}'
		msg '{"jsonrpc":"2.0","id":9,"method":"workspace/symbol","params":{"query":"NORM"}}'
		msg '{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"'"$b"'","version":2},"contentChanges":[{"text":'"$bad"'}]}}'
		msg '{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"'"$one"'","languageId":"kekkai","version":1,"text":'"$(json_text tests/lsp/single/one.kek)"'}}}'
		msg '{"jsonrpc":"2.0","id":10,"method":"textDocument/implementation","params":{"textDocument":{"uri":"'"$b"'"},"position":{"line":0,"character":0}}}'
		msg '{"jsonrpc":"2.0","id":11,"method":"shutdown"}'
		msg '{"jsonrpc":"2.0","method":"exit"}'
	} >"$d/in"
	r=
	"$KEK" lsp <"$d/in" >"$d/raw" 2>"$d/err" || r="exit $?"
	# one message per line, the checkout's path replaced
	awk 'BEGIN { RS = "Content-Length: [0-9]+\r\n\r\n" } NR > 1 { print }' "$d/raw" |
		sed "s|file://$ROOT/|file://ROOT/|g" >"$d/got"
	if [ "${KEK_UPDATE_GOLDEN:-}" = 1 ]; then
		cp "$d/got" tests/lsp/golden.txt
	fi
	if ! cmp -s "$d/got" tests/lsp/golden.txt; then
		r="$r
$(diff tests/lsp/golden.txt "$d/got")"
	fi
	if [ -n "$r" ]; then
		case_fail session "$r
--- stderr
$(cat "$d/err")"
	else
		case_ok session
	fi
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	"t_$2"
	exit 0
fi
run_parallel "$0" session
