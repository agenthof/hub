# Contributing to the Agenthof hub

Thanks for your interest. The hub is a collection of signed, enrollable agent
definitions for the Agenthof engine. Each agent is a directory under `agents/`
holding its definition and a card describing what it asks for. Issues,
discussion, and new agents are all welcome.

## Developer Certificate of Origin (DCO)

We use the [DCO](https://developercertificate.org/) instead of a CLA. Every
commit must be signed off, certifying you have the right to submit the work
under the project's Apache-2.0 license:

    git commit -s -m "feat(agents): add your agent"

which adds a `Signed-off-by: Your Name <you@example.com>` trailer. PRs with
unsigned commits will be asked to amend.

## Contributing an agent

- Put the agent in `agents/<name>/` with its card (`card.json`).
- Declare only the grants the agent actually needs; every requested grant is
  shown to the operator at enroll time.
- Declare a *logical* model name. Operators map it to a real model in their
  own gateway catalog.
- Cards are signed by the curator when a release is cut; do not add
  signatures to a pull request.
- Never commit secrets or credentials.

## License

Contributions are accepted under the [Apache License 2.0](LICENSE). Copyright
belongs to the Agenthof contributors.

## Security

Report vulnerabilities privately to security@agenthof.dev rather than opening
a public issue.
