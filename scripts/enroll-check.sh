#!/usr/bin/env bash
# Disclose-check every committed agent card against the published curator key.
#
# For each agents/*/card.json this runs `agenthof enroll` WITHOUT --accept
# against a scratch installed configuration. The card is verified against the
# trusted signer and its disclosure is printed; nothing is installed. Any
# refusal (bad signature, unknown model, malformed card) fails the script.
#
# Environment:
#   AGENTHOF_BIN  path to a prebuilt agenthof binary (used as-is when set)
#   ENGINE_DIR    path to an agenthof engine checkout at the pinned commit;
#                 the binary is built from it when AGENTHOF_BIN is unset
#
# Pinned engine commit: 82c20c9e576c851e29a9f20399cd270fed829e85
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

bin=${AGENTHOF_BIN:-}
if [ -z "$bin" ]; then
  if [ -z "${ENGINE_DIR:-}" ]; then
    echo "set AGENTHOF_BIN or ENGINE_DIR" >&2
    exit 2
  fi
  bin="$tmp/agenthof"
  go -C "$ENGINE_DIR" build -o "$bin" ./cmd/agenthof
fi

cfg="$tmp/cfg"
ctl="$tmp/ctl/control.jsonl"
mkdir -p "$cfg/agents" "$cfg/roles" "$cfg/workflows" "$tmp/ctl"

cat >"$cfg/gateway.yaml" <<'YAML'
models:
  chat: {endpoint: http://localhost:4000, model: chat, api_key_env: K}
  router: {endpoint: http://localhost:4000, model: router, api_key_env: K}
defaults: {model: chat}
trusted_signers:
  - {kid: f07e492a38568409cbcb459b1feac8b70e08eec00149ac1981b4f9c053412404, key: k2troapQ5SHzGpMeckKzHBUNcI517qZmiX/bw9Bl05o=}
YAML
cat >"$cfg/roles/ops.yaml" <<'YAML'
name: ops
workflows: [main]
allowed_groups: [ops]
control: [apply, enroll]
YAML
cat >"$cfg/workflows/main.yaml" <<'YAML'
name: main
steps: [{name: s, agent: orch}]
YAML
cat >"$cfg/agents/orch.yaml" <<'YAML'
name: orch
endpoint: http://localhost:9
YAML

"$bin" apply --config "$cfg" --control-log "$ctl" --as checker --groups ops >/dev/null

n=0
for card in "$root"/agents/*/card.json; do
  name=$(basename "$(dirname "$card")")
  echo "== disclose $name"
  "$bin" enroll "$card" --name "$name" --role ops --config "$cfg" \
    --control-log "$ctl" --as checker --groups ops
  n=$((n + 1))
done

[ "$n" -gt 0 ] || { echo "no cards found" >&2; exit 1; }
echo "ok: $n card(s) disclosed"
