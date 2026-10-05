#!/bin/sh
# Add scripts/assure-pre-commit.sh to this clone's pre-commit hook, keeping
# whatever the hook already runs: the line goes right after the shebang, so
# the hook's last command still decides its exit status. Running it again
# changes nothing.
set -eu

root=$(git rev-parse --show-toplevel)
hooks=$(git rev-parse --git-path hooks)
hook="$hooks/pre-commit"
line='"$(git rev-parse --show-toplevel)/scripts/assure-pre-commit.sh" || exit 1'

mkdir -p "$hooks"
if [ ! -f "$hook" ]; then
	printf '#!/bin/sh\n' >"$hook"
fi
if grep -F 'scripts/assure-pre-commit.sh' "$hook" >/dev/null; then
	echo "already installed in $hook"
else
	{
		head -n 1 "$hook"
		printf '%s\n' "$line"
		tail -n +2 "$hook"
	} >"$hook.tmp"
	mv "$hook.tmp" "$hook"
	echo "installed in $hook"
fi
chmod +x "$hook" "$root/scripts/assure-pre-commit.sh"
