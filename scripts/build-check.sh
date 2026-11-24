#!/usr/bin/env bash
# Determinism + leak check for the agent build recipe.
# For both agents, for the host {os,arch} and linux/amd64: build twice (the
# second with a fresh GOCACHE), assert identical hashes, and assert the binary
# contains no forbidden strings. These are the hashes pinned in public cards.
#
# The native darwin/arm64 build is the authoritative hash for the darwin entry;
# linux/amd64 is cross-compiled with the same flags. Both are meant to be
# re-verified independently.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

targets=("$host_os/$host_arch")
if [ "$host_os/$host_arch" != "linux/amd64" ]; then
  targets+=("linux/amd64")
fi

forbidden=(/Users/ /home/   "$root")

for agent in generic-chat router-chat; do
  for t in "${targets[@]}"; do
    goos=${t%/*}
    goarch=${t#*/}
    h1=$("$here/build.sh" "$agent" "$goos" "$goarch" "$tmp/a")
    h2=$(GOCACHE="$tmp/cache-$agent-$goos-$goarch" "$here/build.sh" "$agent" "$goos" "$goarch" "$tmp/b")
    if [ "$h1" != "$h2" ]; then
      echo "FAIL: $agent $t non-deterministic: $h1 != $h2" >&2
      exit 1
    fi
    for s in "${forbidden[@]}"; do
      if grep -aqF -- "$s" "$tmp/a"; then
        echo "FAIL: $agent $t binary contains forbidden string: $s" >&2
        exit 1
      fi
    done
    echo "PASS: $agent $t $h1 (identical across two builds, no forbidden strings)"
  done
done
