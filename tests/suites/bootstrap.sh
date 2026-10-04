#!/bin/sh
# bootstrap: the self-hosting fixed point. `./kek bootstrap-check` builds
# compiler/ with the committed bootstrap compiler (stage1), rebuilds it
# with stage1 (stage2) and requires stage1 == stage2 byte for byte.
# compiler/prelude_src.kek and compiler/core_src.kek must be generated from
# lib/prelude and lib/core.
#
# mutability: the committed bootstrap compiler enforces the mutability
# rules (docs/mutability.md) and the compiler's own sources, the core
# library and the prelude pass them: the fixed point holds with the rules
# on, and a program that breaks them is rejected by the bootstrap too.
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
if out=$("$KEK" embed-prelude lib/core compiler/core_src.kek -as core -check 2>&1); then
	case_ok core-embedded
else
	case_fail core-embedded "$out"
fi
if out=$(KEK_STAGE=bootstrap "$KEK" check compiler 2>&1) &&
	out2=$("$KEK" fix compiler 2>&1) && [ -z "$out2" ] &&
	! bad=$(KEK_STAGE=bootstrap "$KEK" check testdata/check/err_mut_not_mut.kek 2>&1) &&
	echo "$bad" | grep -q 'not declared as mutable'; then
	case_ok mutability
else
	case_fail mutability "bootstrap check of compiler/: $out
kek fix compiler (must print nothing): ${out2:-}
bootstrap check of err_mut_not_mut.kek (must fail): ${bad:-}"
fi
