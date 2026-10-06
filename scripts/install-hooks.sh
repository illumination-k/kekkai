#!/bin/sh
# Add scripts/assure-pre-commit.sh to this clone's pre-commit hook, keeping
# whatever the hook already runs: the line goes right after the shebang, so
# the hook's last command still decides its exit status. Also define the
# merge driver `kek` that .gitattributes names (scripts/merge-driver.sh,
# docs/merge.md); the clone's worktrees share it. Running it again changes
# nothing.
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
git config merge.kek.name "kek merge (insertions kept, .kek by syntax tree)"
git config merge.kek.driver 'sh scripts/merge-driver.sh %O %A %B %L %P'
echo "merge driver kek: $(git config merge.kek.driver)"
