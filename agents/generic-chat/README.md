# generic-chat

A least-privilege agent that answers a prompt from the model's own knowledge.
It has no tools, reads no files, and makes no network calls of its own: its
only capability is one chat completion per run, made through the per-run proxy
the gateway provides.

## What it does

The run input is sent as a single user message to the logical model `chat`.
The model's reply becomes the run's result. If the model call fails, the run
fails with the error as its reason.

## Operator setup

The agent asks for a model named `chat`. You must define a model with that
name in your gateway catalog, mapping the logical name to the real model you
want to use. Without it, every run fails at the model call.

## Enroll

```
agenthof enroll https://raw.githubusercontent.com/agenthof/hub/<tag>/agents/generic-chat/card.json ...
```

Replace `<tag>` with a release tag of this repository, and see
`agenthof enroll --help` for the remaining arguments.

## Limits

The agent does not browse, retrieve documents, or call tools, so answers are
limited to what the configured model already knows. The program is started by
the engine's supervisor with `-socket <path>` and is not meant to be run by
hand.
