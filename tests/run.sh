#!/bin/sh
# Test entry point. Needs the toolchain of ./kek (wasmtime); the e2e suite
# also needs workerd and difftest the Lean reference interpreter
# (`mise run lean`), and they are skipped without them.
#
#   tests/run.sh [options] [suite...]
#
# Suites (default: all, in this order):
#   bootstrap     ./kek bootstrap-check (the self-hosting fixed point) and
#                 compiler/prelude_src.kek up to date with lib/prelude
#   check         testdata/check/*.kek and tests/syntax/*.kek (syntax errors)
#                 against their `// ERROR "..."` annotations
#   bad-examples  examples/*/*.bad.kek are rejected (`// kek check: ...`)
#   run           testdata/run/*.kek: stdout == .out, exit code == `// exit: N`
#   kek-test      kek test: discovery, mocks, failures, flags
#   cover         kek cover: probes keep behaviour, report, -json, -lcov, min_line
#   mutate        kek mutate: the schema keeps behaviour, report, -json, cache,
#                 -base / -diff, min_score
#   pbt           kek test: property tests (generation, shrinking, -seed, -cases)
#   test-cache    kek test: result cache, -j, -json; the kek build cache
#   agent-cmds    check -json, ir -json, caps and search against tests/agent_cmds goldens
#   assure        kek assure plan/apply/check on testdata/assure against
#                 tests/assure goldens
#   daemon        kek daemon: the compiler kept running gives the same results
#   lsp           kek lsp: a language server session on tests/lsp against
#                 its golden (diagnostics, hover, definition, references,
#                 symbols, UTF-16 positions)
#   actions       kek affected / test -affected (git changes), the input-digest
#                 action cache of build and test, the remote cache (file://)
#   similar       kek similar (duplicate / similar code) against tests/similar goldens
#   complexity    kek complexity (cognitive / cyclomatic complexity, nesting)
#                 against tests/complexity goldens
#   smt           the refinement-type solver (kek smt): testdata/smt goldens,
#                 random formulas against brute force, a timed benchmark
#   refine        refinement types in kek check: plain, -v and -json
#                 diagnostics (counterexamples, facts) and the [refine]
#                 configuration against tests/refine goldens
#   fmt           kek fmt goldens, -check / -w, and round trips (idempotent,
#                 same AST, comments and diagnostics) over the sources and
#                 random programs
#   fix           kek fix: the `mut` it adds (diff, -w, comments kept, a
#                 fixed point), what it leaves, usage
#   difftest      random programs: WasmGC (wasmtime) vs the Lean reference interpreter
#   e2e           Workers programs (testdata/e2e, examples) and the store adapter
#                 conformance suite on workerd
#
# Options:
#   -j N          parallel jobs (default: number of CPUs)
#   -n N          difftest: number of random programs (default 60)
#   --fmt-gen N   fmt: number of random programs to round-trip (default 40)
#   --seed S      difftest: first seed (default 1)
#   --keep        difftest: keep the generated programs
#   --short       fewer random programs (difftest 20, fmt 10)
#   -v            print the output of passing cases too
set -u
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

JOBS=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)
N=60
FMT_GEN=40
SEED=1
KEEP=0
SHORT=0
VERBOSE=0
suites=
while [ $# -gt 0 ]; do
	case $1 in
	-j) JOBS=$2; shift ;;
	-n) N=$2; shift ;;
	--fmt-gen) FMT_GEN=$2; shift ;;
	--seed) SEED=$2; shift ;;
	--keep) KEEP=1 ;;
	--short) SHORT=1 ;;
	-v) VERBOSE=1 ;;
	-h | --help)
		sed -n '2,/^set -u/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
		exit 0
		;;
	-*) echo "unknown option $1" >&2; exit 2 ;;
	*) suites="$suites $1" ;;
	esac
	shift
done
if [ "$SHORT" = 1 ]; then
	[ "$N" = 60 ] && N=20
	[ "$FMT_GEN" = 40 ] && FMT_GEN=10
fi
export JOBS N FMT_GEN SEED KEEP SHORT VERBOSE
all="bootstrap check bad-examples run kek-test pbt test-cache actions daemon lsp cover mutate agent-cmds assure similar complexity smt refine fmt fix difftest e2e"
[ -n "$suites" ] || suites=$all
for s in $suites; do
	[ -f "tests/suites/$s.sh" ] || { echo "unknown suite $s (suites: $all)" >&2; exit 2; }
done

start=$(date +%s)
out=$(mktemp -d "${TMPDIR:-/tmp}/kek-tests-XXXXXX")
trap 'rm -rf "$out"' EXIT

# Build the compiler once before the suites share it.
./kek help >/dev/null || { echo "building the compiler failed" >&2; exit 1; }

i=0
for s in $suites; do
	i=$((i + 1))
	echo "== $s" >"$out/$i"
	sh "tests/suites/$s.sh" >>"$out/$i" 2>&1 || echo "FAIL $s (suite script exited $?)" >>"$out/$i"
	cat "$out/$i"
done

pass=$(cat "$out"/* | grep -c '^ok ' || true)
failed=$(cat "$out"/* | grep -c '^FAIL ' || true)
skipped=$(cat "$out"/* | grep -c '^skip ' || true)
echo
echo "$pass passed, $failed failed, $skipped skipped ($(($(date +%s) - start))s)"
[ "$failed" -eq 0 ]
