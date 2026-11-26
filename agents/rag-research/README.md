# rag-research

An agent that answers from the indexed meeting notes plus the model's own
general knowledge. It reads the memory store and makes one chat call per run;
it cannot write to the store and does not read Drive.

## What it does

The retrieval is the same as rag-retrieve: the agent embeds the run input (the
question) through the logical embeddings model `embed`, searches the `memory`
collection `notes` for the top 5 most similar chunks, and makes one chat call
through the logical model `chat` with the passages carried in the user message.
Here the instruction permits the model to draw on its own general knowledge in
addition to the notes, to make clear which parts come from which, and to say so
when the notes do not cover a detail. It answers even when no notes match.

## Operator setup

Define the same resources as for rag-retrieve:

- A tool resource named `memory` that exposes `memory_search` (read-only is
  fine).
- An embeddings model named `embed`.
- A chat model named `chat`.

After enrolling, stamp `grounding: mixed` on the agent so the run receipt
reports it as answering from your notes and the model.

## Enroll

```
agenthof enroll https://raw.githubusercontent.com/agenthof/hub/<tag>/agents/rag-research/card.json ...
```

Replace `<tag>` with a release tag of this repository, and see
`agenthof enroll --help` for the remaining arguments.

## Limits

Because the agent may use world knowledge, its answers are not strictly
grounded in your notes; use rag-retrieve when you need notes-only answers. The
program is started by the engine's supervisor with `-socket <path>` and is not
meant to be run by hand.
