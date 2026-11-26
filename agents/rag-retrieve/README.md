# rag-retrieve

An agent that answers a question strictly from the indexed meeting notes. It
reads the memory store and makes one chat call per run; it cannot write to the
store and does not read Drive.

## What it does

The agent embeds the run input (the question) through the logical embeddings
model `embed` and searches the `memory` collection `notes` for the most similar
chunks (the top 5). It then makes one chat call through the logical model
`chat`. The retrieved passages are carried in the user message, and the model
is instructed to answer using only those passages, citing their source and
date, and to say the answer is not in the notes rather than invent one.

If nothing matches, the agent answers that it is not in the notes without
calling the model.

## Operator setup

Define these in your gateway configuration:

- A tool resource named `memory` that exposes `memory_search` (read-only is
  fine).
- An embeddings model named `embed`.
- A chat model named `chat`.

After enrolling, stamp `grounding: grounded` on the agent so the run receipt
reports it as answering from your data.

## Enroll

```
agenthof enroll https://raw.githubusercontent.com/agenthof/hub/<tag>/agents/rag-retrieve/card.json ...
```

Replace `<tag>` with a release tag of this repository, and see
`agenthof enroll --help` for the remaining arguments.

## Limits

The agent answers only from what rag-ingest has stored; it never reads Drive
directly. The passages are placed in the user message and treated as quoted
data, but a crafted note could still try to influence the answer. The program
is started by the engine's supervisor with `-socket <path>` and is not meant
to be run by hand.
