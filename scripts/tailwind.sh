#!/bin/sh
# Installs the Tailwind standalone CLI into .bin/ in the current directory (gitignored), pinned
# and checksum-checked; each app's `make css` runs it. Bump by changing the version.
set -eu
VERSION=4.3.3
bin=.bin/tailwindcss
if [ -x $bin ] && $bin --help 2>/dev/null | grep -q "v$VERSION"; then exit 0; fi
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) f=tailwindcss-linux-x64 ;;
  Linux-aarch64) f=tailwindcss-linux-arm64 ;;
  Darwin-arm64) f=tailwindcss-macos-arm64 ;;
  Darwin-x86_64) f=tailwindcss-macos-x64 ;;
  *) echo "no Tailwind build for $(uname -sm)" >&2; exit 1 ;;
esac
base=https://github.com/tailwindlabs/tailwindcss/releases/download/v$VERSION
tmp=$(mktemp -d)
curl -fsSL -o "$tmp/$f" "$base/$f"
curl -fsSL -o "$tmp/sums" "$base/sha256sums.txt"
(cd "$tmp" && grep " ./$f\$" sums | sed 's# ./# #' | sha256sum -c -)
mkdir -p .bin
install "$tmp/$f" $bin
rm -rf "$tmp"
