# router-chat

A least-privilege classifier. Given a menu of workflows and a user utterance,
it asks the model which one fits best and reports that pick. It does not spawn
or run anything: whatever consumes its answer decides what to do with it. It
has no tools, reads no files, and makes no network calls of its own; its only
capability is one chat completion per run, made through the per-run proxy the
gateway provides.

## Contract (unstable, 0.x)

The input and output shapes below are 0.x and may change before 1.0.

Input is JSON:

```json
{"menu":[{"role":"...","workflow":"...","description":"..."}],"utterance":"..."}
```

The agent numbers the menu entries, asks the model for the number of the best
fit, and checks the answer against the menu. The result artifact is either the
chosen entry's identifying pair:

```json
{"role":"...","workflow":"..."}
```

or the literal `null` when the answer does not name a menu entry (including
empty, malformed, or out-of-range replies). It never emits a role or workflow
that was not in the menu. A model-call failure, or input that is not the JSON
above, fails the run with a reason.

## Operator setup

The agent asks for a model named `router`. You must define a model with that
name in your gateway catalog, mapping the logical name to the real model you
want to use. `agenthof enroll` refuses the card until a model of that logical
name is defined in your gateway catalog.

## Enroll

```
agenthof enroll https://raw.githubusercontent.com/agenthof/hub/<tag>/agents/router-chat/card.json ...
```

Replace `<tag>` with a release tag of this repository, and see
`agenthof enroll --help` for the remaining arguments.

## Limits

The pick is the model's judgment and can be wrong; a `null` means no usable
answer, not proof that no workflow fits. The program is started by the
engine's supervisor with `-socket <path>` and is not meant to be run by hand.
