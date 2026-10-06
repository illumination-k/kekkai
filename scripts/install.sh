#!/bin/sh
# install.sh: install the single binary kek from the GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/illumination-k/kekkai/main/scripts/install.sh | sh
#
# KEK_VERSION=v0.1.0 picks a release (default: the latest);
# KEK_INSTALL_DIR the directory (default: ~/.local/bin).
set -eu

repo=illumination-k/kekkai
dir=${KEK_INSTALL_DIR:-$HOME/.local/bin}
version=${KEK_VERSION:-latest}

die() {
	echo "install.sh: $*" >&2
	exit 1
}

case $(uname -s)-$(uname -m) in
Darwin-arm64) target=aarch64-apple-darwin ;;
Linux-x86_64) target=x86_64-unknown-linux-gnu ;;
Linux-aarch64 | Linux-arm64) target=aarch64-unknown-linux-gnu ;;
*) die "no prebuilt kek for $(uname -s) $(uname -m)" ;;
esac

if [ "$version" = latest ]; then
	base=https://github.com/$repo/releases/latest/download
else
	base=https://github.com/$repo/releases/download/$version
fi
asset=kek-$target.tar.gz

tmp=$(mktemp -d "${TMPDIR:-/tmp}/kek-install-XXXXXX")
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "$base/$asset" || die "cannot download $base/$asset"
curl -fsSL -o "$tmp/$asset.sha256" "$base/$asset.sha256" || die "cannot download $base/$asset.sha256"
(
	cd "$tmp"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -c "$asset.sha256" >/dev/null
	else
		shasum -a 256 -c "$asset.sha256" >/dev/null
	fi
) || die "checksum mismatch for $asset"
tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$dir"
mv "$tmp/kek" "$dir/kek"
chmod +x "$dir/kek"
echo "installed $("$dir/kek" version) to $dir/kek"
case :$PATH: in
*:"$dir":*) ;;
*) echo "add $dir to PATH to run kek" ;;
esac
