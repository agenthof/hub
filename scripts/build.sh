#!/usr/bin/env bash
# Reproducible, leak-free build of one agent for one {os,arch}.
#
# usage: scripts/build.sh <agent> <os> <arch> <outpath>
#   agent: generic-chat | router-chat | rag-ingest | rag-retrieve | rag-research
#
# Prints sha256:<hex> of the built binary. A signed card pins this hash.
#
# Authoritative hashes: the NATIVE darwin/arm64 build on the maintainer's host
# is the authoritative hash for the darwin entry; linux/amd64 is produced by
# cross-compiling with the same flags. Both are meant to be re-verified
# independently.
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "usage: $0 <agent> <os> <arch> <outpath>" >&2
  exit 2
fi
agent=$1 goos=$2 goarch=$3 out=$4

case "$agent" in
  generic-chat|router-chat|rag-ingest|rag-retrieve|rag-research) ;;
  *) echo "unknown agent: $agent" >&2; exit 2 ;;
esac

# The card hashes are only reproducible on the pinned toolchain.
want_go=go1.27.4
have_go=$(go env GOVERSION)
if [ "$have_go" != "$want_go" ]; then
  echo "build.sh needs $want_go (found $have_go); the pinned hashes do not reproduce on other toolchains" >&2
  exit 1
fi

# Neutralize environment that would change the produced bytes.
export GOFLAGS=
export GOAMD64='' GOARM64='' GOEXPERIMENT=''

root=$(cd "$(dirname "$0")/.." && pwd)

# go -C changes directory, so make the output path absolute first.
case "$out" in
  /*) ;;
  *) out="$PWD/$out" ;;
esac

CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
  go -C "$root" build -trimpath -buildvcs=false -ldflags=-buildid= \
  -o "$out" "./agents/$agent"

if command -v sha256sum >/dev/null 2>&1; then
  sum=$(sha256sum "$out" | cut -d' ' -f1)
else
  sum=$(shasum -a 256 "$out" | cut -d' ' -f1)
fi
echo "sha256:$sum"
