#!/usr/bin/env bash
# Build the Lean reference interpreter of the Kekkai IR and smoke-test it on
# IR produced by `kek ir -json`. Usage: lean/test_ref.sh (from anywhere).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(dirname "$here")"
(cd "$here" && lake build kekkai-ref >/dev/null)
ref="$here/.lake/build/bin/kekkai-ref"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/kekkai-ref.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
(cd "$root" && ./kek ir -json lean/test/smoke.kek >"$tmp/smoke.json")
(cd "$root" && ./kek ir -json testdata/check/ok_basic.kek >"$tmp/basic.json")
(cd "$root" && ./kek ir -json lean/test/coll.kek >"$tmp/coll.json")

fail=0
check() { # check <ir> <expected> <fn> <args...>
  local ir="$1" want="$2"; shift 2
  local got
  got="$("$ref" "$tmp/$ir.json" "$@")"
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $ir $*: got $got, want $want"; fail=1
  fi
}
ok() { echo "{\"ok\":$1,\"log\":${2:-[]}}"; }

check basic "$(ok 5)" add 2 3
check basic "$(ok -9223372036854775808)" add 9223372036854775807 1
check basic "$(ok 5050)" sum_to 100
check basic '{"error":"timeout"}' sum_to 1000000
check basic "$(ok '{"tag":0,"fields":[42]}')" parse_id '"42"'
check basic "$(ok '{"tag":1,"fields":[{"tag":1,"fields":["abc"]}]}')" parse_id '"abc"'
check basic "$(ok null '[["info","hello bob"]]')" greet '{"fields":[1,"bob"]}'
check basic '{"error":"unsupported await db.begin"}' create '{"fields":[1,"bob"]}'

check smoke "$(ok -3)" div 7 -2
check smoke "$(ok 0)" div 5 0
check smoke "$(ok -9223372036854775808)" div -9223372036854775808 -1
check smoke "$(ok -1)" rem -7 2
check smoke "$(ok 5)" rem 5 0
check smoke "$(ok 0)" rem -9223372036854775808 -1
check smoke "$(ok 1)" mul 9223372036854775807 9223372036854775807
check smoke "$(ok -9223372036854775808)" neg -9223372036854775808
check smoke "$(ok -9223372036854775808)" abs -9223372036854775808
check smoke "$(ok 11)" pick 0 10
check smoke "$(ok 3)" pick 1 10
check smoke "$(ok -10)" pick 2 10
check smoke "$(ok -4249290049419214848)" fact 21
check smoke '{"error":"timeout"}' spin 0
check smoke "$(ok '"  [-12/TRUE]  "')" describe -12 true
check smoke "$(ok 5001)" strs '"  hello  "' '"lo"'
check smoke "$(ok 5111)" strs '"hello"' '""'
check smoke "$(ok 12)" parse '"+12"'
check smoke "$(ok -1)" parse '"9223372036854775808"'
check smoke "$(ok 5 '[["info","hi HeLLo"],["warn","hello"]]')" shout '"HeLLo"'

# collections, mutable structs, aliasing (expected values checked against the WasmGC output)
check coll "$(ok 426)" alias 3
check coll "$(ok 15751)" vecs 30
check coll "$(ok -10)" vecs 0
check coll "$(ok 231430)" maps 10
check coll "$(ok '"x|y"')" keys_str
check coll "$(ok '"a+bc++de/8/,bc/a,bc,,de/2/a--bc----de/97/-1/Ba,bc,,de/-43"')" strops '"a,bc,,de"'
check coll "$(ok '{"vec":[0,1,2]}')" retvec 3
check coll "$(ok '{"vec":["k"]}')" retmap
check coll "$(ok '{"fields":[1,{"vec":[2]}]}')" retcell
check coll "$(ok 40389)" nested 3
check coll "$(ok 123)" vsum3 '{"vec":[0,1,2,3]}'
if [[ $fail -ne 0 ]]; then exit 1; fi
echo "kekkai-ref: all smoke tests passed"
