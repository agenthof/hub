#!/usr/bin/env bash
# Determinism + leak check for the agent build recipe.
# Builds generic-chat for the host {os,arch} twice, asserts identical hashes,
# and asserts the binary contains no forbidden strings.
#
# The native darwin/arm64 build is the authoritative hash for the darwin entry;
# linux/amd64 is cross-compiled with the same flags; CI re-verifies both.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
goos=$(go env GOOS)
goarch=$(go env GOARCH)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

h1=$("$here/build.sh" generic-chat "$goos" "$goarch" "$tmp/a")
h2=$("$here/build.sh" generic-chat "$goos" "$goarch" "$tmp/b")
if [ "$h1" != "$h2" ]; then
  echo "FAIL: non-deterministic build: $h1 != $h2" >&2
  exit 1
fi

for s in /Users/  ; do
  if grep -aqF -- "$s" "$tmp/a"; then
    echo "FAIL: binary contains forbidden string: $s" >&2
    exit 1
  fi
done

echo "PASS: $goos/$goarch $h1 (identical across two builds, no forbidden strings)"
