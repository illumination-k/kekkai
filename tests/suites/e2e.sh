#!/bin/sh
# e2e: Workers programs and the store adapters on workerd.
#
# Cases:
#   <program>   every testdata/e2e/*.kek and examples/*/*.kek with a sibling
#               *.test.mjs is built with `./kek build`; its scenario runs
#               against the compiled module (tests/e2e/harness.js), with
#               every store adapter, and its generated worker.js must load
#   adapters    the store adapter conformance suite (tests/e2e/adapters.test.js):
#               MemoryStore, D1KvStore on FakeD1 (D1 API over Durable Object
#               SQLite), DurableObjectStore on Durable Object storage,
#               RemoteKvStore on the in-process reference gateway
#   worker      testdata/e2e/bank.kek built with `-target do`, served by its
#               generated worker.js and driven over HTTP on the D1 (FakeD1)
#               and Durable Object backends: forced optimistic conflicts,
#               concurrent requests, outbox deliveries by fetch
#               (tests/e2e/worker_driver.js, worker_wrapper.js, hooks.js)
#
# All cases run in one `workerd test` process: the script generates a
# workerd config with one service per case (each with its own modules and
# Durable Object namespaces; storage on a temporary disk directory), and
# every test() handler prints "KEK <case> | <line>" output lines and a final
# "KEK <case> ok|fail", which are turned into result lines here.
#
# Skipped when workerd is not on PATH (it is installed by `mise install`).
. "$(dirname "$0")/../lib.sh"
SUITE=e2e
COMPAT_DATE=2026-09-01
# Seconds before a hanging workerd is killed.
WORKERD_TIMEOUT=${WORKERD_TIMEOUT:-300}

programs() {
	for f in testdata/e2e/*.kek examples/*/*.kek; do
		[ -f "$f" ] || continue
		case $f in *.bad.kek) continue ;; esac
		[ -f "${f%.kek}.test.mjs" ] && echo "$f"
	done
}

cases() {
	for f in $(programs); do basename "$f" .kek; done
	echo adapters
	echo worker
}

if ! command -v workerd >/dev/null 2>&1; then
	for c in $(cases); do
		case_skip "$c" "workerd not found (installed by mise: mise install)"
	done
	exit 0
fi

