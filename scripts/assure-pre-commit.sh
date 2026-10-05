#!/bin/sh
# Git pre-commit hook: the assurance ledger must match the program.
#
# For every project (a directory with kekkai.toml and kekkai.assure.lock)
# that the commit touches, run `kek assure check` there. If the guarantees
# drifted from the lock, show `kek assure plan` and stop the commit: review
# the plan, approve it with `kek assure apply`, stage the lock and commit
# again. `git commit --no-verify` skips the hook.
#
# The check runs on the working tree, not on the staged contents.
#
# Install with scripts/install-hooks.sh (adds a line to .git/hooks/pre-commit).
set -eu

root=$(git rev-parse --show-toplevel)
cd "$root"
staged=$(git diff --cached --name-only --diff-filter=ACMRD)
[ -n "$staged" ] || exit 0

status=0
for lock in $(git ls-files '*kekkai.assure.lock'); do
	dir=$(dirname "$lock")
	prefix=
	[ "$dir" = . ] || prefix="$dir/"
	echo "$staged" | grep -E "^${prefix}(.*\.kek|kekkai\.toml|kekkai\.assure\.lock)$" >/dev/null || continue
	# The program: the top-level entries that the lock's definitions live in.
	srcs=$(sed -n 's/^ *"file": "\([^/"]*\).*/\1/p' "$lock" | sort -u)
	[ -n "$srcs" ] || continue
	for src in $srcs; do
		if ! (cd "$dir" && "$root/kek" assure check "$src" >/dev/null 2>&1); then
			echo "kek assure: the guarantees of ${prefix}$src changed and ${lock} is not up to date" >&2
			echo >&2
			(cd "$dir" && "$root/kek" assure plan "$src") >&2 || true
			echo >&2
			echo "Review the plan above, then approve it and stage the lock:" >&2
			echo "  (cd $dir && $root/kek assure apply -yes $src)   # weakening also needs -reason -owner -expires" >&2
			echo "  git add $lock" >&2
			status=1
		fi
	done
done
exit $status
