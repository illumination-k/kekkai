#!/bin/sh
# bootstrap: the self-hosting fixed point. `./kek bootstrap-check` builds
# compiler/ with the committed bootstrap compiler (stage1), rebuilds it
# with stage1 (stage2) and requires stage1 == stage2 byte for byte.
# compiler/prelude_src.kek must be generated from lib/prelude.
. "$(dirname "$0")/../lib.sh"
SUITE=bootstrap

if out=$("$KEK" bootstrap-check 2>&1); then
	case_ok fixed-point "$out"
else
	case_fail fixed-point "$out"
fi
if out=$("$KEK" embed-prelude lib/prelude compiler/prelude_src.kek -check 2>&1); then
	case_ok prelude-embedded
else
	case_fail prelude-embedded "$out"
fi