T=$(tmpdir)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/lib" "$T/disk"
cp tests/e2e/*.js "$T/lib/"
cp js/kekkai_runtime.js "$T/lib/kekkai_runtime.js"
echo 'export { default as module } from "./module.wasm"; export { default as meta } from "./kekkai_meta.js";' >"$T/lib/program.js"
echo 'export const module = null, meta = null;' >"$T/lib/no_program.js"

# Build every program (and bank.kek for the worker case) in parallel.
# <dir>.code holds the exit status of the build, <dir>.log its output.
build() {
	_dir=$1
	shift
	"$KEK" build -o "$T/$_dir" "$@" >"$T/$_dir.log" 2>&1
	echo $? >"$T/$_dir.code"
}
for f in $(programs); do
	build "p-$(basename "$f" .kek)" "$f" &
done
build worker -target do testdata/e2e/bank.kek &
wait

built() {
	[ "$(cat "$T/$1.code")" = 0 ]
}

modules_common() {
	# $1: case directory under $T
	cat <<EOF
        (name = "kekkai_meta.js", esModule = embed "$1/kekkai_meta.js"),
        (name = "module.wasm", wasm = embed "$1/module.wasm"),
        (name = "worker.js", esModule = embed "$1/worker.js"),
EOF
}

# harness_service <case> <runtime module path> <program module path> <with worker> [<case dir>]
harness_service() {
	cat <<EOF
    (name = "case-$1", worker = (
      modules = [
        (name = "harness.js", esModule = embed "lib/harness.js"),
        (name = "fakes.js", esModule = embed "lib/fakes.js"),
        (name = "scenario.js", esModule = embed "$1.scenario.js"),
        (name = "program.js", esModule = embed "$3"),
        (name = "kekkai_runtime.js", esModule = embed "$2"),
EOF
	[ -n "${5:-}" ] && modules_common "$5"
	cat <<EOF
      ],
      compatibilityDate = "$COMPAT_DATE",
      durableObjectNamespaces = [
        (className = "Runner", uniqueKey = "$1-runner", enableSql = true),
        (className = "FakeD1Object", uniqueKey = "$1-d1", enableSql = true),
      ],
      durableObjectStorage = (localDisk = "disk"),
      bindings = [
        (name = "RUNNER", durableObjectNamespace = "Runner"),
        (name = "FAKE_D1", durableObjectNamespace = "FakeD1Object"),
        (name = "CASE", text = "$1"),
        (name = "WITH_WORKER", text = "$4"),
      ],
    )),
EOF
}

worker_services() {
	cat <<EOF
    (name = "worker", worker = (
      modules = [
        (name = "worker_wrapper.js", esModule = embed "lib/worker_wrapper.js"),
        (name = "fakes.js", esModule = embed "lib/fakes.js"),
        (name = "kekkai_runtime.js", esModule = embed "worker/kekkai_runtime.js"),
EOF
	modules_common worker
	cat <<EOF
      ],
      compatibilityDate = "$COMPAT_DATE",
      durableObjectNamespaces = [
        (className = "TestKekkaiObject", uniqueKey = "worker-do", enableSql = true),
        (className = "FakeD1Object", uniqueKey = "worker-d1", enableSql = true),
      ],
      durableObjectStorage = (localDisk = "disk"),
      globalOutbound = "hooks",
      bindings = [
        (name = "TEST_DO", durableObjectNamespace = "TestKekkaiObject"),
        (name = "FAKE_D1", durableObjectNamespace = "FakeD1Object"),
        (name = "KEKKAI_OUTBOX", text = "fetch"),
        (name = "KEKKAI_DO_SHARD", text = "global"),
      ],
    )),
    (name = "hooks", worker = (
      modules = [(name = "hooks.js", esModule = embed "lib/hooks.js")],
      compatibilityDate = "$COMPAT_DATE",
      durableObjectNamespaces = [(className = "HookLog", uniqueKey = "hooks-log")],
      durableObjectStorage = (inMemory = void),
      bindings = [(name = "LOG", durableObjectNamespace = "HookLog")],
    )),
    (name = "case-worker", worker = (
      modules = [(name = "worker_driver.js", esModule = embed "lib/worker_driver.js")],
      compatibilityDate = "$COMPAT_DATE",
      bindings = [
        (name = "WORKER", service = "worker"),
        (name = "HOOKS", service = "hooks"),
        (name = "CASE", text = "worker"),
      ],
    )),
EOF
}

# early_fail <case> <report>: the case fails before workerd runs (its build
# failed); it gets no service in the config.
early_fail() {
	printf '%s\n' "$2" >"$T/$1.early"
}

{
	echo 'using Workerd = import "/workerd/workerd.capnp";'
	echo 'const config :Workerd.Config = ('
	echo '  services = ['
	for f in $(programs); do
		c=$(basename "$f" .kek)
		if built "p-$c"; then
			cp "${f%.kek}.test.mjs" "$T/$c.scenario.js"
			harness_service "$c" "p-$c/kekkai_runtime.js" lib/program.js 1 "p-$c"
		else
			early_fail "$c" "./kek build $f failed:
$(cat "$T/p-$c.log")"
		fi
	done
	cp tests/e2e/adapters.test.js "$T/adapters.scenario.js"
	harness_service adapters lib/kekkai_runtime.js lib/no_program.js 0
	if ! built worker; then
		early_fail worker "./kek build -target do testdata/e2e/bank.kek failed:
$(cat "$T/worker.log")"
	elif ! grep -q 'class_name = "KekkaiObject"' "$T/worker/wrangler.toml" ||
		! grep -q 'new_sqlite_classes = \["KekkaiObject"\]' "$T/worker/wrangler.toml"; then
		early_fail worker "wrangler.toml of -target do does not bind the KekkaiObject Durable Object:
$(cat "$T/worker/wrangler.toml")"
	else
		worker_services
	fi
	echo "    (name = \"disk\", disk = (path = \"$T/disk\", writable = true)),"
	echo '  ],'
	echo ');'
} >"$T/config.capnp"

start=$(date +%s)
workerd test "$T/config.capnp" >"$T/out" 2>&1 &
wpid=$!
(
	i=0
	while [ $i -lt "$WORKERD_TIMEOUT" ] && kill -0 $wpid 2>/dev/null; do
		sleep 1
		i=$((i + 1))
	done
	kill $wpid 2>/dev/null && echo "killed after ${WORKERD_TIMEOUT}s" >>"$T/out"
) >/dev/null 2>&1 &
wait $wpid
code=$?
elapsed=$(($(date +%s) - start))

for c in $(cases); do
	if [ -f "$T/$c.early" ]; then
		case_fail "$c" "$(cat "$T/$c.early")"
	elif grep -q "^KEK $c ok\$" "$T/out"; then
		case_ok "$c" "$(sed -n "s/^KEK $c | //p" "$T/out")"
	elif grep -q "^KEK $c fail\$" "$T/out"; then
		case_fail "$c" "$(sed -n "s/^KEK $c | //p" "$T/out")"
	else
		case_fail "$c" "no result from workerd test (exit $code, ${elapsed}s):
$(grep -v '^KEK ' "$T/out" | tail -n 60)"
	fi
done
