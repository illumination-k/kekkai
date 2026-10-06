#!/bin/sh
# dist.sh [cargo build flags...]: build the single binary kek (cli/), with
# the current compiler (./kek stage) embedded, into cli/target/<...>/kek.
# With KEK_COMPILER_WASM set, that module is embedded instead.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
if [ -z "${KEK_COMPILER_WASM:-}" ]; then
	mkdir -p "$root/cli/embed"
	cp "$("$root/kek" stage)" "$root/cli/embed/kek.wasm"
fi
cd "$root/cli"
cargo build --release "$@"
