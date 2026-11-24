# Agenthof hub

A place to install **governed, signed agents you don't have to trust.**

Each agent in the hub is described by a signed card. You do not have to trust
the agent itself, because:

- it is **contained** by your own Agenthof engine, not by anything the agent
  says about itself;
- the grants it requests (models, tools, and so on) are **disclosed** in its
  card before anything runs;
- you **consent** explicitly by enrolling it with `agenthof enroll`;
- its runs are **audited** in your engine's ledger.

The ledger is limited evidence of what happened, not proof against a
determined attacker, and containment is only as strong as the runtime you
configure. See the engine's documentation for the honest limits.

## Install

Enroll an agent from a tagged release:

    agenthof enroll https://raw.githubusercontent.com/agenthof/hub/<tag>/agents/<name>/card.json

Pin a tag rather than a branch so the card you review is the card you enroll.

## Model names

An agent declares a *logical* model name, not a specific provider model. You
map that name to a real model in your own gateway catalog, so you choose what
the agent runs on and what it costs.

## Curator key

Cards are signed by the hub curator. The published public key and key id go
here so you can add them to your engine's trusted signers:

Cards in this repo are signed by the hub curator. The curator public key (safe to publish):

- public key: `k2troapQ5SHzGpMeckKzHBUNcI517qZmiX/bw9Bl05o=`
- kid: `f07e492a38568409cbcb459b1feac8b70e08eec00149ac1981b4f9c053412404`

To trust cards signed by this curator, add this to your gateway.yaml:

```yaml
trusted_signers:
  - {kid: f07e492a38568409cbcb459b1feac8b70e08eec00149ac1981b4f9c053412404, key: k2troapQ5SHzGpMeckKzHBUNcI517qZmiX/bw9Bl05o=}
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under Apache-2.0.
