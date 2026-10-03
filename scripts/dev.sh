#!/usr/bin/env bash
# Build a Kekkai program and serve it locally on workerd (no Cloudflare
# account or network needed).
#
#   scripts/dev.sh <file.kek> [wrangler dev args...]
#
# Environment:
#   TARGET=d1|do     storage backend (default d1; do = Durable Objects)
#   OUT=dir          output directory (default out/<name>-<target>)
#   OUTBOX=log|fetch outbox delivery (default log: print entries instead of POSTing)
set -euo pipefail

file=${1:?usage: scripts/dev.sh <file.kek> [wrangler dev args...]}
shift
target=${TARGET:-d1}
name=$(basename "$file" .kek)
out=${OUT:-out/$name-$target}

./kek build -target "$target" -o "$out" "$file"
# Stay in the repository so mise resolves the pinned wrangler.
exec wrangler dev --local -c "$out/wrangler.toml" --persist-to "$out/.state" --var "KEKKAI_OUTBOX:${OUTBOX:-log}" "$@"
